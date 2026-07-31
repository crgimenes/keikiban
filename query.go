package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// The SQL editor. There is exactly one, and every screen that shows data goes
// through it: the statement is always on screen, always editable, and always
// the one that ran. Nothing is hidden behind a button.
//
// Each run opens its own short-lived connection and closes it when the answer
// is in. That is deliberate: "idle in transaction" is a standing complaint
// about DBeaver, and an editor that leaves sessions parked in an open
// transaction would hold back vacuum and keep locks — exactly what this app's
// own Maintenance screen exists to denounce. The cost is that session state
// (SET, temp tables, a hand-written BEGIN) does not survive between runs.

const (
	// queryTimeout is the ceiling for one statement. It is generous because a
	// legitimate report can be slow; Cancel is the fast way out, and it works
	// because pgx cancels through the context for real.
	queryTimeout = 5 * time.Minute
	// queryRowLimit protects the window from a SELECT with no LIMIT. Reaching
	// it is reported, never silent.
	queryRowLimit = 2000
	// queryCellRunes bounds one cell so a single huge text or json value
	// cannot bloat the payload. Runes, not bytes: cutting mid-character would
	// corrupt the text.
	queryCellRunes = 4096
)

// queryColumn is one column of a result set.
type queryColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// queryOut is one run of the editor. A cell is a *string so that SQL NULL and
// the empty string stay distinguishable all the way to the screen: collapsing
// them is a documented DBeaver annoyance and a real source of wrong reads.
type queryOut struct {
	Error   string        `json:"error,omitempty"`
	SQL     string        `json:"sql"`
	Columns []queryColumn `json:"columns"`
	Rows    [][]*string   `json:"rows"`
	// Truncated says the row limit cut the answer short.
	Truncated bool `json:"truncated"`
	// ReturnsRows separates a SELECT from an UPDATE: the second reports how
	// many rows it touched instead of showing a grid.
	ReturnsRows bool    `json:"returnsRows"`
	Command     string  `json:"command"`
	Affected    int64   `json:"affected"`
	ElapsedMS   float64 `json:"elapsedMS"`
}

func emptyQueryOut() queryOut {
	return queryOut{
		Columns: []queryColumn{},
		Rows:    [][]*string{},
	}
}

// initialQuery is what the editor holds when a table's window opens: the
// statement the user would have written, already written, with modest bounds.
// Showing it beats running something invisible on their behalf.
func initialQuery(schema, name string) string {
	return "SELECT *\nFROM " + pgx.Identifier{schema, name}.Sanitize() +
		"\nLIMIT 100\nOFFSET 0;"
}

// runQuery executes exactly what the editor holds. Failure is data: the error
// the server gave, shown as it came.
func runQuery(ctx context.Context, dbURL, sql string) queryOut {
	out := emptyQueryOut()
	out.SQL = sql

	if strings.TrimSpace(sql) == "" {
		out.Error = "nothing to run"
		return out
	}

	connCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	conn, err := pgx.Connect(connCtx, dbURL)
	cancel()
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), sampleTimeout)
		_ = conn.Close(closeCtx)
		closeCancel()
	}()

	qctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	start := time.Now()
	rows, err := conn.Query(qctx, sql)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	out.ReturnsRows = len(fields) > 0
	for _, f := range fields {
		col := queryColumn{Name: f.Name, Type: "?"}
		t, ok := conn.TypeMap().TypeForOID(f.DataTypeOID)
		if ok {
			col.Type = t.Name
		}
		out.Columns = append(out.Columns, col)
	}

	for rows.Next() {
		if len(out.Rows) >= queryRowLimit {
			out.Truncated = true
			break
		}
		values, valErr := rows.Values()
		if valErr != nil {
			out.Error = valErr.Error()
			return out
		}
		row := make([]*string, 0, len(values))
		for _, v := range values {
			row = append(row, formatCell(v))
		}
		out.Rows = append(out.Rows, row)
	}

	// A truncated read leaves the query running; closing first keeps the
	// command tag and the error check honest.
	rows.Close()
	if rows.Err() != nil && !out.Truncated {
		out.Error = rows.Err().Error()
		return out
	}

	tag := rows.CommandTag()
	out.Command = tag.String()
	out.Affected = tag.RowsAffected()
	out.ElapsedMS = round2(float64(time.Since(start).Microseconds()) / 1000)
	debugf("event=query rows=%d truncated=%v ms=%.1f", len(out.Rows), out.Truncated, out.ElapsedMS)
	return out
}

// formatCell renders one value for the grid. nil stays nil so the page can
// tell NULL from an empty string.
func formatCell(v any) *string {
	if v == nil {
		return nil
	}

	s := ""
	switch t := v.(type) {
	case string:
		s = t
	case []byte:
		// Binary is shown the way psql shows it, not dumped raw into the page.
		s = "\\x" + hex.EncodeToString(t)
	case time.Time:
		s = t.Format(time.RFC3339Nano)
	default:
		s = fmt.Sprint(v)
	}

	r := []rune(s)
	if len(r) > queryCellRunes {
		s = string(r[:queryCellRunes]) + "..."
	}
	return &s
}
