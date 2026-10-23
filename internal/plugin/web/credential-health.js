// These are CPA's own recent-request snapshots, not reconstructed plugin usage records.
const credentialHealthQueries = { auth: createQuery(m("health.title")), provider: createQuery(m("health.title")) };

function normalizeCredentialHealth(entry) {
  if (!Array.isArray(entry?.recent_requests)) return null;
  const count = (value) => typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
  const buckets = entry.recent_requests.slice(-20);
  if (buckets.some((bucket) => !bucket || !count(bucket.success) || !count(bucket.failed))) return null;
  return buckets.map((bucket) => ({
    success: bucket.success,
    failed: bucket.failed,
    time: typeof bucket.time === "string" ? bucket.time : ""
  }));
}

function readCredentialHealth(kind, reload = false) {
  const query = credentialHealthQueries[kind];
  return query.read(
    kind,
    async () => {
      const path = kind === "auth" ? "/auth-files" : "/api-key-usage";
      const response = requireObject(await api("GET", "/v0/management" + path, null), m("health.title"));
      const result = new Map();
      if (kind === "auth") {
        for (const file of requireArray(response.files, m("ui.auth_files"))) {
          if (typeof file.auth_index === "string" && file.auth_index) result.set(file.auth_index, normalizeCredentialHealth(file));
        }
      } else {
        // Hash composite lookup keys before retaining the snapshot; never persist or display API keys.
        for (const [provider, entries] of Object.entries(response)) {
          const snapshots = new Map();
          for (const [key, entry] of Object.entries(requireObject(entries, m("health.title")))) {
            snapshots.set(await sha256Hex(key), normalizeCredentialHealth(entry));
          }
          result.set(provider.toLowerCase(), snapshots);
        }
      }
      return result;
    },
    reload
  );
}

function credentialHealthCell(row, kind) {
  const query = credentialHealthQueries[kind];
  const source = kind === "provider" ? aiProviderSources.get(row) : null;
  const buckets =
    kind === "auth" ? query.value?.get(row.auth_index) : query.value?.get((row.name || row.type).toLowerCase())?.get(source?.healthKey);
  const label = el("span", { class: "health-label", text: m("health.title") });
  const cell = el("td", { class: "credential-health-cell" }, label);
  if (query.error || !buckets) {
    const loading = query.task && !query.value;
    cell.append(
      el("span", {
        class: "muted small",
        text: m(loading ? "ui.loading" : "health.unavailable"),
        "data-tip": m(query.error ? "health.load_failed" : "health.unsupported")
      })
    );
    return cell;
  }
  const success = buckets.reduce((sum, bucket) => sum + bucket.success, 0);
  const failed = buckets.reduce((sum, bucket) => sum + bucket.failed, 0);
  const total = success + failed;
  const percent = total ? ((success / total) * 100).toLocaleString(locale(), { maximumFractionDigits: 1 }) + "%" : "—";
  const details = (bucket) => [
    bucket.time && el("div", { class: "muted", text: m("health.period", { time: bucket.time }) }),
    el("div", {
      text:
        bucket.success + bucket.failed
          ? m("health.counts", { success: int(bucket.success), failed: int(bucket.failed) })
          : m("health.empty")
    })
  ];
  const blocks = [...Array.from({ length: Math.max(0, 20 - buckets.length) }, () => ({ success: 0, failed: 0 })), ...buckets];
  cell.append(
    el(
      "div",
      { class: "credential-health" },
      el(
        "div",
        { class: "health-summary" },
        el("span", { class: "health-rate", text: percent, "data-tip": m("health.rate_help") }),
        el("span", {
          class: "muted small",
          text: total ? m("health.counts", { success: int(success), failed: int(failed) }) : m("health.empty")
        })
      ),
      el(
        "div",
        { class: "health-blocks", role: "group", "aria-label": m("health.recent") },
        blocks.map((bucket) => {
          const total = bucket.success + bucket.failed;
          const rate = total ? bucket.success / total : -1;
          const color = rate < 0 ? "" : rate >= 0.9 ? "success" : rate >= 0.5 ? "mixed" : "failure";
          return el("span", {
            class: "health-block " + color,
            tabindex: 0,
            "aria-label":
              (bucket.time ? bucket.time + ": " : "") +
              (total ? m("health.counts", { success: int(bucket.success), failed: int(bucket.failed) }) : m("health.empty")),
            ...hoverTooltipProps(() => details(bucket), true)
          });
        })
      )
    )
  );
  return cell;
}
