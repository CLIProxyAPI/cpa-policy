// Tag tones for the plan labels the backend reports, per provider: a Claude Pro is not a Codex Pro 200.
// Antigravity reports its upstream tier name as is.
const AUTH_PLAN_TONES = {
  codex: {
    elite: ["pro500", "pro200"],
    premium: ["pro100", "businesspremium"],
    info: ["plus"],
    ok: ["go"],
    business: ["business", "enterprise", "enterpriseautomation"]
  },
  claude: { premium: ["max"], info: ["pro"], business: ["team"] },
  antigravity: { elite: ["ultra", "googleaiultra"], premium: ["ultralite", "googleaiultralite"], info: ["pro", "googleaipro"] },
  xai: { premium: ["paid"] }
};

function authPlanTone(category, plan) {
  const key = plan.toLowerCase().replace(/[^a-z0-9]/g, "");
  return Object.entries(AUTH_PLAN_TONES[category] || {}).find(([, plans]) => plans.includes(key))?.[0] || "plain";
}

function authFileQuota(file, owner) {
  const manual = owner.authQuotas.get(file.auth_index);
  const observed = file.quota;
  if (!manual) return observed;
  if (!observed) return manual;
  // A request reports only the windows of the limit it used, so a window observed after the manual query replaces
  // just that window, in place. A queried window drops out once its limit is observed after its reset time without it.
  const newer = observed.quota.filter((row) => Date.parse(row.observed_at) > Date.parse(manual.fetched_at));
  const reportedAt = new Map();
  for (const row of newer) reportedAt.set(row.limit_key, Math.max(reportedAt.get(row.limit_key) ?? 0, Date.parse(row.observed_at)));
  const windows = new Map(manual.quota.map((row) => [authQuotaWindowKey(row), row]));
  for (const row of newer) windows.set(authQuotaWindowKey(row), row);
  const quota = [...windows.values()].filter((row) => newer.includes(row) || !(reportedAt.get(row.limit_key) >= Date.parse(row.reset_at)));
  const result = { ...observed, ...manual, quota };
  for (const field of ["plan", "credit_balance", "credits_unlimited"])
    if (Date.parse(observed[field + "_observed_at"]) > Date.parse(manual.fetched_at)) result[field] = observed[field];
  return result;
}

// The label carries a window's length, so it tells the windows of one limit apart.
function authQuotaWindowKey(row) {
  return JSON.stringify([row.limit_key || "", row.group_label || "", row.label_prefix || "", row.label || ""]);
}

function authFileMeta(file, quota) {
  const plan = quota?.plan?.trim();
  return el(
    "div",
    { class: "credential-meta" },
    plan && el("span", { class: "tag " + authPlanTone(file.category, plan) }, el("span", { text: plan })),
    credentialRoutingTag(file),
    file.unavailable && el("span", { class: "tag bad", text: m("ui.unavailable") })
  );
}

function authProviderIcon(category) {
  return providerIcon(category === "gemini-cli" || category === "gemini-interactions" ? "gemini" : category);
}

function authUI(account) {
  const prefix = account ? "account-auth" : "auth";
  return {
    owner: account ? accountUIState : adminUIState,
    files: authFilesQueries[account ? "account" : "admin"],
    body: $(prefix + "-files-body"),
    enabledOnly: $(prefix + "-enabled-only"),
    // Account usage covers only the presenting key, so it cannot be projected to the whole window.
    estimate: account ? null : $("auth-quota-estimate"),
    provider: $(prefix + "-provider"),
    search: $(prefix + "-search"),
    bulk: $(prefix + "-refresh-filtered")
  };
}

const AUTH_QUOTA_CACHE_PREFIX = "cpa-key-billing.auth-quotas.v7.";

function resetAuthQuotaMemory(account) {
  const role = account ? "account" : "admin";
  const owner = account ? accountUIState : adminUIState;
  authQuotaGeneration[role]++;
  owner.authQuotas.clear();
  owner.authQuotaLoading.clear();
  owner.authQuotaErrors.clear();
  owner.authFileRevisions.clear();
  resetAuthQuotaBatch(account);
}

function resetAuthQuotaBatch(account) {
  const bulk = authUI(account).bulk;
  bulk.dataset.loading = "false";
  setLabeledActionIcon(bulk, "refresh", m("ui.update_quotas"));
}

function authQuotaStorageKey(role) {
  const scope = credentialScope[role];
  return scope ? AUTH_QUOTA_CACHE_PREFIX + role + "." + scope : "";
}

function persistAuthQuotaCache(account) {
  const role = account ? "account" : "admin";
  const key = authQuotaStorageKey(role);
  if (!key) return;
  const owner = account ? accountUIState : adminUIState;
  try { sessionStorage.setItem(key, JSON.stringify({ entries: [...owner.authQuotas] })); } catch (_) {}
}

function restoreAuthQuotaCache(account) {
  const role = account ? "account" : "admin";
  let cached;
  try { cached = JSON.parse(sessionStorage.getItem(authQuotaStorageKey(role)) || "null"); } catch (_) { return; }
  if (!Array.isArray(cached?.entries)) return;
  const owner = account ? accountUIState : adminUIState;
  for (const entry of cached.entries) {
    if (Array.isArray(entry) && typeof entry[0] === "string" && Array.isArray(entry[1]?.quota)) {
      owner.authQuotas.set(entry[0], entry[1]);
    }
  }
}

