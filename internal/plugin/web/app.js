let selectedTab = initialRoute.tab;
let credentialConcurrencyTimer = null;
let credentialConcurrencyRefreshEpoch = 0;
function activeTab() { return selectedTab; }
function scheduleCredentialConcurrencyRefresh(immediate = false) {
  clearTimeout(credentialConcurrencyTimer);
  credentialConcurrencyTimer = null;
  const epoch = ++credentialConcurrencyRefreshEpoch;
  if (!currentRole || !["auth-files", "ai-providers"].includes(activeTab()) || document.hidden) return;
  credentialConcurrencyTimer = setTimeout(
    async () => {
      credentialConcurrencyTimer = null;
      await guard(() => readCredentialConcurrency(true));
      if (epoch === credentialConcurrencyRefreshEpoch) scheduleCredentialConcurrencyRefresh();
    },
    immediate ? 0 : 3000
  );
}
document.addEventListener("visibilitychange", () => scheduleCredentialConcurrencyRefresh(true));
function updateRefreshControls() {
  const loading =
    !!authFilesQueries.admin.task || !!aiProvidersQuery.task || !!credentialConcurrencyQuery.task || !!credentialLocationQuery.task ||
    !!credentialModerationQuery.task ||
    Object.values(credentialHealthQueries).some((query) => !!query.task) ||
    Object.values(diagnostics).some((item) => item.loading) ||
    (activeTab() === "moderation" && (moderationLoading || moderationSaving || moderationTesting || moderationEvents.loading)) ||
    (activeTab() === "settings" && (locationLoading || locationSaving || locationDetecting || concurrencyLoading || concurrencySaving));
  document.querySelectorAll('[data-page-action="refresh"]').forEach((button) => (button.disabled = loading));
}
function privacyMasked() { return document.body.classList.contains("privacy-masked"); }
function schedulePageTabLayout() {
  requestAnimationFrame(() => {
    const row = document.querySelector(".page-navigation");
    const switcher = row.querySelector(".page-tab-switcher");
    row.classList.toggle("compact", switcher.querySelector(".tabs").scrollWidth > switcher.clientWidth + 1);
  });
}
function setAdminTab(name) {
  selectedTab = ADMIN_TAB_IDS.includes(name) ? name : "auth-files";
  writePageRoute("admin", selectedTab);
  for (const tab of document.querySelectorAll("[data-tab]")) {
    const active = tab.dataset.tab === selectedTab;
    tab.classList.toggle("active", active);
    tab.setAttribute("aria-current", active ? "page" : "false");
  }
  $("page-tab-select").value = selectedTab;
  const visible = selectedTab === "settings" ? ["concurrency", "location", "plugin-logs"] : [selectedTab];
  for (const section of document.querySelectorAll('#app > section[id^="tab-"]'))
    section.classList.toggle("hidden", !visible.includes(section.id.slice(4)));
  schedulePageTabLayout();
  renderPage();
  scheduleCredentialConcurrencyRefresh();
}
function showGate(_mode, error) {
  sessionGeneration++;
  for (const controller of activeRequests) controller.abort();
  for (const dialog of document.querySelectorAll("dialog[open]")) dialog.close();
  closeHoverTooltip();
  hideNotice();
  if (currentRole) {
    clearCredentialScope();
    resetRoleData();
  }
  authKey = "";
  currentRole = "";
  savedLocationSettings = null;
  updateLocationControls();
  savedConcurrencySettings = null;
  updateConcurrencyControls();
  scheduleCredentialConcurrencyRefresh();
  try { sessionStorage.removeItem(SESSION_CREDENTIAL_PREFIX + "admin"); } catch (_) {}
  $("app").classList.add("hidden");
  $("gate").classList.remove("hidden");
  setText($("gate-error"), error || "");
  $("gate-error").className = error ? "notice err show" : "notice err";
  $("gate-key").value = "";
  recalledCredential("admin").then((saved) => {
    $("gate-remember").checked = !!saved;
    setTextAttribute($("gate-key"), "placeholder", m(saved ? "ui.key_remembered" : "ui.enter_management_key"));
  });
}
async function startAdmin(key) {
  authKey = key;
  currentRole = "admin";
  const generation = sessionGeneration;
  await loadLocationSettings();
  await activateCredentialScope(key);
  if (generation !== sessionGeneration) throw new StaleRequestError();
  restoreRolePreferences();
  setAdminTab(pageRoute().tab);
  $("gate").classList.add("hidden");
  $("app").classList.remove("hidden");
  schedulePageTabLayout();
  await loadPage();
}
async function submitLogin() {
  const generation = sessionGeneration;
  const key = $("gate-key").value.trim() || (await recalledCredential("admin"));
  if (!key) {
    setText($("gate-error"), m("ui.enter_a_management_key"));
    $("gate-error").className = "notice err show";
    return;
  }
  $("gate-submit").disabled = true;
  try {
    await startAdmin(key);
    if (generation !== sessionGeneration) return;
    try { await saveCredential("admin", key, $("gate-remember").checked); } catch (error) {
      if (generation === sessionGeneration) notify(error.message, "err");
    }
    $("gate-key").value = "";
  } catch (error) {
    if (generation === sessionGeneration && !(error instanceof StaleRequestError)) showGate("admin", error.message);
  } finally { $("gate-submit").disabled = false; }
}
$("gate-form").onsubmit = (event) => {
  event.preventDefault();
  guard(submitLogin);
};
const tabs = [...document.querySelectorAll("[data-tab]")];
$("page-tab-select").replaceChildren(...tabs.map((tab) => el("option", { value: tab.dataset.tab }, window.billingI18n.boundText(tab))));
$("page-tab-select").onchange = () => {
  setAdminTab($("page-tab-select").value);
  guard(() => loadPage());
};
for (const tab of tabs)
  tab.onclick = () => {
    setAdminTab(tab.dataset.tab);
    guard(() => loadPage());
  };
