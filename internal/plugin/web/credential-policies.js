async function readCredentialPolicies() {
  const result = await plugin("GET", "/credential-policies");
  const label = m("policy.concurrency_column");
  requireObject(result, label);
  if (!Object.hasOwn(result, "policies") || !Object.hasOwn(result, "active")) throw new UIError(m("policy.concurrency_unavailable"));
  const policies = requireObject(result.policies ?? {}, label);
  const active = requireObject(result.active, label);
  const concurrency = requireConcurrencySettings(result.concurrency);
  const waiting = requireObject(result.waiting, label);
  for (const policy of Object.values(policies)) {
    requireObject(policy, label);
    if (!Number.isSafeInteger(policy.max_concurrency) || policy.max_concurrency < 0) throw new UIError(m("policy.concurrency_unavailable"));
  }
  if ([...Object.values(active), ...Object.values(waiting)].some((count) => !Number.isSafeInteger(count) || count < 0))
    throw new UIError(m("policy.concurrency_unavailable"));
  return { policies, active, waiting, concurrency };
}

function readCredentialConcurrency(reload = false) {
  if (credentialConcurrencyQuery.error) credentialConcurrencyQuery.value = null;
  return credentialConcurrencyQuery.task || credentialConcurrencyQuery.read("credential-policies", readCredentialPolicies, reload);
}

function credentialConcurrencyCell(index) {
  const value = el("span", { class: "concurrency-value", "data-concurrency-value": "" });
  updateCredentialConcurrency(index, value);
  return el(
    "td",
    { class: "credential-concurrency-cell" },
    el("span", { class: "concurrency-label", text: m("policy.concurrency_column") }),
    value
  );
}

function updateCredentialConcurrency(index, value) {
  const query = credentialConcurrencyQuery;
  const known = !!index && !!query.value && !query.error;
  const policy = query.value?.policies[index];
  const limit = known ? (policy?.max_concurrency ?? 0) : null;
  const active = known ? (query.value.active[index] ?? 0) : null;
  setText(value, known ? `${int(active)}/${limit === 0 ? "∞" : int(limit)}` : "—");
  value.classList.toggle("at-limit", known && limit > 0 && active >= limit);
  const description = known
    ? m("policy.concurrency_summary", { active: int(active), limit: limit === 0 ? m("ui.unlimited") : int(limit) })
    : query.error || !index ? m("policy.concurrency_unavailable") : m("ui.loading");
  setTextAttribute(value, "title", description);
  setTextAttribute(value, "aria-label", description);
}

function renderCredentialConcurrency() {
  if (!currentRole) return;
  if (["auth-files", "ai-providers"].includes(activeTab())) {
    for (const row of $(activeTab() + "-body").querySelectorAll("[data-auth-index]"))
      updateCredentialConcurrency(row.dataset.authIndex, row.querySelector("[data-concurrency-value]"));
  }
  renderDataStatus();
  updateRefreshControls();
}

function defaultCredentialPolicy() {
  return { max_concurrency: 0, location_mode: "inherit", location: { country: "", region: "", city: "", timezone: "" } };
}

function credentialSettingsButton(row, kind, busy = false) {
  const name = row.email || row.name || row.base_url || row.auth_index;
  return el(
    "button",
    {
      type: "button",
      class: "labeled-icon-button credential-icon-button credential-settings-button",
      disabled: !row.auth_index || busy,
      "aria-label": m("policy.edit", { name }),
      "data-tip": m("ui.settings"),
      onclick: (event) => {
        const button = event.currentTarget;
        button.disabled = true;
        guard(async () => { try { await editCredentialPolicy(row, kind, name); } finally { button.disabled = !row.auth_index; } });
      }
    },
    actionIcon("settings")
  );
}