async function readAuthFileWeights() {
  try {
    const result = await api("GET", "/v0/management/auth-files", null, { keepSessionOnForbidden: true });
    return new Map(requireArray(result?.files, m("ui.auth_files")).map((file) => [file.auth_index, file.weight ?? 1]));
  } catch (err) {
    if (err instanceof AuthError || err instanceof StaleRequestError) throw err;
    return new Map();
  }
}

function readAuthFiles(account, reload = false) {
  const role = account ? "account" : "admin";
  const path = "/auth-files?" + new URLSearchParams(selectedRange(account));
  return authFilesQueries[role].read(
    JSON.stringify(sharedTimeRanges[role]),
    async () => {
      const [result, weights] = await Promise.all([
        account ? accountAPI(path) : plugin("GET", path),
        account ? null : readAuthFileWeights()
      ]);
      const files = requireArray(result?.files, m("ui.auth_files"));
      return account ? files : files.map((file) => ({ ...file, weight: weights.get(file.auth_index) }));
    },
    reload
  );
}

function updateAuthFiles(account, files) {
  const role = account ? "account" : "admin";
  const owner = account ? accountUIState : adminUIState;
  // Status changes awaiting confirmation outlast reads that started before them.
  for (const file of files)
    if (owner.authStatusSubmissions?.has(file.auth_index)) file.disabled = owner.authStatusSubmissions.get(file.auth_index);
  const byIndex = new Map(files.map((file) => [file.auth_index, file]));
  const previous = owner.authFileRevisions;
  const changed = new Set(
    files.filter((file) => previous.has(file.auth_index) && previous.get(file.auth_index) !== (file.cache_revision || ""))
      .map((file) => file.auth_index)
  );
  const staleIndex = (index) => {
    const file = byIndex.get(index);
    if (!file || changed.has(index)) return true;
    const result = owner.authQuotas.get(index);
    return !!file.cache_revision && !!result && result.auth_revision !== file.cache_revision;
  };
  const stale = [...owner.authQuotas.keys(), ...owner.authQuotaErrors.keys(), ...owner.authQuotaLoading].some(staleIndex);
  owner.authFileRevisions = new Map(files.map((file) => [file.auth_index, file.cache_revision || ""]));
  if (!stale) return;
  authQuotaGeneration[role]++;
  resetAuthQuotaBatch(account);
  for (const index of owner.authQuotas.keys()) if (staleIndex(index)) owner.authQuotas.delete(index);
  for (const index of owner.authQuotaErrors.keys()) if (staleIndex(index)) owner.authQuotaErrors.delete(index);
  owner.authQuotaLoading.clear();
  persistAuthQuotaCache(account);
}

function authQuotaCacheCurrent(account, owner, generation, index) {
  const role = account ? "account" : "admin";
  const currentOwner = account ? accountUIState : adminUIState;
  return owner === currentOwner && generation === authQuotaGeneration[role] && owner.authFileRevisions.has(index);
}

function filteredAuthFiles(account) {
  const ui = authUI(account);
  const provider = ui.provider.value;
  const search = ui.search.value.trim().toLocaleLowerCase();
  return (ui.files.value || []).filter((file) => {
    if (ui.enabledOnly.checked && file.disabled) return false;
    if (provider && file.category !== provider) return false;
    if (!search) return true;
    const currentPlan = authFileQuota(file, ui.owner)?.plan;
    return [file.name, file.email, currentPlan, file.category].some((value) => String(value || "").toLocaleLowerCase().includes(search));
  });
}

function quotaResetMeta(row) {
  const resetAt = row.reset_at ? new Date(row.reset_at) : null;
  if (!resetAt || isNaN(resetAt.getTime())) return { at: "", after: "" };
  const after = Math.max(0, Math.floor((resetAt.getTime() - Date.now()) / 1000));
  return { at: compactResetDate(resetAt), after: compactTimeUntil(after) };
}

function quotaAmount(row) {
  const hasUsed = row.used !== undefined;
  const hasLimit = row.limit !== undefined;
  if (!hasUsed && !hasLimit) return "";
  const format = (value) => quotaNumber(value, row.currency);
  if (hasUsed && hasLimit) return format(row.used) + DISPLAY_SLASH + format(row.limit);
  return hasUsed ? m("ui.used_value", { v0: format(row.used) }) : m("ui.quota_value", { v0: format(row.limit) });
}

function quotaNumber(value, currency) {
  return currency === "USD" ? usd(value) : Number(value).toLocaleString(locale(), { maximumFractionDigits: 2 });
}

function quotaLabel(row) {
  const label = window.billingI18n.serverMessage(row.label_message, row.label || "");
  const group = window.billingI18n.serverMessage(row.group_message, row.group_label || "");
  const fullLabel = row.label_prefix ? m("ui.join", { v0: row.label_prefix, v1: "", v2: label }) : label;
  return group && String(group).toLowerCase() !== String(fullLabel).toLowerCase()
    ? m("ui.join", { v0: group, v1: DISPLAY_SEPARATOR, v2: fullLabel })
    : group || fullLabel;
}

