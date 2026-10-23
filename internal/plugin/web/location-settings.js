let savedLocationSettings = null;
let locationSaving = false;
let locationDetecting = false;
let locationLoading = false;
const LOCATION_FIELDS = ["country", "region", "city", "timezone"];
const globalLocationEditor = locationEditor({
  prefix: "location",
  enableLabel: m("location.enable_override"),
  location: {},
  enabled: false,
  request: {},
  onChange: () => updateLocationControls(),
  onBusy: (value) => {
    locationDetecting = value;
    updateLocationControls();
  }
});
$("location-editor-host").append(globalLocationEditor.node);

function updateLocationControls() {
  const busy = locationSaving || locationLoading || locationDetecting;
  globalLocationEditor.setDisabled(locationSaving || locationLoading || !savedLocationSettings);
  $("location-save").disabled = busy || !savedLocationSettings;
  $("location-discard").disabled = busy || !locationSettingsDirty();
  if (currentRole) updateRefreshControls();
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
  if (locationSaving || locationLoading || locationDetecting) return;
  const generation = sessionGeneration;
  locationLoading = true;
  updateLocationControls();
  $("location-error").classList.remove("show");
  try {
    const response = await api("GET", PLUGIN_BASE + "/location", null);
    if (generation !== sessionGeneration) throw new StaleRequestError();
    applyLocationSettings(response);
    return response;
  } finally {
    locationLoading = false;
    updateLocationControls();
  }
}

function sameLocation(left, right) {
  return left?.enabled === right?.enabled && LOCATION_FIELDS.every((field) => (left?.[field] || "") === (right?.[field] || ""));
}

function locationSettingsDirty() {
  return (
    !!savedLocationSettings &&
    !sameLocation(savedLocationSettings, { enabled: globalLocationEditor.toggle.checked, ...globalLocationEditor.getLocation() })
  );
}

async function confirmSettingsReload(dirty, discard = false) {
  if (!dirty) return true;
  const generation = sessionGeneration;
  const label = m(discard ? "settings.discard" : "ui.refresh");
  const accepted = await openActionDialog(
    label,
    m(discard ? "settings.discard_confirm" : "settings.reload_confirm"),
    undefined,
    undefined,
    label
  );
  return generation === sessionGeneration && accepted;
}

async function saveLocationSettings() {
  if (locationSaving || locationLoading || locationDetecting || !savedLocationSettings) return;
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
    renderPage();
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
$("location-discard").addEventListener("click", () =>
  guard(async () => {
    if (!locationSettingsDirty() || !(await confirmSettingsReload(true, true))) return;
    globalLocationEditor.setLocation(savedLocationSettings);
    globalLocationEditor.setEnabled(savedLocationSettings.enabled);
    setText(globalLocationEditor.notice, "");
    $("location-error").classList.remove("show");
    updateLocationControls();
  })
);
setTextAttribute($("location-discard"), "title", m("settings.discard_help"));
updateLocationControls();
