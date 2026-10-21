function usd(value, fixedDigits) {
  const amount = Number(value || 0);
  const absolute = Math.abs(amount);
  const digits = fixedDigits ?? (absolute >= 1 ? 2 : absolute >= 0.01 ? 4 : 6);
  const compact = amount.toLocaleString("en-US", { minimumFractionDigits: fixedDigits ?? 0, maximumFractionDigits: digits });
  if (amount === 0 || fixedDigits !== undefined) return "$" + compact;
  const rounded = Number(compact.replaceAll(",", ""));
  if (rounded === 0) return "<$0.000001";
  const tolerance = Number.EPSILON * Math.max(1, absolute) * 8;
  if (Math.abs(amount - rounded) <= tolerance) return "$" + compact;
  return "$" + amount.toLocaleString("en-US", { minimumFractionDigits: digits, maximumFractionDigits: digits });
}

function loadedCountText(loaded, total, complete) {
  return loaded > total || (complete && loaded !== total)
    ? m("ui.loaded_value_records_value_currently_match", { v0: int(loaded), v1: int(total) })
    : m("ui.loaded_value_value_records", { v0: int(loaded), v1: int(total) });
}

function int(value) { return Number(value || 0).toLocaleString(locale()); }
function tokens(value, thousandsDigits = 1) {
  const n = Number(value || 0);
  if (n >= 1e9) return (n / 1e9).toFixed(2) + "B";
  if (n >= 1e6) return (n / 1e6).toFixed(2) + "M";
  if (n >= 1e3) return (n / 1e3).toFixed(thousandsDigits) + "K";
  return String(n);
}

function latency(value) {
  const milliseconds = Number(value || 0);
  if (!Number.isFinite(milliseconds) || milliseconds <= 0) return "—";
  if (milliseconds < 1000) return Math.round(milliseconds) + " ms";
  return (milliseconds / 1000).toFixed(2) + " s";
}

function when(value) {
  const parts = dateTimeParts(value);
  return parts ? parts.date + " " + parts.time : "—";
}
let dateFormatter = new Intl.DateTimeFormat(locale(), billingDateOptions());
let timeFormatter = new Intl.DateTimeFormat(
  locale(),
  billingDateOptions({ hour: "numeric", minute: "numeric", second: "numeric", hour12: false })
);
function dateTimeParts(value) {
  if (!value) return null;
  const date = new ZonedDate(value);
  if (isNaN(date.getTime()) || date.getFullYear() < 2000) return null;
  return { time: timeFormatter.format(date), date: dateFormatter.format(date) };
}
function dateTimeLines(value) {
  const parts = dateTimeParts(value);
  if (!parts) return el("div", {}, "—");
  return [el("div", {}, parts.time), el("div", { class: "muted small mono" }, parts.date)];
}

function compactResetDate(date) {
  const pad = (part) => String(part).padStart(2, "0");
  return pad(date.getMonth() + 1) + "/" + pad(date.getDate()) + " " + pad(date.getHours()) + ":" + pad(date.getMinutes());
}

// Time until a moment in its largest whole unit, as "in 3 hours" or, with the left form, "3 hours left".
function compactTimeUntil(seconds, form = "in") {
  if (seconds < 60) return m(form === "left" ? "ui.less_than_a_minute_left" : "ui.in_less_than_a_minute");
  const [unit, size] = seconds >= 86400 ? ["day", 86400] : seconds >= 3600 ? ["hour", 3600] : ["minute", 60];
  return m("time." + form + "_" + unit, { count: Math.floor(seconds / size) });
}

function previewCredential(value) {
  value = String(value || "").trim();
  if (value.length <= 12) return "*".repeat(value.length);
  return value.slice(0, 6) + "…" + value.slice(-4);
}

// Format display labels without changing API values or editable text.
function displayLabel(value) {
  return window.billingI18n.isMessage(value) ? value : String(value ?? "").replace(/\s*·\s*/g, DISPLAY_SEPARATOR);
}

function displayText(value) {
  if (window.billingI18n.isMessage(value)) return el("span", {}, value);
  if (typeof value !== "string" || !value.includes("·")) return value;
  const fragment = document.createDocumentFragment();
  displayLabel(value).split(DISPLAY_SEPARATOR).forEach((part, index) => {
    if (index) fragment.append(el("span", { class: "text-separator", text: DISPLAY_SEPARATOR }));
    fragment.append(part);
  });
  return fragment;
}

function maskedTailText(provider, account) {
  return [provider, el("span", { class: "text-separator", text: DISPLAY_SEPARATOR }), maskedText(account)];
}

function maskedText(value) { return el("span", { class: "mask-blur" }, displayText(value)); }

function splitCredentialLabel(value) {
  const label = displayLabel(value);
  const separator = typeof label === "string" ? label.indexOf(DISPLAY_SEPARATOR) : -1;
  return separator <= 0 ? ["", value] : [label.slice(0, separator), label.slice(separator + DISPLAY_SEPARATOR.length)];
}

function maskedLabelText(value) {
  const [provider, account] = splitCredentialLabel(value);
  return provider ? maskedTailText(provider, account) : maskedText(account);
}

function matchesTerm(text, term) {
  return (!term || String(displayLabel(text)).toLowerCase().includes(String(displayLabel(term)).toLowerCase()));
}

// Group prefixed and unprefixed variants by base model name.
