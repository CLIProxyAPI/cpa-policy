function defaultModerationPolicy() {
  return {
    mode: "off",
    strategy: "keywords",
    scope: "latest_user",
    model_mode: "all",
    models: [],
    rules: [],
    failure_action: "block",
    log_excerpt: false
  };
}

function moderationSelect(id, values) {
  const select = el("select", { id }, values.map((value) => el("option", { value, text: m("risk." + value) })));
  return select;
}
function moderationField(label, input, help) {
  return el(
    "label",
    { class: "field" },
    el("span", { text: m("risk." + label) }),
    input,
    help ? el("span", { class: "small muted", text: m("risk." + help) }) : null
  );
}

function moderationRuleID() {
  // Unlike randomUUID, getRandomValues is also available on HTTP management pages.
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  return "rule-" + Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
}

function revealModerationField(input) {
  for (let parent = input.parentElement; parent; parent = parent.parentElement) {
    if (parent.tagName === "DETAILS") parent.open = true;
    if (parent.classList.contains("moderation-rule-section")) {
      parent.dataset.validationVisible = "true";
      parent.hidden = false;
    }
  }
  input.scrollIntoView({ block: "center" });
  input.focus();
}
function moderationFieldError(input, message) {
  revealModerationField(input);
  throw new UIError(message);
}