// Usage covers a window's period. An estimate scales it to the whole window by the share already used,
// so it needs both a used share and recorded usage.
function quotaEstimateScale(usedPercent, usage) { return usedPercent > 0 && usage.tokens > 0 ? 100 / usedPercent : 0; }

function renderQuotaMetrics(usage, scale = 1, tooltip = null) {
  const metric = (icon, label, value) =>
    el("span", { class: "auth-quota-metric" }, el("span", { role: "img", "aria-label": label }, actionIcon(icon)), value);
  return el(
    "span",
    { class: "auth-quota-metrics", "data-tip": scale ? null : m("ui.quota_estimate_unavailable"), tooltip },
    metric("square-text", m("ui.tokens"), scale ? tokens(Math.round(usage.tokens * scale)) : "—"),
    metric("circle-dollar-sign", m("ui.amount"), scale ? usd(usage.cost_usd * scale, 2) : "—")
  );
}

function renderQuotaBar(percent, label) {
  const known = Number.isFinite(percent);
  const level = percent >= 70 ? "high" : percent >= 30 ? "medium" : "low";
  return el(
    "div",
    {
      class: "bar",
      role: "progressbar",
      "aria-label": label,
      "aria-valuemin": "0",
      "aria-valuemax": "100",
      "aria-valuenow": known ? Math.round(percent) : null
    },
    el("i", { class: "quota-fill " + (known ? level : "high"), style: "width:" + (known ? percent : 0) + "%" })
  );
}

function renderQuotaRow(row, estimate) {
  const label = quotaLabel(row);
  const percent = row.remaining_percent;
  const reset = quotaResetMeta(row);
  const elapsed = row.reset_at && new Date(row.reset_at).getTime() <= Date.now();
  // The row sits inside the button that opens the drawer, so its tooltip is hover-only.
  const usage =
    row.usage &&
    (estimate
      ? renderQuotaMetrics(row.usage, quotaEstimateScale(100 - percent, row.usage))
      : renderQuotaMetrics(row.usage, 1, modelUsageTooltip(row.usage.models)));
  const amount = quotaAmount(row);
  return el(
    "div",
    { class: "auth-quota-row" + (elapsed ? " elapsed" : "") },
    el(
      "div",
      { class: "auth-quota-head" },
      el("span", { class: "auth-quota-label", "data-tip": label, "data-tip-overflow": "", text: label }),
      reset.at && el("span", { class: "auth-quota-reset-after", text: elapsed ? m("ui.quota_reset_elapsed") : reset.after }),
      el("span", { class: "auth-quota-value", text: Number.isFinite(percent) ? percent.toFixed(0) + "%" : "—" })
    ),
    renderQuotaBar(percent, label),
    (usage || amount || reset.at) &&
      el(
        "div",
        { class: "auth-quota-foot" },
        usage,
        amount && el("span", { text: amount }),
        reset.at && el("span", { class: "auth-quota-reset-time", text: reset.at })
      )
  );
}

function renderAuthResetCredits(file, result) {
  if (file.category !== "codex") return null;
  if (result.rate_limit_reset_credits_available_count === 0) {
    return el("div", { class: "muted small", text: m("ui.no_manual_resets_available") });
  }
  const credits = result.rate_limit_reset_credits || [];
  if (!credits.length) { return el("div", { class: "muted small", text: m("ui.reset_credits_expiry_unavailable") }); }
  const timezone =
    new Intl.DateTimeFormat("en", { timeZoneName: "shortOffset" }).formatToParts(new Date()).find((part) => part.type === "timeZoneName")
      ?.value || "";
  return el(
    "div",
    { class: "auth-reset-credits" },
    el("div", { class: "auth-reset-credits-title", text: m("ui.reset_credits_expiry", { v0: timezone }) }),
    credits.map((credit, index) => {
      const expiresAt = new Date(credit.expires_at);
      const left = (expiresAt.getTime() - Date.now()) / 1000;
      const time = Number.isFinite(left)
        ? [left > 0 ? compactTimeUntil(left, "left") : m("ui.expired"), DISPLAY_SEPARATOR, compactResetDate(expiresAt)]
        : [credit.expires_at || "—"];
      return el(
        "div",
        { class: "auth-reset-credit" },
        el("span", { text: m("ui.reset_credit_number", { v0: index + 1 }) }),
        el("span", { class: "auth-reset-credit-time" }, time)
      );
    })
  );
}

function canRefreshAuthQuota(file) { return file.quota_supported && !file.disabled; }

function authQuotaRefreshTitle(file) {
  if (file.quota_supported) return null;
  return (
    window.billingI18n.serverMessage(file.quota_unavailable_message, file.quota_unavailable_reason) ||
    m("ui.quota_queries_are_not_supported_for_this_auth_file_type")
  );
}

