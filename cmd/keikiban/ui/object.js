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

// A new window always opens on Properties. Deliberately not remembered:
// localStorage is shared by every window of the app, so a sticky tab here
// would make one table's habit leak into the next table's window.
let tab = "properties";
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
// result is the last answer from the editor; the grid edits are relative to
// it. pending holds the cells changed since then.
let result = null;
// The editor is loaded once, when the Data tab is first opened: reloading it
// on every tab switch would throw away whatever the user had typed.
let editorReady = false;

// Cmd on macOS, Ctrl elsewhere. navigator.platform is deprecated but is what
// a webview reliably answers; the label is cosmetic either way.
const MOD_LABEL = /mac/i.test(navigator.platform) ? "Cmd+Enter" : "Ctrl+Enter";

// ensureEditor fills the editor the first time the data tab is shown, and runs
// that first statement. Auto-running is safe here precisely because nothing is
// hidden: the statement is on screen, bounded by LIMIT 100, and it is the same
// one the user would have typed. It happens once — after that the statement
// only runs when the user says so.
async function ensureEditor() {
  if (editorReady) return;
  editorReady = true;
  try {
    el("sql-text").value = await window.initialQuery();
  } catch (err) {
    el("sql-status").textContent = String(err);
    return;
  }
  await runSQL();
}

async function runSQL() {
  if (running) return;
  running = true;
  // A new answer replaces the rows the pending edits pointed at, so keeping
  // them would aim an UPDATE at whatever now sits in that position.
  pending.clear();
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
  // The last result stays around: the grid edits refer to its rows, columns
  // and target, and a stale copy would write to the wrong place.
  result = out;
  const box = el("sql-result");
  box.replaceChildren();
  el("grid-note").textContent = "";

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
  out.rows.forEach((r, rowIndex) => {
    const row = body.insertRow();
    r.forEach((v, colIndex) => {
      const td = row.insertCell();
      paintCell(td, v);
      if (!out.target.editable || !out.editable[colIndex]) return;
      td.classList.add("cell-editable");
      td.title = "Double-click to edit";
      td.addEventListener("dblclick", () => editCell(td, rowIndex, colIndex));
    });
  });
  box.append(table);

  const note = el("grid-note");
  if (out.target.editable) {
    note.textContent = "Editing " + out.target.schema + "." + out.target.table +
      " — double-click a cell. Save shows the exact UPDATE first.";
  }
  if (!out.target.editable) {
    note.textContent = out.target.reason
      ? "Read-only: " + out.target.reason + "."
      : "";
  }
  renderPending();
}

// paintCell keeps SQL NULL and the empty string distinguishable, on screen
// and in the data behind it.
function paintCell(td, v) {
  td.classList.remove("cell-null", "cell-changed");
  if (v === null) {
    td.textContent = "NULL";
    td.classList.add("cell-null");
    return;
  }
  td.textContent = v;
  if (v !== "") td.title = v;
}

/* Editing the grid ---------------------------------------------------------
   Which results can be written back is decided by the server and arrives with
   the result; the page never guesses. Edits are collected per row and saved
   one row at a time, showing the exact UPDATE first — the same anatomy as
   dropping an index, and for the same reason: the connected database may be
   production. */

// pending maps a row index to {column: value}, value null meaning SQL NULL.
let pending = new Map();

function renderPending() {
  const n = pending.size;
  el("grid-save").hidden = n === 0;
  el("grid-discard").hidden = n === 0;
  el("grid-save").textContent = n === 1
    ? "Save 1 row"
    : "Save " + n + " rows";
}

// editCell swaps the cell for an input. Escape cancels, Enter commits to the
// pending set (not to the database), and the NULL button is explicit because
// clearing the text means the empty string, which is a different value.
function editCell(td, rowIndex, colIndex) {
  if (td.querySelector("input")) return;

  const col = result.columns[colIndex].name;
  const current = pendingValue(rowIndex, col, result.rows[rowIndex][colIndex]);

  const input = document.createElement("input");
  input.type = "text";
  input.className = "cell-input";
  input.value = current === null ? "" : current;
  const nullBtn = document.createElement("button");
  nullBtn.type = "button";
  nullBtn.className = "cell-null-btn";
  nullBtn.textContent = "NULL";
  nullBtn.title = "Set this cell to SQL NULL";

  const close = (value) => {
    td.replaceChildren();
    if (value !== undefined) stageEdit(rowIndex, col, value);
    const staged = pendingValue(rowIndex, col, result.rows[rowIndex][colIndex]);
    paintCell(td, staged);
    if (rowChanged(rowIndex)) td.classList.add("cell-changed");
    renderPending();
  };

  input.addEventListener("keydown", (e) => {
    if (e.key === "Escape") close(undefined);
    if (e.key === "Enter") close(input.value);
  });
  input.addEventListener("blur", () => close(input.value));
  nullBtn.addEventListener("mousedown", (e) => {
    // mousedown, not click: blur would fire first and close the editor.
    e.preventDefault();
    close(null);
  });

  td.replaceChildren(input, nullBtn);
  input.focus();
  input.select();
}

