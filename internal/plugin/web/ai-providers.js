const AI_PROVIDER_TYPES = [
  { type: "claude", list: "claude-api-key", brand: "claude" },
  { type: "codex", list: "codex-api-key", brand: "codex" },
  { type: "gemini", list: "gemini-api-key", brand: "gemini" },
  { type: "interactions", list: "interactions-api-key", brand: "gemini" },
  { type: "vertex", list: "vertex-api-key", brand: "vertex" },
  { type: "xai", list: "xai-api-key", brand: "xai" },
  { type: "meta", list: "meta-api-key", brand: "meta" },
  { type: "openai-compatibility", list: "openai-compatibility", brand: "openai" }
];

// Upstreams whose API keys can read their own account balance. A key's base URL must name one of
// these hosts exactly, so a key is only ever sent to the upstream that issued it.
const AI_BALANCE_UPSTREAMS = [
  {
    hosts: ["api.deepseek.com"],
    async read(call) {
      const body = await call("/user/balance");
      return requireArray(body.balance_infos, m("ui.balance")).map((info) => ({
        label: "ui.balance",
        currency: balanceCurrency(requireObject(info, m("ui.balance")).currency),
        amount: balanceAmount(info.total_balance),
        depleted: body.is_available === false,
        parts: [["ui.balance_topped_up_value", info.topped_up_balance], ["ui.balance_granted_value", info.granted_balance]]
      }));
    }
  },
  {
    hosts: ["openrouter.ai"],
    async read(call) {
      // The key's own spending limit is optional detail; the account credits are the balance.
      const [credits, key] = await Promise.all([
        call("/api/v1/credits"),
        call("/api/v1/key").catch((error) => {
          if (error instanceof AuthError || error instanceof StaleRequestError) throw error;
          return null;
        })
      ]);
      const data = requireObject(credits.data, m("ui.balance"));
      const total = balanceAmount(data.total_credits), used = balanceAmount(data.total_usage);
      const balances = [{ label: "ui.account_credits", currency: "USD", amount: total - used, used, total, depleted: total - used <= 0 }];
      const limit = key?.data;
      if (Number.isFinite(limit?.limit) && Number.isFinite(limit?.limit_remaining)) {
        balances.push({
          label: "ui.key_limit",
          currency: "USD",
          amount: limit.limit_remaining,
          used: limit.limit - limit.limit_remaining,
          total: limit.limit,
          depleted: limit.limit_remaining <= 0
        });
      }
      return balances;
    }
  },
  {
    hosts: ["api.moonshot.cn", "api.moonshot.ai"],
    async read(call, host) {
      const data = requireObject((await call("/v1/users/me/balance")).data, m("ui.balance"));
      const amount = balanceAmount(data.available_balance);
      return [
        {
          label: "ui.balance",
          currency: host.endsWith(".cn") ? "CNY" : "USD",
          amount,
          depleted: amount <= 0,
          parts: [["ui.balance_cash_value", data.cash_balance], ["ui.balance_voucher_value", data.voucher_balance]]
        }
      ];
    }
  },
  {
    hosts: ["api.siliconflow.cn", "api.siliconflow.com"],
    async read(call, host) {
      const data = requireObject((await call("/v1/user/info")).data, m("ui.balance"));
      const amount = balanceAmount(data.totalBalance);
      return [
        {
          label: "ui.balance",
          currency: host.endsWith(".cn") ? "CNY" : "USD",
          amount,
          depleted: amount <= 0,
          parts: [["ui.balance_topped_up_value", data.chargeBalance], ["ui.balance_granted_value", data.balance]]
        }
      ];
    }
  },
  {
    hosts: ["api.stepfun.com", "api.stepfun.ai"],
    async read(call, host) {
      const body = await call("/v1/accounts");
      const amount = balanceAmount(body.balance);
      // Postpaid accounts keep calling below zero.
      return [
        { label: "ui.balance", currency: host.endsWith(".com") ? "CNY" : "USD", amount, depleted: body.type !== "postpaid" && amount <= 0 }
      ];
    }
  }
];

// CPA ends a proxied call after 60 seconds.
const AI_BALANCE_TIMEOUT_MS = 65000;
const AI_BALANCE_BATCH_CONCURRENCY = 3;
const AI_BALANCE_CACHE_PREFIX = "cpa-key-billing.ai-balances.v1.admin.";

