"use strict";

const el = (id) => document.getElementById(id);

// JS failures inside the webview are invisible from the terminal; forward
// them to Go so -debug shows them as event=ui_error lines.
window.addEventListener("error", (ev) => {
  window.logError?.(String(ev.message || ev.error));
});
window.addEventListener("unhandledrejection", (ev) => {
  window.logError?.(String(ev.reason));
});

// Keyboard zoom: Cmd+/Cmd-/Cmd0 (Ctrl elsewhere) scale the root font size,
// and everything sized in rem follows. Persisted per app.
const ZOOM_KEY = "keikiban.zoom";
let zoom = Number(localStorage.getItem(ZOOM_KEY)) || 100;

function applyZoom() {
  document.documentElement.style.fontSize = zoom + "%";
}

window.addEventListener("keydown", (ev) => {
  if (!(ev.metaKey || ev.ctrlKey) || ev.altKey) return;
  switch (ev.key) {
    case "+":
    case "=":
      zoom = Math.min(zoom + 10, 200);
      break;
    case "-":
      zoom = Math.max(zoom - 10, 60);
      break;
    case "0":
      zoom = 100;
      break;
    default:
      return;
  }
  ev.preventDefault();
  localStorage.setItem(ZOOM_KEY, String(zoom));
  applyZoom();
});

applyZoom();


let state = { path: "", exists: false, error: "", connections: [] };
// editingIndex: null = form closed, -1 = adding, >= 0 = editing that entry.
// It only applies to the connections screen.
let editingIndex = null;

// One screen at a time, chosen in the sidebar. Opening the app always lands
// on the dashboard — deliberately not remembered across runs: the monitoring
// screen is the app's home, and a predictable start beats a sticky one. The
// ?screen= override exists for the screenshot harness, which needs to open a
// given screen headlessly; the app itself navigates with no query string.
const SCREENS = ["dashboard", "browser", "sessions", "indexes", "maintenance",
  "migrations", "connections"];
let screen = new URLSearchParams(location.search).get("screen") || "dashboard";
if (!SCREENS.includes(screen)) screen = "dashboard";

const NAV_ICONS = {
  dashboard: "speedometer",
  browser: "table",
  sessions: "activity",
  indexes: "diagram",
  maintenance: "wrench",
  migrations: "stack",
  connections: "gear",
};

const NAV_COLLAPSED_KEY = "keikiban.navCollapsed";
let navCollapsed = localStorage.getItem(NAV_COLLAPSED_KEY) === "1";

// loadScreen fetches what a report screen shows. Startup needs it as much as
// navigation does: reopening on a report screen must not land on an empty one.
function loadScreen(name) {
  if (name === "sessions") loadSessions();
  if (name === "indexes") loadIndexes();
  if (name === "maintenance") loadMaintenance();
  if (name === "browser") loadBrowser();
  if (name === "migrations") loadMigrations();
}

function goTo(name) {
  screen = name;
  render();
  loadScreen(name);
}

const WINDOW_KEY = "keikiban.window";
let windowSeconds = Number(localStorage.getItem(WINDOW_KEY)) || 300;
const SLICE_KEY = "keikiban.sliceBy";
let sliceBy = localStorage.getItem(SLICE_KEY) || "waits";
const TOP_KEY = "keikiban.topBy";
let topBy = localStorage.getItem(TOP_KEY) || "sql";

// Heading per Top dimension; the tab labels are in the page.
const TOP_TITLES = {
  sql: "Top SQL",
  waits: "Top waits",
  users: "Top users",
  hosts: "Top hosts",
  applications: "Top applications",
  databases: "Top databases",
};

// Wait events are open-ended ("IO:WALSync", "LWLock:WALWrite", ...), so
// colors are assigned by name hash from a fixed palette; only the two anchors
// of Performance Insights are pinned: CPU green, Other gray. Mid-saturation
// values stay readable on both themes.
const PALETTE = [
  "#1e88e5", "#e53935", "#fb8c00", "#8e24aa", "#26a69a", "#c2185b",
  "#00acc1", "#6d4c41", "#f06292", "#7cb342", "#5c6bc0", "#fdd835",
];

function classColor(name) {
  if (name === "CPU") return "#2e7d32";
  if (name === "Other") return "#9e9e9e";
  let h = 0;
  for (const ch of name) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return PALETTE[h % PALETTE.length];
}

function button(label, onClick, iconName) {
  const b = document.createElement("button");
  b.type = "button";
  if (iconName) b.append(icon(iconName));
  b.append(label);
  b.addEventListener("click", onClick);
  return b;
}

function buttonRow(...buttons) {
  const row = document.createElement("div");
  row.className = "buttons";
  row.append(...buttons);
  return row;
}

function renderCell(conn, i) {
  const cell = document.createElement("div");
  cell.className = "cell";

  const title = document.createElement("div");
  title.className = "conn-title";
  title.textContent = conn.title;
  if (i === 0) {
    const badge = document.createElement("span");
    badge.className = "conn-default";
    badge.textContent = "default";
    title.append(badge);
  }
  if (i === state.active) {
    const badge = document.createElement("span");
    badge.className = "conn-default";
    badge.textContent = "connected";
    title.append(badge);
  }

  const edit = button("", () => openForm(i), "pencil");
  edit.classList.add("icon-only");
  edit.title = "Edit";
  edit.setAttribute("aria-label", "Edit");

  const row = buttonRow(edit);
  if (i !== 0) {
    // "Default" is a position in the file, not a flag: promoting one moves its
    // line to the top, which is where keikiban looks when it opens.
    row.append(button("Make default", () => makeDefaultIndex(i)));
  }
  if (i === state.active) {
    row.append(button("Disconnect", disconnectActive, "plug"));
  } else {
    // Attaching here detaches from the current one: exactly one open server,
    // so a command can never land on the database you were not looking at.
    row.append(button("Connect", () => connectToIndex(i), "plug"));
  }
  cell.append(title, row);
  return cell;
}

async function makeDefaultIndex(index) {
  showListResult("");
  try {
    state = await window.makeDefault(index);
  } catch (err) {
    showListResult("Could not promote: " + err);
    return;
  }
  render();
}

// Closing the connection is a resting state, not a failure: keikiban stays on
// this screen attached to nothing until you pick a database again.
async function disconnectActive() {
  showListResult("");
  try {
    state = await window.disconnect();
  } catch (err) {
    showListResult("Could not disconnect: " + err);
    return;
  }
  idxReport = null;
  maint = null;
  sessions = null;
  render();
}

async function connectToIndex(index) {
  showListResult("");
  try {
    state = await window.connectTo(index);
  } catch (err) {
    showListResult("Could not connect: " + err);
    return;
  }
  // Reports belong to the previous server; drop them so nothing stale is
  // read as if it came from the new one.
  idxReport = null;
  maint = null;
  sessions = null;
  goTo("dashboard");
}

function render() {
  const sections = ["setup", "list", "dashboard", "sessions", "indexes",
    "maintenance", "migrations", "browser"];
  const hideAll = () => sections.forEach((id) => { el(id).hidden = true; });

  el("config-error-box").hidden = !state.error;
  if (state.error) {
    el("config-error").textContent = state.error;
    hideAll();
    return;
  }

  el("cfg-path").textContent = state.path;

  // What decides the screen is the RESULT of running the config: no databases
  // configured means keikiban asks for one, whether the file is missing,
  // empty, or holds only comments.
  const unconfigured = state.connections.length === 0;
  if (unconfigured) {
    screen = "connections";
    if (editingIndex === null) editingIndex = -1;
  }
  const formOpen = screen === "connections" && editingIndex !== null;

  hideAll();
  // Every screen is its own section except connections, which has two faces:
  // the list, and the add/edit form that takes over when one is open.
  let section = screen;
  if (screen === "connections") section = "list";
  if (formOpen) section = "setup";
  el(section).hidden = false;

  el("first-run-note").hidden = !unconfigured;
  el("cancel").hidden = unconfigured;
  // Deleting is a form of editing: the destructive action lives here, not in
  // the list.
  el("delete").hidden = editingIndex === null || editingIndex < 0;
  el("setup-heading").textContent =
    editingIndex >= 0 ? "Edit connection" : "Add a PostgreSQL connection";

  for (const b of document.querySelectorAll(".nav-item[data-screen]")) {
    if (b.dataset.screen === screen) {
      b.setAttribute("aria-current", "page");
    } else {
      b.removeAttribute("aria-current");
    }
  }
  applyNarrow();

  const attached = state.connections[state.active];
  el("attached").hidden = !attached;
  if (attached) {
    el("attached-name").textContent = attached.title;
    el("attached").title = "Connected to " + attached.title +
      ". Only one connection is open at a time.";
  }

  const box = el("connections");
  box.replaceChildren();
  state.connections.forEach((conn, i) => box.append(renderCell(conn, i)));

  if (!el("dashboard").hidden) refreshDashboard();
}