function renderAuthFileQuota(file, account) {
  const ui = authUI(account);
  const owner = ui.owner;
  const index = file.auth_index;
  const result = authFileQuota(file, owner);
  if (owner.authQuotaLoading.has(index) && !result) {
    return el("div", { class: "auth-file-quota" }, el("div", { class: "muted small", text: m("ui.updating_quotas") }));
  }
  const error = owner.authQuotaErrors.get(index);
  if (error && !result) return el("div", { class: "auth-file-quota" }, el("div", { class: "credential-error", text: error }));
  if (!result) return el("div", { class: "muted small", text: authQuotaRefreshTitle(file) || m("ui.refresh_to_query_quota") });
  const summary = [];
  const balance = Number(result.credit_balance);
  if (result.credits_unlimited || (Number.isFinite(balance) && balance !== 0)) {
    const credits = result.credits_unlimited ? m("ui.unlimited") : quotaNumber(balance, result.credit_currency);
    summary.push(el("span", { text: m("ui.credits_value", { v0: credits }) }));
  }
  if (Number.isFinite(result.rate_limit_reset_credits_available_count)) {
    summary.push(el("span", { text: m("ui.available_resets_value", { v0: int(result.rate_limit_reset_credits_available_count) }) }));
  }
  const rows = result.quota;
  return el(
    "div",
    { class: "auth-file-quota" },
    summary.length ? el("div", { class: "auth-quota-summary" }, summary) : null,
    error && el("div", { class: "credential-error", text: error }),
    rows.length
      ? el("div", { class: "auth-quota-list" }, rows.map((row) => renderQuotaRow(row, ui.estimate?.checked === true)))
      : el("div", { class: "muted small", text: m("ui.the_upstream_returned_no_displayable_quota") })
  );
}

function renderAuthFiles(account) {
  const ui = authUI(account);
  if ($("auth-drawer").open && authDrawer.account === account) renderAuthDrawerFile();
  if (renderDataPending(ui.body, [ui.files])) {
    ui.bulk.disabled = true;
    return;
  }
  const files = filteredAuthFiles(account);
  const refreshable = files.filter(canRefreshAuthQuota).length;
  ui.bulk.disabled = refreshable === 0 || ui.bulk.dataset.loading === "true";
  renderCollection(ui.body, files, {
    headers: [
      m("ui.auth_files"),
      m("ui.requests"),
      m("ui.auth_total_tokens"),
      m("ui.cache_rate"),
      { label: m("ui.quota"), class: "credential-quota-column" },
      m("ui.actions")
    ],
    attrs: { class: "credential-table" },
    empty: m("ui.no_auth_files_match_the_filters"),
    render: (file) => renderAuthFileRow(file, account)
  });
}

function renderAuthFileRow(file, account) {
  const ui = authUI(account);
  const quota = authFileQuota(file, ui.owner);
  const loading = ui.owner.authQuotaLoading.has(file.auth_index);
  const resettable =
    (!account || resources.account.profile.value?.can_reset_auth_quota === true) &&
    Number(quota?.rate_limit_reset_credits_available_count) > 0;
  const quotaActions = el(
    "div",
    { class: "auth-quota-actions" },
    resettable &&
      el(
        "button",
        {
          type: "button",
          class: "labeled-icon-button round-icon-button",
          "aria-label": m("ui.reset_quota"),
          ...hoverTooltipProps(
            () => [el("span", { text: m("ui.reset_opportunities", { count: quota.rate_limit_reset_credits_available_count }) })],
            true
          ),
          disabled: !canResetAuthQuota(file, account),
          onclick: () => guard(() => resetAuthQuota(file, account))
        },
        actionIcon("rotate-cw-clock")
      ),
    el(
      "button",
      {
        type: "button",
        class: "labeled-icon-button round-icon-button" + (loading ? " loading" : ""),
        disabled: !canRefreshAuthQuota(file) || loading || ui.bulk.dataset.loading === "true",
        "aria-label": m("ui.update_quotas"),
        "data-tip": authQuotaRefreshTitle(file) || m("ui.update_quotas"),
        onclick: () => guard(() => refreshAuthQuota(file, account))
      },
      actionIcon("refresh")
    )
  );
  return el(
    "tr",
    { class: "credential-row" + (file.disabled ? " disabled" : ""), "data-auth-index": file.auth_index },

    el(
      "td",
      { class: "credential-identity-cell" },
      el(
        "div",
        { class: "credential-head" },
        authProviderIcon(file.category),
        el(
          "div",
          { class: "credential-identity" },
          el("div", {
            class: "credential-title mask-blur",
            "data-tip": file.email || file.name,
            "data-tip-overflow": "",
            text: file.email || file.name || m("ui.no_email_provided")
          }),
          authFileMeta(file, quota)
        )
      )
    ),
    renderCredentialUsage(file.usage),
    el(
      "td",
      { class: "credential-quota-cell" },
      el(
        "div",
        {
          class: "auth-quota-open",
          role: "button",
          tabindex: "0",
          "aria-haspopup": "dialog",
          "aria-label": m("ui.view_quota_history"),
          onclick: () => openAuthDrawer(file, account),
          onkeydown: (event) => {
            if (event.key !== "Enter" && event.key !== " ") return;
            event.preventDefault();
            openAuthDrawer(file, account);
          }
        },
        renderAuthFileQuota(file, account)
      )
    ),
    el(
      "td",
      { class: "credential-actions-cell" },
      el(
        "div",
        { class: "credential-actions" },
        quotaActions,
        !account &&
          switchControl({
            input: el("input", {
              type: "checkbox",
              role: "switch",
              checked: !file.disabled,
              "aria-label": m("ui.enable"),
              disabled: adminUIState.authStatusSubmissions.has(file.auth_index),
              onchange: (event) => guard(() => setAuthFileEnabled(file, event.target.checked))
            })
          })
      )
    )
  );
}