function configuredAIProviders(kind, entries) {
  const text = (value) => (typeof value === "string" ? value.trim() : "");
  // CPA weighs a credential without a weight as 1 and a negative one as 0.
  const weight = (value) => (Number.isFinite(value) ? Math.max(0, value) : 1);
  const providers = [];
  for (const entry of entries) {
    const base = {
      type: kind.type,
      brand: kind.brand,
      base_url: text(entry["base-url"]),
      priority: Number.isFinite(entry.priority) ? entry.priority : 0
    };
    if (kind.list !== "openai-compatibility") {
      const key = text(entry["api-key"]);
      // CPA loads no credential for an entry with neither a key nor a base URL.
      if (!key && !base.base_url) continue;
      const excluded = entry["excluded-models"] || [];
      providers.push({
        ...base,
        auth_index: text(entry["auth-index"]),
        key_preview: previewCredential(key),
        weight: weight(entry.weight),
        disabled: excluded.some((model) => model.trim() === "*")
      });
      continue;
    }
    // Compatible providers carry their keys; one without keys is still a credential of its own.
    const provider = { ...base, name: text(entry.name), disabled: entry.disabled === true };
    const keys = entry["api-key-entries"] || [];
    if (!keys.length) providers.push({ ...provider, auth_index: text(entry["auth-index"]), key_preview: "", weight: 1 });
    for (const key of keys) {
      providers.push({
        ...provider,
        auth_index: text(key["auth-index"]),
        key_preview: previewCredential(text(key["api-key"])),
        weight: weight(key.weight)
      });
    }
  }
  return providers;
}

async function fetchAIProviders() {
  const lists = await Promise.all(
    AI_PROVIDER_TYPES.map(async (kind) => {
      let config;
      try { config = requireObject(await api("GET", "/v0/management/" + kind.list, null), m("ui.ai_providers")); } catch (error) {
        // Earlier CLIProxyAPI versions have no list for later provider types.
        if (error.status === 404) return [];
        throw error;
      }
      if (!Object.hasOwn(config, kind.list)) throw new UIError(m("ui.invalid_response_for_value", { v0: m("ui.ai_providers") }));
      return configuredAIProviders(kind, configuredCredentialEntries(config, kind.list));
    })
  );
  const providers = lists.flat();
  const indexes = [...new Set(providers.map((provider) => provider.auth_index).filter(Boolean))];
  const usage = {};
  if (indexes.length) {
    const range = selectedRange(false);
    await Promise.all(
      batchedSearchParams("auth_index", indexes).map(async (query) => {
        for (const [name, value] of Object.entries(range)) query.set(name, value);
        Object.assign(usage, requireObject((await plugin("GET", "/ai-providers/usage?" + query))?.usage, m("ui.ai_providers")));
      })
    );
  }
  return providers.map((provider) => ({ ...provider, usage: provider.auth_index ? usage[provider.auth_index] || {} : null }));
}

function readAIProviders(reload = false) { return aiProvidersQuery.read(JSON.stringify(sharedTimeRanges.admin), fetchAIProviders, reload); }

function updateAIProviders(providers) {
  const owner = adminUIState;
  const listed = new Set(providers.map((provider) => provider.auth_index));
  const stale = [...owner.aiBalances.keys()].filter((index) => !listed.has(index));
  for (const index of stale) owner.aiBalances.delete(index);
  for (const index of owner.aiBalanceErrors.keys()) if (!listed.has(index)) owner.aiBalanceErrors.delete(index);
  if (stale.length) persistAIBalances();
}

function aiBalanceStorageKey() { return credentialScope.admin ? AI_BALANCE_CACHE_PREFIX + credentialScope.admin : ""; }

function persistAIBalances() {
  const key = aiBalanceStorageKey();
  if (!key) return;
  try { sessionStorage.setItem(key, JSON.stringify([...adminUIState.aiBalances])); } catch (_) {}
}

