// Mock of the Go bind layer, so the interface opens in a plain browser with
// no database behind it: for screenshots, and for working on the UI offline.
// Loaded before app.js by tools/uimock/serve.sh.
//
// The data comes from recorded/*.json, which are REAL captures taken with
// `keikiban -json <command>` against a pgbench workload. Screenshots must not
// invent numbers: a chart drawn from a smooth synthetic curve looks nothing
// like a database under load, and publishing it as if it were a measurement
// would be a lie. Re-record with tools/record_demo.sh.
"use strict";

const PARAMS = new URLSearchParams(location.search);
const THEME = PARAMS.get("theme");
if (THEME) document.documentElement.style.colorScheme = THEME;
// ?screen=indexes opens that screen straight away, so a headless capture can
// reach every screen without clicking.
if (PARAMS.get("screen")) {
  localStorage.setItem("keikiban.screen", PARAMS.get("screen"));
}

async function recorded(name) {
  const r = await fetch("recorded/" + name + ".json");
  if (!r.ok) throw new Error("missing recorded/" + name + ".json");
  return r.json();
}

// The recorded load chart carries the timestamps of the moment it was taken.
// Shifting them to now keeps the axis readable without touching a value.
function shiftToNow(buckets) {
  if (!buckets || buckets.length === 0) return buckets;
  const offset = Date.now() - buckets[buckets.length - 1].t;
  return buckets.map((b) => ({ t: b.t + offset, v: b.v }));
}

window.configState = async () => ({
  path: "~/.config/keikiban/init.filo",
  exists: true,
  error: "",
  active: 0,
  connections: [
    { title: "keikibench", url: "postgres://postgres:...@db.local:5432/keikibench" },
  ],
});

window.dashboardState = async (_win, _slice, topBy) => {
  const d = await recorded("dashboard");
  d.buckets = shiftToNow(d.buckets);
  d.conns = shiftToNow(d.conns);
  d.tps = shiftToNow(d.tps);
  d.io = shiftToNow(d.io);
  d.topBy = topBy || d.topBy;
  const locks = await recorded("locks");
  d.blocking = locks.blocking || [];
  return d;
};

window.sessionList = async () => recorded("sessions");
window.indexReport = async () => recorded("indexes");
window.maintenanceReport = async () => recorded("maintenance");

window.connectTo = async () => window.configState();
window.logError = async (m) => console.error("ui_error:", m);
window.testConnection = async () => ({ ok: true, version: "PostgreSQL 16.14" });
window.connectionURL = async () => "postgres://postgres:secret@db.local:5432/keikibench";
window.vacuumProgress = async () => ({ running: false });
