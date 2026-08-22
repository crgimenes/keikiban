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
// ?screen=indexes is read by app.js itself, so a headless capture reaches any
// screen without clicking and the real app still always opens on the dashboard.

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

// The connections screen is the one place the mock has to keep state: connect,
// disconnect and promote say nothing unless the list answers back. A second
// entry is what makes the promote and the "one open at a time" rule visible;
// no captured screenshot shows this screen, so nothing published depends on it.
const MOCK = {
  active: 0,
  connections: [
    { title: "keikibench", url: "postgres://postgres:...@db.local:5432/keikibench" },
    { title: "staging", url: "postgres://postgres:...@stage.local:5432/app" },
  ],
};

window.configState = async () => ({
  path: "~/.config/keikiban/init.filo",
  exists: true,
  error: "",
  active: MOCK.active,
  connections: MOCK.connections,
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

// The browser is recorded per node, since its queries take arguments. A group
// with no capture answers empty rather than inventing objects.
window.browserTree = async () => recorded("schemas");

window.browserObjects = async (schema, group) => {
  try {
    return await recorded("objects-" + schema + "-" + group);
  } catch {
    return { objects: [], total: 0, truncated: false };
  }
};

// In the app, opening an object creates a native window and the Go side
// captures which object it is. A browser cannot do that, so the mock opens a
// tab and carries the object in the URL; object.js never sees the difference,
// because it only ever calls objectDetail().
window.openObject = async (schema, name) => {
  const q = new URLSearchParams({ schema, name });
  window.open("object.html?" + q, "_blank");
};

// The editor opens with a real statement even here, so the layout can be
// worked on offline. Running it cannot work: the mock has no server, and
// saying so plainly beats inventing rows that were never measured.
window.initialQuery = async () => {
  const schema = PARAMS.get("schema") || "public";
  const name = PARAMS.get("name") || "table";
  return 'SELECT *\nFROM "' + schema + '"."' + name + '"\nLIMIT 100\nOFFSET 0;';
};

// A small editable result so the grid can be worked on offline. The shape is
// the app's own; the rows are fictional and no published screenshot uses this
// screen, so the real-data rule is not touched.
const GRID = {
  columns: [
    { name: "id", type: "int8" },
    { name: "name", type: "text" },
    { name: "age", type: "int4" },
  ],
  rows: [["1", "ana", "30"], ["2", null, "40"], ["3", "", "50"]],
};

window.runQuery = async (sql) => ({
  sql,
  columns: GRID.columns,
  rows: GRID.rows.map((r) => [...r]),
  returnsRows: true,
  truncated: false,
  command: "SELECT 3",
  affected: 3,
  elapsedMS: 1.2,
  target: {
    editable: true,
    schema: "public",
    table: "people",
    pkColumns: ["id"],
  },
  editable: [true, true, true],
});

window.gridPreview = async (in_) => ({
  sql: 'UPDATE "public"."people"\nSET ' +
    in_.edits.map((e, i) => '"' + e.column + '" = $' + (i + 1) + "::text").join(",\n    ") +
    '\nWHERE "id" = $' + (in_.edits.length + 1) + "::int8;",
});

window.gridSave = async (in_) => {
  const row = GRID.rows.find((r) => r[0] === in_.key[0].value);
  for (const e of in_.edits) {
    const i = GRID.columns.findIndex((c) => c.name === e.column);
    row[i] = e.value;
  }
  return { affected: 1 };
};

window.cancelQuery = async () => {};

// Migrations screen: stateful like the connections mock, so run/revert/capture
// exercise the whole flow offline. No published screenshot uses this screen,
// so the fictional file names break no policy.
const MIG = {
  applied: 2,
  pending: ["003_add_invoices.up.sql"],
  drift: ["index_added public.users.users_email_idx"],
};

window.migrationStatus = async (dir) => ({
  dir: dir || "/home/crg/app/migrations",
  dirSource: dir ? "manual" : "migration config",
  tableExists: true,
  applied: MIG.applied,
  pending: MIG.pending,
  drift: { snapVersion: MIG.applied, changes: MIG.drift },
});

window.migrationPreview = async (dir, action, name) => {
  if (action === "up") {
    return {
      files: MIG.pending.map((f) => ({
        name: f,
        def: "CREATE TABLE invoices (\n  id bigserial PRIMARY KEY,\n  total numeric NOT NULL\n);",
      })),
    };
  }
  if (action === "down") {
    return { files: [{ name: "002_add_users.down.sql", def: "DROP TABLE users;" }] };
  }
  return {
    files: [
      { name: "004_" + name + ".up.sql", def: "CREATE INDEX users_email_idx ON users (email);" },
      { name: "004_" + name + ".down.sql", def: "DROP INDEX users_email_idx;" },
    ],
  };
};

window.migrationApply = async (dir, action, name) => {
  let message = "";
  if (action === "up") {
    MIG.applied += MIG.pending.length;
    message = "applied " + MIG.pending.length + " migration(s): " + MIG.pending.join(", ");
    MIG.pending = [];
  }
  if (action === "down") {
    MIG.applied -= 1;
    MIG.pending = ["00" + (MIG.applied + 1) + "_reverted.up.sql", ...MIG.pending];
    message = "reverted 1 migration(s)";
  }
  if (action === "capture") {
    message = "captured drift as version " + (MIG.applied + 1) + " (004_" + name + ".up.sql, 004_" + name + ".down.sql)";
    MIG.drift = [];
    MIG.applied += 1;
  }
  return { message, status: await window.migrationStatus(dir) };
};

window.objectDetail = async () => {
  const schema = PARAMS.get("schema");
  const name = PARAMS.get("name");
  try {
    return await recorded("describe-" + schema + "-" + name);
  } catch {
    return { schema, name, error: "no capture for " + schema + "." + name };
  }
};

// Filtering the recorded names is what the server does anyway; no name here
// is invented, and the escaping rule is exercised for real in the Go tests.
window.browserSearch = async (term) => {
  const list = await window.browserObjects("public", "tables");
  const hits = list.objects
    .filter((o) => o.name.includes(term))
    .map((o) => ({ schema: o.schema, name: o.name, kind: o.kind }));
  return { hits, total: hits.length, truncated: false };
};

window.connectTo = async (i) => {
  MOCK.active = i;
  return window.configState();
};

window.disconnect = async () => {
  MOCK.active = -1;
  return window.configState();
};

// Same index bookkeeping the Go side does: the attached entry moves with the
// list instead of being swapped for whoever lands on its old position.
window.makeDefault = async (i) => {
  const [conn] = MOCK.connections.splice(i, 1);
  MOCK.connections.unshift(conn);
  if (MOCK.active === i) {
    MOCK.active = 0;
  } else if (MOCK.active >= 0 && MOCK.active < i) {
    MOCK.active += 1;
  }
  return window.configState();
};
window.logError = async (m) => console.error("ui_error:", m);
window.testConnection = async () => ({ ok: true, version: "PostgreSQL 16.14" });
window.connectionURL = async () => "postgres://postgres:secret@db.local:5432/keikibench";
window.vacuumProgress = async () => ({ running: false });
