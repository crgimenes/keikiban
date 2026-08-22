package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Editing rows in the result grid.
//
// What makes a result editable is decided by the SERVER, not by parsing the
// statement: every column the server describes carries the OID of the table
// it came from and its attribute number. One table behind every real column
// plus its whole primary key present in the result means each row can be
// addressed unambiguously. A join, an aggregate or a projection that dropped
// the key simply is not editable, and the grid says which of those it is.
//
// This is the part dbv could not do — it browses one table at a time, so it
// never has to ask the question. Two of its habits are deliberately not
// copied: it resolves the primary key by table_name alone (ambiguous across
// schemas) and quotes identifiers with fmt.Sprintf.

// gridColumn adds the origin of a result column to what the grid already
// shows. Computed columns (count(*), upper(x)) have no origin and stay
// read-only.
type gridColumn struct {
	Editable bool `json:"editable"`
	// attNum is the column's position in its base table, needed to match the
	// primary key. Not sent to the page: it means nothing there.
	attNum int
}

// gridTarget is the table a result set can be written back to.
type gridTarget struct {
	Editable bool   `json:"editable"`
	Schema   string `json:"schema,omitempty"`
	Table    string `json:"table,omitempty"`
	// PKColumns are the result-set column names that identify a row.
	PKColumns []string `json:"pkColumns,omitempty"`
	// Reason explains a non-editable result in the user's terms. Empty when
	// the result is editable.
	Reason string `json:"reason,omitempty"`
	// declared maps a result column name to the type the table declares for
	// it, used to cast the edited text back. Server-provided (format_type),
	// never guessed.
	declared map[string]string
}

const sqlRelationInfo = `SELECT
		n.nspname,                                            -- 1
		c.relname,                                            -- 2
		c.relkind,                                            -- 3
		COALESCE(string_to_array(i.indkey::text, ' ')::int2[], '{}') -- 4
	FROM pg_class c
	JOIN pg_namespace n ON n.oid = c.relnamespace
	LEFT JOIN pg_index i ON i.indrelid = c.oid AND i.indisprimary
	WHERE c.oid = $1;` // 1

const sqlRelationTypes = `SELECT
		a.attnum,                              -- 1
		format_type(a.atttypid, a.atttypmod)   -- 2
	FROM pg_attribute a
	WHERE a.attrelid = $1   -- 1
	AND a.attnum > 0
	AND NOT a.attisdropped;`

// detectGridTarget answers whether this result set can be written back, and
// says why not when it cannot. Failure to work it out is never fatal: the
// grid falls back to read-only.
func detectGridTarget(ctx context.Context, conn *pgx.Conn, fields []pgconn.FieldDescription, cols []queryColumn) (gridTarget, []gridColumn) {
	out := gridTarget{}
	gcols := make([]gridColumn, len(fields))

	// One table behind every real column, or nothing to write back to.
	oid := uint32(0)
	real := 0
	for i, f := range fields {
		gcols[i].attNum = int(f.TableAttributeNumber)
		if f.TableOID == 0 {
			continue
		}
		real++
		if oid == 0 {
			oid = f.TableOID
		}
		if f.TableOID != oid {
			out.Reason = "the result mixes columns from more than one table"
			return out, gcols
		}
	}
	if real == 0 {
		out.Reason = "these columns are computed, not stored in a table"
		return out, gcols
	}

	relkind := ""
	pkAttNums := []int16{}
	err := conn.QueryRow(ctx, sqlRelationInfo, oid).Scan(
		&out.Schema, // 1
		&out.Table,  // 2
		&relkind,    // 3
		&pkAttNums,  // 4
	)
	if err != nil {
		out.Reason = "could not identify the table behind this result: " + err.Error()
		return out, gcols
	}

	// Views and materialized views need rules or triggers to accept a write;
	// promising an edit the server would refuse is worse than saying no.
	if relkind != "r" && relkind != "p" {
		out.Reason = "only ordinary tables can be edited here"
		return out, gcols
	}
	if len(pkAttNums) == 0 {
		out.Reason = out.Schema + "." + out.Table + " has no primary key, so a row cannot be addressed"
		return out, gcols
	}

	// Every primary key column has to be in the result, or there is no way to
	// say which row an edit belongs to.
	byAttNum := map[int]string{}
	for i, f := range fields {
		if f.TableOID == oid && f.TableAttributeNumber > 0 {
			byAttNum[int(f.TableAttributeNumber)] = cols[i].Name
		}
	}
	for _, want := range pkAttNums {
		name, ok := byAttNum[int(want)]
		if !ok {
			out.Reason = "add the primary key of " + out.Schema + "." + out.Table +
				" to the query to edit these rows"
			return out, gcols
		}
		out.PKColumns = append(out.PKColumns, name)
	}

	out.declared, err = relationTypes(ctx, conn, oid, fields, cols)
	if err != nil {
		out.Reason = "could not read the column types: " + err.Error()
		out.PKColumns = nil
		return out, gcols
	}

	for i, f := range fields {
		gcols[i].Editable = f.TableOID == oid && f.TableAttributeNumber > 0
	}
	out.Editable = true
	return out, gcols
}