// Both the global form and credential dialog use this compact location control.
function locationEditor({
  prefix,
  location,
  enabled,
  request,
  enableLabel = m("location.enable"),
  onChange = () => {},
  onBusy = () => {}
}) {
  let disabled = false;
  let detecting = false;
  const toggle = el("input", {
    id: prefix + "-enabled",
    type: "checkbox",
    role: "switch",
    checked: enabled,
    "aria-label": enableLabel,
    "aria-controls": prefix + "-fields"
  });
  const inputs = Object.fromEntries(
    LOCATION_FIELDS.map((field) => [
      field,
      el("input", {
        id: prefix + "-" + field,
        value: location?.[field] || "",
        autocomplete: "off",
        maxlength: field === "country" ? 2 : 128,
        placeholder: m("location.example_" + field)
      })
    ])
  );
  const fields = el(
    "div",
    { id: prefix + "-fields", class: "location-fields" },
    ...LOCATION_FIELDS.map((field) =>
      el(
        "label",
        { class: "field" },
        el("span", {}, m("location." + field), el("span", { class: "location-required", text: " *", "aria-hidden": "true" })),
        inputs[field]
      )
    )
  );
  const notice = el("p", { id: prefix + "-detection-notice", class: "small muted location-detection-notice", role: "status" });
  const detect = el(
    "button",
    {
      id: prefix + "-detect",
      type: "button",
      class: "location-detect-button",
      "aria-label": m("location.detect"),
      title: m("location.detect")
    },
    iconSVG(
      '<svg xmlns="http://www.w3.org/2000/svg" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><circle cx="12" cy="12" r="7"/><circle cx="12" cy="12" r="3"/><path d="M12 2v3m0 14v3M2 12h3m14 0h3"/></svg>'
    )
  );
  const node = el(
    "section",
    { class: "location-editor" },
    el(
      "div",
      { class: "location-editor-header" },
      el("label", { class: "location-editor-label" }, switchControl({ input: toggle }), el("span", { text: enableLabel })),
      detect
    ),
    fields,
    notice
  );
  function update() {
    toggle.disabled = disabled || detecting;
    toggle.setAttribute("aria-expanded", String(toggle.checked));
    fields.hidden = !toggle.checked;
    for (const input of Object.values(inputs)) {
      input.disabled = disabled || detecting || !toggle.checked;
      input.required = toggle.checked;
    }
    detect.disabled = disabled || detecting;
    detect.setAttribute("aria-busy", String(detecting));
    detect.classList.toggle("detecting", detecting);
  }
  const editor = {
    node,
    toggle,
    notice,
    getLocation: () => Object.fromEntries(LOCATION_FIELDS.map((field) => [field, inputs[field].value.trim()])),
    setLocation: (value) => { for (const field of LOCATION_FIELDS) inputs[field].value = value?.[field] || ""; },
    setEnabled: (value) => {
      toggle.checked = value;
      update();
    },
    setDisabled: (value) => {
      disabled = value;
      update();
    }
  };
  toggle.addEventListener("change", () => {
    update();
    onChange();
  });
  for (const input of Object.values(inputs)) input.addEventListener("input", onChange);
  detect.addEventListener("click", () =>
    guard(async () => {
      const generation = sessionGeneration;
      detecting = true;
      update();
      onBusy(true);
      setText(notice, m("location.detecting"));
      try {
        const result = await plugin("POST", "/location/detect", typeof request === "function" ? request() : request, {
          command: true,
          timeoutMS: 90000
        });
        if (generation !== sessionGeneration || !node.isConnected) throw new StaleRequestError();
        editor.setLocation(result.location);
        editor.setEnabled(true);
        onChange();
        setText(notice, m("location.detected", { ips: result.exit_ips.join(", ") }));
      } catch (error) {
        if (error instanceof AuthError || error instanceof StaleRequestError) throw error;
        setText(notice, m("location.detect_failed") + " " + (error.message || String(error)));
      } finally {
        detecting = false;
        update();
        onBusy(false);
      }
    })
  );
  update();
  return editor;
}

