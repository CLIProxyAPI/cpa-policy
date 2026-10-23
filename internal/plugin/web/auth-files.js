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

function authFileQuota(file, owner) { return owner.authQuotas.get(file.auth_index); }

function authFileMeta(file, quota) {
  const plan = quota?.plan?.trim();
  return el(
    "div",
    { class: "credential-meta" },
    el("span", { class: "tag plain", text: PROVIDER_LABELS[file.category] || file.category }),
    plan && el("span", { class: "tag " + authPlanTone(file.category, plan) }, el("span", { text: plan })),
    file.unavailable && el("span", { class: "tag bad", text: m("ui.unavailable") })
  );
}

function authProviderIcon(category) {
  return providerIcon(category === "gemini-cli" || category === "gemini-interactions" ? "gemini" : category);
}

function authUI() {
  const prefix = "auth";
  return {
    owner: adminUIState,
    files: authFilesQueries["admin"],
    body: $(prefix + "-files-body"),
    enabledOnly: $(prefix + "-enabled-only"),

    search: $(prefix + "-search"),
    bulk: $(prefix + "-refresh-filtered")
  };
}

const AUTH_QUOTA_CACHE_PREFIX = "cpa-codex-controls.auth-quotas.v7.";

function resetAuthQuotaMemory() {
  const role = "admin";
  const owner = adminUIState;
  authQuotaGeneration[role]++;
  owner.authQuotas.clear();
  owner.authQuotaLoading.clear();
  owner.authQuotaErrors.clear();
  owner.authFileRevisions.clear();
  resetAuthQuotaBatch();
}

function resetAuthQuotaBatch() {
  const bulk = authUI().bulk;
  bulk.dataset.loading = "false";
  setLabeledActionIcon(bulk, "refresh", m("ui.update_quotas"));
}

function authQuotaStorageKey(role) {
  const scope = credentialScope[role];
  return scope ? AUTH_QUOTA_CACHE_PREFIX + role + "." + scope : "";
}

function persistAuthQuotaCache() {
  const role = "admin";
  const key = authQuotaStorageKey(role);
  if (!key) return;
  const owner = adminUIState;
  try { sessionStorage.setItem(key, JSON.stringify({ entries: [...owner.authQuotas] })); } catch (_) {}
}

function restoreAuthQuotaCache() {
  const role = "admin";
  let cached;
  try { cached = JSON.parse(sessionStorage.getItem(authQuotaStorageKey(role)) || "null"); } catch (_) { return; }
  if (!Array.isArray(cached?.entries)) return;
  const owner = adminUIState;
  for (const entry of cached.entries) {
    if (Array.isArray(entry) && typeof entry[0] === "string" && Array.isArray(entry[1]?.quota)) {
      owner.authQuotas.set(entry[0], entry[1]);
    }
  }
}

function readAuthFiles(reload = false) {
  return authFilesQueries.admin.read(
    "auth-files",
    async () => {
      const result = await plugin("GET", "/auth-files");
      return requireArray(result.files, m("ui.auth_files"));
    },
    reload
  );
}

function updateAuthFiles(files) {
  const role = "admin";
  const owner = adminUIState;
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
  resetAuthQuotaBatch();
  for (const index of owner.authQuotas.keys()) if (staleIndex(index)) owner.authQuotas.delete(index);
  for (const index of owner.authQuotaErrors.keys()) if (staleIndex(index)) owner.authQuotaErrors.delete(index);
  owner.authQuotaLoading.clear();
  persistAuthQuotaCache();
}

function authQuotaCacheCurrent(owner, generation, index) {
  const role = "admin";
  const currentOwner = adminUIState;
  return owner === currentOwner && generation === authQuotaGeneration[role] && owner.authFileRevisions.has(index);
}

function filteredAuthFiles() {
  const ui = authUI();
  const search = ui.search.value.trim().toLocaleLowerCase();
  return (ui.files.value || []).filter((file) => {
    if (ui.enabledOnly.checked && file.disabled) return false;
    if (!search) return true;
    const currentPlan = authFileQuota(file, ui.owner)?.plan;
    return [file.name, file.email, currentPlan, file.category].some((value) => String(value || "").toLocaleLowerCase().includes(search));
  });
}

