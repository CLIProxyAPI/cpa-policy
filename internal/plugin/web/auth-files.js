const AUTH_CATEGORY_LABELS = { claude: "Claude", antigravity: "Antigravity", codex: "Codex", gemini: "Gemini", xai: "xAI", kimi: "Kimi" };

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
  const rows = new Map(manual.quota.map((row) => [authQuotaRowKey(row), row]));
  for (const row of observed.quota) {
    const previous = rows.get(authQuotaRowKey(row));
    if (!previous || new Date(row.observed_at) > new Date(previous.observed_at || manual.fetched_at)) rows.set(authQuotaRowKey(row), row);
  }
  const result = { ...observed, ...manual, quota: [...rows.values()] };
  for (const field of ["plan", "credit_balance", "credits_unlimited"])
    if (new Date(observed[field + "_observed_at"]) > new Date(manual.fetched_at)) result[field] = observed[field];
  return result;
}

function authQuotaRowKey(row) { return JSON.stringify([row.group_label || "", row.label_prefix || "", row.label || ""]); }

function authFileMeta(file, quota) {
  const plan = quota?.plan?.trim();
  return el(
    "div",
    { class: "auth-file-meta" },
    plan && el("span", { class: "tag " + authPlanTone(file.category, plan) }, el("span", { text: plan })),
    el("span", { class: "tag plain", text: "P" + file.priority }),
    file.unavailable && el("span", { class: "tag bad", text: m("ui.unavailable") })
  );
}

