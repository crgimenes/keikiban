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

// Icons from Bootstrap Icons (https://icons.getbootstrap.com), MIT, © The
// Bootstrap Authors. Only the handful we use, inlined; fill follows the text
// color, so both themes are covered.
const ICONS = {
  "plus-lg": '<path fill-rule="evenodd" d="M8 2a.5.5 0 0 1 .5.5v5h5a.5.5 0 0 1 0 1h-5v5a.5.5 0 0 1-1 0v-5h-5a.5.5 0 0 1 0-1h5v-5A.5.5 0 0 1 8 2"/>',
  pencil: '<path d="M12.146.146a.5.5 0 0 1 .708 0l3 3a.5.5 0 0 1 0 .708l-10 10a.5.5 0 0 1-.168.11l-5 2a.5.5 0 0 1-.65-.65l2-5a.5.5 0 0 1 .11-.168zM11.207 2.5 13.5 4.793 14.793 3.5 12.5 1.207zm1.586 3L10.5 3.207 4 9.707V10h.5a.5.5 0 0 1 .5.5v.5h.5a.5.5 0 0 1 .5.5v.5h.293zm-9.761 5.175-.106.106-1.528 3.821 3.821-1.528.106-.106A.5.5 0 0 1 5 12.5V12h-.5a.5.5 0 0 1-.5-.5V11h-.5a.5.5 0 0 1-.468-.325"/>',
  trash: '<path d="M5.5 5.5A.5.5 0 0 1 6 6v6a.5.5 0 0 1-1 0V6a.5.5 0 0 1 .5-.5m2.5 0a.5.5 0 0 1 .5.5v6a.5.5 0 0 1-1 0V6a.5.5 0 0 1 .5-.5m3 .5a.5.5 0 0 0-1 0v6a.5.5 0 0 0 1 0z"/><path d="M14.5 3a1 1 0 0 1-1 1H13v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V4h-.5a1 1 0 0 1-1-1V2a1 1 0 0 1 1-1H6a1 1 0 0 1 1-1h2a1 1 0 0 1 1 1h3.5a1 1 0 0 1 1 1zM4.118 4 4 4.059V13a1 1 0 0 0 1 1h6a1 1 0 0 0 1-1V4.059L11.882 4zM2.5 3h11V2h-11z"/>',
  plug: '<path d="M6 0a.5.5 0 0 1 .5.5V3h3V.5a.5.5 0 0 1 1 0V3h1a.5.5 0 0 1 .5.5v3A3.5 3.5 0 0 1 8.5 10c-.002.434-.01.845-.04 1.22-.041.514-.126 1.003-.317 1.424a2.08 2.08 0 0 1-.97 1.028C6.725 13.9 6.169 14 5.5 14c-.998 0-1.61.33-1.974.718A1.92 1.92 0 0 0 3 16H2c0-.616.232-1.367.797-1.968C3.374 13.42 4.261 13 5.5 13c.581 0 .962-.088 1.218-.219.241-.123.4-.3.514-.55.121-.266.193-.621.23-1.09.027-.34.035-.718.037-1.141A3.5 3.5 0 0 1 4 6.5v-3a.5.5 0 0 1 .5-.5h1V.5A.5.5 0 0 1 6 0M5 4v2.5A2.5 2.5 0 0 0 7.5 9h1A2.5 2.5 0 0 0 11 6.5V4z"/>',
  gear: '<path d="M8 4.754a3.246 3.246 0 1 0 0 6.492 3.246 3.246 0 0 0 0-6.492M5.754 8a2.246 2.246 0 1 1 4.492 0 2.246 2.246 0 0 1-4.492 0"/><path d="M9.796 1.343c-.527-1.79-3.065-1.79-3.592 0l-.094.319a.873.873 0 0 1-1.255.52l-.292-.16c-1.64-.892-3.433.902-2.54 2.541l.159.292a.873.873 0 0 1-.52 1.255l-.319.094c-1.79.527-1.79 3.065 0 3.592l.319.094a.873.873 0 0 1 .52 1.255l-.16.292c-.892 1.64.901 3.434 2.541 2.54l.292-.159a.873.873 0 0 1 1.255.52l.094.319c.527 1.79 3.065 1.79 3.592 0l.094-.319a.873.873 0 0 1 1.255-.52l.292.16c1.64.893 3.434-.902 2.54-2.541l-.159-.292a.873.873 0 0 1 .52-1.255l.319-.094c1.79-.527 1.79-3.065 0-3.592l-.319-.094a.873.873 0 0 1-.52-1.255l.16-.292c.893-1.64-.902-3.433-2.541-2.54l-.292.159a.873.873 0 0 1-1.255-.52zm-2.633.283c.246-.835 1.428-.835 1.674 0l.094.319a1.873 1.873 0 0 0 2.693 1.115l.291-.16c.764-.415 1.6.42 1.184 1.185l-.159.292a1.873 1.873 0 0 0 1.116 2.692l.318.094c.835.246.835 1.428 0 1.674l-.319.094a1.873 1.873 0 0 0-1.115 2.693l.16.291c.415.764-.42 1.6-1.185 1.184l-.291-.159a1.873 1.873 0 0 0-2.693 1.116l-.094.318c-.246.835-1.428.835-1.674 0l-.094-.319a1.873 1.873 0 0 0-2.692-1.115l-.292.16c-.764.415-1.6-.42-1.184-1.185l.159-.291A1.873 1.873 0 0 0 1.945 8.93l-.319-.094c-.835-.246-.835-1.428 0-1.674l.319-.094A1.873 1.873 0 0 0 3.06 4.377l-.16-.292c-.415-.764.42-1.6 1.185-1.184l.292.159a1.873 1.873 0 0 0 2.692-1.115z"/>',
  "arrow-left": '<path fill-rule="evenodd" d="M15 8a.5.5 0 0 0-.5-.5H2.707l3.147-3.146a.5.5 0 1 0-.708-.708l-4 4a.5.5 0 0 0 0 .708l4 4a.5.5 0 0 0 .708-.708L2.707 8.5H14.5A.5.5 0 0 0 15 8"/>',
  warn: '<path d="M8.982 1.566a1.13 1.13 0 0 0-1.96 0L.165 13.233c-.457.778.091 1.767.98 1.767h13.713c.889 0 1.438-.99.98-1.767zM8 5c.535 0 .954.462.9.995l-.35 3.507a.552.552 0 0 1-1.1 0L7.1 5.995A.905.905 0 0 1 8 5m.002 6a1 1 0 1 1 0 2 1 1 0 0 1 0-2"/>',
  "pause-fill": '<path d="M5.5 3.5A1.5 1.5 0 0 1 7 5v6a1.5 1.5 0 0 1-3 0V5a1.5 1.5 0 0 1 1.5-1.5m5 0A1.5 1.5 0 0 1 12 5v6a1.5 1.5 0 0 1-3 0V5a1.5 1.5 0 0 1 1.5-1.5"/>',
  speedometer: '<path d="M8 4a.5.5 0 0 1 .5.5V6a.5.5 0 0 1-1 0V4.5A.5.5 0 0 1 8 4M3.732 5.732a.5.5 0 0 1 .707 0l.915.914a.5.5 0 1 1-.708.708l-.914-.915a.5.5 0 0 1 0-.707M2 10a.5.5 0 0 1 .5-.5h1.586a.5.5 0 0 1 0 1H2.5A.5.5 0 0 1 2 10m9.5 0a.5.5 0 0 1 .5-.5h1.5a.5.5 0 0 1 0 1H12a.5.5 0 0 1-.5-.5m.754-4.246a.39.39 0 0 0-.527-.02L7.547 9.31a.91.91 0 1 0 1.302 1.258l3.434-4.297a.39.39 0 0 0-.029-.518z"/><path fill-rule="evenodd" d="M0 10a8 8 0 1 1 15.547 2.661c-.442 1.253-1.845 1.602-2.932 1.25C11.309 13.488 9.475 13 8 13c-1.474 0-3.31.488-4.615.911-1.087.352-2.49.003-2.932-1.25A8 8 0 0 1 0 10m8-7a7 7 0 0 0-6.603 9.329c.203.575.923.876 1.68.63C4.397 12.533 6.358 12 8 12s3.604.532 4.923.96c.757.245 1.477-.056 1.68-.631A7 7 0 0 0 8 3"/>',
  activity: '<path fill-rule="evenodd" d="M6 2a.5.5 0 0 1 .47.33L10 12.036l1.53-4.208A.5.5 0 0 1 12 7.5h3.5a.5.5 0 0 1 0 1h-3.15l-1.88 5.17a.5.5 0 0 1-.94 0L6 3.964 4.47 8.171A.5.5 0 0 1 4 8.5H.5a.5.5 0 0 1 0-1h3.15l1.88-5.17A.5.5 0 0 1 6 2"/>',
  diagram: '<path fill-rule="evenodd" d="M6 3.5A1.5 1.5 0 0 1 7.5 2h1A1.5 1.5 0 0 1 10 3.5v1A1.5 1.5 0 0 1 8.5 6v1H14a.5.5 0 0 1 .5.5v1a.5.5 0 0 1-1 0V8h-5v.5a.5.5 0 0 1-1 0V8h-5v.5a.5.5 0 0 1-1 0v-1A.5.5 0 0 1 2 7h5.5V6A1.5 1.5 0 0 1 6 4.5zM8.5 5a.5.5 0 0 0 .5-.5v-1a.5.5 0 0 0-.5-.5h-1a.5.5 0 0 0-.5.5v1a.5.5 0 0 0 .5.5zM0 11.5A1.5 1.5 0 0 1 1.5 10h1A1.5 1.5 0 0 1 4 11.5v1A1.5 1.5 0 0 1 2.5 14h-1A1.5 1.5 0 0 1 0 12.5zm1.5-.5a.5.5 0 0 0-.5.5v1a.5.5 0 0 0 .5.5h1a.5.5 0 0 0 .5-.5v-1a.5.5 0 0 0-.5-.5zm4.5.5A1.5 1.5 0 0 1 7.5 10h1a1.5 1.5 0 0 1 1.5 1.5v1A1.5 1.5 0 0 1 8.5 14h-1A1.5 1.5 0 0 1 6 12.5zm1.5-.5a.5.5 0 0 0-.5.5v1a.5.5 0 0 0 .5.5h1a.5.5 0 0 0 .5-.5v-1a.5.5 0 0 0-.5-.5zm4.5.5a1.5 1.5 0 0 1 1.5-1.5h1a1.5 1.5 0 0 1 1.5 1.5v1a1.5 1.5 0 0 1-1.5 1.5h-1a1.5 1.5 0 0 1-1.5-1.5zm1.5-.5a.5.5 0 0 0-.5.5v1a.5.5 0 0 0 .5.5h1a.5.5 0 0 0 .5-.5v-1a.5.5 0 0 0-.5-.5z"/>',
  wrench: '<path d="M16 4.5a4.5 4.5 0 0 1-1.703 3.526L13 5l2.959-1.11q.04.3.041.61"/><path d="M11.5 9c.653 0 1.273-.139 1.833-.39L12 5.5 11 3l3.826-1.53A4.5 4.5 0 0 0 7.29 6.092l-6.116 5.096a2.583 2.583 0 1 0 3.638 3.638L9.908 8.71A4.5 4.5 0 0 0 11.5 9m-1.292-4.361-.596.893.809-.27a.25.25 0 0 1 .287.377l-.596.893.809-.27.158.475-1.5.5a.25.25 0 0 1-.287-.376l.596-.893-.809.27a.25.25 0 0 1-.287-.377l.596-.893-.809.27-.158-.475 1.5-.5a.25.25 0 0 1 .287.376M3 14a1 1 0 1 1 0-2 1 1 0 0 1 0 2"/>',
  list: '<path fill-rule="evenodd" d="M2.5 12a.5.5 0 0 1 .5-.5h10a.5.5 0 0 1 0 1H3a.5.5 0 0 1-.5-.5m0-4a.5.5 0 0 1 .5-.5h10a.5.5 0 0 1 0 1H3a.5.5 0 0 1-.5-.5m0-4a.5.5 0 0 1 .5-.5h10a.5.5 0 0 1 0 1H3a.5.5 0 0 1-.5-.5"/>',
  "play-fill": '<path d="m11.596 8.697-6.363 3.692c-.54.313-1.233-.066-1.233-.697V4.308c0-.63.692-1.01 1.233-.696l6.363 3.692a.802.802 0 0 1 0 1.393"/>',
};