function showResult(text) {
  el("result").textContent = text;
}

// The list and the edit form are never on screen together, so a failure in the
// list needs its own place to be seen; #result lives inside the form.
function showListResult(text) {
  el("list-result").textContent = text;
}

async function openForm(index) {
  editingIndex = index;
  el("confirm").hidden = true;
  showResult("");
  el("title").value = "";
  el("url").value = "";
  if (index >= 0) {
    try {
      el("url").value = await window.connectionURL(index);
    } catch (err) {
      showResult(String(err));
    }
    el("title").value = state.connections[index].title;
  }
  render();
  el("url").focus();
}

function closeForm() {
  editingIndex = null;
  el("confirm").hidden = true;
  el("url").value = "";
  el("title").value = "";
  showResult("");
  render();
}

async function removeConnection(i) {
  try {
    state = await window.deleteConnection(i);
  } catch (err) {
    el("confirm").hidden = true;
    showResult("Could not remove: " + err);
    return;
  }
  closeForm();
}

el("test").addEventListener("click", async () => {
  const url = el("url").value.trim();
  if (!url) {
    showResult("Enter a connection URL first.");
    return;
  }
  showResult("Connecting...");
  const r = await window.testConnection(url);
  if (r.ok) {
    showResult("Connected.\n" + r.version);
    return;
  }
  showResult("Connection failed: " + r.error);
});

el("setup-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const url = el("url").value.trim();
  const title = el("title").value.trim();
  if (!url) {
    showResult("Enter a connection URL first.");
    return;
  }
  try {
    if (editingIndex >= 0) {
      state = await window.updateConnection(editingIndex, url, title);
    } else {
      state = await window.addConnection(url, title);
    }
  } catch (err) {
    showResult("Could not save: " + err);
    return;
  }
  closeForm();
});

// --- Dashboard ---

let dash = null;

async function refreshDashboard() {
  if (el("dashboard").hidden || paused) return;
  try {
    dash = await window.dashboardState(windowSeconds, sliceBy, topBy);
  } catch (err) {
    el("dash-status").textContent = String(err);
    return;
  }
  el("dash-title").textContent = dash.title || "";
  // Never leave the user guessing: idle-and-sampling must not look broken.
  let status = dash.status;
  if (dash.connected) {
    status = "sampling, " + dash.samples + " samples in this window";
  }
  el("dash-status").textContent = status;
  el("ext-badge").hidden = dash.missingExtensions.length === 0;
  el("ext-names").textContent = dash.missingExtensions.join(", ");
  // When the library is already preloaded on the server, one CREATE EXTENSION
  // suffices; otherwise the restart step comes first.
  const allPreloaded = dash.missingExtensions.length > 0 &&
    dash.missingExtensions.every((m) => dash.missingPreloaded.includes(m));
  el("ext-cmds").textContent = allPreloaded
    ? "-- the library is already preloaded on this server;\n" +
      "-- run this in the connected database:\n" +
      "CREATE EXTENSION IF NOT EXISTS pg_stat_statements;"
    : "-- 1. add to postgresql.conf, then restart PostgreSQL:\n" +
      "shared_preload_libraries = 'pg_stat_statements'\n\n" +
      "-- 2. then, in the connected database:\n" +
      "CREATE EXTENSION IF NOT EXISTS pg_stat_statements;";
  for (const b of document.querySelectorAll(".win")) {
    b.setAttribute("aria-pressed", String(Number(b.dataset.window) === windowSeconds));
  }
  for (const b of document.querySelectorAll(".tab")) {
    b.setAttribute("aria-pressed", String(b.dataset.top === topBy));
  }
  // A single-database cluster has nothing to say per database; the tab only
  // appears where it can actually answer "which database loads the server".
  el("tab-databases").hidden = dash.databaseCount < 2;
  if (dash.databaseCount < 2 && topBy === "databases") {
    topBy = "sql";
    localStorage.setItem(TOP_KEY, topBy);
  }
  el("top-heading").firstChild.textContent = (TOP_TITLES[topBy] || "Top") + " ";
  drawChart();
  drawCounters();
  renderLegend();
  renderBlocking();
  renderTopSQL();
}

// signalling: "cancel:<pid>" or "terminate:<pid>" while its confirmation is
// on screen.
let signalling = null;

function renderBlocking() {
  const box = el("blocking");
  box.replaceChildren();

  if (dash.blocking.length === 0) {
    const note = document.createElement("p");
    note.className = "hint";
    note.textContent = "No session is waiting on a lock.";
    box.append(note);
    return;
  }

  for (const b of dash.blocking) {
    const cell = document.createElement("div");
    cell.className = "cell";

    const title = document.createElement("div");
    title.className = "conn-title";
    title.textContent = "pid " + b.pid + " blocks " + b.blocked.length +
      (b.blocked.length === 1 ? " session" : " sessions");
    cell.append(title);

    const meta = document.createElement("div");
    meta.className = "idx-meta";
    const st = document.createElement("span");
    st.textContent = b.state + " for " + b.stateSeconds.toFixed(1) + "s";
    meta.append(st);
    if (b.user) {
      const who = document.createElement("span");
      who.textContent = b.user + (b.app ? " · " + b.app : "");
      meta.append(who);
    }
    if (b.alsoBlocked) {
      const chain = document.createElement("span");
      chain.className = "warn-text chain-note";
      chain.textContent = "also waiting: the root cause is further up the chain";
      meta.append(chain);
    }
    cell.append(meta);

    const q = document.createElement("div");
    q.className = "sql-text";
    q.textContent = b.query || "(no query text)";
    cell.append(q);

    for (const w of b.blocked) {
      const item = document.createElement("div");
      item.className = "blocked-item";
      const head = document.createElement("div");
      head.className = "idx-meta";
      const pid = document.createElement("span");
      pid.textContent = "pid " + w.pid + " waiting " + w.waitSeconds.toFixed(1) + "s";
      head.append(pid);
      if (w.lockMode) {
        const mode = document.createElement("span");
        mode.textContent = "wants " + w.lockMode;
        head.append(mode);
      }
      if (w.waitEvent) {
        const ev = document.createElement("span");
        ev.textContent = "Lock:" + w.waitEvent;
        head.append(ev);
      }
      const wq = document.createElement("div");
      wq.className = "sql-text";
      wq.textContent = w.query || "(no query text)";
      item.append(head, wq);
      cell.append(item);
    }

    cell.append(blockerActions(b));
    box.append(cell);
  }
}

function blockerActions(b) {
  const key = signalling ? signalling.split(":") : null;
  if (key && Number(key[1]) === b.pid) {
    const terminate = key[0] === "terminate";
    const wrap = document.createElement("div");
    const q = document.createElement("div");
    q.textContent = terminate
      ? "Terminate connection " + b.pid + "? Its transaction is rolled back " +
        "and the client is disconnected."
      : "Cancel the running query of pid " + b.pid + "? The transaction stays " +
        "open, so its locks are only released on COMMIT or ROLLBACK.";
    const stmt = document.createElement("div");
    stmt.className = "sql-text";
    const sql = terminate ? b.terminateSQL : b.cancelSQL;
    stmt.textContent = sql;
    const copy = button("Copy SQL", async () => {
      await navigator.clipboard.writeText(sql);
    });
    const act = button(terminate ? "Confirm termination" : "Confirm cancellation",
      () => runSignal(b, terminate));
    act.classList.add("destructive");
    // "Back" rather than "Cancel": in this dialog cancelling IS the action,
    // so a Cancel button would mean two opposite things at once.
    wrap.append(q, stmt, buttonRow(
      copy,
      button("Back", () => {
        signalling = null;
        renderBlocking();
      }),
      act,
    ));
    return wrap;
  }

  const cancelBtn = button("Cancel query...", () => {
    signalling = "cancel:" + b.pid;
    renderBlocking();
  });
  cancelBtn.classList.add("destructive");
  const termBtn = button("Terminate...", () => {
    signalling = "terminate:" + b.pid;
    renderBlocking();
  });
  termBtn.classList.add("destructive");
  return buttonRow(cancelBtn, termBtn);
}

async function runSignal(b, terminate) {
  signalling = null;
  try {
    await window.signalBackend(b.pid, terminate);
  } catch (err) {
    el("dash-status").textContent = String(err);
    return;
  }
  refreshDashboard();
}

const CHART_PAD = { left: 40, right: 8, top: 8, bottom: 18 };