const AUTH_PROVIDER_ICONS = {
  claude: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" aria-hidden="true"><path fill="currentColor" d="M4.709 15.955l4.72-2.647.08-.23-.08-.128H9.2l-.79-.048-2.698-.073-2.339-.097-2.266-.122-.571-.121L0 11.784l.055-.352.48-.321.686.06 1.52.103 2.278.158 1.652.097 2.449.255h.389l.055-.157-.134-.098-.103-.097-2.358-1.596-2.552-1.688-1.336-.972-.724-.491-.364-.462-.158-1.008.656-.722.881.06.225.061.893.686 1.908 1.476 2.491 1.833.365.304.145-.103.019-.073-.164-.274-1.355-2.446-1.446-2.49-.644-1.032-.17-.619a2.97 2.97 0 01-.104-.729L6.283.134 6.696 0l.996.134.42.364.62 1.414 1.002 2.229 1.555 3.03.456.898.243.832.091.255h.158V9.01l.128-1.706.237-2.095.23-2.695.08-.76.376-.91.747-.492.584.28.48.685-.067.444-.286 1.851-.559 2.903-.364 1.942h.212l.243-.242.985-1.306 1.652-2.064.73-.82.85-.904.547-.431h1.033l.76 1.129-.34 1.166-1.064 1.347-.881 1.142-1.264 1.7-.79 1.36.073.11.188-.02 2.856-.606 1.543-.28 1.841-.315.833.388.091.395-.328.807-1.969.486-2.309.462-3.439.813-.042.03.049.061 1.549.146.662.036h1.622l3.02.225.79.522.474.638-.079.485-1.215.62-1.64-.389-3.829-.91-1.312-.329h-.182v.11l1.093 1.068 2.006 1.81 2.509 2.33.127.578-.322.455-.34-.049-2.205-1.657-.851-.747-1.926-1.62h-.128v.17l.444.649 2.345 3.521.122 1.08-.17.353-.608.213-.668-.122-1.374-1.925-1.415-2.167-1.143-1.943-.14.08-.674 7.254-.316.37-.729.28-.607-.461-.322-.747.322-1.476.389-1.924.315-1.53.286-1.9.17-.632-.012-.042-.14.018-1.434 1.967-2.18 2.945-1.726 1.845-.414.164-.717-.37.067-.662.401-.589 2.388-3.036 1.44-1.882.93-1.086-.006-.158h-.055L4.132 18.56l-1.13.146-.487-.456.061-.746.231-.243 1.908-1.312-.006.006z"/></svg>`,
  antigravity: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 59" aria-hidden="true"><path fill="#3789F9" transform="translate(28,0)" d="M0 0h8l6 4 5 10 8 26 5 10 4 4-1 5h-5l-8-7-11-17-5-2-7 1-5 5-8 13-8 7h-6l1-6 5-6 5-13 7-22 5-9z"/><path fill="#6D80D8" transform="translate(28,0)" d="M0 0h8l6 4 5 10 6 21-4-1-5-5-5-3-4-6v-2l-5-2-5-1-5 3-4 1 3-10 5-7z"/><path fill="#D78240" transform="translate(28,0)" d="M0 0h8l6 4 5 10 1 5-7-4-3-3-7-2-4-2-6-1 3-5z"/><path fill="#3294CC" transform="translate(25,14)" d="M0 0l5 1 5 3 2 5-11-1-6 5-5 8-3 5h-3l7-21 5-3z"/><path fill="#E45C49" transform="translate(36,1)" d="M0 0l5 2 5 8 2 8-7-4-4-4-1-6-3-1 3-1z"/><path fill="#90AE64" transform="translate(21,7)" d="M0 0l9 1 3 2v2l-5 1-3 2-5 3-4 1 3-10z"/><path fill="#53A89A" transform="translate(25,14)" d="M0 0l5 1v3l-7 3-5 4-4-1 2-5 5-3z"/><path fill="#B5677D" transform="translate(33,11)" d="M0 0h5l11 9 1 4-5-1-4-3V7L4 5 0 2z"/><path fill="#778998" transform="translate(27,12)" d="M0 0h6l8 6 5 5 4 1-1 3-7-3-5-4V6L4 5z"/><path fill="#3390DF" transform="translate(26,21)" d="M0 0l4 2-15 15-1-3 7-10z"/><path fill="#3FA1B7" transform="translate(27,18)" d="M0 0l2 1-6 4-5 4-4 4-1-3 1-3 7-3 3-3z"/><path fill="#8277BB" transform="translate(37,18)" d="M0 0h4l5 5 4 1-1 3-7-3-5-4z"/><path fill="#4989CF" transform="translate(30,17)" d="M0 0l5 1 2 5-9-1z"/><path fill="#71B774" transform="translate(23,12)" d="M0 0l5 1-3 2-5 3-4 1 1-4z"/><path fill="#6687E9" transform="translate(44,28)" d="M0 0l7 1 2 6-4-1-5-5z"/><path fill="#C7AF38" transform="translate(23,3)" d="M0 0h7l-2 1v2l3 1-4 1-6-1z"/><path fill="#EF842A" transform="translate(28,0)" d="M0 0h8v3L4 4l-8-1z"/><path fill="#F35241" transform="translate(36,1)" d="M0 0l5 2 4 6-1 3-6-8-2-1z"/></svg>`,
  codex: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" aria-hidden="true"><path fill="#5965e8" d="M9.064 3.344a4.578 4.578 0 012.285-.312c1 .115 1.891.54 2.673 1.275a.09.09 0 00.08.021 4.55 4.55 0 013.046.275l.163.079a4.581 4.581 0 012.188 2.399c.209.51.313 1.041.315 1.595a4.24 4.24 0 01-.134 1.223.123.123 0 00.03.115c.594.607.988 1.33 1.183 2.17.289 1.425-.007 2.71-.887 3.854l-.136.166a4.548 4.548 0 01-2.201 1.388.123.123 0 00-.081.076c-.191.551-.383 1.023-.74 1.494-.9 1.187-2.222 1.846-3.711 1.838-1.187-.006-2.239-.44-3.157-1.302a.107.107 0 00-.105-.024c-.388.125-.78.143-1.204.138a4.441 4.441 0 01-1.945-.466 4.544 4.544 0 01-1.61-1.335c-.152-.202-.303-.392-.414-.617a5.81 5.81 0 01-.37-.961 4.582 4.582 0 01-.014-2.298.124.124 0 00-.021-.104 4.467 4.467 0 01-1.034-1.651 3.896 3.896 0 01-.251-1.192 5.189 5.189 0 01.141-1.6c.337-1.112.982-1.985 1.933-2.618.212-.141.413-.251.601-.33.215-.089.43-.164.646-.227a.098.098 0 00.065-.066 4.51 4.51 0 01.829-1.615 4.535 4.535 0 011.837-1.388zm3.482 10.565a.637.637 0 000 1.272h3.636a.637.637 0 100-1.272h-3.636zM8.462 9.23a.637.637 0 00-1.106.631l1.272 2.224-1.266 2.136a.636.636 0 101.095.649l1.454-2.455a.636.636 0 00.005-.64L8.462 9.23z"/></svg>`,
  gemini: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" aria-hidden="true"><path fill="#3186FF" d="M20.616 10.835a14.147 14.147 0 01-4.45-3.001 14.111 14.111 0 01-3.678-6.452.503.503 0 00-.975 0 14.134 14.134 0 01-3.679 6.452 14.155 14.155 0 01-4.45 3.001c-.65.28-1.318.505-2.002.678a.502.502 0 000 .975c.684.172 1.35.397 2.002.677a14.147 14.147 0 014.45 3.001 14.112 14.112 0 013.679 6.453.502.502 0 00.975 0c.172-.685.397-1.351.677-2.003a14.145 14.145 0 013.001-4.45 14.113 14.113 0 016.453-3.678.503.503 0 000-.975 13.245 13.245 0 01-2.003-.678z"/></svg>`,
  kimi: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" aria-hidden="true"><path fill="#1783FF" d="M21.846 0a1.923 1.923 0 110 3.846H20.15a.226.226 0 01-.227-.226V1.923C19.923.861 20.784 0 21.846 0z"/><path fill="#fff" d="M11.065 11.199l7.257-7.2c.137-.136.06-.41-.116-.41H14.3a.164.164 0 00-.117.051l-7.82 7.756c-.122.12-.302.013-.302-.179V3.82c0-.127-.083-.23-.185-.23H3.186c-.103 0-.186.103-.186.23V19.77c0 .128.083.23.186.23h2.69c.103 0 .186-.102.186-.23v-3.25c0-.069.025-.135.069-.178l2.424-2.406a.158.158 0 01.205-.023l6.484 4.772a7.677 7.677 0 003.453 1.283c.108.012.2-.095.2-.23v-3.06c0-.117-.07-.212-.164-.227a5.028 5.028 0 01-2.027-.807l-5.613-4.064c-.117-.078-.132-.279-.028-.381z"/></svg>`,
  xai: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" aria-hidden="true"><path fill="currentColor" d="M9.27 15.29l7.978-5.897c.391-.29.95-.177 1.137.272.98 2.369.542 5.215-1.41 7.169-1.951 1.954-4.667 2.382-7.149 1.406l-2.711 1.257c3.889 2.661 8.611 2.003 11.562-.953 2.341-2.344 3.066-5.539 2.388-8.42l.006.007c-.983-4.232.242-5.924 2.75-9.383L24 .5l-3.301 3.305v-.01L9.267 15.292M7.623 16.723c-2.792-2.67-2.31-6.801.071-9.184 1.761-1.763 4.647-2.483 7.166-1.425l2.705-1.25a7.808 7.808 0 00-1.829-1A8.975 8.975 0 005.984 5.83c-2.533 2.536-3.33 6.436-1.962 9.764 1.022 2.487-.653 4.246-2.34 6.022-.599.63-1.199 1.259-1.682 1.925l7.62-6.815"/></svg>`
};