async function setAuthFileEnabled(file, enabled) {
  const owner = adminUIState;
  if (owner.authStatusSubmissions.has(file.auth_index)) return;
  owner.authStatusSubmissions.set(file.auth_index, !enabled);
  file.disabled = !enabled;
  renderAuthFiles(false);
  try {
    const body = { name: file.name, auth_index: file.auth_index, disabled: !enabled };
    await api("PATCH", "/v0/management/auth-files/status", body);
  } catch (error) {
    owner.authStatusSubmissions.delete(file.auth_index);
    file.disabled = enabled;
    if (owner === adminUIState) renderAuthFiles(false);
    if (error instanceof AuthError || error instanceof StaleRequestError) throw error;
    throw new UIError(m(enabled ? "ui.enable_auth_file_failed_value" : "ui.disable_auth_file_failed_value", { v0: error.message }));
  }
  try {
    // A list read already in flight started before this change, so only the next read confirms it.
    await authFilesQueries.admin.task?.catch(() => {});
    await readAuthFiles(false, true);
  } finally {
    owner.authStatusSubmissions.delete(file.auth_index);
    if (owner === adminUIState) renderAuthFiles(false);
  }
}

const AUTH_QUOTA_TIMEOUT_MS = 65000;
const AUTH_QUOTA_BATCH_CONCURRENCY = 3;

function canResetAuthQuota(file, account) {
  if (currentRole !== (account ? "account" : "admin") || (account && resources.account.profile.value?.can_reset_auth_quota !== true))
    return false;
  const owner = account ? accountUIState : adminUIState;
  return (
    file.quota_supported && !file.disabled && !owner.authQuotaLoading.has(file.auth_index) &&
    Number(owner.authQuotas.get(file.auth_index)?.rate_limit_reset_credits_available_count) > 0
  );
}

async function resetAuthQuota(file, account) {
  if (!canResetAuthQuota(file, account)) return;
  const owner = account ? accountUIState : adminUIState;
  const generation = authQuotaGeneration[account ? "account" : "admin"], index = file.auth_index;
  const quota = owner.authQuotas.get(index);
  await openActionDialog(
    m("ui.reset_provider_quota", { v0: PROVIDER_LABELS[file.category] || file.category }),
    el(
      "div",
      { class: "auth-reset-confirmation" },
      el(
        "div",
        { class: "auth-reset-available" },
        el("span", { class: "auth-reset-count-badge", text: int(quota.rate_limit_reset_credits_available_count) }),
        m("ui.available_reset_count")
      ),
      el("div", { text: m("ui.consume_one_reset_now") }),
      renderAuthResetCredits(file, quota)
    ),
    undefined,
    async () => {
      if (!authQuotaCacheCurrent(account, owner, generation, index) || !canResetAuthQuota(file, account)) {
        throw new UIError(m("ui.the_auth_file_or_quota_changed_close_this_dialog_update_the_quota_and_try_again"));
      }
      owner.authQuotaLoading.add(index);
      owner.authQuotas.delete(index);
      owner.authQuotaErrors.delete(index);
      persistAuthQuotaCache(account);
      renderAuthFiles(account);
      let resetConfirmed = false, cooldownCleared = true;
      try {
        const params = new URLSearchParams({ auth_index: file.auth_index, auth_name: file.name, auth_revision: file.cache_revision || "" });
        const path = "/auth-files/quota/reset?" + params;
        // getRandomValues also works on HTTP installations where randomUUID is unavailable.
        const bytes = crypto.getRandomValues(new Uint8Array(16));
        bytes[6] = (bytes[6] & 0x0f) | 0x40;
        bytes[8] = (bytes[8] & 0x3f) | 0x80;
        const resetID = Array.from(
          bytes,
          (byte, index) => ([4, 6, 8, 10].includes(index) ? "-" : "") + byte.toString(16).padStart(2, "0")
        ).join("");
        const opts = { timeoutMS: AUTH_QUOTA_TIMEOUT_MS, headers: { "X-Quota-Reset-ID": resetID }, keepSessionOnForbidden: account };
        const result = account ? await accountAPI(path, opts) : await plugin("POST", path, null, opts);
        if (result?.reset !== true) throw new UIError(m("ui.reset_was_not_confirmed_update_the_quota_to_check_its_current_status"));
        resetConfirmed = true;
        cooldownCleared = result.cooldown_cleared === true;
      } finally {
        if (authQuotaCacheCurrent(account, owner, generation, index)) {
          owner.authQuotaLoading.delete(index);
          if (!resetConfirmed)
            owner.authQuotaErrors.set(index, m("ui.reset_was_not_confirmed_update_the_quota_to_check_its_current_status"));
          renderAuthFiles(account);
        }
      }
      if (!authQuotaCacheCurrent(account, owner, generation, index)) return;
      await refreshAuthQuota(file, account);
      if (authQuotaCacheCurrent(account, owner, generation, index)) {
        const failed = owner.authQuotaErrors.has(index);
        if (!cooldownCleared) {
          notify(m("ui.quota_reset_but_cooldown_clear_failed"), "err");
          return;
        }
        notify(
          failed
            ? m("ui.quota_reset_but_refresh_failed_please_update_the_quota_manually")
            : m("ui.reset_quota_for_value", { v0: file.name }),
          failed ? "err" : "ok"
        );
      }
    },
    m("ui.confirm_reset")
  );
}

