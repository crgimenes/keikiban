// Mock of the Go bind layer, so the interface can be opened in a plain
// browser with no database behind it: for screenshots, and for working on the
// UI while offline. Loaded before app.js by tools/uimock/serve.sh.
"use strict";

// ?theme=dark|light forces the scheme for screenshots. The app itself always
// follows the OS; this only overrides the used color-scheme, so light-dark()
// resolves the way the shot needs.
const PARAMS = new URLSearchParams(location.search);
const THEME = PARAMS.get("theme");
if (THEME) document.documentElement.style.colorScheme = THEME;
// ?screen=indexes opens that screen straight away, so a headless capture can
// reach every screen without clicking.
if (PARAMS.get("screen")) {
  localStorage.setItem("keikiban.screen", PARAMS.get("screen"));
}

const NOW = Date.now();
const BUCKETS = 100;

// A window of load that looks like a real afternoon: a quiet start, a spike of
// lock contention, and a tail of IO.
function series(classes, shape) {
  const out = [];
  for (let i = 0; i < BUCKETS; i++) {
    const v = {};
    const t = i / BUCKETS;
    for (const [name, fn] of Object.entries(shape)) {
      const value = fn(t, i);
      if (value > 0) v[name] = Math.round(value * 100) / 100;
    }
    out.push({ t: NOW - (BUCKETS - i) * 3000, v });
  }
  return out;
}

const wave = (center, width, height) => (t) =>
  Math.max(0, height * Math.exp(-((t - center) ** 2) / (2 * width * width)));

const CLASSES = ["CPU", "IO:DataFileRead", "LWLock:WALWrite", "Lock:transactionid",
  "Client:ClientRead", "Other"];

const BUCKETS_LOAD = series(CLASSES, {
  "CPU": (t) => 0.8 + Math.sin(t * 18) * 0.35 + wave(0.62, 0.09, 1.4)(t),
  "IO:DataFileRead": (t) => 1.1 + Math.sin(t * 11 + 1) * 0.5 + wave(0.3, 0.12, 1.9)(t),
  "LWLock:WALWrite": (t) => 0.5 + Math.sin(t * 7 + 2) * 0.25 + wave(0.62, 0.07, 1.1)(t),
  "Lock:transactionid": (t) => wave(0.63, 0.05, 2.6)(t),
  "Client:ClientRead": (t) => 0.35 + Math.sin(t * 5) * 0.15,
  "Other": (t) => 0.2 + Math.sin(t * 9 + 4) * 0.1,
});

const CONNS = series(["active", "idle in transaction", "idle", "other"], {
  "active": (t) => 4 + Math.sin(t * 12) * 2 + wave(0.62, 0.08, 9)(t),
  "idle in transaction": (t) => 1 + wave(0.6, 0.1, 3)(t),
  "idle": () => 22 + Math.random() * 2,
  "other": () => 1,
});

const TPS = series(["commits/s", "rollbacks/s"], {
  "commits/s": (t) => 320 + Math.sin(t * 14) * 90 - wave(0.62, 0.07, 210)(t),
  "rollbacks/s": (t) => 4 + wave(0.62, 0.06, 22)(t),
});

const IO = series(["from cache/s", "from disk/s"], {
  "from cache/s": (t) => 9800 + Math.sin(t * 13) * 2600,
  "from disk/s": (t) => 900 + Math.sin(t * 9 + 1) * 420 + wave(0.3, 0.12, 2600)(t),
});

const TOP_SQL = [
  {
    query: "SELECT id, destination_id, source_id, compressed_data\n  FROM api_relations\n  WHERE company_id = $1 AND created_at > $2\n  ORDER BY created_at DESC LIMIT $3",
    aas: 3.42, pct: 31.2,
    byClass: { "IO:DataFileRead": 2.1, "CPU": 0.9, "Other": 0.42 },
    hasStats: true, callsPS: 417.98, rowsPerCall: 0.86, msPerCall: 30.98,
  },
  {
    query: "UPDATE core_payment SET status_text = $1, updated_at = now()\n  WHERE txid_text = $2",
    aas: 2.61, pct: 23.8,
    byClass: { "Lock:transactionid": 1.8, "LWLock:WALWrite": 0.5, "CPU": 0.31 },
    hasStats: true, callsPS: 88.4, rowsPerCall: 1.0, msPerCall: 12.4,
  },
  {
    query: "COMMIT",
    aas: 1.94, pct: 17.7,
    byClass: { "LWLock:WALWrite": 1.5, "CPU": 0.44 },
    hasStats: true, callsPS: 302.86, rowsPerCall: 0.0, msPerCall: 3.1,
  },
  {
    query: "SELECT count(*) FROM nox_processattribute WHERE key = $1 AND value = $2",
    aas: 1.18, pct: 10.8,
    byClass: { "IO:DataFileRead": 0.9, "CPU": 0.28 },
    hasStats: true, callsPS: 129.58, rowsPerCall: 0.24, msPerCall: 17.97,
  },
  {
    query: "INSERT INTO audit_log (entity, action, payload) VALUES ($1, $2, $3)",
    aas: 0.74, pct: 6.7,
    byClass: { "LWLock:WALWrite": 0.5, "CPU": 0.24 },
    hasStats: true, callsPS: 61.2, rowsPerCall: 1.0, msPerCall: 2.2,
  },
];