function icon(name) {
  const s = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  s.setAttribute("viewBox", "0 0 16 16");
  s.setAttribute("fill", "currentColor");
  s.setAttribute("aria-hidden", "true");
  s.classList.add("icon");
  s.innerHTML = ICONS[name];
  return s;
}

let state = { path: "", exists: false, error: "", connections: [] };
// editingIndex: null = form closed, -1 = adding, >= 0 = editing that entry.
// It only applies to the connections screen.
let editingIndex = null;

// One screen at a time, chosen in the sidebar. The dashboard is home.
const SCREEN_KEY = "keikiban.screen";
const SCREENS = ["dashboard", "sessions", "indexes", "maintenance", "connections"];
let screen = localStorage.getItem(SCREEN_KEY) || "dashboard";
if (!SCREENS.includes(screen)) screen = "dashboard";

const NAV_ICONS = {
  dashboard: "speedometer",
  sessions: "activity",
  indexes: "diagram",
  maintenance: "wrench",
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
}

function goTo(name) {
  screen = name;
  localStorage.setItem(SCREEN_KEY, name);
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
    "maintenance"];
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

el("test").prepend(icon("plug"));
el("add").prepend(icon("plus-lg"));
el("delete").prepend(icon("trash"));
el("ext-badge").append(icon("warn"));

(async () => {
  state = await window.configState();
  render();
  loadScreen(screen);
})();
