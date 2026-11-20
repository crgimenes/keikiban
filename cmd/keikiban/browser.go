package main

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
)

// The object browser answers "what is in this database?". Everything here is
// read-only: it navigates and describes, it never changes anything.
//
// Two rules shape the design, both taken from what people complain about in
// DBeaver and pgAdmin:
//
//  1. Search runs on the SERVER, over the whole catalog, never over the nodes
//     that happen to be expanded. A filter that only finds what you already
//     found is the top complaint about DBeaver's navigator.
//  2. The tree is three levels deep at most (schema, group, object). Columns
//     and definitions live in the detail pane, not as further branches: depth
//     is the other half of that same complaint.

const (
	// browserLimit bounds one expansion. A schema with thousands of tables
	// must not freeze the window; the UI says how many were left out and the
	// search box is the way through.
	browserLimit = 300
	searchLimit  = 100
)

// Relation kinds, as pg_class.relkind. Partitioned tables sit with ordinary
// tables: the difference matters to the planner, not to someone looking for
// where the data lives.
var (
	kindTables    = []string{"r", "p"}
	kindViews     = []string{"v"}
	kindMatViews  = []string{"m"}
	kindSequences = []string{"S"}
)

// browserKinds maps the group node the UI asks for to the relkinds behind it.
var browserKinds = map[string][]string{
	"tables":    kindTables,
	"views":     kindViews,
	"matviews":  kindMatViews,
	"sequences": kindSequences,
}

// schemaNode is one schema plus how much sits inside it. The counts are shown
// on the group nodes so that expanding something expensive is a decision, not
// a surprise.
type schemaNode struct {
	Name      string `json:"name"`
	Tables    int    `json:"tables"`
	Views     int    `json:"views"`
	MatViews  int    `json:"matViews"`
	Sequences int    `json:"sequences"`
}

type browserTreeOut struct {
	Error   string       `json:"error,omitempty"`
	Schemas []schemaNode `json:"schemas"`
}

// objectNode is one relation in a group listing.
type objectNode struct {
	Schema    string `json:"schema"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	SizeBytes int64  `json:"sizeBytes"`
	Size      string `json:"size"`
	Comment   string `json:"comment"`
}

type objectListOut struct {
	Error   string       `json:"error,omitempty"`
	Objects []objectNode `json:"objects"`
	// Total is the true count in the schema, so a truncated list can say what
	// it is hiding instead of quietly ending.
	Total     int  `json:"total"`
	Truncated bool `json:"truncated"`
}

// columnInfo is one column as the detail pane shows it.
type columnInfo struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	NotNull bool   `json:"notNull"`
	Default string `json:"default"`
	PK      bool   `json:"pk"`
	// FK names the referenced relation, empty when the column is not part of
	// a foreign key.
	FK      string `json:"fk"`
	Comment string `json:"comment"`
}

// namedDef is a catalog object shown by its exact server-provided definition.
type namedDef struct {
	Name string `json:"name"`
	Def  string `json:"def"`
}

type sequenceInfo struct {
	Start     int64 `json:"start"`
	Min       int64 `json:"min"`
	Max       int64 `json:"max"`
	Increment int64 `json:"increment"`
	Cycle     bool  `json:"cycle"`
	LastValue int64 `json:"lastValue"`
	// Called is false while the sequence has never handed out a value, when
	// LastValue is the start rather than a value in use.
	Called bool `json:"called"`
}

// objectDetailOut describes one object. Definitions come straight from the
// server (pg_get_viewdef, pg_get_indexdef, pg_get_constraintdef), never
// reassembled here: a DDL that is subtly wrong is worse than no DDL, and this
// app does not show what it cannot vouch for.
type objectDetailOut struct {
	Error  string `json:"error,omitempty"`
	Schema string `json:"schema"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Size   string `json:"size"`
	// RowEstimate is the planner's estimate, -1 when the relation has never
	// been analyzed. It is an estimate and the UI says so.
	RowEstimate int64         `json:"rowEstimate"`
	Comment     string        `json:"comment"`
	Columns     []columnInfo  `json:"columns"`
	Constraints []namedDef    `json:"constraints"`
	Indexes     []namedDef    `json:"indexes"`
	ViewDef     string        `json:"viewDef"`
	Sequence    *sequenceInfo `json:"sequence,omitempty"`
}

type searchHit struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
}

type searchOut struct {
	Error     string      `json:"error,omitempty"`
	Hits      []searchHit `json:"hits"`
	Total     int         `json:"total"`
	Truncated bool        `json:"truncated"`
}

func emptyObjectList() objectListOut {
	return objectListOut{Objects: []objectNode{}}
}