// drawStacked renders one stacked-column chart in the AWS PI look: thin bars
// with gaps, integer grid, a few HH:MM ticks. Returns the data maximum so
// callers can annotate empty charts.
function drawStacked(canvas, classes, buckets, opts = {}) {
  const dpr = window.devicePixelRatio || 1;
  const w = canvas.clientWidth;
  const h = canvas.clientHeight;
  canvas.width = w * dpr;
  canvas.height = h * dpr;
  const ctx = canvas.getContext("2d");
  ctx.scale(dpr, dpr);
  ctx.clearRect(0, 0, w, h);

  const textColor = getComputedStyle(document.body).color;
  const pad = CHART_PAD;
  const plotW = w - pad.left - pad.right;
  const plotH = h - pad.top - pad.bottom;

  let dataMax = 0;
  for (const b of buckets) {
    let total = 0;
    for (const k in b.v) total += b.v[k];
    if (total > dataMax) dataMax = total;
  }
  let yMax = Math.max(1, Math.ceil(dataMax));
  // A reference line (e.g. max_connections) joins the scale only when it
  // would not flatten the data.
  const ref = opts.refValue || 0;
  if (ref > 0 && ref <= dataMax * 4) yMax = Math.max(yMax, Math.ceil(ref));

  ctx.font = "11px system-ui";
  ctx.strokeStyle = "rgba(127,127,127,0.3)";
  ctx.fillStyle = textColor;
  const step = Math.max(1, Math.ceil(yMax / 5));
  for (let y = 0; y <= yMax; y += step) {
    const py = pad.top + plotH - (y / yMax) * plotH;
    ctx.beginPath();
    ctx.moveTo(pad.left, py);
    ctx.lineTo(w - pad.right, py);
    ctx.stroke();
    ctx.textAlign = "right";
    ctx.textBaseline = "middle";
    ctx.fillText(String(y), pad.left - 6, py);
  }

  const n = buckets.length;
  const slotW = plotW / n;
  const barW = Math.max(1, slotW * 0.6);
  for (let i = 0; i < n; i++) {
    const b = buckets[i];
    let y = pad.top + plotH;
    const x = pad.left + i * slotW + (slotW - barW) / 2;
    for (const cls of classes) {
      const v = b.v[cls];
      if (!v) continue;
      const barH = (v / yMax) * plotH;
      y -= barH;
      ctx.fillStyle = (opts.colorOf || classColor)(cls);
      ctx.fillRect(x, y, barW, barH);
    }
  }

  if (ref > 0 && ref <= yMax) {
    const py = pad.top + plotH - (ref / yMax) * plotH;
    ctx.save();
    ctx.strokeStyle = textColor;
    ctx.setLineDash([5, 4]);
    ctx.globalAlpha = 0.6;
    ctx.beginPath();
    ctx.moveTo(pad.left, py);
    ctx.lineTo(w - pad.right, py);
    ctx.stroke();
    ctx.restore();
  }

  ctx.fillStyle = textColor;
  ctx.textBaseline = "top";
  ctx.textAlign = "center";
  const fmt = (ms) => new Date(ms).toLocaleTimeString(undefined,
    { hour: "2-digit", minute: "2-digit" });
  for (let i = 0; i < n; i += Math.floor(n / 4)) {
    ctx.fillText(fmt(buckets[i].t), pad.left + (i + 0.5) * slotW,
      pad.top + plotH + 4);
  }
  ctx.textAlign = "right";
  ctx.fillText("now", w - pad.right, pad.top + plotH + 4);

  return dataMax;
}

function drawChart() {
  const canvas = el("chart");
  const maxAAS = drawStacked(canvas, dash.classes, dash.buckets);

  // An empty chart while sampling means the database is idle; say so instead
  // of looking broken.
  if (maxAAS === 0 && dash.connected && dash.samples > 0) {
    const ctx = canvas.getContext("2d");
    const w = canvas.clientWidth;
    const h = canvas.clientHeight;
    ctx.globalAlpha = 0.7;
    ctx.fillStyle = getComputedStyle(document.body).color;
    ctx.font = "12px system-ui";
    ctx.textAlign = "center";
    ctx.textBaseline = "middle";
    ctx.fillText("No active sessions sampled, the database looks idle.",
      w / 2, h / 2);
    ctx.globalAlpha = 1;
  }
}

// Fixed colors for the counter charts.
const CONN_COLORS = {
  active: "#1e88e5",
  "idle in transaction": "#fb8c00",
  idle: "#9e9e9e",
  other: "#8e24aa",
};
const TPS_COLORS = { "commits/s": "#2e7d32", "rollbacks/s": "#e53935" };
// Buffer traffic: cached reads are cheap (teal), disk reads are the ones
// that hurt (amber). Their proportion is the cache hit ratio, made visible
// without the ratio's blind spot at low volume.
const IO_COLORS = { "from cache/s": "#00acc1", "from disk/s": "#fb8c00" };

function drawCounters() {
  drawStacked(el("conns-chart"), dash.connClasses, dash.conns, {
    colorOf: (c) => CONN_COLORS[c] || "#78909c",
    refValue: dash.maxConnections,
  });
  el("conn-max").textContent =
    dash.maxConnections > 0 ? "max_connections: " + dash.maxConnections : "";
  drawStacked(el("tps-chart"), dash.tpsClasses, dash.tps, {
    colorOf: (c) => TPS_COLORS[c] || "#78909c",
  });
  drawStacked(el("io-chart"), dash.ioClasses, dash.io, {
    colorOf: (c) => IO_COLORS[c] || "#78909c",
  });
  renderMiniLegend("conns-legend", dash.connClasses,
    (c) => CONN_COLORS[c] || "#78909c");
  renderMiniLegend("tps-legend", dash.tpsClasses,
    (c) => TPS_COLORS[c] || "#78909c");
  renderMiniLegend("io-legend", dash.ioClasses,
    (c) => IO_COLORS[c] || "#78909c");
}

function renderMiniLegend(id, classes, colorOf) {
  const box = el(id);
  box.replaceChildren();
  for (const cls of classes) {
    const item = document.createElement("span");
    item.className = "legend-item";
    const swatch = document.createElement("span");
    swatch.className = "legend-swatch";
    swatch.style.background = colorOf(cls);
    item.append(swatch, cls);
    box.append(item);
  }
}

function renderLegend() {
  const box = el("legend");
  box.replaceChildren();
  for (const cls of dash.classes) {
    const item = document.createElement("span");
    item.className = "legend-item";
    // A SQL slice key is a whole statement: the row is clipped, the full
    // text stays reachable on hover.
    item.title = cls;
    const swatch = document.createElement("span");
    swatch.className = "legend-swatch";
    swatch.style.background = classColor(cls);
    item.append(swatch, cls);
    box.append(item);
  }
}

function renderTopSQL() {
  const box = el("topsql");
  box.replaceChildren();
  for (const q of dash.topSQL) {
    const cell = document.createElement("div");
    cell.className = "cell";
    const load = document.createElement("div");
    load.className = "sql-load";
    let label = q.aas.toFixed(2) + " avg active sessions (" +
      q.pct.toFixed(1) + "%)";
    if (q.hasStats) {
      label += " · " + q.callsPS.toFixed(2) + " calls/s · " +
        q.rowsPerCall.toFixed(1) + " rows/call · " +
        q.msPerCall.toFixed(1) + " ms/call";
    }
    load.textContent = label;
    // The bar spans the query's share of the window and splits it by wait
    // class in the chart colors, like Performance Insights.
    const bar = document.createElement("div");
    bar.className = "sql-bar";
    bar.style.width = Math.max(1, q.pct) + "%";
    // Always wait classes here, whatever the chart is sliced by: the bar
    // answers "where does this query spend its time".
    for (const cls of dash.waitClasses) {
      const v = q.byClass[cls];
      if (!v) continue;
      const seg = document.createElement("span");
      seg.style.background = classColor(cls);
      seg.style.flexGrow = v;
      seg.title = cls;
      bar.append(seg);
    }
    // A statement gets monospace and wrapping; a user or host name is a
    // short label and reads better as plain text.
    const text = document.createElement("div");
    text.className = dash.topBy === "sql" ? "sql-text" : "top-label";
    text.textContent = q.query;
    cell.append(load, bar, text);
    box.append(cell);
  }
  if (dash.topSQL.length === 0) {
    const note = document.createElement("p");
    note.className = "hint";
    note.textContent = "No active sessions sampled in this window yet.";
    box.append(note);
  }
}