function restoreAIBalances() {
  const key = aiBalanceStorageKey();
  let cached;
  try { cached = key && JSON.parse(sessionStorage.getItem(key) || "null"); } catch (_) { return; }
  if (!Array.isArray(cached)) return;
  for (const entry of cached) {
    if (Array.isArray(entry) && typeof entry[0] === "string" && validAIBalanceResult(entry[1]))
      adminUIState.aiBalances.set(entry[0], entry[1]);
  }
}

function resetAIBalanceMemory() {
  adminUIState.aiBalances.clear();
  adminUIState.aiBalanceLoading.clear();
  adminUIState.aiBalanceErrors.clear();
  resetAIBalanceBatch();
}

function forgetAIBalances() {
  const key = aiBalanceStorageKey();
  if (key) { try { sessionStorage.removeItem(key); } catch (_) {} }
  resetAIBalanceMemory();
}

function validAIBalanceResult(result) {
  return (
    typeof result?.fetched_at === "string" && Number.isFinite(Date.parse(result.fetched_at)) && Array.isArray(result.balances) &&
    result.balances.length > 0 &&
    result.balances.every(
      (balance) =>
        balance && typeof balance.label === "string" && typeof balance.currency === "string" && /^[A-Z]{3}$/.test(balance.currency) &&
        Number.isFinite(balance.amount) &&
        [balance.used, balance.total].every((value) => value === undefined || Number.isFinite(value)) &&
        (balance.parts === undefined ||
          (Array.isArray(balance.parts) &&
            balance.parts.every(
              (part) => Array.isArray(part) && part.length === 2 && typeof part[0] === "string" && Number.isFinite(part[1])
            )))
    )
  );
}

function aiBalanceUpstream(provider) {
  if (!provider.auth_index || !provider.key_preview) return null;
  let host;
  try { host = new URL(provider.base_url).hostname.toLowerCase(); } catch (_) { return null; }
  const upstream = AI_BALANCE_UPSTREAMS.find((item) => item.hosts.includes(host));
  return upstream ? { read: upstream.read, host } : null;
}

function balanceAmount(value) {
  const amount = typeof value === "string" && value.trim() ? Number(value) : value;
  if (typeof amount !== "number" || !Number.isFinite(amount))
    throw new UIError(m("ui.invalid_response_for_value", { v0: m("ui.balance") }));
  return amount;
}

function balanceCurrency(value) {
  if (typeof value !== "string" || !/^[A-Z]{3}$/.test(value))
    throw new UIError(m("ui.invalid_response_for_value", { v0: m("ui.balance") }));
  return value;
}

function formatBalance(value, currency) {
  return Number(value).toLocaleString(locale(), {
    style: "currency",
    currency,
    currencyDisplay: "narrowSymbol",
    minimumFractionDigits: 2,
    maximumFractionDigits: 2
  });
}

// CPA substitutes the selected credential's key for $TOKEN$.
async function upstreamBalanceJSON(provider, url) {
  const request = {
    auth_index: provider.auth_index,
    method: "GET",
    url,
    header: { Authorization: "Bearer $TOKEN$", Accept: "application/json" }
  };
  const result = requireObject(
    await api("POST", "/v0/management/api-call", request, { timeoutMS: AI_BALANCE_TIMEOUT_MS }),
    m("ui.balance")
  );
  if (!Number.isInteger(result.status_code) || result.status_code < 100 || result.status_code > 599)
    throw new UIError(m("ui.invalid_response_for_value", { v0: m("ui.balance") }));
  if (!(result.status_code >= 200 && result.status_code < 300)) {
    throw new UIError(m("ui.upstream_returned_http_value", { v0: result.status_code }));
  }
  let body = null;
  try { body = JSON.parse(result.body); } catch (_) {}
  return requireObject(body, m("ui.balance"));
}