function pendingValue(rowIndex, col, original) {
  const row = pending.get(rowIndex);
  if (row && Object.prototype.hasOwnProperty.call(row, col)) return row[col];
  return original;
}

function rowChanged(rowIndex) {
  return pending.has(rowIndex);
}

// stageEdit records a change, and forgets it again when the value returns to
// what the database gave: a row with nothing different is not a pending save.
function stageEdit(rowIndex, col, value) {
  const colIndex = result.columns.findIndex((c) => c.name === col);
  const original = result.rows[rowIndex][colIndex];
  const row = pending.get(rowIndex) || {};

  if (value === original) {
    delete row[col];
  } else {
    row[col] = value;
  }
  if (Object.keys(row).length === 0) {
    pending.delete(rowIndex);
    return;
  }
  pending.set(rowIndex, row);
}

// saveInFor builds one row's payload: the changed columns plus the primary
// key values as they were read, which is what finds the row again.
function saveInFor(rowIndex) {
  const row = pending.get(rowIndex);
  const edits = Object.keys(row).map((c) => ({ column: c, value: row[c] }));
  const key = result.target.pkColumns.map((c) => ({
    column: c,
    value: result.rows[rowIndex][result.columns.findIndex((x) => x.name === c)],
  }));
  return {
    schema: result.target.schema,
    table: result.target.table,
    edits,
    key,
  };
}

async function gridAskConfirm() {
  el("grid-result").textContent = "";
  const rows = [...pending.keys()].sort((a, b) => a - b);
  const previews = [];
  for (const rowIndex of rows) {
    let p = null;
    try {
      p = await window.gridPreview(saveInFor(rowIndex));
    } catch (err) {
      p = { error: String(err) };
    }
    if (p.error) {
      el("grid-result").textContent = p.error;
      return;
    }
    previews.push(p.sql);
  }

  el("grid-confirm-text").textContent = rows.length === 1
    ? "Save this row to " + result.target.schema + "." + result.target.table +
      "? This is exactly what will run:"
    : "Save " + rows.length + " rows to " + result.target.schema + "." +
      result.target.table + "? This is exactly what will run:";
  const box = el("grid-confirm-sql");
  box.replaceChildren();
  for (const sql of previews) {
    const pre = document.createElement("div");
    pre.className = "sql-text";
    pre.textContent = sql;
    box.append(pre);
  }
  el("grid-confirm").hidden = false;
}

async function gridConfirm() {
  el("grid-go").disabled = true;
  const rows = [...pending.keys()].sort((a, b) => a - b);
  let saved = 0;
  let failure = "";

  for (const rowIndex of rows) {
    let out = null;
    try {
      out = await window.gridSave(saveInFor(rowIndex));
    } catch (err) {
      out = { error: String(err) };
    }
    if (out.error) {
      failure = out.error;
      break;
    }
    saved++;
    pending.delete(rowIndex);
  }

  el("grid-go").disabled = false;
  el("grid-confirm").hidden = true;
  // Each row is its own statement, so a failure halfway leaves the earlier
  // rows saved. Saying how many went through beats a bare error.
  el("grid-result").textContent = failure
    ? "saved " + saved + " row(s), then stopped: " + failure
    : "saved " + saved + " row(s)";
  renderPending();
  if (saved > 0) runSQL();
}

function gridDiscard() {
  pending.clear();
  el("grid-result").textContent = "";
  renderResult(result);
}

el("sql-shortcut").textContent = MOD_LABEL;
el("sql-run").addEventListener("click", runSQL);
el("sql-cancel").addEventListener("click", cancelSQL);
el("grid-save").addEventListener("click", gridAskConfirm);
el("grid-discard").addEventListener("click", gridDiscard);
el("grid-cancel").addEventListener("click", () => {
  el("grid-confirm").hidden = true;
});
el("grid-go").addEventListener("click", gridConfirm);

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
    render();
    if (tab === "data") ensureEditor();
  });
}

el("obj-refresh").prepend(icon("arrow-clockwise"));
el("obj-refresh").addEventListener("click", loadObject);

render();
loadObject();