for (const button of document.querySelectorAll('[data-page-action="refresh"]')) {
  setActionIcon(button, "refresh", m("ui.refresh"));
  button.onclick = () =>
    guard(async () => {
      if (activeTab() === "settings" && !(await confirmSettingsReload(locationSettingsDirty() || concurrencySettingsDirty()))) return;
      if (activeTab() === "moderation" && !(await confirmSettingsReload(moderationSettingsDirty()))) return;
      await loadPage({ reload: true });
    });
}
$("privacy-mask").onclick = () => {
  document.body.classList.toggle("privacy-masked");
  $("privacy-mask").setAttribute("aria-pressed", String(privacyMasked()));
};
setActionIcon($("privacy-mask"), "eye-off", m("controls.privacy"));
addEventListener("resize", schedulePageTabLayout);
new ResizeObserver(schedulePageTabLayout).observe(document.querySelector(".page-tabs-row"));
const THEME_MODES = {
  "": { icon: "sun-moon", label: () => m("ui.theme_follows_system") },
  white: { icon: "sun", label: () => m("ui.theme_light") },
  dark: { icon: "moon", label: () => m("ui.theme_dark") }
};
function updateThemeButtons() {
  const mode = THEME_MODES[window.billingTheme?.preference()] || THEME_MODES[""];
  document.querySelectorAll('[data-page-action="theme"]').forEach((button) => setActionIcon(button, mode.icon, mode.label()));
}
function addLanguageControl(group) {
  const menu = el("div", { class: "language-menu", popover: "auto", role: "menu", "aria-label": m("language.select") });
  const button = el(
    "button",
    {
      type: "button",
      class: "icon-action",
      "data-page-action": "language",
      "aria-haspopup": "menu",
      "aria-expanded": "false",
      "aria-label": m("language.select"),
      onclick: () => {
        if (menu.matches(":popover-open")) return menu.hidePopover();
        const current = window.billingI18n.current();
        menu.replaceChildren(
          ...window.billingI18n.languages().map((item) =>
            el("button", {
              type: "button",
              class: "language-option",
              role: "menuitemradio",
              "aria-checked": String(item.value === current),
              text: item.label,
              onclick: () => {
                menu.hidePopover();
                button.focus();
                window.billingI18n.select(item.value);
              }
            })
          )
        );
        menu.showPopover();
        positionAnchoredPopover(menu, button, { align: "end" });
        menu.querySelector('[aria-checked="true"]')?.focus({ preventScroll: true });
      }
    },
    actionIcon("globe")
  );
  menu.addEventListener("toggle", (event) => button.setAttribute("aria-expanded", String(event.newState === "open")));
  menu.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      menu.hidePopover();
      button.focus();
      return;
    }
    if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
    event.preventDefault();
    const items = Array.from(menu.querySelectorAll("button"));
    const index = items.indexOf(document.activeElement);
    items[(index + (event.key === "ArrowDown" ? 1 : items.length - 1)) % items.length].focus();
  });
  group.prepend(button, menu);
}
document.querySelectorAll(".standalone-actions").forEach(addLanguageControl);
for (const button of document.querySelectorAll('[data-page-action="theme"]'))
  button.onclick = () => {
    window.billingTheme.cycle();
    updateThemeButtons();
  };
for (const button of document.querySelectorAll('[data-page-action="logout"]')) {
  setActionIcon(button, "logout", m("ui.sign_out"));
  button.onclick = () =>
    guard(async () => {
      await deleteCredential("admin");
      showGate("admin", "");
    });
}
window.addEventListener("billing-theme-change", updateThemeButtons);
window.addEventListener("billing-language-change", () => {
  refreshBillingDateFormatters();
  updateThemeButtons();
  renderPage();
  schedulePageTabLayout();
});
(async () => {
  document.querySelectorAll('input[type="checkbox"]').forEach((input) => {
    if (input.closest(".switch-control, .checkbox-control")) return;
    const placeholder = document.createTextNode("");
    input.replaceWith(placeholder);
    placeholder.replaceWith(input.getAttribute("role") === "switch" ? switchControl({ input }) : checkboxControl({ input }));
  });
  document.querySelectorAll("[data-lucide-icon]").forEach((node) => node.replaceWith(actionIcon(node.dataset.lucideIcon, node.className)));
  updateThemeButtons();
  try {
    const key = (embedded && readPanelKey()) || (await recalledCredential("admin", true));
    if (key) await startAdmin(key); else showGate("admin", "");
  } catch (error) { showGate("admin", error.message); }
})();