async function loadAIBalance(provider) {
  const owner = adminUIState;
  const index = provider.auth_index;
  const upstream = aiBalanceUpstream(provider);
  if (!upstream || owner.aiBalanceLoading.has(index)) return;
  owner.aiBalanceLoading.add(index);
  owner.aiBalanceErrors.delete(index);
  try {
    const balances = await upstream.read((path) => upstreamBalanceJSON(provider, "https://" + upstream.host + path), upstream.host);
    const result = {
      fetched_at: new Date().toISOString(),
      balances: balances.map((balance) => ({
        ...balance,
        parts: balance.parts?.filter(([, value]) => value != null).map(([label, value]) => [label, balanceAmount(value)])
      }))
    };
    if (!validAIBalanceResult(result)) throw new UIError(m("ui.invalid_response_for_value", { v0: m("ui.balance") }));
    if (owner === adminUIState && aiProvidersQuery.value?.some((item) => item.auth_index === index)) owner.aiBalances.set(index, result);
  } catch (error) {
    if (error instanceof AuthError || error instanceof StaleRequestError) throw error;
    if (owner === adminUIState && aiProvidersQuery.value?.some((item) => item.auth_index === index))
      owner.aiBalanceErrors.set(index, error.message || String(error));
  } finally { owner.aiBalanceLoading.delete(index); }
}

async function queryAIBalance(provider) {
  const owner = adminUIState;
  const task = loadAIBalance(provider);
  renderAIProviders();
  try { await task; } finally {
    if (owner === adminUIState) {
      persistAIBalances();
      renderAIProviders();
    }
  }
}

function resetAIBalanceBatch() {
  const bulk = $("ai-provider-query-balances");
  bulk.dataset.loading = "false";
  setLabeledActionIcon(bulk, "refresh", m("ui.query_balance"));
}

async function queryFilteredAIBalances() {
  const owner = adminUIState;
  const bulk = $("ai-provider-query-balances");
  // A key listed twice shares one auth index, and so one query.
  const providers = [
    ...new Map(
      filteredAIProviders().filter((provider) => aiBalanceUpstream(provider) && !owner.aiBalanceLoading.has(provider.auth_index))
        .map((provider) => [provider.auth_index, provider])
    ).values()
  ];
  if (!providers.length || bulk.dataset.loading === "true") return;
  bulk.dataset.loading = "true";
  const progress = el("span", { text: m("ui.querying_0_value", { v0: providers.length }) });
  bulk.removeAttribute("aria-label");
  bulk.replaceChildren(actionIcon("refresh"), progress);
  renderAIProviders();
  let next = 0, completed = 0;
  try {
    const worker = async () => {
      while (next < providers.length && owner === adminUIState) {
        await loadAIBalance(providers[next++]);
        setText(progress, m("ui.querying_value_value", { v0: ++completed, v1: providers.length }));
      }
    };
    await settleLoads(Array.from({ length: Math.min(AI_BALANCE_BATCH_CONCURRENCY, providers.length) }, worker));
  } finally {
    if (owner === adminUIState) {
      persistAIBalances();
      resetAIBalanceBatch();
      renderAIProviders();
    }
  }
}

function filteredAIProviders() {
  const type = $("ai-provider-type").value;
  const search = $("ai-provider-search").value.trim().toLocaleLowerCase();
  return (aiProvidersQuery.value || []).filter((provider) => {
    if ($("ai-provider-enabled-only").checked && provider.disabled) return false;
    if (type && provider.type !== type) return false;
    if (!search) return true;
    return [provider.base_url, provider.type, provider.name, provider.key_preview].some((value) =>
      String(value || "").toLocaleLowerCase().includes(search)
    );
  });
}

function renderAIBalanceRow(balance, fetchedAt) {
  const parts = balance.parts || [];
  const used =
    balance.total > 0 && formatBalance(balance.used, balance.currency) + DISPLAY_SLASH + formatBalance(balance.total, balance.currency);
  return el(
    "div",
    { class: "ai-balance-row" + (balance.depleted ? " depleted" : "") },
    el(
      "div",
      { class: "ai-balance-head" },
      el("span", { class: "ai-balance-value", "data-tip": m(balance.label), text: formatBalance(balance.amount, balance.currency) }),
      balance.label !== "ui.balance" && el("span", { class: "ai-balance-label", text: m(balance.label) }),
      fetchedAt &&
        el("time", {
          class: "ai-balance-time",
          datetime: fetchedAt,
          text: m("ui.balance_queried_at_value", { v0: compactResetDate(new Date(fetchedAt)) })
        })
    ),
    (used || parts.length > 0) &&
      el(
        "div",
        { class: "ai-balance-details" },
        used && el("span", { text: used }),
        parts.map(([key, value]) => el("span", { text: m(key, { v0: formatBalance(value, balance.currency) }) }))
      )
  );
}