// Fetching quota updates memory only. Single and batch actions own their
// display and persistence, so a batch never redraws the list per response.
async function loadAuthQuota(file, account) {
  const owner = account ? accountUIState : adminUIState;
  const role = account ? "account" : "admin";
  const generation = authQuotaGeneration[role];
  if (owner.authQuotaLoading.has(file.auth_index)) return;
  owner.authQuotaLoading.add(file.auth_index);
  owner.authQuotaErrors.delete(file.auth_index);
  try {
    const path = "/auth-files/quota?auth_index=" + encodeURIComponent(file.auth_index);
    const opts = { timeoutMS: AUTH_QUOTA_TIMEOUT_MS };
    const result = account ? await accountAPI(path, opts) : await plugin("GET", path, null, opts);
    requireObject(result, m("ui.upstream_quota"));
    requireArray(result.quota, m("ui.upstream_quota"));
    if (authQuotaCacheCurrent(account, owner, generation, file.auth_index)) owner.authQuotas.set(file.auth_index, result);
  } catch (error) {
    if (error instanceof AuthError || error instanceof StaleRequestError) throw error;
    if (authQuotaCacheCurrent(account, owner, generation, file.auth_index))
      owner.authQuotaErrors.set(file.auth_index, error.message || String(error));
  } finally { if (authQuotaCacheCurrent(account, owner, generation, file.auth_index)) owner.authQuotaLoading.delete(file.auth_index); }
}

async function refreshAuthQuota(file, account) {
  if (!canRefreshAuthQuota(file)) return;
  const role = account ? "account" : "admin";
  const generation = authQuotaGeneration[role];
  const task = loadAuthQuota(file, account);
  renderAuthFiles(account);
  try { await task; } finally {
    if (generation === authQuotaGeneration[role]) {
      persistAuthQuotaCache(account);
      renderAuthFiles(account);
    }
  }
}

async function refreshFilteredAuthQuotas(account) {
  const ui = authUI(account);
  const role = account ? "account" : "admin";
  const generation = authQuotaGeneration[role];
  const files = filteredAuthFiles(account).filter((file) => canRefreshAuthQuota(file) && !ui.owner.authQuotaLoading.has(file.auth_index));
  if (!files.length || ui.bulk.dataset.loading === "true") return;
  ui.bulk.dataset.loading = "true";
  const progress = el("span", { text: m("ui.updating_0_value", { v0: files.length }) });
  ui.bulk.removeAttribute("aria-label");
  ui.bulk.replaceChildren(actionIcon("refresh"), progress);
  renderAuthFiles(account);
  let next = 0, completed = 0;
  try {
    const worker = async () => {
      while (next < files.length && generation === authQuotaGeneration[role]) {
        await loadAuthQuota(files[next++], account);
        setText(progress, m("ui.updating_value_value", { v0: ++completed, v1: files.length }));
      }
    };
    await settleLoads(Array.from({ length: Math.min(AUTH_QUOTA_BATCH_CONCURRENCY, files.length) }, worker));
  } finally {
    if (generation === authQuotaGeneration[role]) {
      persistAuthQuotaCache(account);
      resetAuthQuotaBatch(account);
      renderAuthFiles(account);
    }
  }
}

// The drawer shows one auth file over the last 30 days: its usage, its manual resets and the periods of each quota window.
const authDrawer = { account: false, file: null, history: null, error: "", window: 0, request: 0 };
const AUTH_RESET_PROVIDERS = ["codex", "claude"];
const QUOTA_PERIOD_ENDS = { scheduled: ["plain", "ui.quota_period_scheduled"], early_reset: ["warn", "ui.quota_period_early_reset"] };

function openAuthDrawer(file, account) {
  closeHoverTooltip();
  Object.assign(authDrawer, { account, file, history: null, error: "", window: 0 });
  renderAuthDrawerFile();
  renderAuthDrawerHistory();
  $("auth-drawer").showModal();
  guard(loadAuthDrawerHistory);
  // Manual resets come only from a quota query.
  const ui = authUI(account);
  if (
    AUTH_RESET_PROVIDERS.includes(file.category) && canRefreshAuthQuota(file) && !ui.owner.authQuotas.has(file.auth_index) &&
    ui.bulk.dataset.loading !== "true"
  )
    guard(() => refreshAuthQuota(file, account));
}