// Hover tooltip: time plus the per-class values of the bucket under the
// cursor, PI style.
el("chart").addEventListener("mousemove", (ev) => {
  const tip = el("chart-tip");
  if (!dash || dash.buckets.length === 0) {
    tip.hidden = true;
    return;
  }
  const rect = el("chart").getBoundingClientRect();
  const pad = CHART_PAD;
  const plotW = rect.width - pad.left - pad.right;
  const x = ev.clientX - rect.left - pad.left;
  if (x < 0 || x > plotW) {
    tip.hidden = true;
    return;
  }
  const i = Math.min(dash.buckets.length - 1,
    Math.floor((x / plotW) * dash.buckets.length));
  const b = dash.buckets[i];

  // PI tooltip shape: each event as "value, pct%", then Total DB load.
  tip.replaceChildren();
  const when = document.createElement("div");
  when.textContent = new Date(b.t).toLocaleTimeString();
  tip.append(when);
  let total = 0;
  for (const cls of dash.classes) total += b.v[cls] || 0;
  for (const cls of dash.classes) {
    const v = b.v[cls];
    if (!v) continue;
    const pct = total > 0 ? Math.round(100 * v / total) : 0;
    const line = document.createElement("div");
    const swatch = document.createElement("span");
    swatch.className = "legend-swatch";
    swatch.style.background = classColor(cls);
    line.append(swatch, cls + ": " + v.toFixed(2) + ", " + pct + "%");
    tip.append(line);
  }
  const sum = document.createElement("div");
  sum.textContent = "Total DB load: " + total.toFixed(2);
  tip.append(sum);

  tip.hidden = false;
  const tipX = Math.min(ev.clientX - rect.left + 12,
    rect.width - tip.offsetWidth - 4);
  tip.style.left = Math.max(0, tipX) + "px";
  tip.style.top = (ev.clientY - rect.top + 12) + "px";
});
el("chart").addEventListener("mouseleave", () => {
  el("chart-tip").hidden = true;
});

// Auto-refresh pause: the sampler keeps collecting in Go; only the screen
// freezes for inspection.
let paused = false;

function renderPause() {
  const b = el("pause");
  b.replaceChildren(icon(paused ? "play-fill" : "pause-fill"));
  const label = paused ? "Resume auto-refresh" : "Pause auto-refresh";
  b.title = label;
  b.setAttribute("aria-label", label);
}

el("pause").addEventListener("click", () => {
  paused = !paused;
  renderPause();
  if (!paused) refreshDashboard();
});
renderPause();

for (const b of document.querySelectorAll(".win")) {
  b.addEventListener("click", () => {
    windowSeconds = Number(b.dataset.window);
    localStorage.setItem(WINDOW_KEY, String(windowSeconds));
    paused = false;
    renderPause();
    refreshDashboard();
  });
}

for (const b of document.querySelectorAll(".tab")) {
  b.addEventListener("click", () => {
    topBy = b.dataset.top;
    localStorage.setItem(TOP_KEY, topBy);
    paused = false;
    renderPause();
    refreshDashboard();
  });
}

el("slice-by").value = sliceBy;
el("slice-by").addEventListener("change", () => {
  sliceBy = el("slice-by").value;
  localStorage.setItem(SLICE_KEY, sliceBy);
  paused = false;
  renderPause();
  refreshDashboard();
});


// --- Index health screen ---

let idxReport = null;
// idxConfirming: "schema.name" of the entry showing the drop confirmation.
let idxConfirming = null;

async function loadIndexes() {
  el("idx-status").textContent = "Collecting index statistics...";
  try {
    idxReport = await window.indexReport();
  } catch (err) {
    el("idx-status").textContent = String(err);
    return;
  }
  renderIndexes();
}

function renderIndexes() {
  const r = idxReport;
  if (!r) return;
  el("idx-status").textContent = r.error || "";

  const summary = el("idx-summary");
  summary.replaceChildren();
  if (!r.error) {
    const cache = document.createElement("span");
    cache.className = "idx-meta";
    const idxHit = document.createElement("span");
    idxHit.textContent = "index cache hit: " + r.indexCacheHitPct + "%";
    if (r.indexCacheHitPct < 95) idxHit.classList.add("warn-text");
    const tblHit = document.createElement("span");
    tblHit.textContent = "table cache hit: " + r.tableCacheHitPct + "%";
    if (r.tableCacheHitPct < 95) tblHit.classList.add("warn-text");
    const reset = document.createElement("span");
    reset.textContent = r.statsReset
      ? "statistics since " + r.statsReset
      : "statistics never reset";
    cache.append(idxHit, tblHit, reset);
    summary.append(cache);
  }

  el("idx-unused-note").textContent =
    "Never scanned since the statistics started, ordered by size. " +
    "Primary keys are excluded; unique indexes may still enforce " +
    "constraints even when never scanned." +
    (r.unusedTruncated ? " Showing the " + r.unused.length + " largest." : "");

  renderIndexList("idx-invalid", r.invalid, "No invalid indexes.");
  renderIndexList("idx-duplicates", r.duplicates, "No duplicate indexes.");
  renderIndexList("idx-unused", r.unused, "No unused indexes.");
  renderSeqScans(r.seqScans);
}

function indexCell(e) {
  const key = e.schema + "." + e.name;
  const cell = document.createElement("div");
  cell.className = "cell";

  if (idxConfirming === key) {
    const q = document.createElement("div");
    q.textContent = 'Drop index "' + e.name + '" on ' +
      e.schema + "." + e.table + " (" + e.size + ")?";
    const ddl = document.createElement("div");
    ddl.className = "sql-text";
    ddl.textContent = e.dropDDL;
    const hint = document.createElement("div");
    hint.className = "hint";
    hint.textContent = "Runs exactly the statement above. CONCURRENTLY does " +
      "not block writes, but can take a while on a big index.";
    // Copy lives here, next to the DROP statement it copies: in the list it
    // sat beside the CREATE INDEX definition and read as copying that one.
    const copy = button("Copy DDL", async () => {
      await navigator.clipboard.writeText(e.dropDDL);
    });
    const drop = button("Confirm deletion", () => runDropIndex(e), "trash");
    drop.classList.add("destructive");
    cell.append(q, ddl, hint, buttonRow(
      copy,
      button("Cancel", () => {
        idxConfirming = null;
        renderIndexes();
      }),
      drop,
    ));
    return cell;
  }

  const title = document.createElement("div");
  title.className = "conn-title";
  title.textContent = e.schema + "." + e.table + " . " + e.name;

  const meta = document.createElement("div");
  meta.className = "idx-meta";
  const size = document.createElement("span");
  size.textContent = e.size;
  meta.append(size);
  const scans = document.createElement("span");
  scans.textContent = e.scans + " scans";
  meta.append(scans);
  if (e.unique) {
    const uq = document.createElement("span");
    uq.className = "warn-text";
    uq.textContent = "unique: may enforce a constraint";
    meta.append(uq);
  }
  if (e.coveredBy) {
    const cov = document.createElement("span");
    cov.textContent = "covered by " + e.coveredBy;
    meta.append(cov);
  }

  const def = document.createElement("div");
  def.className = "sql-text";
  def.textContent = e.definition;

  const drop = button("Drop index...", () => {
    idxConfirming = key;
    renderIndexes();
  }, "trash");
  drop.classList.add("destructive");
  drop.disabled = idxBusy;

  cell.append(title, meta, def, buttonRow(drop));
  return cell;
}

function renderIndexList(id, entries, emptyText) {
  const box = el(id);
  box.replaceChildren();
  if (entries.length === 0) {
    const note = document.createElement("p");
    note.className = "hint";
    note.textContent = emptyText;
    box.append(note);
    return;
  }
  for (const e of entries) box.append(indexCell(e));
}

function renderSeqScans(tables) {
  const box = el("idx-seqscans");
  box.replaceChildren();
  if (tables.length === 0) {
    const note = document.createElement("p");
    note.className = "hint";
    note.textContent = "No large tables dominated by sequential scans.";
    box.append(note);
    return;
  }
  const table = document.createElement("table");
  table.className = "scans";
  const head = table.insertRow();
  for (const h of ["Table", "Seq scans", "Index scans", "Index use", "Live rows"]) {
    const th = document.createElement("th");
    th.textContent = h;
    if (h !== "Table") th.className = "num";
    head.append(th);
  }
  for (const t of tables) {
    const row = table.insertRow();
    const cells = [
      t.schema + "." + t.table,
      String(t.seqScans),
      String(t.idxScans),
      t.indexUsePct + "%",
      String(t.liveRows),
    ];
    cells.forEach((text, i) => {
      const td = row.insertCell();
      td.textContent = text;
      if (i > 0) td.className = "num";
    });
  }
  box.append(table);
}

/* Object browser ---------------------------------------------------------
   The tree is deliberately shallow (schema > group > object) and its search
   runs on the server: DBeaver's navigator is the counter-example on both
   counts, where the filter only ever finds what is already expanded. */

