function createQuery(label, receive = (value) => value, render = renderPage) {
  const query = {
    label,
    value: null,
    error: "",
    readAt: 0,
    key: "",
    task: null,
    request: null,
    clear() { Object.assign(query, { value: null, error: "", readAt: 0, key: "", task: null, request: null }); },
    read(key, fetch, reload = false) {
      if (key !== query.key) query.clear();
      if (!reload && query.task) return query.task;
      if (!reload && (query.value !== null || query.error)) return Promise.resolve(query.value);
      const request = {};
      query.key = key;
      query.request = request;
      query.error = "";
      const task = Promise.resolve().then(() => {
        if (query.request !== request) throw new StaleRequestError();
        return fetch();
      })
        .then((value) => {
          if (query.request !== request) throw new StaleRequestError();
          query.value = receive(value);
          return query.value;
        })
        .catch((error) => {
          if (query.request !== request) throw new StaleRequestError();
          if (error instanceof AuthError || error instanceof StaleRequestError) throw error;
          // Ordinary read failures belong to the query's inline status.
          query.error = error.message || String(error);
          return query.value;
        })
        .finally(() => {
          if (query.request === request) {
            query.task = null;
            query.readAt = Date.now();
            render();
          }
        });
      query.task = task;
      updateRefreshControls();
      return task;
    }
  };
  return query;
}
const authFilesQueries = {
  admin: createQuery(m("ui.auth_files"), (files) => {
    updateAuthFiles(files);
    return files;
  })
};
const aiProvidersQuery = createQuery(m("ui.ai_providers"), (providers) => {
  updateAIProviders(providers);
  return providers;
});
const credentialConcurrencyQuery = createQuery(m("policy.concurrency_column"), (value) => value, renderCredentialConcurrency);
function viewPreferenceKey(role, name) { return VIEW_PREFERENCE_PREFIX + (credentialScope.admin || "") + "." + name; }
function saveChoice(role, id) {
  const control = $(id);
  const value = control.type === "checkbox" ? control.checked : control.value;
  try { localStorage.setItem(viewPreferenceKey(role, id), JSON.stringify(value)); } catch (_) {}
}
function restoreRolePreferences() {
  for (const id of ["auth-enabled-only", "ai-provider-enabled-only", "plugin-log-level"]) {
    const control = $(id);
    try {
      const value = JSON.parse(localStorage.getItem(viewPreferenceKey("admin", id)));
      if (value !== null) { if (control.type === "checkbox") control.checked = !!value; else control.value = value; }
    } catch (_) {}
  }
}
function resetRoleData() {
  authFilesQueries.admin.clear();
  aiProvidersQuery.clear();
  credentialConcurrencyQuery.clear();
  Object.values(credentialHealthQueries).forEach((query) => query.clear());
  resetDiagnostics();
  resetModeration();
}
function renderDataStatus() {
  if (!currentRole) return;
  const tab = activeTab();
  const query = tab === "auth-files" ? authFilesQueries.admin : tab === "ai-providers" ? aiProvidersQuery : null;
  const credentials = ["auth-files", "ai-providers"].includes(tab);
  const health = credentialHealthQueries[tab === "auth-files" ? "auth" : "provider"];
  const error =
    query?.error || (credentials && credentialConcurrencyQuery.error ? m("policy.concurrency_unavailable") : "") ||
    (credentials && health.error ? m("health.load_failed") : "");
  setText($("admin-data-status"), error);
  $("admin-data-status").classList.toggle("show", !!error);
}
function renderPage() {
  if (!currentRole) return;
  const tab = activeTab();
  renderDataStatus();
  if (tab === "auth-files") renderAuthFiles();
  else if (tab === "ai-providers") renderAIProviders();
  else if (tab === "moderation") renderModerationEvents();
  else if (tab === "errors") renderDiagnostics("errors"); else if (tab === "settings") renderDiagnostics("logs");
  updateRefreshControls();
}
async function loadPage({ reload = false } = {}) {
  if (!currentRole) return;
  const tab = activeTab();
  if (tab === "auth-files") await settleLoads([readAuthFiles(reload), readCredentialConcurrency(true), readCredentialHealth("auth", true)]);
  else if (tab === "ai-providers")
    await settleLoads([readAIProviders(reload), readCredentialConcurrency(true), readCredentialHealth("provider", true)]);
  else if (tab === "errors") await loadDiagnostics("errors", reload);
  else if (tab === "moderation") await settleLoads([loadModerationSettings(reload), loadModerationEvents(reload)]);
  else if (tab === "settings")
    await settleLoads([loadDiagnostics("logs", reload), loadConcurrencySettings(reload), ...(reload ? [loadLocationSettings()] : [])]);
  renderPage();
}
