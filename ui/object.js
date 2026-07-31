// The object window: one native window per database object, opened from the
// browser tree. Several can stay open side by side, each with its own tabs.
//
// The window knows which object it is: the Go side captured the schema and
// name when it created the window, so objectDetail() takes no arguments. The
// database, though, is resolved on every call rather than captured, so a
// window can never keep reading a connection the user has since left.
"use strict";

const el = (id) => document.getElementById(id);

const KIND_LABELS = {
  r: "table",
  p: "partitioned table",
  v: "view",
  m: "materialized view",
  S: "sequence",
};

const TAB_KEY = "keikiban.objectTab";
let tab = localStorage.getItem(TAB_KEY) || "properties";
let detail = null;

async function loadObject() {
  el("obj-status").textContent = "Describing...";
  try {
    detail = await window.objectDetail();
  } catch (err) {
    el("obj-status").textContent = String(err);
    return;
  }
  el("obj-status").textContent = detail.error || "";
  render();
}

function render() {
  el("obj-props").hidden = tab !== "properties";
  el("obj-data").hidden = tab !== "data";
  for (const b of document.querySelectorAll(".tab[data-tab]")) {
    b.setAttribute("aria-pressed", String(b.dataset.tab === tab));
  }
  renderProperties();
}

function metaRow(...parts) {
  const meta = document.createElement("div");
  meta.className = "br-meta";
  for (const p of parts) {
    if (!p) continue;
    const span = document.createElement("span");
    span.textContent = p;
    meta.append(span);
  }
  return meta;
}

function defList(title, defs, hint) {
  const box = document.createDocumentFragment();
  const h = document.createElement("h3");
  h.textContent = title;
  box.append(h);
  if (hint) {
    const note = document.createElement("p");
    note.className = "hint";
    note.textContent = hint;
    box.append(note);
  }
  if (defs.length === 0) {
    const none = document.createElement("p");
    none.className = "br-empty";
    none.textContent = "None.";
    box.append(none);
    return box;
  }
  for (const d of defs) {
    const cell = document.createElement("div");
    cell.className = "sql-text";
    cell.textContent = d.def;
    box.append(cell);
  }
  return box;
}

function sequenceBlock(s) {
  const box = document.createDocumentFragment();
  const h = document.createElement("h3");
  h.textContent = "Sequence";
  box.append(h);
  box.append(metaRow(
    "start " + s.start,
    "increment " + s.increment,
    "min " + s.min,
    "max " + s.max,
    s.cycle ? "cycles" : "does not cycle",
    s.called ? "last value " + s.lastValue : "never used",
  ));
  return box;
}

function columnsTable(columns) {
  const box = document.createDocumentFragment();
  const h = document.createElement("h3");
  h.textContent = "Columns";
  box.append(h);

  if (columns.length === 0) {
    const none = document.createElement("p");
    none.className = "br-empty";
    none.textContent = "None.";
    box.append(none);
    return box;
  }

  const table = document.createElement("table");
  table.className = "scans";
  const head = table.insertRow();
  for (const label of ["Column", "Type", "Null", "Default", "Key"]) {
    const th = document.createElement("th");
    th.textContent = label;
    head.append(th);
  }
  for (const c of columns) {
    const row = table.insertRow();
    let key = "";
    if (c.pk) key = "PK";
    if (c.fk) key = (key ? key + ", " : "") + "FK → " + c.fk;
    const cells = [
      c.name,
      c.type,
      c.notNull ? "not null" : "null",
      c.default,
      key,
    ];
    for (const text of cells) {
      row.insertCell().textContent = text;
    }
    if (c.comment) row.title = c.comment;
  }
  box.append(table);
  return box;
}

function renderProperties() {
  const box = el("obj-props");
  box.replaceChildren();
  if (!detail) return;

  if (detail.schema && detail.name) {
    el("obj-title").textContent = detail.schema + "." + detail.name;
  }
  if (detail.error) return;

  const d = detail;
  // reltuples is -1 until the relation is analyzed, and an estimate after
  // that; neither is a row count and the label never pretends otherwise.
  let rows = "never analyzed";
  if (d.rowEstimate >= 0) rows = "~" + d.rowEstimate + " rows (estimate)";
  box.append(metaRow(KIND_LABELS[d.kind] || d.kind, d.size, rows));

  if (d.comment) {
    const comment = document.createElement("p");
    comment.textContent = d.comment;
    box.append(comment);
  }

  if (d.sequence) {
    box.append(sequenceBlock(d.sequence));
    return;
  }

  box.append(columnsTable(d.columns));

  if (d.viewDef) {
    const h = document.createElement("h3");
    h.textContent = "Definition";
    const def = document.createElement("div");
    def.className = "sql-text";
    def.textContent = d.viewDef;
    box.append(h, def);
  }

  box.append(defList("Constraints", d.constraints));
  box.append(defList("Indexes", d.indexes,
    "Exactly as the server reports them; the Indexes screen judges whether " +
    "they earn their keep."));
}

