"use strict";
function panelStoredValue(name) {
  try {
    let raw = localStorage.getItem(name);
    if (!raw) return null;
    const prefix = "enc::v1::";
    if (raw.startsWith(prefix)) {
      const secret = new TextEncoder().encode("cli-proxy-api-webui::secure-storage|" + location.host + "|" + navigator.userAgent);
      const binary = atob(raw.slice(prefix.length));
      const bytes = new Uint8Array(binary.length);
      for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i) ^ secret[i % secret.length];
      raw = new TextDecoder().decode(bytes);
    }
    return JSON.parse(raw);
  } catch (_) { return null; }
}

/* Apply the host or stored theme before painting the body. */
(function () {
  const STORE_KEY = "cli-proxy-theme";
  const PLUGIN_STORE_KEY = "cpa-codex-controls:theme";
  const HOST_THEME_TOKENS = [
    "bg-primary",
    "bg-secondary",
    "bg-tertiary",
    "bg-hover",
    "floating-surface",
    "floating-shadow",
    "text-primary",
    "text-secondary",
    "text-tertiary",
    "border-color",
    "border-primary",
    "border-hover",
    "primary-color",
    "primary-hover",
    "primary-active",
    "primary-contrast",
    "error-color",
    "shadow",
    "success-badge-bg",
    "success-badge-text",
    "success-badge-border",
    "failure-badge-bg",
    "failure-badge-text",
    "failure-badge-border",
    "quota-medium-color",
    "header-height"
  ];

  function parentDocument() {
    if (window.parent === window) return null;
    try { return window.parent.document; } catch (_) { return null; }
  }

  function storedTheme() {
    const theme = panelStoredValue(STORE_KEY)?.state?.theme;
    return typeof theme === "string" ? theme : "";
  }

  const systemDark = () => !!(window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches);

  function resolve(theme) {
    if (theme === "dark" || theme === "white") return theme;
    if (theme === "" || theme === "auto") return systemDark() ? "dark" : "white";
    return "";
  }

  function readPluginTheme() {
    try {
      const own = localStorage.getItem(PLUGIN_STORE_KEY);
      if (own === "dark" || own === "white") return own;
    } catch (_) {}
    return "";
  }

  let selectedTheme = readPluginTheme();
  function currentTheme() {
    const doc = parentDocument();
    if (doc) return doc.documentElement.getAttribute("data-theme") || "";
    return selectedTheme || resolve(storedTheme());
  }

  let appliedTheme = null;
  function apply(theme) {
    const root = document.documentElement;
    const next = theme === "dark" || theme === "white" ? theme : "";
    if (next) root.setAttribute("data-theme", next); else root.removeAttribute("data-theme");
    root.style.colorScheme = next === "dark" ? "dark" : "light";
    const changed = appliedTheme !== null && appliedTheme !== next;
    appliedTheme = next;
    if (changed) { window.dispatchEvent(new CustomEvent("billing-theme-change", { detail: next })); }
  }

  function syncHostTokens() {
    const doc = parentDocument();
    if (!doc) return;
    const style = doc.defaultView.getComputedStyle(doc.documentElement);
    // CPAMP already injects its own semantic tokens into plugin frames.
    const cpamp = !!style.getPropertyValue("--app-surface").trim();
    for (const name of HOST_THEME_TOKENS) {
      const value = cpamp ? "" : style.getPropertyValue("--" + name).trim();
      if (value) document.documentElement.style.setProperty("--" + name, value);
      else document.documentElement.style.removeProperty("--" + name);
    }
  }
  const sync = () => {
    apply(currentTheme());
    syncHostTokens();
  };
  sync();

  const doc = parentDocument();
  if (doc) {
    new MutationObserver(sync).observe(doc.documentElement, { attributes: true, attributeFilter: ["data-theme", "class", "style"] });
  } else {
    window.addEventListener("storage", (event) => {
      if (!event.key || event.key === STORE_KEY || event.key === PLUGIN_STORE_KEY) {
        selectedTheme = readPluginTheme();
        sync();
      }
    });
  }
  if (window.matchMedia) { window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", sync); }
  window.billingTheme = {
    current: () => appliedTheme,
    preference: () => selectedTheme,
    cycle() {
      const order = ["", "white", "dark"];
      selectedTheme = order[(order.indexOf(selectedTheme) + 1) % order.length];
      try {
        if (selectedTheme) localStorage.setItem(PLUGIN_STORE_KEY, selectedTheme); else localStorage.removeItem(PLUGIN_STORE_KEY);
      } catch (_) {}
      sync();
      return selectedTheme;
    }
  };
})();
