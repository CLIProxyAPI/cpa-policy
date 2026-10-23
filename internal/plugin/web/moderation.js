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
    }
  });
  const modelField = moderationField("models", models, "models_help");
  const ruleSection = el(
    "section",
    { class: "moderation-rule-section" },
    el("div", { class: "row spread" }, el("h3", { text: m("risk.rules") }), add),
    el("p", { class: "small muted", text: m("risk.rules_help") }),
    rules
  );
  const node = el(
    "div",
    { class: "moderation-policy-editor" },
    el(
      "div",
      { class: "moderation-grid" },
      moderationField("mode", mode, "mode_help"),
      moderationField("strategy", strategy),
      moderationField("scope", scope, "scope_help"),
      moderationField("model_mode", filter)
    ),
    modelField,
    ruleSection,
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
  function addRule(rule) {
    const name = el("input", { value: rule.name, maxlength: 128, "aria-label": m("risk.rule_name"), placeholder: m("risk.rule_name") });
    const kind = moderationSelect(null, ["keyword", "regex"]);
    kind.value = rule.kind;
    setTextAttribute(kind, "aria-label", m("risk.rule_kind"));
    const pattern = el("input", {
      value: rule.pattern,
      maxlength: 512,
      required: true,
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
    ruleSection.hidden = strategy.value === "api";
    for (const row of rows) row.pattern.required = strategy.value !== "api";
    add.disabled = disabled || rows.length >= 100;
  }
  function change() {
    update();
    onChange();
  }
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
      rules: rows.map((row) => ({ id: row.id, name: row.name.value.trim(), kind: row.kind.value, pattern: row.pattern.value })),
      failure_action: failure.value,
      log_excerpt: excerpt.checked
    }),
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
    setDisabled: (value) => {
      disabled = value;
      mode.disabled = value;
      update();
    }
  };
}

let savedModeration = null;
let moderationLoading = false;
let moderationSaving = false;
let moderationTesting = false;
const moderationEvents = { entries: [], next: "", loading: false, loaded: false, error: "" };
const globalModerationEditor = moderationPolicyEditor("moderation", defaultModerationPolicy(), moderationDraftChanged);
$("moderation-policy-host").append(globalModerationEditor.node);
const moderationAPIFields = {
  url: el("input", { id: "moderation-api-url", type: "url", maxlength: 2048, placeholder: "https://audit.example.com/v1/moderations" }),
  model: el("input", { id: "moderation-api-model", maxlength: 128 }),
  key_env: el("input", { id: "moderation-api-key-env", maxlength: 128, autocomplete: "off", spellcheck: "false" }),
  timeout_ms: el("input", { id: "moderation-api-timeout", type: "number", min: 100, max: 10000, step: 100 }),
  max_in_flight: el("input", { id: "moderation-api-concurrency", type: "number", min: 1, max: 64, step: 1 })
};
const moderationThresholds = el("textarea", {
  id: "moderation-api-thresholds",
  rows: 3,
  maxlength: 5000,
  placeholder: "violence=0.8\nsexual=0.9",
  spellcheck: "false"
});
$("moderation-api-host").append(
  el(
    "div",
    { class: "moderation-grid" },
    ...Object.entries(moderationAPIFields).map(([key, input]) =>
      moderationField("api_" + key, input, key === "key_env" ? "api_key_help" : null)
    )
  ),
  moderationField("thresholds", moderationThresholds, "thresholds_help")
);

function moderationDraft() {
  const thresholds = {};
  for (const line of moderationThresholds.value.split("\n").map((s) => s.trim()).filter(Boolean)) {
    const match = /^([a-z][a-z0-9_/-]*)\s*=\s*(0(?:\.\d+)?|1(?:\.0+)?)$/.exec(line);
    if (!match || Object.hasOwn(thresholds, match[1])) throw new UIError(m("risk.threshold_invalid"));
    thresholds[match[1]] = Number(match[2]);
  }
  return {
    ...globalModerationEditor.getValue(),
    api: {
      ...Object.fromEntries(
        Object.entries(moderationAPIFields).map(([key, input]) => [key, input.type === "number" ? Number(input.value) : input.value.trim()])
      ),
      thresholds
    }
  };
}
function moderationSettingsDirty() {
  if (!savedModeration) return false;
  try { return JSON.stringify(moderationDraft()) !== JSON.stringify(savedModeration); } catch (_) { return true; }
}
function moderationDraftChanged() {
  $("moderation-test-result").replaceChildren();
  updateModerationControls();
}
function updateModerationControls() {
  const busy = moderationLoading || moderationSaving || moderationTesting;
  globalModerationEditor.setDisabled(busy || !savedModeration);
  for (const input of [...Object.values(moderationAPIFields), moderationThresholds]) input.disabled = busy || !savedModeration;
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
  moderationThresholds.value = Object.entries(config.api.thresholds).map(([key, value]) => `${key}=${value}`).join("\n");
  savedModeration = moderationDraft();
  setText($("moderation-key-status"), m(response.api_key_configured ? "risk.key_ready" : "risk.key_missing"));
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
    setText($("moderation-error"), error.message);
    $("moderation-error").classList.add("show");
  } finally {
    moderationLoading = false;
    updateModerationControls();
  }
}
async function saveModerationSettings() {
  if (moderationSaving || moderationLoading || !savedModeration) return;
  moderationSaving = true;
  updateModerationControls();
  const generation = sessionGeneration;
  try {
    const draft = moderationDraft();
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
  moderationTesting = true;
  updateModerationControls();
  const target = $("moderation-test-result");
  target.replaceChildren(el("p", { text: m("ui.loading") }));
  try {
    const { result } = await plugin(
      "POST",
      "/moderation/test",
      { config: moderationDraft(), text: $("moderation-test-text").value, model: $("moderation-test-model").value.trim() },
      { command: true, timeoutMS: 15000 }
    );
    target.replaceChildren(moderationResultNode(result));
  } catch (error) {
    if (error instanceof AuthError || error instanceof StaleRequestError) throw error;
    target.replaceChildren(el("p", { class: "notice err show", text: error.message }));
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
  globalModerationEditor.setValue(defaultModerationPolicy());
  $("moderation-test-text").value = "";
  $("moderation-test-result").replaceChildren();
  Object.assign(moderationEvents, { entries: [], next: "", loading: false, loaded: false, error: "" });
  renderModerationEvents();
  updateModerationControls();
}
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
