let savedConcurrencySettings = null;
let concurrencySaving = false;
let concurrencyLoading = false;
const CONCURRENCY_FIELDS = [["account_queue_capacity", 0, 1000], ["queue_timeout_seconds", 1, 120]];
function requireConcurrencySettings(value) {
  requireObject(value, m("concurrency.title"));
  for (const [field, min, max] of CONCURRENCY_FIELDS) {
    if (!Number.isSafeInteger(value[field]) || value[field] < min || value[field] > max)
      throw new UIError(m("concurrency.invalid_response"));
  }
  return Object.fromEntries(CONCURRENCY_FIELDS.map(([field]) => [field, value[field]]));
}
function updateConcurrencyControls() {
  const busy = concurrencySaving || concurrencyLoading;
  for (const [field] of CONCURRENCY_FIELDS) $("concurrency-" + field).disabled = busy || !savedConcurrencySettings;
  $("concurrency-save").disabled = busy || !savedConcurrencySettings;
  $("concurrency-reload").disabled = busy;
}
function applyConcurrencySettings(response) {
  savedConcurrencySettings = requireConcurrencySettings(response.concurrency);
  for (const [field] of CONCURRENCY_FIELDS) $("concurrency-" + field).value = savedConcurrencySettings[field];
}
async function loadConcurrencySettings(reload = false) {
  if (concurrencySaving || concurrencyLoading || (savedConcurrencySettings && !reload)) return;
  const generation = sessionGeneration;
  concurrencyLoading = true;
  updateConcurrencyControls();
  $("concurrency-error").classList.remove("show");
  try {
    const response = await plugin("GET", "/concurrency");
    if (generation !== sessionGeneration) throw new StaleRequestError();
    applyConcurrencySettings(response);
  } catch (error) {
    if (error instanceof AuthError || error instanceof StaleRequestError) throw error;
    savedConcurrencySettings = null;
    setText($("concurrency-error"), error.message || String(error));
    $("concurrency-error").classList.add("show");
  } finally {
    concurrencyLoading = false;
    updateConcurrencyControls();
  }
}
async function saveConcurrencySettings() {
  if (concurrencySaving || concurrencyLoading || !savedConcurrencySettings) return;
  const generation = sessionGeneration;
  const config = requireConcurrencySettings(
    Object.fromEntries(CONCURRENCY_FIELDS.map(([field]) => [field, Number($("concurrency-" + field).value)]))
  );
  concurrencySaving = true;
  updateConcurrencyControls();
  $("concurrency-error").classList.remove("show");
  try {
    const latest = requireConcurrencySettings((await plugin("GET", "/concurrency")).concurrency);
    if (JSON.stringify(latest) !== JSON.stringify(savedConcurrencySettings)) throw new UIError(m("concurrency.changed"));
    const validated = await plugin("POST", "/concurrency/validate", config, { command: true });
    const expected = requireConcurrencySettings(validated.concurrency);
    await api("PATCH", PLUGIN_BASE + "/config", { concurrency: expected }, { command: true });
    let confirmed = null;
    for (let attempt = 0; attempt < 12; attempt++) {
      if (generation !== sessionGeneration) throw new StaleRequestError();
      const response = await plugin("GET", "/concurrency");
      if (JSON.stringify(requireConcurrencySettings(response.concurrency)) === JSON.stringify(expected)) {
        confirmed = response;
        break;
      }
      await new Promise((resolve) => setTimeout(resolve, 250));
    }
    if (!confirmed) throw new UIError(m("location.reload_pending"));
    applyConcurrencySettings(confirmed);
    credentialConcurrencyQuery.clear();
    notify(m("concurrency.saved"));
  } catch (error) {
    if (error instanceof AuthError || error instanceof StaleRequestError) throw error;
    setText($("concurrency-error"), error.message || String(error));
    $("concurrency-error").classList.add("show");
  } finally {
    concurrencySaving = false;
    updateConcurrencyControls();
  }
}
$("concurrency-form").addEventListener("submit", (event) => {
  event.preventDefault();
  guard(saveConcurrencySettings);
});
$("concurrency-reload").addEventListener("click", () => guard(() => loadConcurrencySettings(true)));
updateConcurrencyControls();