function moderationPolicyEditor(prefix, value, onChange = () => {}) {
  const mode = moderationSelect(prefix + "-mode", ["off", "observe", "pre_block"]);
  const strategy = moderationSelect(prefix + "-strategy", ["keywords", "api", "keywords_and_api"]);
  const scope = moderationSelect(prefix + "-scope", ["latest_user", "all_users"]);
  const filter = moderationSelect(prefix + "-model-mode", ["all", "include", "exclude"]);
  const models = el("textarea", { id: prefix + "-models", rows: 3, maxlength: 26000 });
  const failure = moderationSelect(prefix + "-failure", ["block", "allow"]);
  const excerpt = el("input", { id: prefix + "-excerpt", type: "checkbox", role: "switch" });
  const rules = el("div", { class: "moderation-rules", id: prefix + "-rules" });
  let rows = [];
  let disabled = false;
  const add = el("button", {
    type: "button",
    text: m("risk.add_rule"),
    onclick: () => {
      addRule({ id: moderationRuleID(), name: "", kind: "keyword", pattern: "" });
      change();
      rows.at(-1).pattern.focus();
    }
  });
  const bulkText = el("textarea", { id: prefix + "-bulk-keywords", rows: 4, maxlength: 52000, placeholder: m("risk.bulk_placeholder") });
  const bulkNotice = el("p", { class: "small muted", role: "status" });
  const bulkAdd = el("button", {
    type: "button",
    text: m("risk.bulk_add"),
    onclick: () => {
      try {
        const existing = new Set(rows.filter((row) => row.kind.value === "keyword").map((row) => row.pattern.value.toLowerCase()));
        const pending = [];
        for (const text of bulkText.value.split(/\r?\n/).map((line) => line.trim()).filter(Boolean)) {
          if (new TextEncoder().encode(text).length > 512) throw new UIError(m("risk.bulk_too_long"));
          const key = text.toLowerCase();
          if (!existing.has(key)) {
            existing.add(key);
            pending.push(text);
          }
        }
        if (rows.filter((row) => row.name.value.trim() || row.pattern.value.trim()).length + pending.length > 100)
          throw new UIError(m("risk.rules_limit"));
        if (!bulkText.value.trim()) {
          bulkText.focus();
          return;
        }
        rows.filter((row) => !row.name.value.trim() && !row.pattern.value.trim()).forEach((row) => row.node.remove());
        rows = rows.filter((row) => row.name.value.trim() || row.pattern.value.trim());
        pending.forEach((pattern) => addRule({ id: moderationRuleID(), name: "", kind: "keyword", pattern }));
        bulkText.value = "";
        setText(bulkNotice, m("risk.bulk_added", { count: pending.length }));
        change();
      } catch (error) { setText(bulkNotice, error.message); }
    }
  });
  const bulk = el(
    "details",
    { class: "moderation-bulk" },
    el("summary", { text: m("risk.bulk_title") }),
    moderationField("bulk_keywords", bulkText, "bulk_help"),
    bulkAdd,
    bulkNotice
  );
  const empty = el("p", { class: "small muted", text: m("risk.rules_empty") });
  const strategyHelp = el("p", { class: "small muted moderation-strategy-help" });
  const modelField = moderationField("models", models, "models_help");
  const ruleSection = el(
    "section",
    { class: "moderation-rule-section" },
    el("div", { class: "row spread" }, el("h3", { text: m("risk.rules") }), add),
    el("p", { class: "small muted", text: m("risk.rules_help") }),
    empty,
    rules,
    bulk
  );
  const advanced = el(
    "details",
    { class: "moderation-advanced" },
    el("summary", { text: m("risk.advanced") }),
    el("div", { class: "moderation-grid" }, moderationField("scope", scope, "scope_help"), moderationField("model_mode", filter)),
    modelField,
    el(
      "div",
      { class: "moderation-grid" },
      moderationField("failure", failure, "failure_help"),
      el(
        "label",
        { class: "field moderation-excerpt" },
        el("span", { text: m("risk.excerpt") }),
        switchControl({ input: excerpt }),
        el("span", { class: "small muted", text: m("risk.excerpt_help") })
      )
    )
  );
  const node = el(
    "div",
    { class: "moderation-policy-editor" },
    el("div", { class: "moderation-grid" }, moderationField("mode", mode, "mode_help"), moderationField("strategy", strategy)),
    strategyHelp,
    ruleSection,
    advanced
  );
  function addRule(rule) {
    const name = el("input", { value: rule.name, maxlength: 128, "aria-label": m("risk.rule_name"), placeholder: m("risk.rule_name") });
    const kind = moderationSelect(null, ["keyword", "regex"]);
    kind.value = rule.kind;
    setTextAttribute(kind, "aria-label", m("risk.rule_kind"));
    const pattern = el("input", {
      value: rule.pattern,
      maxlength: 512,
      "aria-label": m("risk.pattern"),
      placeholder: m("risk.pattern"),
      autocomplete: "off",
      spellcheck: "false"
    });
    const remove = el(
      "button",
      {
        type: "button",
        class: "icon-action danger",
        "aria-label": m("risk.remove_rule"),
        title: m("risk.remove_rule"),
        onclick: () => {
          rows = rows.filter((entry) => entry !== row);
          row.node.remove();
          change();
        }
      },
      actionIcon("x")
    );
    const row = { id: rule.id, name, kind, pattern, node: el("div", { class: "moderation-rule" }, name, kind, pattern, remove) };
    rows.push(row);
    rules.append(row.node);
  }
  function update() {
    modelField.hidden = filter.value === "all";
    models.required = filter.value !== "all";
    ruleSection.hidden = strategy.value === "api" && !ruleSection.dataset.validationVisible;
    empty.hidden = rows.length > 0;
    setText(strategyHelp, m(strategy.value === "keywords" ? "risk.local_help" : "risk.external_help"));
    add.disabled = disabled || rows.length >= 100;
    bulkAdd.disabled = disabled;
    bulkText.disabled = disabled;
  }
  function change() {
    update();
    onChange();
  }
  strategy.addEventListener("change", () => { delete ruleSection.dataset.validationVisible; });
  node.addEventListener("input", change);
  node.addEventListener("change", change);
  const editor = {
    node,
    getValue: () => ({
      mode: mode.value,
      strategy: strategy.value,
      scope: scope.value,
      model_mode: filter.value,
      models: models.value.split("\n").map((s) => s.trim()).filter(Boolean),
      rules: rows.filter((row) => row.name.value.trim() || row.pattern.value.trim())
        .map((row) => ({ id: row.id, name: row.name.value.trim(), kind: row.kind.value, pattern: row.pattern.value })),
      failure_action: failure.value,
      log_excerpt: excerpt.checked
    }),
    hasPendingInput: () => !!bulkText.value.trim(),
    revealError: (error) => {
      if (!error.messageKey?.startsWith("risk.validation_rule_")) return;
      const index = Number(error.messageParams?.v0) - 1;
      const row = rows.filter((row) => row.name.value.trim() || row.pattern.value.trim())[index];
      if (row) revealModerationField(row.pattern);
    },
    validate: () => {
      if (bulkText.value.trim()) moderationFieldError(bulkText, m("risk.bulk_pending"));
      const policy = editor.getValue();
      if (policy.mode !== "off" && policy.strategy === "keywords" && !policy.rules.length)
        moderationFieldError(add, m("risk.rules_required"));
      if (policy.model_mode !== "all" && !policy.models.length) moderationFieldError(models, m("risk.models_required"));
      for (const row of rows) {
        if (row.name.value.trim() && !row.pattern.value.trim()) moderationFieldError(row.pattern, m("risk.pattern_required"));
      }
    },
    setValue: (p) => {
      mode.value = p.mode;
      strategy.value = p.strategy;
      scope.value = p.scope;
      filter.value = p.model_mode;
      models.value = p.models.join("\n");
      failure.value = p.failure_action;
      excerpt.checked = p.log_excerpt;
      rows = [];
      rules.replaceChildren();
      p.rules.forEach(addRule);
      bulkText.value = "";
      bulkNotice.replaceChildren();
      delete ruleSection.dataset.validationVisible;
      advanced.open = p.scope !== "latest_user" || p.model_mode !== "all" || p.failure_action !== "block" || p.log_excerpt;
      update();
    },
    setDisabled: (busy) => {
      disabled = busy;
      node.querySelectorAll("input, select, textarea, button").forEach((input) => (input.disabled = busy));
      update();
    }
  };
  editor.setValue(value);
  return editor;
}