async function loadAuthDrawerHistory() {
  const { account, file } = authDrawer;
  const request = ++authDrawer.request;
  try {
    const path = "/auth-files/history?auth_index=" + encodeURIComponent(file.auth_index);
    const history = requireObject(account ? await accountAPI(path) : await plugin("GET", path), m("ui.quota_windows"));
    requireArray(history.windows, m("ui.quota_windows"));
    if (request === authDrawer.request) Object.assign(authDrawer, { history, error: "" });
  } catch (error) {
    if (error instanceof AuthError || error instanceof StaleRequestError) throw error;
    if (request === authDrawer.request) authDrawer.error = error.message || String(error);
  } finally { if (request === authDrawer.request) renderAuthDrawerHistory(); }
}

function renderAuthDrawerFile() {
  const ui = authUI(authDrawer.account);
  const file = (ui.files.value || []).find((item) => item.auth_index === authDrawer.file.auth_index) || authDrawer.file;
  const title = file.email || file.name;
  authDrawer.file = file;
  $("auth-drawer-identity").replaceChildren(
    authProviderIcon(file.category),
    el(
      "div",
      { class: "drawer-title" },
      el("h2", {
        id: "auth-drawer-title",
        class: "mask-blur",
        "data-tip": title,
        "data-tip-overflow": "",
        text: title || m("ui.no_email_provided")
      }),
      authFileMeta(file, authFileQuota(file, ui.owner))
    )
  );
  renderAuthDrawerResets(file, authDrawer.account);
}

function renderAuthDrawerResets(file, account) {
  $("auth-drawer-reset-section").classList.toggle("hidden", !AUTH_RESET_PROVIDERS.includes(file.category));
  const owner = authUI(account).owner;
  const result = owner.authQuotas.get(file.auth_index);
  const error = owner.authQuotaErrors.get(file.auth_index);
  const count = result?.rate_limit_reset_credits_available_count;
  setText($("auth-drawer-reset-count"), Number.isFinite(count) ? int(count) : "");
  const reset = $("auth-drawer-reset");
  reset.classList.toggle("hidden", account && resources.account.profile.value?.can_reset_auth_quota !== true);
  reset.disabled = !canResetAuthQuota(file, account);
  reset.onclick = () => guard(() => resetAuthQuota(file, account));
  const status = (text) => el("div", { class: "muted small", text });
  const content = owner.authQuotaLoading.has(file.auth_index)
    ? status(m("ui.updating_quotas"))
    : error && !result
      ? el("div", { class: "credential-error", text: error })
      : !result
        ? status(authQuotaRefreshTitle(file) || m("ui.refresh_to_query_quota"))
        : (renderAuthResetCredits(file, result) ?? (count === 0 ? status(m("ui.no_manual_resets_available")) : null));
  $("auth-drawer-resets").replaceChildren(...(content ? [content] : []));
}

function renderAuthDrawerHistory() {
  const { history, error } = authDrawer;
  const switcher = $("auth-drawer-window-switch");
  switcher.classList.toggle("hidden", !history?.windows.length);
  if (!history) {
    $("auth-drawer-usage").replaceChildren(
      ...(error
        ? [
            el("div", { class: "credential-error", text: error }),
            el("button", { class: "retry-button", onclick: () => retryAuthDrawerHistory() }, m("ui.retry"))
          ]
        : [el("div", { class: "muted small", text: m("ui.loading") })])
    );
    $("auth-drawer-models").replaceChildren();
    switcher.replaceChildren();
    $("auth-drawer-periods").replaceChildren();
    return;
  }
  const models = history.usage.models;
  $("auth-drawer-usage").replaceChildren(renderAuthDrawerUsage(history.usage));
  $("auth-drawer-models").replaceChildren(...(models?.length ? [renderModelUsageTable(models)] : []));
  if (authDrawer.window >= history.windows.length) authDrawer.window = 0;
  switcher.replaceChildren(
    ...history.windows.map((window, index) =>
      el(
        "button",
        {
          type: "button",
          role: "tab",
          onclick: () => {
            authDrawer.window = index;
            renderAuthDrawerPeriods();
          }
        },
        quotaLabel(window)
      )
    )
  );
  renderAuthDrawerPeriods();
}

function retryAuthDrawerHistory() {
  authDrawer.error = "";
  renderAuthDrawerHistory();
  guard(loadAuthDrawerHistory);
}

function renderAuthDrawerUsage(usage) {
  const { count, success, total } = credentialUsageSummary(usage);
  const stat = (label, value, tooltip) =>
    el(
      "div",
      { class: "inset-card drawer-stat" },
      el("span", { class: "drawer-stat-label", text: label }),
      el("span", { class: "drawer-stat-value", "data-tip": tooltip }, value)
    );
  return el(
    "div",
    { class: "drawer-stats" },
    stat(m("ui.requests"), tokens(count, 2), count >= 1000 ? int(count) : null),
    stat(m("ui.success_rate"), success),
    stat(m("ui.auth_total_tokens"), tokens(total), total >= 1000 ? exactTokens(total) : null),
    stat(m("ui.cost"), usd(usage.cost_usd, 2))
  );
}

