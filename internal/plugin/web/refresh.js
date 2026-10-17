const PAGE_REFRESH_INTERVALS = [0, 5, 10, 30, 60, 300];
const PAGE_STALE_MS = 5 * 60 * 1000;
let pageRefreshTimer = 0;
let pageRefreshTarget = null;

function pendingPageLoads(page) {
  // Filters and pagination can read sources outside a full-page load.
  return page ? [page.task, ...page.sources.map((source) => source.task)].filter(Boolean) : [];
}

function clearPageRefresh() {
  clearTimeout(pageRefreshTimer);
  pageRefreshTimer = 0;
  pageRefreshTarget = null;
}

// One deadline belongs to the visible page. Loads restart it on completion;
// hiding the document pauses the timer while retaining an overdue deadline.
function schedulePageRefresh(page, loading) {
  const seconds = currentRole ? Number($((currentRole === "account" ? "account-" : "") + "auto-refresh").value) : 0;
  if (!page?.autoRefresh || !seconds || loading) {
    clearPageRefresh();
    return;
  }
  if (pageRefreshTarget?.page !== page || pageRefreshTarget.generation !== sessionGeneration || pageRefreshTarget.seconds !== seconds) {
    clearPageRefresh();
    pageRefreshTarget = { page, generation: sessionGeneration, seconds, dueAt: Date.now() + seconds * 1000 };
  }
  if (document.visibilityState !== "visible") {
    clearTimeout(pageRefreshTimer);
    pageRefreshTimer = 0;
    return;
  }
  if (pageRefreshTimer) return;
  const target = pageRefreshTarget;
  pageRefreshTimer = setTimeout(
    () => {
      clearPageRefresh();
      if (currentPage() !== target.page || sessionGeneration !== target.generation || document.visibilityState !== "visible") {
        updateRefreshControls();
        return;
      }
      refreshPage();
    },
    Math.max(0, target.dueAt - Date.now())
  );
}

function updateRefreshControls() {
  const page = currentPage();
  const loading = pendingPageLoads(page).length > 0;
  const label = currentRole
    ? document.querySelector(currentRole === "account" ? "[data-account-tab].active" : "[data-tab].active")?.textContent.trim()
    : "";
  for (const button of document.querySelectorAll('[data-page-action="refresh"]')) {
    button.disabled = loading;
    setTextAttribute(
      button,
      "data-tip",
      setTextAttribute(button, "aria-label", label ? m("ui.refresh_value", { v0: label }) : m("ui.refresh"))
    );
  }
  schedulePageRefresh(page, loading);
}

function refreshPage() {
  const page = currentPage();
  if (!page) return Promise.resolve();
  const pending = pendingPageLoads(page);
  return guard(() => (pending.length ? settleLoads(pending) : loadPage(currentRole === "account", { reload: true })));
}

function refreshStalePage() {
  if (currentPage()?.sources.some((source) => source.readAt && Date.now() - source.readAt >= PAGE_STALE_MS)) return refreshPage();
}

for (const role of ["admin", "account"]) {
  const select = $((role === "account" ? "account-" : "") + "auto-refresh");
  select.replaceChildren(
    ...PAGE_REFRESH_INTERVALS.map((seconds) =>
      el("option", {
        value: String(seconds),
        text: seconds ? m("ui.auto_refresh_every", { v0: seconds < 60 ? seconds + "s" : seconds / 60 + "m" }) : m("ui.auto_refresh_off")
      })
    )
  );
  select.onchange = () => {
    saveChoice(role, select.id);
    updateRefreshControls();
  };
}

for (const button of document.querySelectorAll('[data-page-action="refresh"]')) {
  setActionIcon(button, "refresh", m("ui.refresh"));
  button.onclick = () => refreshPage();
}

document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible") refreshStalePage();
  updateRefreshControls();
});