function credentialModerationEditor(original, global) {
  let disabled = false;
  const mode = moderationSelect("credential-moderation-override", ["inherit", "off", "custom"]);
  mode.value = original?.mode || "inherit";
  const editor = moderationPolicyEditor("credential-moderation", original?.policy || global);
  const node = el("section", { class: "credential-moderation" }, moderationField("account_policy", mode, "account_help"), editor.node);
  function update() {
    editor.node.hidden = mode.value !== "custom";
    editor.setDisabled(disabled || mode.value !== "custom");
  }
  mode.addEventListener("change", update);
  update();
  return {
    node,
    getValue: () =>
      mode.value === "inherit" ? null : mode.value === "off" ? { mode: "off" } : { mode: "custom", policy: editor.getValue() },
    validate: () => { if (mode.value === "custom") editor.validate(); },
    revealError: (error) => editor.revealError(error),
    setDisabled: (value) => {
      disabled = value;
      mode.disabled = value;
      update();
    }
  };
}

let savedModeration = null;
let moderationKeyConfigured = false;
let moderationLoading = false;
let moderationSaving = false;
let moderationTesting = false;
const moderationEvents = { entries: [], next: "", loading: false, loaded: false, error: "" };
const globalModerationEditor = moderationPolicyEditor("moderation", defaultModerationPolicy(), moderationDraftChanged);
$("moderation-policy-host").append(globalModerationEditor.node);
const moderationAPIAuth = moderationSelect("moderation-api-auth", ["env_key", "no_auth"]);
const moderationAPIFields = {
  url: el("input", { id: "moderation-api-url", type: "url", maxlength: 2048, placeholder: "https://audit.example.com/v1/moderations" }),
  model: el("input", { id: "moderation-api-model", maxlength: 128 }),
  key_env: el("input", { id: "moderation-api-key-env", maxlength: 128, autocomplete: "off", spellcheck: "false" }),
  timeout_ms: el("input", { id: "moderation-api-timeout", type: "number", min: 100, max: 10000, step: 1, required: true }),
  max_in_flight: el("input", { id: "moderation-api-concurrency", type: "number", min: 1, max: 64, step: 1, required: true })
};
const moderationThresholds = el("textarea", {
  id: "moderation-api-thresholds",
  rows: 3,
  maxlength: 5000,
  placeholder: "violence=0.8\nsexual=0.9",
  spellcheck: "false"
});
const moderationAPIKeyField = moderationField("api_key_env", moderationAPIFields.key_env, "api_key_help");
const moderationAPIAdvanced = el(
  "details",
  { class: "moderation-advanced" },
  el("summary", { text: m("risk.api_advanced") }),
  el(
    "div",
    { class: "moderation-grid" },
    moderationField("api_timeout_ms", moderationAPIFields.timeout_ms),
    moderationField("api_max_in_flight", moderationAPIFields.max_in_flight)
  ),
  moderationField("thresholds", moderationThresholds, "thresholds_help")
);
const moderationAPITest = el("button", { type: "button", text: m("risk.api_test"), onclick: () => guard(testModerationAPI) });
const moderationAPIResult = el("div", { id: "moderation-api-result", "aria-live": "polite" });
$("moderation-api-host").append(
  el(
    "div",
    { class: "moderation-grid" },
    moderationField("api_url", moderationAPIFields.url),
    moderationField("api_model", moderationAPIFields.model),
    moderationField("api_auth", moderationAPIAuth),
    moderationAPIKeyField
  ),
  moderationAPIAdvanced,
  el(
    "div",
    { class: "row moderation-api-actions" },
    moderationAPITest,
    el("span", { class: "small muted", text: m("risk.api_test_help") })
  ),
  moderationAPIResult
);
moderationAPIAuth.addEventListener("change", () => {
  if (moderationAPIAuth.value === "env_key" && !moderationAPIFields.key_env.value)
    moderationAPIFields.key_env.value = "CPA_POLICY_MODERATION_API_KEY";
  moderationDraftChanged();
});
$("moderation-strategy").addEventListener("change", () => {
  $("moderation-api-settings").open = $("moderation-strategy").value !== "keywords";
});
$("moderation-api-host").addEventListener("input", () => moderationAPIResult.replaceChildren());
$("moderation-api-host").addEventListener("change", () => moderationAPIResult.replaceChildren());

