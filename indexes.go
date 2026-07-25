package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	indexQueryTimeout = 10 * time.Second
	dropIndexTimeout  = 120 * time.Second
	unusedLimit       = 50
)

// indexEntry is one index in the health report.
type indexEntry struct {
	Schema     string `json:"schema"`
	Table      string `json:"table"`
	Name       string `json:"name"`
	SizeBytes  int64  `json:"sizeBytes"`
	Size       string `json:"size"`
	Scans      int64  `json:"scans"`
	Unique     bool   `json:"unique"`
	Definition string `json:"definition"`
	// CoveredBy, on a duplicate, names the wider index that makes this one
	// redundant.
	CoveredBy string `json:"coveredBy,omitempty"`
	// DropDDL is the exact statement the UI shows, copies, and (after
	// confirmation) executes. The app never runs anything it does not show.
	DropDDL string `json:"dropDDL"`
}

// tableScans is one table in the seq-scan offenders list.
type tableScans struct {
	Schema      string  `json:"schema"`
	Table       string  `json:"table"`
	SeqScans    int64   `json:"seqScans"`
	IdxScans    int64   `json:"idxScans"`
	IndexUsePct float64 `json:"indexUsePct"`
	LiveRows    int64   `json:"liveRows"`
}

// indexReport is the index-health snapshot for one database.
type indexReport struct {
	Error string `json:"error,omitempty"`
	// StatsReset reminds that every number here is cumulative since this
	// moment; young stats make "unused" claims weak.
	StatsReset       string       `json:"statsReset"`
	IndexCacheHitPct float64      `json:"indexCacheHitPct"`
	TableCacheHitPct float64      `json:"tableCacheHitPct"`
	Invalid          []indexEntry `json:"invalid"`
	Unused           []indexEntry `json:"unused"`
	UnusedTruncated  bool         `json:"unusedTruncated"`
	Duplicates       []indexEntry `json:"duplicates"`
	SeqScans         []tableScans `json:"seqScans"`
}

// emptyIndexReport keeps every collection non-nil: nil marshals to JSON null
// and a null breaks the UI's iteration.
func emptyIndexReport() indexReport {
	return indexReport{
		Invalid:    []indexEntry{},
		Unused:     []indexEntry{},
		Duplicates: []indexEntry{},
		SeqScans:   []tableScans{},
	}
}

func dropIndexDDL(schema, name string) string {
	return "DROP INDEX CONCURRENTLY " + pgx.Identifier{schema, name}.Sanitize() + ";"
}

// collectIndexReport connects and gathers the index-health report. Failure is
// data (report.Error), never a blank screen.
func collectIndexReport(ctx context.Context, dbURL string) indexReport {
	report := emptyIndexReport()

	connCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	conn, err := pgx.Connect(connCtx, dbURL)
	cancel()
	if err != nil {
		report.Error = err.Error()
		return report
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), sampleTimeout)
		_ = conn.Close(closeCtx)
		cancel()
	}()

	for _, step := range []func(context.Context, *pgx.Conn, *indexReport) error{
		collectStatsMeta,
		collectInvalid,
		collectUnused,
		collectDuplicates,
		collectSeqScans,
	} {
		err = step(ctx, conn, &report)
		if err != nil {
			report.Error = err.Error()
			return report
		}
	}
	return report
}

const sqlStatsMeta = `SELECT
		COALESCE((SELECT stats_reset::text FROM pg_stat_database
			WHERE datname = current_database()), ''),               -- 1
		(SELECT CASE WHEN COALESCE(sum(idx_blks_hit + idx_blks_read), 0) = 0 THEN 100
			ELSE round(100.0 * sum(idx_blks_hit) / sum(idx_blks_hit + idx_blks_read), 2)
			END FROM pg_statio_user_indexes),                       -- 2
		(SELECT CASE WHEN COALESCE(sum(heap_blks_hit + heap_blks_read), 0) = 0 THEN 100
			ELSE round(100.0 * sum(heap_blks_hit) / sum(heap_blks_hit + heap_blks_read), 2)
			END FROM pg_statio_user_tables);` // 3

