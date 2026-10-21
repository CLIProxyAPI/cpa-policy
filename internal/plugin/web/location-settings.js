let savedLocationSettings = null;
let locationSaving = false;
let locationDetecting = false;
const LOCATION_FIELDS = ["country", "region", "city", "timezone"];
const globalLocationEditor = locationEditor({
  prefix: "location",
  enableLabel: m("location.enable_override"),
  location: {},
  enabled: false,
  request: {},
  onBusy: (value) => {
    locationDetecting = value;
    updateLocationControls();
  }
});
$("location-editor-host").append(globalLocationEditor.node);

function updateLocationControls() {
  globalLocationEditor.setDisabled(locationSaving || !savedLocationSettings);
  $("location-save").disabled = locationSaving || locationDetecting || !savedLocationSettings;
  $("location-reload").disabled = locationSaving || locationDetecting;
}

function applyLocationSettings(response) {
  const settings = response.custom_location;
  if (!settings || typeof settings.enabled !== "boolean" || typeof response.effective_timezone !== "string")
    throw new UIError(m("location.invalid_response"));
  setBillingTimezone(response.effective_timezone);
  refreshBillingDateFormatters();
  {
    savedLocationSettings = settings;
    globalLocationEditor.setLocation(settings);
    globalLocationEditor.setEnabled(settings.enabled);
    setText(globalLocationEditor.notice, "");
    updateLocationControls();
  }
}

async function loadLocationSettings() {
  const response = await api("GET", PLUGIN_BASE + "/location", null);
  applyLocationSettings(response);
  return response;
}

function sameLocation(left, right) {
  return left?.enabled === right?.enabled && LOCATION_FIELDS.every((field) => (left?.[field] || "") === (right?.[field] || ""));
}

async function saveLocationSettings() {
  if (locationSaving || locationDetecting) return;
  const generation = sessionGeneration;
  const settings = { enabled: globalLocationEditor.toggle.checked, ...globalLocationEditor.getLocation() };
  locationSaving = true;
  $("location-error").classList.remove("show");
  updateLocationControls();
  try {
    const validated = await plugin("POST", "/location/validate", settings, { command: true });
    await api("PATCH", PLUGIN_BASE + "/config", { custom_location: validated.custom_location }, { command: true });
    let response = null;
    for (let attempt = 0; attempt < 12; attempt++) {
      if (generation !== sessionGeneration) throw new StaleRequestError();
      response = await plugin("GET", "/location", null);
      if (sameLocation(response.custom_location, validated.custom_location)) break;
      response = null;
      await new Promise((resolve) => setTimeout(resolve, 250));
    }
    if (!response) throw new UIError(m("location.reload_pending"));
    applyLocationSettings(response);
    resetRoleData("admin");
    restoreRolePreferences("admin");
    await loadPage({ reload: true });
    notify(m("location.saved"));
  } catch (error) {
    if (error instanceof AuthError || error instanceof StaleRequestError) throw error;
    setText($("location-error"), error.message || String(error));
    $("location-error").classList.add("show");
  } finally {
    locationSaving = false;
    updateLocationControls();
  }
}

$("location-form").addEventListener("submit", (event) => {
  event.preventDefault();
  guard(saveLocationSettings);
});
$("location-reload").addEventListener("click", () =>
  guard(async () => {
    await loadLocationSettings();
    resetRoleData("admin");
    restoreRolePreferences("admin");
    await loadPage({ reload: true });
  })
);
updateLocationControls();