func emptyDetail() objectDetailOut {
	return objectDetailOut{
		Columns:     []columnInfo{},
		Constraints: []namedDef{},
		Indexes:     []namedDef{},
	}
}

// browserConnect opens the short-lived connection every browser query uses.
// The browser is navigation, not monitoring: it has no long-running session
// of its own to keep alive.
func browserConnect(ctx context.Context, dbURL string) (*pgx.Conn, func(), error) {
	connCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	conn, err := pgx.Connect(connCtx, dbURL)
	cancel()
	if err != nil {
		return nil, nil, err
	}
	closer := func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), sampleTimeout)
		_ = conn.Close(closeCtx)
		closeCancel()
	}
	return conn, closer, nil
}

// sqlSchemas counts what each visible schema holds. System schemas are left
// out: they are noise for someone browsing their own database, and the ones
// that matter (pg_catalog) are reached through the reports, not here.
const sqlSchemas = `SELECT
		n.nspname,                                        -- 1
		count(*) FILTER (WHERE c.relkind IN ('r', 'p')),  -- 2
		count(*) FILTER (WHERE c.relkind = 'v'),          -- 3
		count(*) FILTER (WHERE c.relkind = 'm'),          -- 4
		count(*) FILTER (WHERE c.relkind = 'S')           -- 5
	FROM pg_namespace n
	LEFT JOIN pg_class c
		ON c.relnamespace = n.oid
		AND c.relkind IN ('r', 'p', 'v', 'm', 'S')
	WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
	AND n.nspname NOT LIKE 'pg\_toast%'
	AND n.nspname NOT LIKE 'pg\_temp%'
	AND n.nspname NOT LIKE 'pg\_toast\_temp%'
	AND has_schema_privilege(n.oid, 'USAGE')
	GROUP BY n.nspname
	ORDER BY n.nspname;`

// collectBrowserTree lists the schemas that form the root of the tree.
// Failure is data, never a blank panel.
func collectBrowserTree(ctx context.Context, dbURL string) browserTreeOut {
	out := browserTreeOut{Schemas: []schemaNode{}}

	conn, closer, err := browserConnect(ctx, dbURL)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer closer()

	qctx, cancel := context.WithTimeout(ctx, indexQueryTimeout)
	defer cancel()

	rows, err := conn.Query(qctx, sqlSchemas)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer rows.Close()

	for rows.Next() {
		var s schemaNode
		err = rows.Scan(
			&s.Name,      // 1
			&s.Tables,    // 2
			&s.Views,     // 3
			&s.MatViews,  // 4
			&s.Sequences, // 5
		)
		if err != nil {
			out.Error = err.Error()
			return out
		}
		out.Schemas = append(out.Schemas, s)
	}
	if rows.Err() != nil {
		out.Error = rows.Err().Error()
	}
	return out
}

// sqlObjects lists one group of one schema. count(*) OVER () is evaluated
// before LIMIT, so the true total travels with the truncated page.
const sqlObjects = `SELECT
		c.relname,                                         -- 1
		pg_total_relation_size(c.oid),                     -- 2
		pg_size_pretty(pg_total_relation_size(c.oid)),     -- 3
		COALESCE(obj_description(c.oid, 'pg_class'), ''),  -- 4
		c.relkind,                                         -- 5
		count(*) OVER ()                                   -- 6
	FROM pg_class c
	JOIN pg_namespace n ON n.oid = c.relnamespace
	WHERE n.nspname = $1        -- 1
	AND c.relkind = ANY($2)     -- 2
	ORDER BY c.relname
	LIMIT $3;` // 3

// collectObjects lists the objects of one group node.
func collectObjects(ctx context.Context, dbURL, schema, group string) objectListOut {
	out := emptyObjectList()

	kinds, ok := browserKinds[group]
	if !ok {
		out.Error = "unknown object group: " + group
		return out
	}

	conn, closer, err := browserConnect(ctx, dbURL)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer closer()

	qctx, cancel := context.WithTimeout(ctx, indexQueryTimeout)
	defer cancel()

	rows, err := conn.Query(
		qctx,
		sqlObjects,
		schema,         // 1
		kinds,          // 2
		browserLimit+1, // 3: one over the limit detects truncation
	)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer rows.Close()

	for rows.Next() {
		var o objectNode
		err = rows.Scan(
			&o.Name,      // 1
			&o.SizeBytes, // 2
			&o.Size,      // 3
			&o.Comment,   // 4
			&o.Kind,      // 5
			&out.Total,   // 6
		)
		if err != nil {
			out.Error = err.Error()
			return out
		}
		o.Schema = schema
		out.Objects = append(out.Objects, o)
	}
	if rows.Err() != nil {
		out.Error = rows.Err().Error()
		return out
	}

	if len(out.Objects) > browserLimit {
		out.Objects = out.Objects[:browserLimit]
		out.Truncated = true
	}
	return out
}

