const diagnostics = {
  errors: { entries: [], next: "", loaded: false, loading: false },
  logs: { entries: [], next: "", loaded: false, loading: false }
};
function resetDiagnostics() {
  for (const item of Object.values(diagnostics)) Object.assign(item, { entries: [], next: "", loaded: false, loading: false });
}
async function loadDiagnostics(kind, reload = false, more = false) {
  const item = diagnostics[kind];
  if (item.loading || (!reload && !more && item.loaded)) return;
  item.loading = true;
  renderDiagnostics(kind);
  try {
    const query = new URLSearchParams({ limit: "50" });
    if (more && item.next) query.set("before_id", item.next);
    if (kind === "logs" && $("plugin-log-level").value) query.set("level", $("plugin-log-level").value);
    const result = await plugin("GET", (kind === "logs" ? "/plugin-logs" : "/errors") + "?" + query);
    const entries = requireArray(result.entries, m(kind === "logs" ? "ui.plugin_logs" : "ui.error_events"));
    item.entries = more ? [...item.entries, ...entries] : entries;
    item.next = result.next_before_id || "";
    item.loaded = true;
    item.error = "";
  } catch (err) {
    if (err instanceof AuthError || err instanceof StaleRequestError) throw err;
    item.error = err.message;
  } finally {
    item.loading = false;
    renderDiagnostics(kind);
  }
}
function renderDiagnostics(kind) {
  const item = diagnostics[kind], target = $(kind === "logs" ? "plugin-logs-body" : "errors-body");
  const term = kind === "logs" ? $("plugin-log-filter").value.toLowerCase() : "";
  const rows = item.entries.filter((row) => !term || row.message?.toLowerCase().includes(term));
  target.replaceChildren();
  if (item.error) target.append(el("p", { class: "notice err show", text: item.error }));
  if (!rows.length) target.append(el("div", { class: "empty", text: m(item.loading ? "ui.loading" : "ui.no_data") }));
  for (const row of rows) {
    const title =
      kind === "logs"
        ? row.level
        : [row.status_code || "", row.provider, row.model, row.error_type].filter(Boolean).join(DISPLAY_SEPARATOR);
    target.append(
      el(
        "article",
        { class: "diagnostic-entry" },
        el("div", { class: "row spread" }, el("strong", { text: title }), el("span", { class: "small muted", text: when(row.at) })),
        kind === "logs"
          ? el("p", { class: "diagnostic-message", text: row.message })
          : el("details", {}, el("summary", { text: row.auth_index || m("controls.details") }), el("pre", { text: row.body || "—" }))
      )
    );
  }
  const more = $(kind + "-more");
  more.hidden = !item.next;
  more.disabled = item.loading;
}
for (const kind of ["logs", "errors"]) $(kind + "-more").onclick = () => guard(() => loadDiagnostics(kind, false, true));
$("plugin-log-filter").oninput = () => renderDiagnostics("logs");
$("plugin-log-level").onchange = () => guard(() => loadDiagnostics("logs", true));
$("plugin-logs-clear").onclick = () =>
  guard(() =>
    openActionDialog(m("ui.clear_logs"), m("controls.clear_logs_confirm"), undefined, async () => {
      await plugin("DELETE", "/plugin-logs");
      await loadDiagnostics("logs", true);
    })
  );