func collectStatsMeta(ctx context.Context, conn *pgx.Conn, report *indexReport) error {
	qctx, cancel := context.WithTimeout(ctx, indexQueryTimeout)
	defer cancel()

	return conn.QueryRow(qctx, sqlStatsMeta).Scan(
		&report.StatsReset,       // 1
		&report.IndexCacheHitPct, // 2
		&report.TableCacheHitPct, // 3
	)
}

const sqlInvalid = `SELECT
		n.nspname,                          -- 1
		t.relname,                          -- 2
		c.relname,                          -- 3
		pg_relation_size(i.indexrelid),     -- 4
		pg_size_pretty(pg_relation_size(i.indexrelid)), -- 5
		pg_get_indexdef(i.indexrelid)       -- 6
	FROM pg_index i
	JOIN pg_class c ON c.oid = i.indexrelid
	JOIN pg_class t ON t.oid = i.indrelid
	JOIN pg_namespace n ON n.oid = c.relnamespace
	WHERE NOT i.indisvalid
	ORDER BY pg_relation_size(i.indexrelid) DESC;`

func collectInvalid(ctx context.Context, conn *pgx.Conn, report *indexReport) error {
	qctx, cancel := context.WithTimeout(ctx, indexQueryTimeout)
	defer cancel()

	rows, err := conn.Query(qctx, sqlInvalid)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var e indexEntry
		err = rows.Scan(
			&e.Schema,     // 1
			&e.Table,      // 2
			&e.Name,       // 3
			&e.SizeBytes,  // 4
			&e.Size,       // 5
			&e.Definition, // 6
		)
		if err != nil {
			return err
		}
		e.DropDDL = dropIndexDDL(e.Schema, e.Name)
		report.Invalid = append(report.Invalid, e)
	}
	return rows.Err()
}

const sqlUnused = `SELECT
		s.schemaname,                       -- 1
		s.relname,                          -- 2
		s.indexrelname,                     -- 3
		pg_relation_size(s.indexrelid),     -- 4
		pg_size_pretty(pg_relation_size(s.indexrelid)), -- 5
		s.idx_scan,                         -- 6
		i.indisunique,                      -- 7
		pg_get_indexdef(s.indexrelid)       -- 8
	FROM pg_stat_user_indexes s
	JOIN pg_index i ON i.indexrelid = s.indexrelid
	WHERE s.idx_scan = 0
	AND NOT i.indisprimary
	AND i.indisvalid
	ORDER BY pg_relation_size(s.indexrelid) DESC
	LIMIT $1;` // 1: limit+1 to detect truncation

func collectUnused(ctx context.Context, conn *pgx.Conn, report *indexReport) error {
	qctx, cancel := context.WithTimeout(ctx, indexQueryTimeout)
	defer cancel()

	rows, err := conn.Query(
		qctx,
		sqlUnused,
		unusedLimit+1, // 1
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var e indexEntry
		err = rows.Scan(
			&e.Schema,     // 1
			&e.Table,      // 2
			&e.Name,       // 3
			&e.SizeBytes,  // 4
			&e.Size,       // 5
			&e.Scans,      // 6
			&e.Unique,     // 7
			&e.Definition, // 8
		)
		if err != nil {
			return err
		}
		e.DropDDL = dropIndexDDL(e.Schema, e.Name)
		report.Unused = append(report.Unused, e)
	}
	if rows.Err() != nil {
		return rows.Err()
	}
	if len(report.Unused) > unusedLimit {
		report.Unused = report.Unused[:unusedLimit]
		report.UnusedTruncated = true
	}
	return nil
}

