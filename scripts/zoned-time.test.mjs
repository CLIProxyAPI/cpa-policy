import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";
import test from "node:test";

function runtime(zone) {
  const context = vm.createContext({ Date, Intl, window: {} });
  vm.runInContext(readFileSync(new URL("../internal/plugin/web/zoned-time.js", import.meta.url), "utf8"), context);
  vm.runInContext(`setBillingTimezone(${JSON.stringify(zone)})`, context);
  return (code) => vm.runInContext(code, context);
}

test("disabled mode preserves browser behavior and does not modify global Date", () => {
  const run = runtime("");
  assert.equal(run("new ZonedDate(2026, 3, 5, 6, 7).getTime()"), new Date(2026, 3, 5, 6, 7).getTime());
  assert.equal(run("Date === ZonedDate"), false);
  assert.equal(run("new ZonedDate('2026-10-20T01:00:00Z').getHours()"), new Date("2026-10-20T01:00:00Z").getHours());
});

test("local fields and constructors use the configured zone", () => {
  const run = runtime("America/Los_Angeles");
  assert.equal(run("new ZonedDate('2026-10-20T01:00:00Z').getDate()"), 19);
  assert.equal(run("new ZonedDate('2026-10-20T01:00:00Z').getHours()"), 18);
  assert.equal(run("new ZonedDate(2026, 9, 20).toISOString()"), "2026-10-20T07:00:00.000Z");
});

test("natural days are 23 or 25 hours across DST", () => {
  const run = runtime("America/New_York");
  assert.equal(run("(new ZonedDate(2026, 2, 9) - new ZonedDate(2026, 2, 8)) / 3600000"), 23);
  assert.equal(run("(new ZonedDate(2026, 10, 2) - new ZonedDate(2026, 10, 1)) / 3600000"), 25);
  assert.equal(run("(() => { const d = new ZonedDate(2026, 2, 8); d.setDate(d.getDate()+1); return d.toISOString(); })()"), "2026-03-09T04:00:00.000Z");
});

test("nonexistent hours advance and repeated hours retain their selected occurrence", () => {
  const run = runtime("America/New_York");
  assert.equal(run("new ZonedDate(2026, 2, 8, 2, 30).getHours()"), 3);
  assert.equal(run("new ZonedDate(2026, 10, 1, 1, 30).toISOString()"), "2026-11-01T05:30:00.000Z");
  assert.equal(run("(() => { const d = new ZonedDate('2026-11-01T06:30:00Z'); d.setMinutes(30); return d.toISOString(); })()"), "2026-11-01T06:30:00.000Z");
  assert.equal(run("(() => { const d = new ZonedDate('2026-11-01T06:30:00Z'); d.setMinutes(0, 0, 0); return d.toISOString(); })()"), "2026-11-01T06:00:00.000Z");
});

test("quarter-hour zones, month rollover, and invalid dates", () => {
  const run = runtime("Asia/Kathmandu");
  assert.equal(run("new ZonedDate(2026, 9, 20).toISOString()"), "2026-10-19T18:15:00.000Z");
  assert.equal(run("new ZonedDate(2026, 11, 32).toISOString()"), "2026-12-31T18:15:00.000Z");
  assert.equal(run("Number.isNaN(new ZonedDate('bad').getDate())"), true);
});