/* SQL editor -------------------------------------------------------------
   One editor serves every place that shows data. It opens holding the
   statement for this object, so what runs is what the user can read and
   change — never something the app decided on its own. */

let running = false;
// The editor is loaded once, when the Data tab is first opened: reloading it
// on every tab switch would throw away whatever the user had typed.
let editorReady = false;

// Cmd on macOS, Ctrl elsewhere. navigator.platform is deprecated but is what
// a webview reliably answers; the label is cosmetic either way.
const MOD_LABEL = /mac/i.test(navigator.platform) ? "Cmd+Enter" : "Ctrl+Enter";

async function ensureEditor() {
  if (editorReady) return;
  editorReady = true;
  try {
    el("sql-text").value = await window.initialQuery();
  } catch (err) {
    el("sql-status").textContent = String(err);
  }
}

async function runSQL() {
  if (running) return;
  running = true;
  el("sql-run").disabled = true;
  el("sql-cancel").hidden = false;
  el("sql-status").textContent = "Running...";

  let out = null;
  try {
    out = await window.runQuery(el("sql-text").value);
  } catch (err) {
    out = { error: String(err) };
  }
  running = false;
  el("sql-run").disabled = false;
  el("sql-cancel").hidden = true;
  renderResult(out);
}

async function cancelSQL() {
  el("sql-status").textContent = "Cancelling...";
  try {
    await window.cancelQuery();
  } catch (err) {
    el("sql-status").textContent = String(err);
  }
}

function renderResult(out) {
  const box = el("sql-result");
  box.replaceChildren();

  if (out.error) {
    el("sql-status").textContent = "";
    const err = document.createElement("p");
    // The server's own wording, unedited: rewriting it would cost the user
    // the one string they can search for.
    err.textContent = out.error;
    box.append(err);
    return;
  }

  const parts = [out.elapsedMS + " ms"];
  if (out.returnsRows) {
    parts.push(out.rows.length + (out.rows.length === 1 ? " row" : " rows"));
    if (out.truncated) {
      parts.push("stopped at the " + out.rows.length +
        "-row ceiling; narrow the query to see the rest");
    }
  }
  if (!out.returnsRows) {
    parts.push(out.command + ", " + out.affected +
      (out.affected === 1 ? " row affected" : " rows affected"));
  }
  el("sql-status").textContent = parts.join(" · ");

  if (!out.returnsRows) return;

  if (out.rows.length === 0) {
    const none = document.createElement("p");
    none.className = "br-empty";
    none.textContent = "No rows.";
    box.append(none);
    return;
  }

  const table = document.createElement("table");
  table.className = "grid";
  const head = table.createTHead().insertRow();
  for (const c of out.columns) {
    const th = document.createElement("th");
    th.textContent = c.name;
    const type = document.createElement("span");
    type.className = "grid-type";
    type.textContent = " " + c.type;
    th.append(type);
    head.append(th);
  }
  const body = table.createTBody();
  for (const r of out.rows) {
    const row = body.insertRow();
    for (const v of r) {
      const td = row.insertCell();
      // null is SQL NULL; "" is a real empty string. They must not look alike.
      if (v === null) {
        td.textContent = "NULL";
        td.className = "cell-null";
        continue;
      }
      td.textContent = v;
      td.title = v;
    }
  }
  box.append(table);
}

el("sql-shortcut").textContent = MOD_LABEL;
el("sql-run").addEventListener("click", runSQL);
el("sql-cancel").addEventListener("click", cancelSQL);

// Enter belongs to the text: this is an editor, and a stray newline must never
// fire a statement at a database. Only the modifier chord runs it.
el("sql-text").addEventListener("keydown", (e) => {
  if (e.key !== "Enter") return;
  if (!e.metaKey && !e.ctrlKey) return;
  e.preventDefault();
  runSQL();
});

for (const b of document.querySelectorAll(".tab[data-tab]")) {
  b.addEventListener("click", () => {
    tab = b.dataset.tab;
    localStorage.setItem(TAB_KEY, tab);
    render();
    if (tab === "data") ensureEditor();
  });
}

el("obj-refresh").prepend(icon("arrow-clockwise"));
el("obj-refresh").addEventListener("click", loadObject);

render();
loadObject();
// Reopening on the Data tab must land on a usable editor, not an empty box.
if (tab === "data") ensureEditor();