const BR_GROUPS = [
  { key: "tables", label: "Tables", count: "tables" },
  { key: "views", label: "Views", count: "views" },
  { key: "matviews", label: "Materialized views", count: "matViews" },
  { key: "sequences", label: "Sequences", count: "sequences" },
];

const KIND_LABELS = {
  r: "table",
  p: "partitioned table",
  v: "view",
  m: "materialized view",
  S: "sequence",
};

const BR_OPEN_KEY = "keikiban.browserOpen";

let brTree = null;
// Group nodes whose children are on screen. Persisted so that reopening the
// app lands where the work was, which DBeaver users keep asking for.
let brOpen = new Set(JSON.parse(localStorage.getItem(BR_OPEN_KEY) || "[]"));
// Loaded children, keyed "schema/group"; a group is fetched once per visit.
const brChildren = new Map();
let brSelected = null;
let brSearch = null;
let brSearchTimer = 0;

function brSaveOpen() {
  localStorage.setItem(BR_OPEN_KEY, JSON.stringify([...brOpen]));
}

async function loadBrowser() {
  el("br-status").textContent = "Reading the catalog...";
  try {
    brTree = await window.browserTree();
  } catch (err) {
    el("br-status").textContent = String(err);
    return;
  }
  el("br-status").textContent = brTree.error || "";

  // A database with a single schema has nothing to choose: open it rather
  // than making the first click a formality.
  if (brTree.schemas.length === 1 && brOpen.size === 0) {
    brOpen.add(brTree.schemas[0].name);
    brSaveOpen();
  }
  // Reopening restores the tree, so the groups that were open need their
  // children back before anything can be drawn.
  await Promise.all([...brOpen]
    .filter((key) => key.includes("/"))
    .map((key) => brFetchGroup(key)));
  renderBrowser();
}

async function brFetchGroup(key) {
  const [schema, group] = key.split("/");
  try {
    brChildren.set(key, await window.browserObjects(schema, group));
  } catch (err) {
    brChildren.set(key, { error: String(err), objects: [] });
  }
}

async function brToggle(key) {
  if (brOpen.has(key)) {
    brOpen.delete(key);
    brSaveOpen();
    renderBrowser();
    return;
  }
  brOpen.add(key);
  brSaveOpen();
  if (key.includes("/") && !brChildren.has(key)) await brFetchGroup(key);
  renderBrowser();
}

// brHighlight marks where you are without opening anything. It moves the mark
// in place instead of redrawing the tree: rebuilding the nodes between the two
// clicks of a double-click would replace the element mid-gesture, and the
// dblclick would never fire.
function brHighlight(node, key) {
  brSelected = key;
  for (const n of document.querySelectorAll("#br-tree .br-node[aria-current]")) {
    n.removeAttribute("aria-current");
  }
  node.setAttribute("aria-current", "true");
}

// brOpenObject opens the object in its own native window. Keeping the
// properties out of this screen is what lets several objects stay open at
// once, each with its own tabs.
async function brOpenObject(schema, name) {
  try {
    await window.openObject(schema, name);
  } catch (err) {
    el("br-status").textContent = String(err);
  }
}

// brObjectNode wires one openable object: a click marks it, a double-click
// opens it. Enter does the same as the double-click, because a double-click
// has no keyboard equivalent and the tree has to stay usable without a mouse.
function brObjectNode(className, schema, name, opts = {}) {
  const key = schema + "." + name;
  const node = brNode(className, opts.label || name, {
    kind: opts.kind,
    current: brSelected === key,
  });
  node.addEventListener("click", () => brHighlight(node, key));
  node.addEventListener("dblclick", () => brOpenObject(schema, name));
  node.addEventListener("keydown", (e) => {
    if (e.key !== "Enter") return;
    e.preventDefault();
    brHighlight(node, key);
    brOpenObject(schema, name);
  });
  return node;
}

// brRunSearch asks the server, so a match is found whether or not its branch
// was ever opened. Debounced: one query per pause, not per keystroke.
function brQueueSearch(term) {
  clearTimeout(brSearchTimer);
  if (!term.trim()) {
    brSearch = null;
    renderBrowser();
    return;
  }
  brSearchTimer = setTimeout(async () => {
    try {
      brSearch = await window.browserSearch(term);
    } catch (err) {
      brSearch = { error: String(err), hits: [] };
    }
    renderBrowser();
  }, 250);
}

function brNode(className, label, opts = {}) {
  const node = document.createElement("button");
  node.type = "button";
  node.className = "br-node " + className;

  // The glyph is decoration: left readable it lands in the accessible name as
  // "black down-pointing triangle", and aria-expanded already says the state.
  const twisty = document.createElement("span");
  twisty.className = "br-twisty";
  twisty.textContent = opts.twisty || "";
  twisty.setAttribute("aria-hidden", "true");
  node.append(twisty);
  if (opts.twisty) node.setAttribute("aria-expanded", String(opts.open));

  const text = document.createElement("span");
  text.textContent = label;
  node.append(text);

  if (opts.kind) {
    const kind = document.createElement("span");
    kind.className = "br-kind";
    kind.textContent = opts.kind;
    node.append(kind);
    node.setAttribute("aria-label", label + ", " + opts.kind);
  }
  if (opts.count !== undefined) {
    const count = document.createElement("span");
    count.className = "br-count";
    count.textContent = opts.count;
    node.append(count);
    // Concatenated text would read as "Tables4"; spell the pair out instead.
    node.setAttribute("aria-label", label + ", " + opts.count);
  }
  if (opts.current) node.setAttribute("aria-current", "true");
  if (opts.onClick) node.addEventListener("click", opts.onClick);
  return node;
}

function renderBrowser() {
  const tree = el("br-tree");
  tree.replaceChildren();

  if (brSearch) {
    renderBrowserSearch(tree);
  } else {
    el("br-search-note").hidden = true;
    renderBrowserTree(tree);
  }
}

function renderBrowserSearch(tree) {
  const note = el("br-search-note");
  note.hidden = false;
  if (brSearch.error) {
    note.textContent = brSearch.error;
    return;
  }
  note.textContent = brSearch.truncated
    ? "Showing " + brSearch.hits.length + " of " + brSearch.total + " matches."
    : brSearch.total + (brSearch.total === 1 ? " match" : " matches");

  for (const h of brSearch.hits) {
    tree.append(brObjectNode("br-hit", h.schema, h.name, {
      label: h.schema + "." + h.name,
      kind: KIND_LABELS[h.kind] || h.kind,
    }));
  }
}

function renderBrowserTree(tree) {
  if (!brTree) return;
  if (brTree.schemas.length === 0 && !brTree.error) {
    const note = document.createElement("p");
    note.className = "hint";
    note.textContent = "No schemas visible to this user.";
    tree.append(note);
    return;
  }

  for (const s of brTree.schemas) {
    const openSchema = brOpen.has(s.name);
    tree.append(brNode("br-schema", s.name, {
      twisty: openSchema ? "▼" : "▶",
      open: openSchema,
      onClick: () => brToggle(s.name),
    }));
    if (!openSchema) continue;

    for (const g of BR_GROUPS) {
      const key = s.name + "/" + g.key;
      const openGroup = brOpen.has(key);
      tree.append(brNode("br-group", g.label, {
        twisty: openGroup ? "▼" : "▶",
        open: openGroup,
        count: String(s[g.count]),
        onClick: () => brToggle(key),
      }));
      if (!openGroup) continue;

      const list = brChildren.get(key);
      if (!list) continue;
      if (list.error) {
        const err = document.createElement("p");
        err.className = "hint br-leaf";
        err.textContent = list.error;
        tree.append(err);
        continue;
      }
      for (const o of list.objects) {
        tree.append(brObjectNode("br-leaf", o.schema, o.name));
      }
      if (list.truncated) {
        const more = document.createElement("p");
        more.className = "hint br-leaf";
        more.textContent = "Showing " + list.objects.length + " of " +
          list.total + ". Use the search box to reach the rest.";
        tree.append(more);
      }
    }
  }
}

// busyBar shows an indeterminate bar for an action whose duration the server
// does not report. It has no percentage on purpose: an invented one lies.
function busyBar(id, label) {
  const box = el(id);
  if (!label) {
    box.hidden = true;
    box.replaceChildren();
    return;
  }
  box.hidden = false;
  box.className = "cell";
  box.replaceChildren();
  const text = document.createElement("div");
  text.textContent = label;
  const track = document.createElement("div");
  track.className = "progress-track";
  const fill = document.createElement("div");
  fill.className = "progress-fill indeterminate";
  track.append(fill);
  box.append(text, track);
}