function renderAuthDrawerPeriods() {
  const { history, window: selected, account, file } = authDrawer;
  for (const [index, button] of Array.from($("auth-drawer-window-switch").children).entries()) {
    button.classList.toggle("active", index === selected);
    button.setAttribute("aria-selected", String(index === selected));
  }
  const window = history.windows[selected];
  $("auth-drawer-periods").replaceChildren(
    window
      ? el("div", { class: "quota-period-list" }, window.periods.map((period) => renderQuotaPeriod(period, file.category, !account)))
      : el("div", { class: "muted small", text: m("ui.no_quota_windows_reported") })
  );
}

// History lists period totals; a period's breakdown by model loads on its first hover.
const authPeriodModels = new WeakMap();

function periodModelsTooltip(period) {
  if (!(period.usage.tokens > 0)) return null;
  return () => {
    const state = authPeriodModels.get(period);
    if (state?.models) return [renderModelUsageTable(state.models)];
    if (state?.error) {
      authPeriodModels.delete(period);
      return [el("div", { class: "hover-tooltip-body", text: state.error })];
    }
    if (!state) guard(() => loadPeriodModels(period));
    return [el("div", { class: "hover-tooltip-body", text: m("ui.loading") })];
  };
}

async function loadPeriodModels(period) {
  const { account, file } = authDrawer;
  const query = period.usage_query;
  authPeriodModels.set(period, { loading: true });
  try {
    const params = new URLSearchParams({ auth_index: file.auth_index, from: query.start, to: query.end });
    for (const model of query.models.include ?? []) params.append("include", model);
    for (const model of query.models.exclude ?? []) params.append("exclude", model);
    const path = "/auth-files/history/models?" + params;
    const result = requireObject(account ? await accountAPI(path) : await plugin("GET", path), m("ui.quota_windows"));
    authPeriodModels.set(period, { models: requireArray(result.models, m("ui.quota_windows")) });
  } catch (error) {
    if (error instanceof AuthError || error instanceof StaleRequestError) throw error;
    authPeriodModels.set(period, { error: error.message || String(error) });
  } finally { refreshHoverTooltip(); }
}

// Estimates need the usage of every request, which API key views do not have.
function renderQuotaPeriod(period, category, estimate) {
  const remaining = Math.max(0, 100 - period.used_percent);
  const end = period.end || period.reset_at;
  const [tone, reason] = QUOTA_PERIOD_ENDS[period.end_reason] || ["ok", "ui.quota_period_running"];
  const range = quotaPeriodRange(new Date(period.start), end && new Date(end));
  const usage = (label, metrics, tooltip) =>
    el("span", { class: "quota-period-usage", ...hoverTooltipProps(tooltip) }, el("span", { text: label }), metrics);
  return el(
    "div",
    { class: "auth-quota-row" },
    el(
      "div",
      { class: "auth-quota-head" },
      el("span", { class: "auth-quota-label", text: range }),
      el("span", { class: "tag " + tone, text: m(reason) }),
      el("span", { class: "auth-quota-value", text: remaining.toFixed(0) + "%" })
    ),
    renderQuotaBar(remaining, range),
    el(
      "div",
      { class: "auth-quota-foot" },
      usage(m("ui.quota_used"), renderQuotaMetrics(period.usage), periodModelsTooltip(period)),
      estimate && usage(m("ui.quota_projected"), renderQuotaMetrics(period.usage, quotaEstimateScale(period.used_percent, period.usage))),
      period.plans?.length > 0 &&
        el(
          "span",
          { class: "quota-period-plans" },
          period.plans.flatMap((plan, index) => [
            index > 0 && "→",
            el("span", { class: "tag " + authPlanTone(category, plan), text: plan })
          ])
        )
    )
  );
}

function quotaPeriodRange(start, end) {
  const from = compactResetDate(start);
  if (!end) return from + " – …";
  const to = compactResetDate(end);
  return from + " – " + (to.slice(0, 5) === from.slice(0, 5) ? to.slice(6) : to);
}

$("auth-drawer-close").replaceChildren(actionIcon("x"));
setLabeledActionIcon($("auth-drawer-reset"), "rotate-cw-clock", m("ui.reset_quota"));
$("auth-drawer-close").onclick = () => $("auth-drawer").close();
$("auth-drawer").addEventListener("click", (event) => { if (event.target === event.currentTarget) event.currentTarget.close(); });
$("auth-drawer").addEventListener("close", () => authDrawer.request++);

for (const account of [false, true]) {
  const ui = authUI(account);
  const role = account ? "account" : "admin";
  setLabeledActionIcon(ui.bulk, "refresh", m("ui.update_quotas"));
  for (const filter of [ui.enabledOnly, ui.estimate, ui.provider].filter(Boolean)) {
    filter.addEventListener("change", () => {
      saveChoice(role, filter.id);
      renderAuthFiles(account);
    });
  }
  ui.search.addEventListener("input", () => renderAuthFiles(account));
  ui.bulk.onclick = () => guard(() => refreshFilteredAuthQuotas(account));
}