function quotaResetMeta(row) {
  const resetAt = row.reset_at ? new ZonedDate(row.reset_at) : null;
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

function renderQuotaRow(row) {
  const label = quotaLabel(row);
  const percent = row.remaining_percent;
  const reset = quotaResetMeta(row);
  const elapsed = row.reset_at && new ZonedDate(row.reset_at).getTime() <= Date.now();
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
    (amount || reset.at) &&
      el(
        "div",
        { class: "auth-quota-foot" },
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
    new Intl.DateTimeFormat("en", billingDateOptions({ timeZoneName: "shortOffset" })).formatToParts(new ZonedDate())
      .find((part) => part.type === "timeZoneName")?.value || "";
  return el(
    "div",
    { class: "auth-reset-credits" },
    el("div", { class: "auth-reset-credits-title", text: m("ui.reset_credits_expiry", { v0: timezone }) }),
    credits.map((credit, index) => {
      const expiresAt = new ZonedDate(credit.expires_at);
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

function canRefreshAuthQuota(file) {
  return (
    file.quota_supported && !file.disabled && !adminUIState.authCredentialRefreshing.has(file.auth_index) &&
    !adminUIState.authStatusSubmissions.has(file.auth_index)
  );
}

function authQuotaRefreshTitle(file) {
  if (file.quota_supported) return null;
  return (
    window.billingI18n.serverMessage(file.quota_unavailable_message, file.quota_unavailable_reason) ||
    m("ui.quota_queries_are_not_supported_for_this_auth_file_type")
  );
}

function refreshAuthQuotaFromControl(file, event) {
  const focused = event.currentTarget === document.activeElement;
  const generation = sessionGeneration;
  return guard(async () => {
    try { await refreshAuthQuota(file); } finally {
      if (focused && generation === sessionGeneration && activeTab() === "auth-files" && document.activeElement === document.body) {
        const row = [...authUI().body.querySelectorAll("[data-auth-index]")].find((node) => node.dataset.authIndex === file.auth_index);
        row?.querySelector(".auth-quota-refresh, .auth-quota-prompt")?.focus({ preventScroll: true });
      }
    }
  });
}

function authQuotaRefreshArea(file, ...content) {
  const ui = authUI();
  const loading = ui.owner.authQuotaLoading.has(file.auth_index);
  return el(
    "div",
    { class: "auth-quota-content auth-quota-open" + (loading ? " loading" : "") },
    el("div", { class: "auth-quota-details" }, ...content),
    el(
      "button",
      {
        type: "button",
        class: "auth-quota-refresh" + (loading ? " loading" : ""),
        disabled: !canRefreshAuthQuota(file) || loading || ui.bulk.dataset.loading === "true",
        "aria-label": m(loading ? "ui.updating_quotas" : "ui.refresh_to_query_quota"),
        "aria-busy": String(loading),
        "data-tip": authQuotaRefreshTitle(file) || m("ui.refresh_to_query_quota"),
        onclick: (event) => refreshAuthQuotaFromControl(file, event)
      },
      loading && actionIcon("refresh"),
      loading && m("ui.updating_quotas")
    )
  );
}

function renderAuthFileQuota(file) {
  const ui = authUI();
  const owner = ui.owner;
  const index = file.auth_index;
  const result = authFileQuota(file, owner);
  const loading = owner.authQuotaLoading.has(index);
  const error = owner.authQuotaErrors.get(index);
  if (!result) {
    if (!file.quota_supported) return el("div", { class: "muted small", text: authQuotaRefreshTitle(file) });
    if (loading || !error)
      return el(
        "button",
        {
          type: "button",
          class: "auth-quota-prompt labeled-icon-button small" + (loading ? " loading" : ""),
          "aria-busy": String(loading),
          disabled: loading || !canRefreshAuthQuota(file) || ui.bulk.dataset.loading === "true",
          onclick: (event) => refreshAuthQuotaFromControl(file, event)
        },
        loading && actionIcon("refresh"),
        m(loading ? "ui.updating_quotas" : "ui.refresh_to_query_quota")
      );
    return el(
      "div",
      { class: "auth-file-quota" },
      authQuotaRefreshArea(file, el("div", { class: "credential-error", text: error })),
      renderAuthQuotaActions(file)
    );
  }
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
    authQuotaRefreshArea(
      file,
      summary.length ? el("div", { class: "auth-quota-summary" }, summary) : null,
      error && el("div", { class: "credential-error", text: error }),
      rows.length
        ? el("div", { class: "auth-quota-list" }, rows.map((row) => renderQuotaRow(row)))
        : el("div", { class: "muted small", text: m("ui.the_upstream_returned_no_displayable_quota") })
    ),
    renderAuthQuotaActions(file)
  );
}

function renderAuthFiles() {
  const ui = authUI();
  if (renderDataPending(ui.body, [ui.files])) {
    ui.bulk.disabled = true;
    return;
  }
  const files = filteredAuthFiles();
  const refreshable = files.filter(canRefreshAuthQuota).length;
  ui.bulk.disabled = refreshable === 0 || ui.bulk.dataset.loading === "true";
  renderCollection(ui.body, files, {
    headers: [
      m("ui.auth_files"),
      { label: m("ui.quota"), class: "credential-quota-column" },
      { label: m("health.title"), class: "credential-health-column" },
      { label: m("policy.concurrency_column"), class: "credential-concurrency-column" },
      { label: m("ui.actions"), class: "credential-actions-column" }
    ],
    attrs: { class: "credential-table policy-credential-table" },
    empty: m("ui.no_auth_files_match_the_filters"),
    render: (file) => renderAuthFileRow(file)
  });
}

function renderAuthQuotaActions(file) {
  const ui = authUI();
  const quota = authFileQuota(file, ui.owner);
  const resettable = Number(quota?.rate_limit_reset_credits_available_count) > 0;
  if (!resettable) return null;
  return el(
    "div",
    { class: "auth-quota-actions" },
    el(
      "button",
      {
        type: "button",
        class: "labeled-icon-button credential-icon-button",
        "aria-label": m("ui.reset_quota"),
        ...hoverTooltipProps(
          () => [
            el("div", { text: m("ui.reset_quota") }),
            el("div", { text: m("ui.reset_opportunities", { count: quota.rate_limit_reset_credits_available_count }) })
          ],
          true
        ),
        disabled: !canResetAuthQuota(file),
        onclick: () => guard(() => resetAuthQuota(file))
      },
      actionIcon("rotate-cw-clock")
    )
  );
}

function renderAuthFileRow(file) {
  const ui = authUI();
  const quota = authFileQuota(file, ui.owner);
  const refreshing = ui.owner.authCredentialRefreshing.has(file.auth_index);
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
    el("td", { class: "credential-quota-cell" }, renderAuthFileQuota(file)),
    credentialHealthCell(file, "auth"),
    credentialConcurrencyCell(file.auth_index),
    el(
      "td",
      { class: "credential-actions-cell" },
      el(
        "div",
        { class: "credential-actions" },
        el(
          "button",
          {
            type: "button",
            class: "labeled-icon-button credential-icon-button" + (refreshing ? " loading" : ""),
            disabled: !canRefreshAuthCredential(file),
            "aria-label": m("ui.refresh_credentials"),
            "aria-busy": String(refreshing),
            "data-tip": m(refreshing ? "ui.refreshing_credentials" : "ui.refresh_credentials"),
            onclick: () => guard(() => refreshAuthCredential(file))
          },
          actionIcon("refresh")
        ),
        credentialSettingsButton(file, "auth", refreshing),

        switchControl({
          input: el("input", {
            type: "checkbox",
            role: "switch",
            checked: !file.disabled,
            "aria-label": m("ui.enable"),
            disabled: adminUIState.authStatusSubmissions.has(file.auth_index) || refreshing,
            onchange: (event) => guard(() => setAuthFileEnabled(file, event.target.checked))
          })
        })
      )
    )
  );
}

function canRefreshAuthCredential(file) {
  const owner = adminUIState;
  return !!file.name && !!file.auth_index && canRefreshAuthQuota(file) && !owner.authQuotaLoading.has(file.auth_index);
}

async function refreshAuthCredential(file) {
  if (!canRefreshAuthCredential(file)) return;
  const owner = adminUIState;
  owner.authCredentialRefreshing.add(file.auth_index);
  renderAuthFiles();
  try {
    // The host owns token rotation; never persist or display its credential response.
    const response = await api(
      "POST",
      "/v0/management/auth-files/refresh",
      { name: file.name, auth_index: file.auth_index },
      { command: true, timeoutMS: 65000 }
    );
    if (response?.ok !== true) throw new UIError(m("ui.invalid_server_response"));
    try {
      // Wait for an earlier list read before requesting the refreshed credential state.
      await authFilesQueries.admin.task?.catch(() => {});
      if (owner !== adminUIState) return;
      await readAuthFiles(true);
      if (authFilesQueries.admin.error) throw new UIError(authFilesQueries.admin.error);
    } catch (error) {
      if (error instanceof AuthError || error instanceof StaleRequestError) throw error;
      notify(m("ui.credentials_refreshed_list_failed"), "err");
      return;
    }
    notify(m("ui.credentials_refreshed", { name: file.email || file.name }), "ok");
  } catch (error) {
    if (error instanceof AuthError || error instanceof StaleRequestError) throw error;
    throw new UIError(m("ui.refresh_credentials_failed", { message: error.message || String(error) }));
  } finally {
    owner.authCredentialRefreshing.delete(file.auth_index);
    if (owner === adminUIState) renderAuthFiles();
  }
}

async function setAuthFileEnabled(file, enabled) {
  const owner = adminUIState;
  if (owner.authStatusSubmissions.has(file.auth_index)) return;
  owner.authStatusSubmissions.set(file.auth_index, !enabled);
  file.disabled = !enabled;
  renderAuthFiles();
  try {
    const body = { name: file.name, auth_index: file.auth_index, disabled: !enabled };
    await api("PATCH", "/v0/management/auth-files/status", body);
  } catch (error) {
    owner.authStatusSubmissions.delete(file.auth_index);
    file.disabled = enabled;
    if (owner === adminUIState) renderAuthFiles();
    if (error instanceof AuthError || error instanceof StaleRequestError) throw error;
    throw new UIError(m(enabled ? "ui.enable_auth_file_failed_value" : "ui.disable_auth_file_failed_value", { v0: error.message }));
  }
  try {
    // A list read already in flight started before this change, so only the next read confirms it.
    await authFilesQueries.admin.task?.catch(() => {});
    await readAuthFiles(true);
  } finally {
    owner.authStatusSubmissions.delete(file.auth_index);
    if (owner === adminUIState) renderAuthFiles();
  }
}

const AUTH_QUOTA_TIMEOUT_MS = 65000;
const AUTH_QUOTA_BATCH_CONCURRENCY = 3;

function canResetAuthQuota(file) {
  if (currentRole !== "admin") return false;
  const owner = adminUIState;
  return (
    canRefreshAuthQuota(file) && !owner.authQuotaLoading.has(file.auth_index) &&
    Number(owner.authQuotas.get(file.auth_index)?.rate_limit_reset_credits_available_count) > 0
  );
}

async function resetAuthQuota(file) {
  if (!canResetAuthQuota(file)) return;
  const owner = adminUIState;
  const generation = authQuotaGeneration["admin"], index = file.auth_index;
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
      if (!authQuotaCacheCurrent(owner, generation, index) || !canResetAuthQuota(file)) {
        throw new UIError(m("ui.the_auth_file_or_quota_changed_close_this_dialog_update_the_quota_and_try_again"));
      }
      owner.authQuotaLoading.add(index);
      owner.authQuotas.delete(index);
      owner.authQuotaErrors.delete(index);
      persistAuthQuotaCache();
      renderAuthFiles();
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
        const opts = { timeoutMS: AUTH_QUOTA_TIMEOUT_MS, headers: { "X-Quota-Reset-ID": resetID }, keepSessionOnForbidden: false };
        const result = await plugin("POST", path, null, opts);
        if (result?.reset !== true) throw new UIError(m("ui.reset_was_not_confirmed_update_the_quota_to_check_its_current_status"));
        resetConfirmed = true;
        cooldownCleared = result.cooldown_cleared === true;
      } finally {
        if (authQuotaCacheCurrent(owner, generation, index)) {
          owner.authQuotaLoading.delete(index);
          if (!resetConfirmed)
            owner.authQuotaErrors.set(index, m("ui.reset_was_not_confirmed_update_the_quota_to_check_its_current_status"));
          renderAuthFiles();
        }
      }
      if (!authQuotaCacheCurrent(owner, generation, index)) return;
      await refreshAuthQuota(file);
      if (authQuotaCacheCurrent(owner, generation, index)) {
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
async function loadAuthQuota(file) {
  const owner = adminUIState;
  const role = "admin";
  const generation = authQuotaGeneration[role];
  if (owner.authQuotaLoading.has(file.auth_index)) return;
  owner.authQuotaLoading.add(file.auth_index);
  owner.authQuotaErrors.delete(file.auth_index);
  try {
    const path = "/auth-files/quota?auth_index=" + encodeURIComponent(file.auth_index);
    const opts = { timeoutMS: AUTH_QUOTA_TIMEOUT_MS };
    const result = await plugin("GET", path, null, opts);
    requireObject(result, m("ui.upstream_quota"));
    requireArray(result.quota, m("ui.upstream_quota"));
    if (authQuotaCacheCurrent(owner, generation, file.auth_index)) owner.authQuotas.set(file.auth_index, result);
  } catch (error) {
    if (error instanceof AuthError || error instanceof StaleRequestError) throw error;
    if (authQuotaCacheCurrent(owner, generation, file.auth_index))
      owner.authQuotaErrors.set(file.auth_index, error.message || String(error));
  } finally { if (authQuotaCacheCurrent(owner, generation, file.auth_index)) owner.authQuotaLoading.delete(file.auth_index); }
}

async function refreshAuthQuota(file) {
  if (!canRefreshAuthQuota(file)) return;
  const role = "admin";
  const generation = authQuotaGeneration[role];
  const task = loadAuthQuota(file);
  renderAuthFiles();
  try { await task; } finally {
    if (generation === authQuotaGeneration[role]) {
      persistAuthQuotaCache();
      renderAuthFiles();
    }
  }
}

async function refreshFilteredAuthQuotas() {
  const ui = authUI();
  const role = "admin";
  const generation = authQuotaGeneration[role];
  const files = filteredAuthFiles().filter((file) => canRefreshAuthQuota(file) && !ui.owner.authQuotaLoading.has(file.auth_index));
  if (!files.length || ui.bulk.dataset.loading === "true") return;
  ui.bulk.dataset.loading = "true";
  const progress = el("span", { text: m("ui.updating_0_value", { v0: files.length }) });
  ui.bulk.removeAttribute("aria-label");
  ui.bulk.replaceChildren(actionIcon("refresh"), progress);
  renderAuthFiles();
  let next = 0, completed = 0;
  try {
    const worker = async () => {
      while (next < files.length && generation === authQuotaGeneration[role]) {
        await loadAuthQuota(files[next++]);
        setText(progress, m("ui.updating_value_value", { v0: ++completed, v1: files.length }));
      }
    };
    await settleLoads(Array.from({ length: Math.min(AUTH_QUOTA_BATCH_CONCURRENCY, files.length) }, worker));
  } finally {
    if (generation === authQuotaGeneration[role]) {
      persistAuthQuotaCache();
      resetAuthQuotaBatch();
      renderAuthFiles();
    }
  }
}

const AUTH_RESET_PROVIDERS = ["codex", "claude"];
{
  const ui = authUI();
  setLabeledActionIcon(ui.bulk, "refresh", m("ui.update_quotas"));
  for (const filter of [ui.enabledOnly])
    filter.addEventListener("change", () => {
      saveChoice("admin", filter.id);
      renderAuthFiles();
    });
  ui.search.addEventListener("input", () => renderAuthFiles());
  ui.bulk.onclick = () => guard(() => refreshFilteredAuthQuotas());
}