let idxBusy = false;

async function runDropIndex(e) {
  idxConfirming = null;
  idxBusy = true;
  el("idx-status").textContent = "";
  renderIndexes();
  // CONCURRENTLY waits for other transactions to finish, so this can take a
  // while with no server-side progress to report.
  busyBar("idx-progress", "Dropping " + e.name + " concurrently...");
  try {
    idxReport = await window.dropIndex(e.schema, e.name);
  } catch (err) {
    el("idx-status").textContent = "Could not drop: " + err;
    return;
  } finally {
    idxBusy = false;
    busyBar("idx-progress", "");
  }
  renderIndexes();
}

el("idx-refresh").addEventListener("click", loadIndexes);

// Refresh drops the cached children too: a stale tree that never updates is
// one of the standing complaints about the tools this replaces.
el("br-refresh").addEventListener("click", () => {
  brChildren.clear();
  loadBrowser();
});
el("br-search").addEventListener("input", (e) => brQueueSearch(e.target.value));

// --- Maintenance screen ---

let maint = null;
// maintConfirming: "vacuum:<schema>.<table>", "cancel:<pid>" or
// "terminate:<pid>" while its confirmation is on screen.
let maintConfirming = null;

function ago(seconds) {
  if (seconds < 0) return "never";
  if (seconds < 90) return Math.round(seconds) + "s ago";
  if (seconds < 5400) return Math.round(seconds / 60) + "min ago";
  if (seconds < 172800) return Math.round(seconds / 3600) + "h ago";
  return Math.round(seconds / 86400) + "d ago";
}

async function loadMaintenance() {
  el("maint-status").textContent = "Collecting maintenance statistics...";
  try {
    maint = await window.maintenanceReport();
  } catch (err) {
    el("maint-status").textContent = String(err);
    return;
  }
  renderMaintenance();
}

function renderMaintenance() {
  const m = maint;
  if (!m) return;
  el("maint-status").textContent = m.error || "";

  const wrap = el("maint-wraparound");
  wrap.replaceChildren();
  const line = document.createElement("span");
  line.className = "idx-meta";
  const pct = document.createElement("span");
  pct.textContent = "database at " + m.wraparoundPct + "% of the freeze " +
    "threshold (" + m.databaseAge.toLocaleString() + " of " +
    m.freezeMaxAge.toLocaleString() + " transactions)";
  if (m.wraparoundPct >= 80) pct.classList.add("warn-text");
  line.append(pct);
  wrap.append(line);

  renderTableAges(m.oldestTables);
  renderLongTx(m.longTx);
  renderVacuum(m.needVacuum);
  renderSequences(m.sequences);
}

function renderTableAges(tables) {
  const box = el("maint-oldest");
  box.replaceChildren();
  if (tables.length === 0) {
    const note = document.createElement("p");
    note.className = "hint";
    note.textContent = "No table statistics available.";
    box.append(note);
    return;
  }
  const table = document.createElement("table");
  table.className = "scans";
  const head = table.insertRow();
  for (const h of ["Oldest tables", "Transactions since freeze", "Of threshold", "Size"]) {
    const th = document.createElement("th");
    th.textContent = h;
    if (h !== "Oldest tables") th.className = "num";
    head.append(th);
  }
  for (const t of tables) {
    const row = table.insertRow();
    const cells = [
      t.schema + "." + t.table,
      t.age.toLocaleString(),
      t.pct + "%",
      t.size,
    ];
    cells.forEach((text, i) => {
      const td = row.insertCell();
      td.textContent = text;
      if (i > 0) td.className = "num";
      if (i === 2 && t.pct >= 80) td.classList.add("warn-text");
    });
  }
  box.append(table);
}

function renderLongTx(list) {
  const box = el("maint-longtx");
  box.replaceChildren();
  if (list.length === 0) {
    const note = document.createElement("p");
    note.className = "hint";
    note.textContent = "No transaction has been open for more than a minute.";
    box.append(note);
    return;
  }

  for (const t of list) {
    const cell = document.createElement("div");
    cell.className = "cell";

    const title = document.createElement("div");
    title.className = "conn-title";
    title.textContent = "pid " + t.pid + " open for " + ago(t.xactSeconds).replace(" ago", "");
    cell.append(title);

    const meta = document.createElement("div");
    meta.className = "idx-meta";
    const st = document.createElement("span");
    st.textContent = t.state;
    if (t.state.startsWith("idle in transaction")) st.classList.add("warn-text");
    meta.append(st);
    if (t.user) {
      const who = document.createElement("span");
      who.textContent = t.user + (t.app ? " · " + t.app : "");
      meta.append(who);
    }
    cell.append(meta);

    const q = document.createElement("div");
    q.className = "sql-text";
    q.textContent = t.query || "(no query text)";
    cell.append(q);

    cell.append(txActions(t));
    box.append(cell);
  }
}

function txActions(t) {
  const key = maintConfirming ? maintConfirming.split(":") : null;
  if (key && (key[0] === "cancel" || key[0] === "terminate") &&
      Number(key[1]) === t.pid) {
    const terminate = key[0] === "terminate";
    const wrap = document.createElement("div");
    const q = document.createElement("div");
    q.textContent = terminate
      ? "Terminate connection " + t.pid + "? Its transaction is rolled back " +
        "and the client is disconnected."
      : "Cancel the running query of pid " + t.pid + "? The transaction stays " +
        "open, so it keeps holding back vacuum until COMMIT or ROLLBACK.";
    const stmt = document.createElement("div");
    stmt.className = "sql-text";
    const sql = terminate ? t.terminateSQL : t.cancelSQL;
    stmt.textContent = sql;
    const copy = button("Copy SQL", async () => {
      await navigator.clipboard.writeText(sql);
    });
    const act = button(terminate ? "Confirm termination" : "Confirm cancellation",
      () => runMaintSignal(t, terminate));
    act.classList.add("destructive");
    wrap.append(q, stmt, buttonRow(
      copy,
      button("Back", () => {
        maintConfirming = null;
        renderMaintenance();
      }),
      act,
    ));
    return wrap;
  }

  const cancelBtn = button("Cancel query...", () => {
    maintConfirming = "cancel:" + t.pid;
    renderMaintenance();
  });
  cancelBtn.classList.add("destructive");
  const termBtn = button("Terminate...", () => {
    maintConfirming = "terminate:" + t.pid;
    renderMaintenance();
  });
  termBtn.classList.add("destructive");
  return buttonRow(cancelBtn, termBtn);
}

async function runMaintSignal(t, terminate) {
  maintConfirming = null;
  try {
    await window.signalBackend(t.pid, terminate);
  } catch (err) {
    el("maint-status").textContent = String(err);
    return;
  }
  loadMaintenance();
}

function renderVacuum(list) {
  const box = el("maint-vacuum");
  box.replaceChildren();
  if (list.length === 0) {
    const note = document.createElement("p");
    note.className = "hint";
    note.textContent = "No table is carrying a meaningful number of dead rows.";
    box.append(note);
    return;
  }

  for (const t of list) {
    const key = t.schema + "." + t.table;
    const cell = document.createElement("div");
    cell.className = "cell";

    if (maintConfirming === "vacuum:" + key) {
      const q = document.createElement("div");
      q.textContent = "Run vacuum on " + key + "?";
      const stmt = document.createElement("div");
      stmt.className = "sql-text";
      stmt.textContent = t.vacuumSQL;
      const hint = document.createElement("div");
      hint.className = "hint";
      hint.textContent = "Reclaims dead rows and refreshes planner " +
        "statistics. Reads and writes keep working, but on a large table " +
        "this can run for a long time.";
      const copy = button("Copy SQL", async () => {
        await navigator.clipboard.writeText(t.vacuumSQL);
      });
      cell.append(q, stmt, hint, buttonRow(
        copy,
        button("Cancel", () => {
          maintConfirming = null;
          renderMaintenance();
        }),
        button("Confirm vacuum", () => runVacuumTable(t)),
      ));
      box.append(cell);
      continue;
    }

    const title = document.createElement("div");
    title.className = "conn-title";
    title.textContent = key;
    const meta = document.createElement("div");
    meta.className = "idx-meta";
    const dead = document.createElement("span");
    dead.textContent = t.deadRows.toLocaleString() + " dead rows (" +
      t.deadPct + "%)";
    if (t.deadPct >= 20) dead.classList.add("warn-text");
    const live = document.createElement("span");
    live.textContent = t.liveRows.toLocaleString() + " live";
    const size = document.createElement("span");
    size.textContent = t.size;
    const vac = document.createElement("span");
    vac.textContent = "vacuum " + ago(t.vacuumAgo);
    if (t.vacuumAgo < 0) vac.classList.add("warn-text");
    const ana = document.createElement("span");
    ana.textContent = "analyze " + ago(t.analyzeAgo);
    meta.append(dead, live, size, vac, ana);

    const run = button("Run vacuum...", () => {
      maintConfirming = "vacuum:" + key;
      renderMaintenance();
    });
    // One vacuum at a time: a second one would queue behind the first with no
    // visible progress of its own.
    run.disabled = vacuumBusy !== null;

    cell.append(title, meta, buttonRow(run));
    box.append(cell);
  }
}