// sqlDuplicates finds index i whose column list equals, or is a leading
// prefix of, index j on the same table (plain btree-style column indexes
// only: no expressions, no predicates). Exact duplicates report once, the
// higher-oid one as droppable.
const sqlDuplicates = `SELECT
		n.nspname,                          -- 1
		t.relname,                          -- 2
		ci.relname,                         -- 3
		pg_relation_size(i.indexrelid),     -- 4
		pg_size_pretty(pg_relation_size(i.indexrelid)), -- 5
		si.idx_scan,                        -- 6
		pg_get_indexdef(i.indexrelid),      -- 7
		cj.relname                          -- 8
	FROM pg_index i
	JOIN pg_index j ON j.indrelid = i.indrelid AND j.indexrelid <> i.indexrelid
	JOIN pg_class ci ON ci.oid = i.indexrelid
	JOIN pg_class cj ON cj.oid = j.indexrelid
	JOIN pg_class t ON t.oid = i.indrelid
	JOIN pg_namespace n ON n.oid = t.relnamespace
	JOIN pg_stat_user_indexes si ON si.indexrelid = i.indexrelid
	WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
	AND i.indpred IS NULL AND j.indpred IS NULL
	AND i.indexprs IS NULL AND j.indexprs IS NULL
	AND NOT i.indisprimary
	AND NOT i.indisunique
	AND i.indisvalid AND j.indisvalid
	AND ((j.indkey::text = i.indkey::text AND i.indexrelid > j.indexrelid)
		OR j.indkey::text LIKE i.indkey::text || ' %')
	ORDER BY pg_relation_size(i.indexrelid) DESC;`

func collectDuplicates(ctx context.Context, conn *pgx.Conn, report *indexReport) error {
	qctx, cancel := context.WithTimeout(ctx, indexQueryTimeout)
	defer cancel()

	rows, err := conn.Query(qctx, sqlDuplicates)
	if err != nil {
		return err
	}
	defer rows.Close()

	// An index covered by several wider ones would repeat, once per coverer;
	// one entry per droppable index is enough.
	seen := map[string]bool{}
	for rows.Next() {
		var e indexEntry
		err = rows.Scan(
			&e.Schema,     // 1
			&e.Table,      // 2
			&e.Name,       // 3
			&e.SizeBytes,  // 4
			&e.Size,       // 5
			&e.Scans,      // 6
			&e.Definition, // 7
			&e.CoveredBy,  // 8
		)
		if err != nil {
			return err
		}
		key := e.Schema + "." + e.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		e.DropDDL = dropIndexDDL(e.Schema, e.Name)
		report.Duplicates = append(report.Duplicates, e)
	}
	return rows.Err()
}

const sqlSeqScans = `SELECT
		schemaname,                         -- 1
		relname,                            -- 2
		seq_scan,                           -- 3
		idx_scan,                           -- 4
		CASE WHEN seq_scan + idx_scan = 0 THEN 0
			ELSE round(100.0 * idx_scan / (seq_scan + idx_scan), 1)
			END,                            -- 5
		n_live_tup                          -- 6
	FROM pg_stat_user_tables
	WHERE n_live_tup > 10000
	AND seq_scan > 0
	ORDER BY seq_scan DESC
	LIMIT 20;`

func collectSeqScans(ctx context.Context, conn *pgx.Conn, report *indexReport) error {
	qctx, cancel := context.WithTimeout(ctx, indexQueryTimeout)
	defer cancel()

	rows, err := conn.Query(qctx, sqlSeqScans)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var t tableScans
		err = rows.Scan(
			&t.Schema,      // 1
			&t.Table,       // 2
			&t.SeqScans,    // 3
			&t.IdxScans,    // 4
			&t.IndexUsePct, // 5
			&t.LiveRows,    // 6
		)
		if err != nil {
			return err
		}
		report.SeqScans = append(report.SeqScans, t)
	}
	return rows.Err()
}

// dropIndex executes the same DDL the UI displayed. CONCURRENTLY avoids
// blocking writes; it can take a while on a big index, hence its own timeout.
func dropIndex(ctx context.Context, dbURL, schema, name string) error {
	connCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	conn, err := pgx.Connect(connCtx, dbURL)
	cancel()
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), sampleTimeout)
		_ = conn.Close(closeCtx)
		cancel()
	}()

	qctx, cancel := context.WithTimeout(ctx, dropIndexTimeout)
	defer cancel()
	_, err = conn.Exec(qctx, dropIndexDDL(schema, name))
	if err != nil {
		return fmt.Errorf("drop index %s.%s: %w", schema, name, err)
	}
	debugf("event=index_dropped schema=%s name=%s", schema, name)
	return nil
}