async function editCredentialPolicy(row, kind, name) {
  const generation = sessionGeneration;
  const supportsLocation = (row.category || row.type) === "codex" || (kind === "provider" && row.type === "openai");
  const [current, global, risk] = await Promise.all([
    readCredentialPolicies(),
    supportsLocation ? plugin("GET", "/location") : null,
    plugin("GET", "/moderation")
  ]);
  if (generation !== sessionGeneration) throw new StaleRequestError();
  const original = current.policies[row.auth_index] || defaultCredentialPolicy();
  const draft = structuredClone(original);
  const defaults = global?.custom_location;
  let mode = draft.location_mode || "inherit";
  let saving = false;
  let detecting = false;
  const dialog = el("dialog", { class: "settings-dialog credential-policy-dialog", "aria-labelledby": "credential-policy-title" });
  const limit = el("input", {
    id: "credential-policy-limit",
    type: "number",
    min: 0,
    max: 100000,
    step: 1,
    required: true,
    value: draft.max_concurrency
  });
  const error = el("p", { class: "credential-error", role: "alert" });
  const cancel = el("button", { type: "button", text: m("ui.cancel"), onclick: () => dialog.close() });
  const close = el(
    "button",
    { type: "button", class: "dialog-close", "aria-label": m("ui.close"), onclick: () => dialog.close() },
    actionIcon("x")
  );
  const save = el("button", { type: "submit", class: "primary", text: m("ui.save") });
  const riskEditor = credentialModerationEditor(draft.moderation, risk.moderation);
  const editor = supportsLocation
    ? locationEditor({
        prefix: "credential-policy",
        location: mode === "inherit" ? defaults : draft.location,
        enabled: mode === "inherit" ? defaults.enabled : mode === "custom",
        request:
          kind === "provider" ? { auth_index: row.auth_index, configured_proxy: row.proxy_url || "" } : { auth_index: row.auth_index },
        onChange: () => {
          mode = editor.toggle.checked ? "custom" : "preserve";
          update();
        },
        onBusy: (value) => {
          detecting = value;
          update();
        }
      })
    : null;
  function update() {
    limit.disabled = saving;
    cancel.disabled = saving;
    close.disabled = saving;
    save.disabled = saving || detecting;
    editor?.setDisabled(saving);
    riskEditor.setDisabled(saving);
  }
  const form = el(
    "form",
    {},
    el("div", { class: "dialog-header" }, el("h2", { id: "credential-policy-title", text: m("policy.title") }), close),
    el(
      "div",
      { class: "dialog-body" },
      el("p", { class: "muted policy-identity", text: name }),
      el("label", { class: "field" }, el("span", { text: m("policy.concurrency") }), limit),
      el("p", { class: "small muted", text: m("policy.concurrency_help") }),
      editor?.node,
      riskEditor.node,
      error
    ),
    el("div", { class: "row dialog-actions" }, cancel, save)
  );
  form.addEventListener("invalid", (event) => revealModerationField(event.target), true);
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    guard(async () => {
      if (saving || detecting) return;
      try { riskEditor.validate(); } catch (failure) {
        setText(error, failure.message);
        return;
      }
      const policy = {
        ...draft,
        max_concurrency: Number(limit.value),
        location_mode: mode,
        location: mode === "inherit" || !editor ? draft.location : editor.getLocation()
      };
      const moderation = riskEditor.getValue();
      if (moderation) policy.moderation = moderation; else delete policy.moderation;
      saving = true;
      update();
      setText(error, "");
      try {
        const latest = await readCredentialPolicies();
        if (JSON.stringify(latest.policies[row.auth_index] || defaultCredentialPolicy()) !== JSON.stringify(original))
          throw new UIError(m("policy.changed"));
        const validated = await plugin(
          "POST",
          "/credential-policies/validate",
          { ...latest.policies, [row.auth_index]: policy },
          { command: true }
        );
        await api("PATCH", PLUGIN_BASE + "/config", { credential_policies: validated.policies }, { command: true });
        let confirmed = false;
        for (let attempt = 0; attempt < 12; attempt++) {
          if (generation !== sessionGeneration) throw new StaleRequestError();
          const response = await readCredentialPolicies();
          if (JSON.stringify(response.policies[row.auth_index]) === JSON.stringify(validated.policies[row.auth_index])) {
            confirmed = true;
            break;
          }
          await new Promise((resolve) => setTimeout(resolve, 250));
        }
        if (!confirmed) throw new UIError(m("location.reload_pending"));
        credentialConcurrencyQuery.clear();
        dialog.close();
        if (kind === "provider") {
          await settleLoads([readAIProviders(true), readCredentialConcurrency(true)]);
          renderAIProviders();
        } else {
          await settleLoads([readAuthFiles(true), readCredentialConcurrency(true)]);
          renderAuthFiles();
        }
        notify(m("policy.saved"));
      } catch (failure) {
        if (failure instanceof AuthError || failure instanceof StaleRequestError) throw failure;
        saving = false;
        update();
        riskEditor.revealError(failure);
        setText(error, failure.message || String(failure));
      } finally {
        saving = false;
        update();
      }
    });
  });
  update();
  dialog.append(form);
  dialog.addEventListener("cancel", (event) => { if (saving) event.preventDefault(); });
  dialog.addEventListener("close", () => dialog.remove(), { once: true });
  document.body.append(dialog);
  dialog.showModal();
  limit.focus();
}