// vacuumBusy is set while a vacuum this app started is running; it drives the
// progress panel and disables the buttons that would start another one.
let vacuumBusy = null;

async function runVacuumTable(t) {
  maintConfirming = null;
  el("maint-status").textContent = "";
  vacuumBusy = { schema: t.schema, table: t.table, phase: "", pct: 0, elapsed: 0 };
  renderMaintenance();
  renderVacuumProgress();

  const timer = setInterval(pollVacuumProgress, 1000);
  try {
    maint = await window.vacuumTable(t.schema, t.table);
  } catch (err) {
    el("maint-status").textContent = String(err);
    return;
  } finally {
    clearInterval(timer);
    vacuumBusy = null;
    renderVacuumProgress();
  }
  renderMaintenance();
}

async function pollVacuumProgress() {
  if (!vacuumBusy) return;
  const p = await window.vacuumProgress();
  if (!p.running || !vacuumBusy) return;
  vacuumBusy = {
    schema: p.schema,
    table: p.table,
    phase: p.phase,
    pct: p.pct,
    elapsed: p.elapsedSeconds,
    blksDone: p.heapBlksDone,
    blksTotal: p.heapBlksTotal,
    indexPasses: p.indexPasses,
  };
  renderVacuumProgress();
}

// renderVacuumProgress owns its own element so polling never rebuilds the
// whole screen underneath the user.
function renderVacuumProgress() {
  const box = el("vacuum-progress");
  if (!vacuumBusy) {
    box.hidden = true;
    box.replaceChildren();
    return;
  }

  const b = vacuumBusy;
  box.hidden = false;
  box.className = "cell";
  box.replaceChildren();

  const title = document.createElement("div");
  title.className = "conn-title";
  title.textContent = "Vacuuming " + b.schema + "." + b.table;

  const meta = document.createElement("div");
  meta.className = "idx-meta";
  const phase = document.createElement("span");
  // The phase is empty until the server registers the vacuum in its progress
  // view, which takes a moment on a big table.
  phase.textContent = b.phase ? b.phase : "starting";
  meta.append(phase);
  const elapsed = document.createElement("span");
  elapsed.textContent = Math.round(b.elapsed) + "s elapsed";
  meta.append(elapsed);

  // Only the heap scan has a meaningful percentage. Once vacuum moves on to
  // the indexes, the heap counters sit at 100% while minutes of work remain,
  // so the bar goes indeterminate instead of claiming the job is done.
  const scanning = b.phase === "scanning heap";
  if (b.blksTotal > 0 && scanning) {
    const blks = document.createElement("span");
    blks.textContent = b.blksDone.toLocaleString() + " of " +
      b.blksTotal.toLocaleString() + " blocks (" + b.pct + "%)";
    meta.append(blks);
  }
  if (b.indexPasses > 0) {
    const passes = document.createElement("span");
    passes.textContent = "index pass " + b.indexPasses;
    meta.append(passes);
  }

  const track = document.createElement("div");
  track.className = "progress-track";
  const fill = document.createElement("div");
  fill.className = "progress-fill";
  if (scanning && b.pct > 0) {
    fill.style.width = b.pct + "%";
  } else {
    fill.classList.add("indeterminate");
  }
  track.append(fill);

  box.append(title, meta, track);
}

function renderSequences(list) {
  const box = el("maint-sequences");
  box.replaceChildren();
  if (list.length === 0) {
    const note = document.createElement("p");
    note.className = "hint";
    note.textContent = "No sequence has been used yet.";
    box.append(note);
    return;
  }
  const table = document.createElement("table");
  table.className = "scans";
  const head = table.insertRow();
  for (const h of ["Sequence", "Last value", "Maximum", "Spent"]) {
    const th = document.createElement("th");
    th.textContent = h;
    if (h !== "Sequence") th.className = "num";
    head.append(th);
  }
  for (const s of list) {
    const row = table.insertRow();
    const cells = [
      s.schema + "." + s.name,
      s.lastValue.toLocaleString(),
      s.maxValue.toLocaleString(),
      s.pct + "%",
    ];
    cells.forEach((text, i) => {
      const td = row.insertCell();
      td.textContent = text;
      if (i > 0) td.className = "num";
      if (i === 3 && s.pct >= 70) td.classList.add("warn-text");
    });
  }
  box.append(table);
}

el("maint-refresh").addEventListener("click", loadMaintenance);

/* Migrations screen --------------------------------------------------------
   Embeds the migration project's library. The dir field empty means "use the
   directory migration itself has saved for this database"; typing one takes
   over. Every action previews its exact SQL and confirms naming the database,
   the same anatomy as dropping an index. */

let migState = null;
let migAction = null;

// migDir is the directory actually in effect: the manual field wins, the one
// resolved from migration's config otherwise.
function migDir() {
  const manual = el("mig-dir").value.trim();
  if (manual) return manual;
  if (migState) return migState.dir;
  return "";
}

async function loadMigrations() {
  el("mig-status").textContent = "Reading migration state...";
  try {
    migState = await window.migrationStatus(el("mig-dir").value.trim());
  } catch (err) {
    el("mig-status").textContent = String(err);
    return;
  }
  renderMigrations();
}

function renderMigrations() {
  const s = migState;
  if (!s) return;
  el("mig-status").textContent = s.error || "";

  const note = el("mig-dir-note");
  if (el("mig-dir").value.trim()) {
    note.textContent = "Manual directory. Save this connection in migration " +
      "to make the pairing permanent.";
  } else if (s.dir) {
    note.textContent = "Using " + s.dir + " (from migration's saved connections).";
  } else {
    note.textContent = "No saved migration connection matches this database " +
      "URL. Enter the migrations directory above.";
  }

  const summary = el("mig-summary");
  summary.replaceChildren();
  if (s.dir && !s.error) {
    const meta = document.createElement("span");
    meta.className = "idx-meta";
    const applied = document.createElement("span");
    applied.textContent = s.tableExists
      ? "version " + s.applied + " applied"
      : "no schema_migrations table yet (created by the first run)";
    const pending = document.createElement("span");
    pending.textContent = s.pending.length + " pending";
    if (s.pending.length > 0) pending.classList.add("warn-text");
    meta.append(applied, pending);
    summary.append(meta);
  }

  const box = el("mig-pending");
  box.replaceChildren();
  if (s.pending.length === 0) {
    const none = document.createElement("p");
    none.className = "hint";
    none.textContent = "Nothing pending.";
    box.append(none);
  }
  for (const f of s.pending) {
    const cell = document.createElement("div");
    cell.className = "cell sql-text";
    cell.textContent = f;
    box.append(cell);
  }

  const driftBox = el("mig-drift");
  driftBox.replaceChildren();
  const d = s.drift;
  if (d && d.error) {
    const err = document.createElement("p");
    err.className = "hint";
    err.textContent = d.error;
    driftBox.append(err);
  }
  if (d && !d.error && d.changes.length === 0) {
    const ok = document.createElement("p");
    ok.className = "hint";
    ok.textContent = "No drift against snapshot " + d.snapVersion + ".";
    driftBox.append(ok);
  }
  if (d && !d.error) {
    for (const c of d.changes) {
      const cell = document.createElement("div");
      cell.className = "cell sql-text";
      cell.textContent = c;
      driftBox.append(cell);
    }
  }

  const ready = s.dir && !s.error;
  el("mig-run").disabled = !ready || s.pending.length === 0;
  el("mig-revert").disabled = !ready || !s.tableExists || s.applied === 0;
  el("mig-capture").disabled = !ready || !d || !!d.error || d.changes.length === 0;
}

function showMigResult(text) {
  el("mig-result").textContent = text;
}

