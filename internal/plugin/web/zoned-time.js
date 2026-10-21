// Explicitly scoped date arithmetic: the browser and other plugins keep their native Date.
let billingTimezone = "";
let billingPartsFormatter = null;

function billingDateOptions(options = {}) { return billingTimezone ? { ...options, timeZone: billingTimezone } : options; }
function billingZoneName() { return billingTimezone || Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC"; }

function setBillingTimezone(value) {
  billingPartsFormatter = value
    ? new Intl.DateTimeFormat("en-US-u-ca-gregory-nu-latn", {
        timeZone: value,
        year: "numeric",
        month: "numeric",
        day: "numeric",
        hour: "numeric",
        minute: "numeric",
        second: "numeric",
        hourCycle: "h23"
      })
    : null;
  billingTimezone = value || "";
}

function billingWallParts(value) {
  const date = new Date(Number(value));
  if (!Number.isFinite(date.getTime())) return [NaN, NaN, NaN, NaN, NaN, NaN, NaN];
  if (!billingPartsFormatter)
    return [
      date.getFullYear(),
      date.getMonth(),
      date.getDate(),
      date.getHours(),
      date.getMinutes(),
      date.getSeconds(),
      date.getMilliseconds()
    ];
  const parts = Object.fromEntries(billingPartsFormatter.formatToParts(date).map((part) => [part.type, Number(part.value)]));
  return [parts.year, parts.month - 1, parts.day, parts.hour, parts.minute, parts.second, date.getUTCMilliseconds()];
}

function wallEpoch(parts) {
  const date = new Date(0);
  date.setUTCFullYear(parts[0], parts[1], parts[2]);
  date.setUTCHours(parts[3] || 0, parts[4] || 0, parts[5] || 0, parts[6] || 0);
  return date.getTime();
}

function billingInstant(parts, reference) {
  if (!billingTimezone) {
    const date = new Date(0);
    date.setFullYear(parts[0], parts[1], parts[2]);
    date.setHours(parts[3] || 0, parts[4] || 0, parts[5] || 0, parts[6] || 0);
    return date.getTime();
  }
  const target = wallEpoch(parts);
  if (!Number.isFinite(target)) return NaN;
  // Collect the offsets around a transition. Keep a selected repeated hour;
  // otherwise use its first occurrence. Gaps advance like native Date setters.
  if (Number.isFinite(reference)) {
    const preferred = target - (wallEpoch(billingWallParts(reference)) - reference);
    if (wallEpoch(billingWallParts(preferred)) === target) return preferred;
  }
  const offsets = new Set(
    [-2, -1, 0, 1, 2].map((days) => {
      const sample = target + days * 86400000;
      return wallEpoch(billingWallParts(sample)) - sample;
    })
  );
  const candidates = [...offsets].map((offset) => target - offset).sort((a, b) => a - b);
  const exact = candidates.filter((candidate) => wallEpoch(billingWallParts(candidate)) === target);
  if (exact.length) return exact[0];
  return candidates.find((candidate) => wallEpoch(billingWallParts(candidate)) > target) ?? NaN;
}

class ZonedDate extends Date {
  constructor(...args) {
    if (billingTimezone && args.length > 1) {
      const [year, month, day = 1, hour = 0, minute = 0, second = 0, ms = 0] = args.map(Number);
      super(billingInstant([year >= 0 && year < 100 ? year + 1900 : year, month, day, hour, minute, second, ms]));
    } else super(...args);
  }
  getFullYear() { return billingWallParts(this.getTime())[0]; }
  getMonth() { return billingWallParts(this.getTime())[1]; }
  getDate() { return billingWallParts(this.getTime())[2]; }
  getHours() { return billingWallParts(this.getTime())[3]; }
  getMinutes() { return billingWallParts(this.getTime())[4]; }
  getSeconds() { return billingWallParts(this.getTime())[5]; }
  getMilliseconds() { return super.getUTCMilliseconds(); }
  getDay() { return new Date(wallEpoch(billingWallParts(this.getTime()))).getUTCDay(); }
  getTimezoneOffset() { return (this.getTime() - wallEpoch(billingWallParts(this.getTime()))) / 60000; }
  setWall(index, values) {
    const reference = this.getTime(), parts = billingWallParts(reference);
    values.forEach((value, offset) => { parts[index + offset] = Number(value); });
    return super.setTime(billingInstant(parts, reference));
  }
  setFullYear(...values) { return billingTimezone ? this.setWall(0, values) : super.setFullYear(...values); }
  setMonth(...values) { return billingTimezone ? this.setWall(1, values) : super.setMonth(...values); }
  setDate(...values) { return billingTimezone ? this.setWall(2, values) : super.setDate(...values); }
  setHours(...values) { return billingTimezone ? this.setWall(3, values) : super.setHours(...values); }
  setMinutes(...values) { return billingTimezone ? this.setWall(4, values) : super.setMinutes(...values); }
  setSeconds(...values) { return billingTimezone ? this.setWall(5, values) : super.setSeconds(...values); }
  setMilliseconds(...values) { return billingTimezone ? this.setWall(6, values) : super.setMilliseconds(...values); }
  toLocaleString(locale, options) { return super.toLocaleString(locale, billingDateOptions(options)); }
  toLocaleDateString(locale, options) { return super.toLocaleDateString(locale, billingDateOptions(options)); }
  toLocaleTimeString(locale, options) { return super.toLocaleTimeString(locale, billingDateOptions(options)); }
}

function refreshBillingDateFormatters() {
  dateFormatter = new Intl.DateTimeFormat(locale(), billingDateOptions());
  timeFormatter = new Intl.DateTimeFormat(
    locale(),
    billingDateOptions({ hour: "numeric", minute: "numeric", second: "numeric", hour12: false })
  );
}

window.billingTime = { options: billingDateOptions };