function authProviderIcon(category) {
  const key = category === "gemini-cli" || category === "gemini-interactions" ? "gemini" : category;
  const markup = AUTH_PROVIDER_ICONS[key];
  if (!markup) return null;
  return el(
    "span",
    { class: "auth-provider-icon", "data-provider": key, role: "img", "aria-label": AUTH_CATEGORY_LABELS[key] || category },
    iconSVG(markup)
  );
}

function authUI(account) {
  const prefix = account ? "account-auth" : "auth";
  return {
    owner: account ? accountUIState : adminUIState,
    files: resources[account ? "account" : "admin"].authFiles,
    body: $(prefix + "-files-body"),
    enabledOnly: $(prefix + "-enabled-only"),
    provider: $(prefix + "-provider"),
    search: $(prefix + "-search"),
    bulk: $(prefix + "-refresh-filtered")
  };
}

const AUTH_QUOTA_CACHE_PREFIX = "cpa-key-billing.auth-quotas.v5.";

function resetAuthQuotaMemory(account) {
  const role = account ? "account" : "admin";
  const owner = account ? accountUIState : adminUIState;
  authQuotaGeneration[role]++;
  owner.authQuotas.clear();
  owner.authQuotaLoading.clear();
  owner.authQuotaErrors.clear();
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

function clearCredentialScope(account) {
  const role = account ? "account" : "admin";
  const key = authQuotaStorageKey(role);
  if (key) { try { sessionStorage.removeItem(key); } catch (_) {} }
  credentialScope[role] = "";
  resetAuthQuotaMemory(account);
}

async function activateCredentialScope(account, credential) {
  const generation = sessionGeneration;
  const role = account ? "account" : "admin";
  resetAuthQuotaMemory(account);
  credentialScope[role] = "";
  try {
    const scope = (await sha256Hex(role + "|" + credential)).slice(0, 32);
    if (generation !== sessionGeneration) throw new StaleRequestError();
    credentialScope[role] = scope;
  } catch (_) {
    if (generation !== sessionGeneration) throw new StaleRequestError();
    return;
  }
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

async function fetchAuthFiles(account) {
  const result = await (account ? accountAPI("/auth-files") : plugin("GET", "/auth-files"));
  return requireArray(result?.files, m("ui.auth_files"));
}

function updateAuthFiles(account, files) {
  const role = account ? "account" : "admin";
  const owner = account ? accountUIState : adminUIState;
  // Status changes awaiting confirmation outlast reads that started before them.
  for (const file of files)
    if (owner.authStatusSubmissions?.has(file.auth_index)) file.disabled = owner.authStatusSubmissions.get(file.auth_index);
  const byIndex = new Map(files.map((file) => [file.auth_index, file]));
  const previous = new Map((resources[role].authFiles.value || []).map((file) => [file.auth_index, file.cache_revision || ""]));
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
  return (
    owner === currentOwner && generation === authQuotaGeneration[role] &&
    (resources[role].authFiles.value || []).some((file) => file.auth_index === index)
  );
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
  return { at: compactResetDate(resetAt), after: compactResetAfter(after) };
}

function quotaAmount(row) {
  const hasUsed = row.used !== undefined;
  const hasLimit = row.limit !== undefined;
  if (!hasUsed && !hasLimit) return "";
  const money = row.currency === "USD";
  const format = (value) => (money ? usd(value) : value.toLocaleString(locale(), { maximumFractionDigits: 2 }));
  if (hasUsed && hasLimit) return format(row.used) + DISPLAY_SLASH + format(row.limit);
  return hasUsed ? m("ui.used_value", { v0: format(row.used) }) : m("ui.quota_value", { v0: format(row.limit) });
}

function quotaLabel(row) {
  const label = window.billingI18n.serverMessage(row.label_message, row.label || "");
  const group = window.billingI18n.serverMessage(row.group_message, row.group_label || "");
  const fullLabel = row.label_prefix ? m("ui.join", { v0: row.label_prefix, v1: "", v2: label }) : label;
  return group && String(group).toLowerCase() !== String(fullLabel).toLowerCase()
    ? m("ui.join", { v0: group, v1: DISPLAY_SEPARATOR, v2: fullLabel })
    : group || fullLabel;
}

function renderQuotaRow(row) {
  const label = quotaLabel(row);
  const percent = row.remaining_percent;
  const hasPercent = Number.isFinite(percent);
  const value = hasPercent ? percent.toFixed(0) + "%" : "—";
  const level = percent >= 70 ? "high" : percent >= 30 ? "medium" : "low";
  const reset = quotaResetMeta(row);
  const amount = quotaAmount(row);
  const elapsed = row.reset_at && new Date(row.reset_at).getTime() <= Date.now();
  const resetText =
    reset.at && m("ui.join", { v0: reset.at, v1: DISPLAY_SEPARATOR, v2: elapsed ? m("ui.quota_reset_elapsed") : reset.after });
  return el(
    "div",
    { class: "auth-quota-row" + (elapsed ? " elapsed" : "") },
    el("span", { class: "auth-quota-label", "data-tip": label, "data-tip-overflow": "", text: label }),
    el("span", { class: "auth-quota-value", text: value }),
    el(
      "div",
      {
        class: "bar",
        role: "progressbar",
        "aria-label": label,
        "aria-valuemin": "0",
        "aria-valuemax": "100",
        "aria-valuenow": hasPercent ? Math.round(percent) : null
      },
      el("i", { class: "quota-fill " + (hasPercent ? level : "high"), style: "width:" + (hasPercent ? percent : 0) + "%" })
    ),
    (amount || resetText) &&
      el(
        "div",
        { class: "auth-quota-stats" },
        amount && el("span", { class: "auth-quota-amount", text: amount }),
        resetText && el("span", { class: "auth-quota-reset-time", text: resetText })
      )
  );
}

function renderAuthResetCredits(file, result) {
  if (file.category !== "codex") return null;
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
      const time = Number.isFinite(expiresAt.getTime())
        ? new Intl.DateTimeFormat("sv-SE", {
            year: "numeric",
            month: "2-digit",
            day: "2-digit",
            hour: "2-digit",
            minute: "2-digit",
            second: "2-digit",
            hour12: false
          }).format(expiresAt)
        : credit.expires_at || "—";
      return el(
        "div",
        { class: "auth-reset-credit" },
        el("span", { text: m("ui.reset_credit_number", { v0: index + 1 }) }),
        el("span", { class: "auth-reset-credit-time", text: time })
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
  const owner = account ? accountUIState : adminUIState;
  const index = file.auth_index;
  const result = authFileQuota(file, owner);
  if (owner.authQuotaLoading.has(index) && !result) {
    return el("div", { class: "auth-file-quota" }, el("div", { class: "muted small", text: m("ui.updating_quotas") }));
  }
  const error = owner.authQuotaErrors.get(index);
  if (error && !result) return el("div", { class: "auth-file-quota" }, el("div", { class: "auth-quota-error", text: error }));
  if (!result) return el("div", { class: "muted small", text: authQuotaRefreshTitle(file) || m("ui.refresh_to_query_quota") });
  const summary = [];
  const balance = Number(result.credit_balance);
  if (result.credits_unlimited || (Number.isFinite(balance) && balance !== 0)) {
    const credits = result.credits_unlimited ? m("ui.unlimited") : result.credit_currency === "USD" ? usd(balance) : result.credit_balance;
    summary.push(el("span", { text: m("ui.credits_value", { v0: credits }) }));
  }
  if (Number.isFinite(result.rate_limit_reset_credits_available_count)) {
    summary.push(
      el("span", {
        class: "auth-reset-count",
        text: m("ui.available_resets_value", { v0: int(result.rate_limit_reset_credits_available_count) }),
        ...hoverTooltipProps(() => [renderAuthResetCredits(file, result)], file.category === "codex")
      })
    );
  }
  const rows = result.quota;
  return el(
    "div",
    { class: "auth-file-quota" },
    summary.length ? el("div", { class: "auth-quota-summary" }, summary) : null,
    error && el("div", { class: "auth-quota-error", text: error }),
    rows.length
      ? el("div", { class: "auth-quota-list" }, rows.map(renderQuotaRow))
      : el("div", { class: "muted small", text: m("ui.the_upstream_returned_no_displayable_quota") })
  );
}

function renderAuthFiles(account) {
  const ui = authUI(account);
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
      m("ui.auth_requests_30_days"),
      m("ui.auth_total_tokens"),
      m("ui.cache_rate"),
      { label: m("ui.quota"), class: "auth-quota-column" },
      m("ui.actions")
    ],
    attrs: { class: "auth-file-table" },
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
          class: "labeled-icon-button auth-quota-reset",
          "aria-label": m("ui.reset_quota"),
          ...hoverTooltipProps(
            () => [el("span", { text: m("ui.reset_opportunities", { count: quota.rate_limit_reset_credits_available_count }) })],
            true
          ),
          disabled: !canResetAuthQuota(file, account),
          onclick: () => guard(() => resetAuthQuota(file, account))
        },
        actionIcon("party-popper")
      ),
    el(
      "button",
      {
        type: "button",
        class: "labeled-icon-button auth-quota-refresh" + (loading ? " loading" : ""),
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
    { class: "auth-file-row" + (file.disabled ? " disabled" : ""), "data-auth-index": file.auth_index },

    el(
      "td",
      { class: "auth-identity-cell" },
      el(
        "div",
        { class: "auth-file-head" },
        authProviderIcon(file.category),
        el(
          "div",
          { class: "auth-file-identity" },
          el("div", {
            class: "auth-file-email-title mask-blur",
            "data-tip": file.email || file.name,
            "data-tip-overflow": "",
            text: file.email || file.name || m("ui.no_email_provided")
          }),
          authFileMeta(file, quota)
        )
      )
    ),
    renderAuthFileUsage(file),
    el("td", { class: "auth-quota-cell" }, renderAuthFileQuota(file, account)),
    el(
      "td",
      { class: "auth-actions-cell" },
      el(
        "div",
        { class: "auth-file-actions" },
        quotaActions,
        !account &&
          switchControl({
            input: el("input", {
              type: "checkbox",
              role: "switch",
              checked: !file.disabled,
              "aria-label": m("ui.enable"),
              "data-tip": "",
              disabled: adminUIState.authStatusSubmissions.has(file.auth_index),
              onchange: (event) => guard(() => setAuthFileEnabled(file, event.target.checked))
            })
          })
      )
    )
  );
}

function renderAuthFileUsage(file) {
  const usage = file.usage || {};
  const count = usage.requests || 0;
  const failed = usage.failed || 0;
  const success = count ? (((count - failed) / count) * 100).toFixed(2) + "%" : "—";
  const total = (usage.input_tokens || 0) + (usage.output_tokens || 0);
  const cache = usage.input_tokens ? ((usage.cache_read_tokens / usage.input_tokens) * 100).toFixed(2) + "%" : "—";
  const metric = (name, label, value, detail, secondary) =>
    el(
      "td",
      { class: "auth-" + name + "-cell" },
      el(
        "div",
        { class: "auth-usage-metric" },
        el("span", { class: "auth-usage-label", text: label }),
        el("span", { class: "auth-usage-value", "data-tip": detail }, value)
      ),
      secondary &&
        el(
          "div",
          { class: "auth-usage-detail" },
          el("span", { class: "auth-usage-detail-label", text: m("ui.success_rate") }),
          el("span", { class: "auth-usage-detail-value", text: secondary })
        )
    );
  const requests = [
    tokens(count, 2),
    " (",
    el("span", { class: "auth-request-success", text: tokens(count - failed, 2) }),
    DISPLAY_SLASH,
    el("span", { class: "auth-request-failed", text: tokens(failed, 2) }),
    ")"
  ];
  return [
    metric(
      "requests",
      m("ui.requests"),
      requests,
      count >= 1000 ? m("ui.auth_request_outcomes", { v0: int(count - failed), v1: int(failed) }) : null,
      success
    ),
    metric("tokens", m("ui.auth_total_tokens"), tokens(total), total >= 1000 ? exactTokens(total) : null),
    metric("cache", m("ui.cache_rate"), cache)
  ];
}

async function setAuthFileEnabled(file, enabled) {
  const owner = adminUIState;
  if (owner.authStatusSubmissions.has(file.auth_index)) return;
  owner.authStatusSubmissions.set(file.auth_index, !enabled);
  file.disabled = !enabled;
  renderAuthFiles(false);
  try {
    const body = { name: file.name, auth_index: file.auth_index, disabled: !enabled };
    await api("PATCH", "/v0/management/auth-files/status", body, { raw: true });
  } catch (error) {
    owner.authStatusSubmissions.delete(file.auth_index);
    file.disabled = enabled;
    if (owner === adminUIState) renderAuthFiles(false);
    if (error instanceof AuthError || error instanceof StaleRequestError) throw error;
    throw new UIError(m(enabled ? "ui.enable_auth_file_failed_value" : "ui.disable_auth_file_failed_value", { v0: error.message }));
  }
  try {
    // A list read already in flight started before this change, so only the next read confirms it.
    await resourceGroups.admin.authFiles.task?.catch(() => {});
    await loadDataGroup(false, "authFiles", true);
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
    m("ui.reset_provider_quota", { v0: AUTH_CATEGORY_LABELS[file.category] || file.category }),
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
      let resetConfirmed = false;
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

for (const account of [false, true]) {
  const ui = authUI(account);
  const role = account ? "account" : "admin";
  setLabeledActionIcon(ui.bulk, "refresh", m("ui.update_quotas"));
  ui.enabledOnly.addEventListener("change", () => {
    saveChoice(role, ui.enabledOnly.id);
    renderAuthFiles(account);
  });
  ui.provider.addEventListener("change", () => {
    saveChoice(role, ui.provider.id);
    renderAuthFiles(account);
  });
  ui.search.addEventListener("input", () => renderAuthFiles(account));
  ui.bulk.onclick = () => guard(() => refreshFilteredAuthQuotas(account));
}