function moderationAPIDraft() {
  const thresholds = {};
  for (const line of moderationThresholds.value.split("\n").map((s) => s.trim()).filter(Boolean)) {
    const match = /^([a-z][a-z0-9_/-]*)\s*=\s*(0(?:\.\d+)?|1(?:\.0+)?)$/.exec(line);
    if (!match || Object.hasOwn(thresholds, match[1]))
      throw Object.assign(new UIError(m("risk.threshold_invalid")), { focusTarget: moderationThresholds });
    thresholds[match[1]] = Number(match[2]);
  }
  return {
    ...Object.fromEntries(
      Object.entries(moderationAPIFields).map(([key, input]) => [key, input.type === "number" ? Number(input.value) : input.value.trim()])
    ),
    key_env: moderationAPIAuth.value === "no_auth" ? "" : moderationAPIFields.key_env.value.trim(),
    thresholds
  };
}
function moderationDraft() { return { ...globalModerationEditor.getValue(), api: moderationAPIDraft() }; }
function validateModerationAPI(required) {
  if (required && !moderationAPIFields.url.value.trim()) moderationFieldError(moderationAPIFields.url, m("risk.api_url_required"));
  if (required && !moderationAPIFields.model.value.trim()) moderationFieldError(moderationAPIFields.model, m("risk.api_model_required"));
  if (moderationAPIAuth.value === "env_key" && !/^[A-Za-z_][A-Za-z0-9_]{0,127}$/.test(moderationAPIFields.key_env.value.trim()))
    moderationFieldError(moderationAPIFields.key_env, m("risk.env_invalid"));
  for (const input of Object.values(moderationAPIFields)) {
    if (!input.disabled && !input.validity.valid) {
      revealModerationField(input);
      input.reportValidity();
      return false;
    }
  }
  return true;
}
function moderationSettingsDirty() {
  if (!savedModeration) return false;
  try {
    return globalModerationEditor.hasPendingInput() || JSON.stringify(moderationDraft()) !== JSON.stringify(savedModeration);
  } catch (_) { return true; }
}
function moderationDraftChanged() {
  $("moderation-test-result").replaceChildren();
  $("moderation-error").classList.remove("show");
  updateModerationControls();
}
function updateModerationControls() {
  const busy = moderationLoading || moderationSaving || moderationTesting;
  globalModerationEditor.setDisabled(busy || !savedModeration);
  for (const input of [...Object.values(moderationAPIFields), moderationThresholds, moderationAPIAuth])
    input.disabled = busy || !savedModeration;
  moderationAPIKeyField.hidden = moderationAPIAuth.value === "no_auth";
  moderationAPIFields.key_env.disabled = busy || !savedModeration || moderationAPIKeyField.hidden;
  moderationAPITest.disabled = busy || !savedModeration;
  const key = moderationAPIAuth.value === "no_auth" ? "" : moderationAPIFields.key_env.value.trim();
  setText(
    $("moderation-key-status"),
    m(
      !key
        ? "risk.key_not_needed"
        : key !== savedModeration?.api.key_env ? "risk.key_unsaved" : moderationKeyConfigured ? "risk.key_ready" : "risk.key_missing"
    )
  );
  $("moderation-save").disabled = busy || !savedModeration;
  $("moderation-discard").disabled = busy || !moderationSettingsDirty();
  $("moderation-test-run").disabled = busy || !savedModeration;
  $("moderation-test-text").disabled = busy || !savedModeration;
  $("moderation-test-model").disabled = busy || !savedModeration;
  if (currentRole) updateRefreshControls();
}
function applyModerationSettings(response) {
  const config = requireObject(response.moderation, m("risk.title"));
  requireObject(config.api, m("risk.api_settings"));
  globalModerationEditor.setValue(config);
  for (const [key, input] of Object.entries(moderationAPIFields)) input.value = config.api[key];
  moderationAPIAuth.value = config.api.key_env ? "env_key" : "no_auth";
  moderationKeyConfigured = !!response.api_key_configured;
  moderationAPIAdvanced.open =
    config.api.timeout_ms !== 3000 || config.api.max_in_flight !== 8 || Object.keys(config.api.thresholds).length > 0;
  $("moderation-api-settings").open = config.strategy !== "keywords";
  moderationAPIResult.replaceChildren();
  moderationThresholds.value = Object.entries(config.api.thresholds).map(([key, value]) => `${key}=${value}`).join("\n");
  savedModeration = moderationDraft();
  credentialModerationQuery.set("moderation", response);
}
async function loadModerationSettings(reload = false) {
  if (moderationLoading || moderationSaving || (savedModeration && !reload)) return;
  moderationLoading = true;
  updateModerationControls();
  try {
    const response = await plugin("GET", "/moderation");
    applyModerationSettings(response);
    $("moderation-error").classList.remove("show");
  } catch (error) {
    if (error instanceof AuthError || error instanceof StaleRequestError) throw error;
    if (error.focusTarget) revealModerationField(error.focusTarget);
    setText($("moderation-error"), error.message);
    $("moderation-error").classList.add("show");
  } finally {
    moderationLoading = false;
    updateModerationControls();
  }
}
async function saveModerationSettings() {
  if (moderationSaving || moderationLoading || !savedModeration) return;
  const generation = sessionGeneration;
  try {
    globalModerationEditor.validate();
    const policy = globalModerationEditor.getValue();
    if (!validateModerationAPI(policy.mode !== "off" && policy.strategy !== "keywords")) return;
    const draft = moderationDraft();
    moderationSaving = true;
    updateModerationControls();
    const latest = (await plugin("GET", "/moderation")).moderation;
    if (JSON.stringify(latest) !== JSON.stringify(savedModeration)) throw new UIError(m("risk.changed"));
    const validated = (await plugin("POST", "/moderation/validate", draft, { command: true })).moderation;
    await api("PATCH", PLUGIN_BASE + "/config", { moderation: validated }, { command: true });
    let confirmed = null;
    for (let attempt = 0; attempt < 12; attempt++) {
      if (generation !== sessionGeneration) throw new StaleRequestError();
      const response = await plugin("GET", "/moderation");
      if (JSON.stringify(response.moderation) === JSON.stringify(validated)) {
        confirmed = response;
        break;
      }
      await new Promise((resolve) => setTimeout(resolve, 250));
    }
    if (!confirmed) throw new UIError(m("location.reload_pending"));
    applyModerationSettings(confirmed);
    $("moderation-error").classList.remove("show");
    notify(m("risk.saved"));
  } catch (error) {
    if (error instanceof AuthError || error instanceof StaleRequestError) throw error;
    moderationSaving = false;
    updateModerationControls();
    globalModerationEditor.revealError(error);
    if (error.focusTarget) revealModerationField(error.focusTarget);
    setText($("moderation-error"), error.message);
    $("moderation-error").classList.add("show");
  } finally {
    moderationSaving = false;
    updateModerationControls();
  }
}
function moderationResultNode(result) {
  const status = result.status === "error" ? "error" : result.status;
  const detail = [
    result.reason ? m("risk.reason_" + result.reason) : "",
    result.rules?.length
      ? m("risk.matched_rules", { value: result.rules.map((id, index) => result.rule_names?.[index] || id).join(", ") })
      : "",
    result.categories?.length ? m("risk.matched_categories", { value: result.categories.join(", ") }) : "",
    result.media_ignored ? m("risk.media_ignored") : ""
  ].filter(Boolean);
  return el(
    "div",
    { class: "moderation-result", "data-status": status },
    el("strong", { text: m("risk.status_" + status) }),
    el("span", { class: "tag", text: m("risk.action_" + result.action) }),
    ...detail.map((text) => el("p", { class: "small muted", text }))
  );
}
async function testModerationDraft() {
  if (moderationTesting || !savedModeration) return;
  const target = $("moderation-test-result");
  try {
    globalModerationEditor.validate();
    const policy = globalModerationEditor.getValue();
    if (!validateModerationAPI(policy.mode !== "off" && policy.strategy !== "keywords")) return;
    const config = moderationDraft();
    moderationTesting = true;
    updateModerationControls();
    target.replaceChildren(el("p", { text: m("ui.loading") }));
    const { result } = await plugin(
      "POST",
      "/moderation/test",
      { config, text: $("moderation-test-text").value, model: $("moderation-test-model").value.trim() },
      { command: true, timeoutMS: 15000 }
    );
    target.replaceChildren(moderationResultNode(result));
  } catch (error) {
    if (error instanceof AuthError || error instanceof StaleRequestError) throw error;
    moderationTesting = false;
    updateModerationControls();
    globalModerationEditor.revealError(error);
    if (error.focusTarget) revealModerationField(error.focusTarget);
    target.replaceChildren(el("p", { class: "notice err show", text: error.message }));
  } finally {
    moderationTesting = false;
    updateModerationControls();
  }
}
async function testModerationAPI() {
  if (moderationTesting || moderationSaving || !savedModeration) return;
  try {
    if (!validateModerationAPI(true)) return;
    const config = { ...defaultModerationPolicy(), mode: "observe", strategy: "api", api: moderationAPIDraft() };
    moderationTesting = true;
    updateModerationControls();
    moderationAPIResult.replaceChildren(el("p", { text: m("ui.loading") }));
    const { result } = await plugin(
      "POST",
      "/moderation/test",
      { config, text: "CPA Policy connection test." },
      { command: true, timeoutMS: 15000 }
    );
    const ok = result.status === "passed" || result.status === "flagged";
    moderationAPIResult.replaceChildren(
      el("p", { class: "notice show " + (ok ? "ok" : "err"), text: ok ? m("risk.api_test_ok") : m("risk.reason_" + result.reason) })
    );
  } catch (error) {
    if (error instanceof AuthError || error instanceof StaleRequestError) throw error;
    if (error.focusTarget) revealModerationField(error.focusTarget);
    moderationAPIResult.replaceChildren(el("p", { class: "notice err show", text: error.message }));
  } finally {
    moderationTesting = false;
    updateModerationControls();
  }
}
async function loadModerationEvents(reload = false, more = false) {
  if (moderationEvents.loading || (moderationEvents.loaded && !reload && !more)) return;
  moderationEvents.loading = true;
  renderModerationEvents();
  try {
    const query = new URLSearchParams({ limit: "50" });
    if (more && moderationEvents.next) query.set("before_id", moderationEvents.next);
    const response = await plugin("GET", "/moderation/events?" + query);
    const entries = requireArray(response.entries, m("risk.records"));
    moderationEvents.entries = more ? [...moderationEvents.entries, ...entries] : entries;
    moderationEvents.next = response.next_before_id || "";
    moderationEvents.loaded = true;
    moderationEvents.error = "";
  } catch (error) {
    if (error instanceof AuthError || error instanceof StaleRequestError) throw error;
    moderationEvents.error = error.message;
  } finally {
    moderationEvents.loading = false;
    renderModerationEvents();
    if (currentRole) updateRefreshControls();
  }
}
function renderModerationEvents() {
  const target = $("moderation-events");
  target.replaceChildren();
  if (moderationEvents.error) target.append(el("p", { class: "notice err show", text: moderationEvents.error }));
  if (!moderationEvents.entries.length)
    target.append(el("div", { class: "empty", text: m(moderationEvents.loading ? "ui.loading" : "risk.no_records") }));
  for (const entry of moderationEvents.entries)
    target.append(
      el(
        "article",
        { class: "diagnostic-entry moderation-event" },
        el(
          "div",
          { class: "row spread" },
          el("strong", { text: [entry.model, entry.source_format].filter(Boolean).join(DISPLAY_SEPARATOR) }),
          el("span", { class: "small muted", text: when(entry.at) })
        ),
        el("p", { class: "small muted", text: entry.auth_index || "—" }),
        moderationResultNode(entry),
        entry.excerpt
          ? el("details", {}, el("summary", { text: m("risk.excerpt") }), el("pre", { class: "privacy-value", text: entry.excerpt }))
          : null
      )
    );
  $("moderation-more").hidden = !moderationEvents.next;
  $("moderation-more").disabled = moderationEvents.loading;
}
function resetModeration() {
  savedModeration = null;
  moderationKeyConfigured = false;
  moderationAPIResult.replaceChildren();
  globalModerationEditor.setValue(defaultModerationPolicy());
  $("moderation-test-text").value = "";
  $("moderation-test-result").replaceChildren();
  Object.assign(moderationEvents, { entries: [], next: "", loading: false, loaded: false, error: "" });
  renderModerationEvents();
  updateModerationControls();
}
$("moderation-form").addEventListener("invalid", (event) => revealModerationField(event.target), true);
$("moderation-form").addEventListener("submit", (event) => {
  event.preventDefault();
  guard(saveModerationSettings);
});
$("moderation-form").addEventListener("input", moderationDraftChanged);
$("moderation-test-form").addEventListener("input", () => $("moderation-test-result").replaceChildren());
$("moderation-test-form").addEventListener("submit", (event) => {
  event.preventDefault();
  guard(testModerationDraft);
});
$("moderation-more").onclick = () => guard(() => loadModerationEvents(false, true));
$("moderation-discard").onclick = () =>
  guard(async () => {
    if (!moderationSettingsDirty() || !(await confirmSettingsReload(true, true))) return;
    await loadModerationSettings(true);
  });
updateModerationControls();