// relationTypes maps each result column to the type its table declares, as
// the server spells it (format_type). The edited text is cast to exactly that
// on the way back, so the server does the conversion — dbv parses timestamps
// by hand, which only covers the types someone remembered.
func relationTypes(ctx context.Context, conn *pgx.Conn, oid uint32, fields []pgconn.FieldDescription, cols []queryColumn) (map[string]string, error) {
	rows, err := conn.Query(ctx, sqlRelationTypes, oid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byAttNum := map[int]string{}
	for rows.Next() {
		attNum := 0
		typeName := ""
		err = rows.Scan(
			&attNum,   // 1
			&typeName, // 2
		)
		if err != nil {
			return nil, err
		}
		byAttNum[attNum] = typeName
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}

	declared := map[string]string{}
	for i, f := range fields {
		if f.TableOID != oid || f.TableAttributeNumber == 0 {
			continue
		}
		t, ok := byAttNum[int(f.TableAttributeNumber)]
		if ok {
			declared[cols[i].Name] = t
		}
	}
	return declared, nil
}

// gridEdit is one column of one row the user changed. Value is nil for SQL
// NULL, which the grid offers explicitly: clearing the text yields an empty
// string, and the two are not the same value.
type gridEdit struct {
	Column string  `json:"column"`
	Value  *string `json:"value"`
}

// gridSaveIn is one row's worth of changes plus the key that finds it.
type gridSaveIn struct {
	Schema string     `json:"schema"`
	Table  string     `json:"table"`
	Edits  []gridEdit `json:"edits"`
	// Key carries the primary key values as the grid received them, so the
	// WHERE matches the row the user was looking at.
	Key []gridEdit `json:"key"`
}

type gridSaveOut struct {
	Error string `json:"error,omitempty"`
	SQL   string `json:"sql"`
	// Affected is how many rows the statement touched. Anything but 1 is
	// reported as an error: the grid edits one identified row.
	Affected int64 `json:"affected"`
}

// buildRowUpdate writes the statement for one row. Identifiers go through
// pgx.Identifier so a table or column named with a quote cannot break out,
// and every value travels as a parameter cast to the column's declared type:
// the server converts, this app never guesses per type.
func buildRowUpdate(in gridSaveIn, declared map[string]string) (string, []any, error) {
	if len(in.Edits) == 0 {
		return "", nil, fmt.Errorf("nothing changed")
	}
	if len(in.Key) == 0 {
		return "", nil, fmt.Errorf("no primary key values to find the row")
	}

	args := []any{}
	sets := make([]string, 0, len(in.Edits))
	for _, e := range in.Edits {
		cast, ok := declared[e.Column]
		if !ok {
			return "", nil, fmt.Errorf("unknown column %q", e.Column)
		}
		args = append(args, valueArg(e.Value))
		sets = append(sets, fmt.Sprintf("%s = $%d::%s",
			pgx.Identifier{e.Column}.Sanitize(), len(args), cast))
	}

	wheres := make([]string, 0, len(in.Key))
	for _, k := range in.Key {
		cast, ok := declared[k.Column]
		if !ok {
			return "", nil, fmt.Errorf("unknown key column %q", k.Column)
		}
		// A NULL key would match nothing with "=", and a row identified by a
		// NULL primary key cannot exist anyway.
		if k.Value == nil {
			return "", nil, fmt.Errorf("key column %q is NULL", k.Column)
		}
		args = append(args, *k.Value)
		wheres = append(wheres, fmt.Sprintf("%s = $%d::%s",
			pgx.Identifier{k.Column}.Sanitize(), len(args), cast))
	}

	sql := "UPDATE " + pgx.Identifier{in.Schema, in.Table}.Sanitize() +
		"\nSET " + strings.Join(sets, ",\n    ") +
		"\nWHERE " + strings.Join(wheres, "\n  AND ") + ";"
	return sql, args, nil
}

// valueArg keeps SQL NULL and the empty string apart all the way to the
// server: a nil pointer becomes NULL, "" stays an empty string.
func valueArg(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}

// previewRowUpdate returns the statement the grid will run, without running
// it. Every write in this app shows its SQL first.
func previewRowUpdate(ctx context.Context, dbURL string, in gridSaveIn) gridSaveOut {
	out := gridSaveOut{}

	conn, closer, err := browserConnect(ctx, dbURL)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer closer()

	qctx, cancel := context.WithTimeout(ctx, indexQueryTimeout)
	defer cancel()

	declared, err := declaredTypesFor(qctx, conn, in.Schema, in.Table)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	sql, _, err := buildRowUpdate(in, declared)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.SQL = sql
	return out
}

// saveRowUpdate runs what the preview showed. It refuses to leave the
// database in a state nobody asked for: the statement runs inside a
// transaction and is rolled back unless it touched exactly one row.
func saveRowUpdate(ctx context.Context, dbURL string, in gridSaveIn) gridSaveOut {
	out := gridSaveOut{}

	conn, closer, err := browserConnect(ctx, dbURL)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer closer()

	qctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	declared, err := declaredTypesFor(qctx, conn, in.Schema, in.Table)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	sql, args, err := buildRowUpdate(in, declared)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.SQL = sql

	tx, err := conn.Begin(qctx)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer func() { _ = tx.Rollback(qctx) }()

	tag, err := tx.Exec(qctx, sql, args...)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Affected = tag.RowsAffected()

	// Zero means the row moved or vanished since it was read; more than one
	// means the key did not identify a single row. Neither is what the user
	// asked for, so neither is committed.
	if out.Affected != 1 {
		out.Error = fmt.Sprintf("statement would affect %d rows, expected exactly 1; nothing was saved", out.Affected)
		return out
	}

	err = tx.Commit(qctx)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	debugf("event=grid_update table=%s.%s columns=%d", in.Schema, in.Table, len(in.Edits))
	return out
}

const sqlDeclaredTypes = `SELECT
		a.attname,                            -- 1
		format_type(a.atttypid, a.atttypmod)  -- 2
	FROM pg_attribute a
	JOIN pg_class c ON c.oid = a.attrelid
	JOIN pg_namespace n ON n.oid = c.relnamespace
	WHERE n.nspname = $1   -- 1
	AND c.relname = $2     -- 2
	AND a.attnum > 0
	AND NOT a.attisdropped;`

// declaredTypesFor re-reads the column types at save time rather than
// trusting what the page sends back: the page could be stale, and the cast
// decides how the text is interpreted.
func declaredTypesFor(ctx context.Context, conn *pgx.Conn, schema, table string) (map[string]string, error) {
	rows, err := conn.Query(ctx, sqlDeclaredTypes, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	declared := map[string]string{}
	for rows.Next() {
		name := ""
		typeName := ""
		err = rows.Scan(
			&name,     // 1
			&typeName, // 2
		)
		if err != nil {
			return nil, err
		}
		declared[name] = typeName
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	if len(declared) == 0 {
		return nil, fmt.Errorf("table %s.%s not found", schema, table)
	}
	return declared, nil
}