const sqlObjectHeader = `SELECT
		c.relkind,                                         -- 1
		pg_size_pretty(pg_total_relation_size(c.oid)),     -- 2
		c.reltuples::bigint,                               -- 3
		COALESCE(obj_description(c.oid, 'pg_class'), '')   -- 4
	FROM pg_class c
	JOIN pg_namespace n ON n.oid = c.relnamespace
	WHERE n.nspname = $1   -- 1
	AND c.relname = $2;` // 2

// sqlColumns describes the columns of one relation. Dropped columns leave
// their slot behind in pg_attribute and must stay hidden.
const sqlColumns = `SELECT
		a.attname,                                      -- 1
		format_type(a.atttypid, a.atttypmod),           -- 2
		a.attnotnull,                                   -- 3
		COALESCE(pg_get_expr(d.adbin, d.adrelid), ''),  -- 4
		EXISTS (SELECT 1 FROM pg_index i
			WHERE i.indrelid = a.attrelid
			AND i.indisprimary
			AND a.attnum = ANY(i.indkey)),              -- 5
		COALESCE((SELECT fn.nspname || '.' || fc.relname
			FROM pg_constraint con
			JOIN pg_class fc ON fc.oid = con.confrelid
			JOIN pg_namespace fn ON fn.oid = fc.relnamespace
			WHERE con.conrelid = a.attrelid
			AND con.contype = 'f'
			AND a.attnum = ANY(con.conkey)
			LIMIT 1), ''),                              -- 6
		COALESCE(col_description(a.attrelid, a.attnum), '') -- 7
	FROM pg_attribute a
	JOIN pg_class c ON c.oid = a.attrelid
	JOIN pg_namespace n ON n.oid = c.relnamespace
	LEFT JOIN pg_attrdef d
		ON d.adrelid = a.attrelid AND d.adnum = a.attnum
	WHERE n.nspname = $1     -- 1
	AND c.relname = $2       -- 2
	AND a.attnum > 0
	AND NOT a.attisdropped
	ORDER BY a.attnum;`

const sqlConstraints = `SELECT
		con.conname,                   -- 1
		pg_get_constraintdef(con.oid)  -- 2
	FROM pg_constraint con
	JOIN pg_class c ON c.oid = con.conrelid
	JOIN pg_namespace n ON n.oid = c.relnamespace
	WHERE n.nspname = $1   -- 1
	AND c.relname = $2     -- 2
	ORDER BY con.contype, con.conname;`

const sqlIndexDefs = `SELECT
		ci.relname,                     -- 1
		pg_get_indexdef(i.indexrelid)   -- 2
	FROM pg_index i
	JOIN pg_class ci ON ci.oid = i.indexrelid
	JOIN pg_class c ON c.oid = i.indrelid
	JOIN pg_namespace n ON n.oid = c.relnamespace
	WHERE n.nspname = $1   -- 1
	AND c.relname = $2     -- 2
	ORDER BY ci.relname;`

const sqlViewDef = `SELECT pg_get_viewdef(c.oid, true)
	FROM pg_class c
	JOIN pg_namespace n ON n.oid = c.relnamespace
	WHERE n.nspname = $1   -- 1
	AND c.relname = $2;` // 2

const sqlSequence = `SELECT
		s.start_value,   -- 1
		s.min_value,     -- 2
		s.max_value,     -- 3
		s.increment_by,  -- 4
		s.cycle,         -- 5
		COALESCE(s.last_value, s.start_value), -- 6
		s.last_value IS NOT NULL               -- 7
	FROM pg_sequences s
	WHERE s.schemaname = $1   -- 1
	AND s.sequencename = $2;` // 2