const BLOCKING = [
  {
    pid: 20481, query: "UPDATE core_payment SET status_text = 'PAID' WHERE id = 88213",
    state: "idle in transaction", stateSeconds: 184.2, user: "app", app: "worker-3",
    alsoBlocked: false,
    cancelSQL: "SELECT pg_cancel_backend(20481);",
    terminateSQL: "SELECT pg_terminate_backend(20481);",
    blocked: [
      { pid: 20602, query: "UPDATE core_payment SET status_text = 'FAILED' WHERE id = 88213",
        user: "app", app: "api-1", waitEvent: "transactionid", waitSeconds: 96.4,
        lockMode: "ShareLock" },
      { pid: 20655, query: "SELECT * FROM core_payment WHERE id = 88213 FOR UPDATE",
        user: "app", app: "api-2", waitEvent: "transactionid", waitSeconds: 41.8,
        lockMode: "ShareLock" },
    ],
  },
];

window.configState = async () => ({
  path: "~/.config/keikiban/init.filo",
  exists: true,
  error: "",
  active: 0,
  connections: [
    { title: "Production", url: "postgres://app:...@db.internal:5432/app" },
    { title: "Staging", url: "postgres://app:...@staging.internal:5432/app" },
  ],
});

window.dashboardState = async (_win, _slice, topBy) => ({
  connected: true,
  status: "sampling",
  title: "Production",
  samples: 300,
  sliceBy: "waits",
  topBy: topBy || "sql",
  windowSeconds: 300,
  bucketSeconds: 3,
  classes: CLASSES,
  waitClasses: CLASSES,
  buckets: BUCKETS_LOAD,
  topSQL: TOP_SQL,
  missingExtensions: [],
  missingPreloaded: [],
  connClasses: ["active", "idle in transaction", "idle", "other"],
  conns: CONNS,
  maxConnections: 100,
  tpsClasses: ["commits/s", "rollbacks/s"],
  tps: TPS,
  ioClasses: ["from cache/s", "from disk/s"],
  io: IO,
  blocking: BLOCKING,
  databaseCount: 3,
});

window.connectTo = async () => window.configState();
window.logError = async (m) => console.error("ui_error:", m);
window.testConnection = async () => ({ ok: true, version: "PostgreSQL 16.14" });
window.connectionURL = async () => "postgres://app:secret@db.internal:5432/app";
window.sessionList = async () => ({
  sessions: [
    { pid: 20481, state: "idle in transaction", user: "app", app: "worker-3",
      host: "10.4.1.22", database: "app", waitEvent: "Client:ClientRead",
      querySeconds: 184.2, xactSeconds: 184.2, stateSeconds: 184.2,
      query: "UPDATE core_payment SET status_text = 'PAID' WHERE id = 88213",
      blocked: false, cancelSQL: "SELECT pg_cancel_backend(20481);",
      terminateSQL: "SELECT pg_terminate_backend(20481);" },
    { pid: 20602, state: "active", user: "app", app: "api-1", host: "10.4.1.31",
      database: "app", waitEvent: "Lock:transactionid", querySeconds: 96.4,
      xactSeconds: 96.4, stateSeconds: 96.4,
      query: "UPDATE core_payment SET status_text = 'FAILED' WHERE id = 88213",
      blocked: true, cancelSQL: "SELECT pg_cancel_backend(20602);",
      terminateSQL: "SELECT pg_terminate_backend(20602);" },
  ],
});
window.indexReport = async () => ({
  statsReset: "2026-06-01 03:00:00+00",
  indexCacheHitPct: 99.32, tableCacheHitPct: 94.1,
  invalid: [], unusedTruncated: false,
  unused: [
    { schema: "public", table: "core_payment", name: "core_payment_merchant_txid_idx",
      size: "1011 MB", sizeBytes: 1059643392, scans: 0, unique: false,
      definition: "CREATE INDEX core_payment_merchant_txid_idx ON public.core_payment USING btree (merchant_id, txid_text)",
      dropDDL: 'DROP INDEX CONCURRENTLY "public"."core_payment_merchant_txid_idx";' },
  ],
  duplicates: [
    { schema: "public", table: "core_payment", name: "core_payment_merchant_id_idx",
      size: "106 MB", sizeBytes: 111149056, scans: 12, unique: false,
      coveredBy: "core_payment_merchant_id_created_at_idx",
      definition: "CREATE INDEX core_payment_merchant_id_idx ON public.core_payment USING btree (merchant_id)",
      dropDDL: 'DROP INDEX CONCURRENTLY "public"."core_payment_merchant_id_idx";' },
  ],
  seqScans: [
    { schema: "public", table: "nox_processattribute", seqScans: 27, idxScans: 41220,
      indexUsePct: 99.9, liveRows: 27335948 },
  ],
});
window.maintenanceReport = async () => ({
  databaseAge: 148200000, freezeMaxAge: 200000000, wraparoundPct: 74.1,
  oldestTables: [
    { schema: "public", table: "core_payment", age: 148200000, pct: 74.1, size: "42 GB" },
  ],
  needVacuum: [
    { schema: "public", table: "nox_processattribute", liveRows: 27335948,
      deadRows: 714271, deadPct: 2.5, size: "12 GB", vacuumAgo: -1,
      analyzeAgo: 86400, vacuumSQL: 'VACUUM (ANALYZE) "public"."nox_processattribute";' },
  ],
  longTx: [
    { pid: 20481, user: "app", app: "worker-3", state: "idle in transaction",
      xactSeconds: 184.2, stateSeconds: 184.2,
      query: "UPDATE core_payment SET status_text = 'PAID' WHERE id = 88213",
      cancelSQL: "SELECT pg_cancel_backend(20481);",
      terminateSQL: "SELECT pg_terminate_backend(20481);" },
  ],
  sequences: [
    { schema: "public", name: "core_payment_id_seq", lastValue: 1683421904,
      maxValue: 2147483647, pct: 78.4 },
  ],
});
window.vacuumProgress = async () => ({ running: false });