function renderAIBalance(provider) {
  const owner = adminUIState;
  const status = (text) => el("div", { class: "muted small", text });
  if (!aiBalanceUpstream(provider)) {
    return status(
      provider.auth_index
        ? m("ui.balance_queries_are_not_supported_for_this_provider")
        : m("ui.cpa_did_not_provide_a_credential_identifier")
    );
  }
  const result = owner.aiBalances.get(provider.auth_index);
  const error = owner.aiBalanceErrors.get(provider.auth_index);
  if (!result) {
    if (owner.aiBalanceLoading.has(provider.auth_index)) return status(m("ui.querying_balance"));
    return error ? el("div", { class: "credential-error", text: error }) : status(m("ui.query_to_check_balance"));
  }
  return el(
    "div",
    { class: "ai-balance" },
    error && el("div", { class: "credential-error", text: error }),
    result.balances.map((balance, index) => renderAIBalanceRow(balance, index === 0 ? result.fetched_at : null))
  );
}

function renderAIProviders() {
  const body = $("ai-providers-body");
  const bulk = $("ai-provider-query-balances");
  if (renderDataPending(body, [aiProvidersQuery])) {
    bulk.disabled = true;
    return;
  }
  const providers = filteredAIProviders();
  bulk.disabled = bulk.dataset.loading === "true" || !providers.some(aiBalanceUpstream);
  renderCollection(body, providers, {
    headers: [
      m("ui.ai_providers"),
      m("ui.requests"),
      m("ui.auth_total_tokens"),
      m("ui.cache_rate"),
      { label: m("ui.balance"), class: "credential-quota-column" },
      m("ui.actions")
    ],
    attrs: { class: "credential-table" },
    empty: m("ui.no_ai_providers_match_the_filters"),
    render: renderAIProviderRow
  });
}

function renderAIProviderRow(provider) {
  const upstream = aiBalanceUpstream(provider);
  const loading = adminUIState.aiBalanceLoading.has(provider.auth_index);
  const baseURL = provider.base_url.replace(/^https:\/\//i, "");
  return el(
    "tr",
    { class: "credential-row" + (provider.disabled ? " disabled" : "") },
    el(
      "td",
      { class: "credential-identity-cell" },
      el(
        "div",
        { class: "credential-head" },
        providerIcon(provider.brand),
        el(
          "div",
          { class: "credential-identity" },
          el("div", {
            class: "credential-title" + (baseURL ? "" : " muted"),
            "data-tip": baseURL || null,
            "data-tip-overflow": "",
            text: baseURL || m("ui.default_base_url")
          }),
          el(
            "div",
            { class: "credential-meta ai-provider-meta" },
            el(
              "span",
              { class: "tag plain ai-provider-key" },
              el("span", {
                class: provider.key_preview ? "mono mask-blur" : "",
                text: provider.key_preview || m("ui.no_api_key_configured")
              })
            ),
            credentialRoutingTag(provider),
            provider.disabled && el("span", { class: "tag warn", text: m("ui.disabled") })
          )
        )
      )
    ),
    renderCredentialUsage(provider.usage),
    el("td", { class: "credential-quota-cell" }, renderAIBalance(provider)),
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
            class: "labeled-icon-button round-icon-button" + (loading ? " loading" : ""),
            disabled: !upstream || loading || $("ai-provider-query-balances").dataset.loading === "true",
            "aria-label": m("ui.query_balance"),
            "data-tip": upstream ? m("ui.query_balance") : m("ui.balance_queries_are_not_supported_for_this_provider"),
            onclick: () => guard(() => queryAIBalance(provider))
          },
          actionIcon("refresh")
        )
      )
    )
  );
}

$("ai-provider-type").append(...AI_PROVIDER_TYPES.map((kind) => el("option", { value: kind.type, text: kind.type })));
resetAIBalanceBatch();
for (const filter of [$("ai-provider-enabled-only"), $("ai-provider-type")]) {
  filter.addEventListener("change", () => {
    saveChoice("admin", filter.id);
    renderAIProviders();
  });
}
$("ai-provider-search").addEventListener("input", renderAIProviders);
$("ai-provider-query-balances").onclick = () => guard(queryFilteredAIBalances);