// migAskConfirm fetches the exact SQL the action would run or write and puts
// it on screen with a confirmation that names the database — the destructive
// action is never the default-looking button.
async function migAskConfirm(action, label) {
  showMigResult("");
  let p = null;
  try {
    p = await window.migrationPreview(migDir(), action,
      el("mig-capture-name").value.trim());
  } catch (err) {
    showMigResult(String(err));
    return;
  }
  if (p.error) {
    showMigResult(p.error);
    return;
  }
  if (p.files.length === 0) {
    showMigResult(p.note || "nothing to do");
    return;
  }

  migAction = action;
  const conn = state.connections[state.active];
  const where = conn ? conn.title : "the connected database";
  el("mig-confirm-text").textContent = label + " on " + where +
    "? This is exactly what will run:";

  const files = el("mig-confirm-files");
  files.replaceChildren();
  for (const f of p.files) {
    const name = document.createElement("div");
    name.className = "top-label";
    name.textContent = f.name;
    const sql = document.createElement("div");
    sql.className = "sql-text";
    sql.textContent = f.def;
    files.append(name, sql);
  }
  el("mig-go").textContent = label;
  el("mig-confirm").hidden = false;
}

async function migConfirm() {
  el("mig-go").disabled = true;
  let out = null;
  try {
    out = await window.migrationApply(migDir(), migAction,
      el("mig-capture-name").value.trim());
  } catch (err) {
    out = { error: String(err) };
  }
  el("mig-go").disabled = false;
  el("mig-confirm").hidden = true;
  if (out.status) migState = out.status;
  showMigResult(out.error || out.message || "");
  renderMigrations();
}

el("mig-refresh").addEventListener("click", loadMigrations);
el("mig-dir").addEventListener("change", loadMigrations);
el("mig-run").addEventListener("click", () => migAskConfirm("up", "Run pending"));
el("mig-revert").addEventListener("click", () => migAskConfirm("down", "Revert last"));
el("mig-capture").addEventListener("click", () => migAskConfirm("capture", "Capture drift"));
el("mig-cancel").addEventListener("click", () => {
  el("mig-confirm").hidden = true;
  migAction = null;
});
el("mig-go").addEventListener("click", migConfirm);

el("ext-badge").addEventListener("click", () => {
  el("ext-tip").hidden = !el("ext-tip").hidden;
});
el("ext-close").addEventListener("click", () => {
  el("ext-tip").hidden = true;
});
el("ext-copy").addEventListener("click", async () => {
  await navigator.clipboard.writeText(el("ext-cmds").textContent);
});

setInterval(refreshDashboard, 2000);

el("cancel").addEventListener("click", closeForm);
el("add").addEventListener("click", () => openForm(-1));

el("delete").addEventListener("click", () => {
  el("confirm-text").textContent =
    'Remove "' + state.connections[editingIndex].title +
    '" from the config file?';
  el("confirm").hidden = false;
});
el("confirm-cancel").addEventListener("click", () => {
  el("confirm").hidden = true;
});
el("confirm-remove").addEventListener("click", () => {
  removeConnection(editingIndex);
});

// --- Sidebar ---

for (const b of document.querySelectorAll(".nav-item[data-screen]")) {
  const label = b.textContent;
  b.replaceChildren(icon(NAV_ICONS[b.dataset.screen]));
  const span = document.createElement("span");
  span.textContent = label;
  b.append(span);
  b.title = label;
  b.addEventListener("click", () => goTo(b.dataset.screen));
}

const collapseBtn = el("nav-collapse");
collapseBtn.replaceChildren(icon("list"));
collapseBtn.addEventListener("click", () => {
  navCollapsed = !navCollapsed;
  localStorage.setItem(NAV_COLLAPSED_KEY, navCollapsed ? "1" : "0");
  render();
});

// A narrow window collapses the rail on its own and restores the user's
// choice when there is room again: their preference is remembered, not
// overwritten by a resize.
const NARROW = window.matchMedia("(max-width: 60rem)");

function applyNarrow() {
  const forced = NARROW.matches;
  document.body.classList.toggle("nav-forced-collapsed", forced);
  collapseBtn.disabled = forced;
  document.body.classList.toggle("nav-collapsed", forced || navCollapsed);
  collapseBtn.setAttribute("aria-expanded",
    String(!(forced || navCollapsed)));
}

NARROW.addEventListener("change", applyNarrow);

// --- Sessions screen ---

let sessions = null;
// sessConfirming: "cancel:<pid>" or "terminate:<pid>" under confirmation.
let sessConfirming = null;

async function loadSessions() {
  el("sess-status").textContent = "Loading sessions...";
  try {
    sessions = await window.sessionList();
  } catch (err) {
    el("sess-status").textContent = String(err);
    return;
  }
  renderSessions();
}

function renderSessions() {
  if (!sessions) return;
  el("sess-status").textContent = sessions.error || "";

  const activeOnly = el("sess-active-only").checked;
  const list = activeOnly
    ? sessions.sessions.filter((s) => s.state === "active")
    : sessions.sessions;

  const box = el("sess-list");
  box.replaceChildren();
  if (list.length === 0) {
    const note = document.createElement("p");
    note.className = "hint";
    note.textContent = activeOnly
      ? "No session is running a query right now."
      : "No client sessions.";
    box.append(note);
    return;
  }

  for (const s of list) {
    const cell = document.createElement("div");
    cell.className = "cell";

    const title = document.createElement("div");
    title.className = "conn-title";
    title.textContent = "pid " + s.pid + " · " + (s.state || "unknown");
    cell.append(title);

    const meta = document.createElement("div");
    meta.className = "idx-meta";
    if (s.state === "active") {
      const run = document.createElement("span");
      run.textContent = "running for " + s.querySeconds.toFixed(1) + "s";
      meta.append(run);
    } else {
      const idle = document.createElement("span");
      idle.textContent = s.state + " for " + s.stateSeconds.toFixed(1) + "s";
      if (s.state.startsWith("idle in transaction")) {
        idle.classList.add("warn-text");
      }
      meta.append(idle);
    }
    if (s.blocked) {
      const blocked = document.createElement("span");
      blocked.className = "warn-text";
      blocked.textContent = "blocked by another session";
      meta.append(blocked);
    }
    if (s.waitEvent) {
      const w = document.createElement("span");
      w.textContent = s.waitEvent;
      meta.append(w);
    }
    const who = document.createElement("span");
    who.textContent = [s.user, s.app, s.host, s.database]
      .filter(Boolean).join(" · ");
    meta.append(who);
    cell.append(meta);

    if (s.query) {
      const q = document.createElement("div");
      q.className = "sql-text";
      q.textContent = s.query;
      cell.append(q);
    }

    cell.append(sessionActions(s));
    box.append(cell);
  }
}

function sessionActions(s) {
  const key = sessConfirming ? sessConfirming.split(":") : null;
  if (key && Number(key[1]) === s.pid) {
    const terminate = key[0] === "terminate";
    const wrap = document.createElement("div");
    const q = document.createElement("div");
    q.textContent = terminate
      ? "Terminate connection " + s.pid + "? Its transaction is rolled back " +
        "and the client is disconnected."
      : "Cancel the running query of pid " + s.pid + "? The transaction stays " +
        "open until the client commits or rolls back.";
    const stmt = document.createElement("div");
    stmt.className = "sql-text";
    const sql = terminate ? s.terminateSQL : s.cancelSQL;
    stmt.textContent = sql;
    const copy = button("Copy SQL", async () => {
      await navigator.clipboard.writeText(sql);
    });
    const act = button(terminate ? "Confirm termination" : "Confirm cancellation",
      () => runSessionSignal(s, terminate));
    act.classList.add("destructive");
    wrap.append(q, stmt, buttonRow(
      copy,
      button("Back", () => {
        sessConfirming = null;
        renderSessions();
      }),
      act,
    ));
    return wrap;
  }

  const cancelBtn = button("Cancel query...", () => {
    sessConfirming = "cancel:" + s.pid;
    renderSessions();
  });
  cancelBtn.classList.add("destructive");
  cancelBtn.disabled = s.state !== "active";
  const termBtn = button("Terminate...", () => {
    sessConfirming = "terminate:" + s.pid;
    renderSessions();
  });
  termBtn.classList.add("destructive");
  return buttonRow(cancelBtn, termBtn);
}

async function runSessionSignal(s, terminate) {
  sessConfirming = null;
  try {
    await window.signalBackend(s.pid, terminate);
  } catch (err) {
    el("sess-status").textContent = String(err);
    return;
  }
  loadSessions();
}

el("sess-refresh").addEventListener("click", loadSessions);
el("sess-active-only").addEventListener("change", renderSessions);

// Every screen's Refresh button carries the same icon, so the action reads the
// same wherever it appears.
for (const id of ["br-refresh", "idx-refresh", "maint-refresh", "mig-refresh",
  "sess-refresh"]) {
  el(id).prepend(icon("arrow-clockwise"));
}
el("test").prepend(icon("plug"));
el("add").prepend(icon("plus-lg"));
el("delete").prepend(icon("trash"));
el("ext-badge").append(icon("warn"));

(async () => {
  state = await window.configState();
  render();
  loadScreen(screen);
})();