// collectObjectDetail describes one object: columns for anything with them,
// plus the exact catalog definitions of what hangs off it.
func collectObjectDetail(ctx context.Context, dbURL, schema, name string) objectDetailOut {
	out := emptyDetail()
	out.Schema = schema
	out.Name = name

	conn, closer, err := browserConnect(ctx, dbURL)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer closer()

	qctx, cancel := context.WithTimeout(ctx, indexQueryTimeout)
	defer cancel()

	err = conn.QueryRow(qctx, sqlObjectHeader, schema, name).Scan(
		&out.Kind,        // 1
		&out.Size,        // 2
		&out.RowEstimate, // 3
		&out.Comment,     // 4
	)
	if err == pgx.ErrNoRows {
		out.Error = "object not found: " + schema + "." + name
		return out
	}
	if err != nil {
		out.Error = err.Error()
		return out
	}

	if out.Kind == "S" {
		var seq sequenceInfo
		err = conn.QueryRow(qctx, sqlSequence, schema, name).Scan(
			&seq.Start,     // 1
			&seq.Min,       // 2
			&seq.Max,       // 3
			&seq.Increment, // 4
			&seq.Cycle,     // 5
			&seq.LastValue, // 6
			&seq.Called,    // 7
		)
		if err != nil {
			out.Error = err.Error()
			return out
		}
		out.Sequence = &seq
		return out
	}

	out.Columns, err = readColumns(qctx, conn, schema, name)
	if err != nil {
		out.Error = err.Error()
		return out
	}

	if out.Kind == "v" || out.Kind == "m" {
		err = conn.QueryRow(qctx, sqlViewDef, schema, name).Scan(&out.ViewDef)
		if err != nil {
			out.Error = err.Error()
			return out
		}
	}

	out.Constraints, err = readDefs(qctx, conn, sqlConstraints, schema, name)
	if err != nil {
		out.Error = err.Error()
		return out
	}

	out.Indexes, err = readDefs(qctx, conn, sqlIndexDefs, schema, name)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	return out
}

func readColumns(ctx context.Context, conn *pgx.Conn, schema, name string) ([]columnInfo, error) {
	cols := []columnInfo{}

	rows, err := conn.Query(ctx, sqlColumns, schema, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var c columnInfo
		err = rows.Scan(
			&c.Name,    // 1
			&c.Type,    // 2
			&c.NotNull, // 3
			&c.Default, // 4
			&c.PK,      // 5
			&c.FK,      // 6
			&c.Comment, // 7
		)
		if err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

// readDefs runs one of the name+definition queries; both have the same shape.
func readDefs(ctx context.Context, conn *pgx.Conn, query, schema, name string) ([]namedDef, error) {
	defs := []namedDef{}

	rows, err := conn.Query(ctx, query, schema, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var d namedDef
		err = rows.Scan(
			&d.Name, // 1
			&d.Def,  // 2
		)
		if err != nil {
			return nil, err
		}
		defs = append(defs, d)
	}
	return defs, rows.Err()
}

// likeEscape makes a search term literal. Without it, the underscore that
// almost every table name carries would match any character, so searching
// "user_id" would also hit "userxid" — surprising in exactly the box where
// people type identifiers.
func likeEscape(term string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`%`, `\%`,
		`_`, `\_`,
	)
	return r.Replace(term)
}

// sqlSearch finds objects by name across every visible schema, whatever the
// tree happens to have expanded.
const sqlSearch = `SELECT
		n.nspname,        -- 1
		c.relname,        -- 2
		c.relkind,        -- 3
		count(*) OVER ()  -- 4
	FROM pg_class c
	JOIN pg_namespace n ON n.oid = c.relnamespace
	WHERE c.relkind IN ('r', 'p', 'v', 'm', 'S')
	AND n.nspname NOT IN ('pg_catalog', 'information_schema')
	AND n.nspname NOT LIKE 'pg\_toast%'
	AND n.nspname NOT LIKE 'pg\_temp%'
	AND n.nspname NOT LIKE 'pg\_toast\_temp%'
	AND has_schema_privilege(n.oid, 'USAGE')
	AND c.relname ILIKE '%' || $1 || '%' ESCAPE '\'   -- 1
	ORDER BY c.relname, n.nspname
	LIMIT $2;` // 2

// searchObjects answers the search box. It queries the catalog directly, so a
// match is found whether or not its branch was ever opened.
func searchObjects(ctx context.Context, dbURL, term string) searchOut {
	out := searchOut{Hits: []searchHit{}}

	term = strings.TrimSpace(term)
	if term == "" {
		return out
	}

	conn, closer, err := browserConnect(ctx, dbURL)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer closer()

	qctx, cancel := context.WithTimeout(ctx, indexQueryTimeout)
	defer cancel()

	rows, err := conn.Query(
		qctx,
		sqlSearch,
		likeEscape(term), // 1
		searchLimit+1,    // 2
	)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer rows.Close()

	for rows.Next() {
		var h searchHit
		err = rows.Scan(
			&h.Schema,  // 1
			&h.Name,    // 2
			&h.Kind,    // 3
			&out.Total, // 4
		)
		if err != nil {
			out.Error = err.Error()
			return out
		}
		out.Hits = append(out.Hits, h)
	}
	if rows.Err() != nil {
		out.Error = rows.Err().Error()
		return out
	}

	if len(out.Hits) > searchLimit {
		out.Hits = out.Hits[:searchLimit]
		out.Truncated = true
	}
	return out
}
