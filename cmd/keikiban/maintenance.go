package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

const vacuumTimeout = 30 * time.Minute

// tableAge is one table's distance from transaction ID wraparound.
type tableAge struct {
	Schema string  `json:"schema"`
	Table  string  `json:"table"`
	Age    int64   `json:"age"`
	Pct    float64 `json:"pct"`
	Size   string  `json:"size"`
}

// tableVacuum is one table carrying dead rows.
type tableVacuum struct {
	Schema string `json:"schema"`
	Table  string `json:"table"`
	// LiveRows and DeadRows are estimates kept by the stats collector.
	LiveRows int64   `json:"liveRows"`
	DeadRows int64   `json:"deadRows"`
	DeadPct  float64 `json:"deadPct"`
	Size     string  `json:"size"`
	// VacuumAgo and AnalyzeAgo are seconds since the last (auto)vacuum and
	// (auto)analyze; -1 means it never happened.
	VacuumAgo  float64 `json:"vacuumAgo"`
	AnalyzeAgo float64 `json:"analyzeAgo"`
	VacuumSQL  string  `json:"vacuumSQL"`
}

// longTx is a transaction open long enough to hold back vacuum and keep locks.
type longTx struct {
	PID          int     `json:"pid"`
	User         string  `json:"user"`
	App          string  `json:"app"`
	State        string  `json:"state"`
	XactSeconds  float64 `json:"xactSeconds"`
	StateSeconds float64 `json:"stateSeconds"`
	Query        string  `json:"query"`
	CancelSQL    string  `json:"cancelSQL"`
	TerminateSQL string  `json:"terminateSQL"`
}

// seqUsage is one sequence and how much of its range is spent.
type seqUsage struct {
	Schema    string  `json:"schema"`
	Name      string  `json:"name"`
	LastValue int64   `json:"lastValue"`
	MaxValue  int64   `json:"maxValue"`
	Pct       float64 `json:"pct"`
}

// maintenanceReport is the health snapshot behind the maintenance screen.
type maintenanceReport struct {
	Error string `json:"error,omitempty"`

	DatabaseAge   int64      `json:"databaseAge"`
	FreezeMaxAge  int64      `json:"freezeMaxAge"`
	WraparoundPct float64    `json:"wraparoundPct"`
	OldestTables  []tableAge `json:"oldestTables"`

	NeedVacuum []tableVacuum `json:"needVacuum"`
	LongTx     []longTx      `json:"longTx"`
	Sequences  []seqUsage    `json:"sequences"`
}

func emptyMaintenanceReport() maintenanceReport {
	return maintenanceReport{
		OldestTables: []tableAge{},
		NeedVacuum:   []tableVacuum{},
		LongTx:       []longTx{},
		Sequences:    []seqUsage{},
	}
}

func vacuumSQL(schema, table string) string {
	return "VACUUM (ANALYZE) " + pgx.Identifier{schema, table}.Sanitize() + ";"
}

// collectMaintenance gathers the maintenance report. Failure is data, never a
// blank screen.
func collectMaintenance(ctx context.Context, dbURL string) maintenanceReport {
	report := emptyMaintenanceReport()

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

	for _, step := range []func(context.Context, *pgx.Conn, *maintenanceReport) error{
		collectWraparound,
		collectNeedVacuum,
		collectLongTx,
		collectSequences,
	} {
		err = step(ctx, conn, &report)
		if err != nil {
			report.Error = err.Error()
			return report
		}
	}
	return report
}

const sqlWraparound = `SELECT
		age(datfrozenxid),                                    -- 1
		current_setting('autovacuum_freeze_max_age')::bigint  -- 2
	FROM pg_database
	WHERE datname = current_database();`

const sqlOldestTables = `SELECT
		n.nspname,                                 -- 1
		c.relname,                                 -- 2
		age(c.relfrozenxid),                       -- 3
		pg_size_pretty(pg_total_relation_size(c.oid)) -- 4
	FROM pg_class c
	JOIN pg_namespace n ON n.oid = c.relnamespace
	WHERE c.relkind IN ('r', 'm')
	AND n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
	-- Relations without storage of their own (a partitioned parent, a
	-- sequence) carry relfrozenxid = 0, and age(0) is the maximum 2^31-1.
	-- Charting those would raise a permanent false wraparound alarm.
	AND c.relfrozenxid <> 0
	ORDER BY age(c.relfrozenxid) DESC
	LIMIT 10;`

func collectWraparound(ctx context.Context, conn *pgx.Conn, report *maintenanceReport) error {
	qctx, cancel := context.WithTimeout(ctx, indexQueryTimeout)
	defer cancel()

	err := conn.QueryRow(qctx, sqlWraparound).Scan(
		&report.DatabaseAge,  // 1
		&report.FreezeMaxAge, // 2
	)
	if err != nil {
		return err
	}
	if report.FreezeMaxAge > 0 {
		report.WraparoundPct = round2(100 * float64(report.DatabaseAge) / float64(report.FreezeMaxAge))
	}

	rows, err := conn.Query(qctx, sqlOldestTables)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var t tableAge
		err = rows.Scan(
			&t.Schema, // 1
			&t.Table,  // 2
			&t.Age,    // 3
			&t.Size,   // 4
		)
		if err != nil {
			return err
		}
		if report.FreezeMaxAge > 0 {
			t.Pct = round2(100 * float64(t.Age) / float64(report.FreezeMaxAge))
		}
		report.OldestTables = append(report.OldestTables, t)
	}
	return rows.Err()
}

const sqlNeedVacuum = `SELECT
		schemaname,                                -- 1
		relname,                                   -- 2
		n_live_tup,                                -- 3
		n_dead_tup,                                -- 4
		CASE WHEN n_live_tup + n_dead_tup = 0 THEN 0
			ELSE round(100.0 * n_dead_tup / (n_live_tup + n_dead_tup), 1)
			END,                                   -- 5
		pg_size_pretty(pg_total_relation_size(relid)), -- 6
		COALESCE(EXTRACT(epoch FROM (now() - GREATEST(last_vacuum, last_autovacuum))), -1),   -- 7
		COALESCE(EXTRACT(epoch FROM (now() - GREATEST(last_analyze, last_autoanalyze))), -1) -- 8
	FROM pg_stat_user_tables
	WHERE n_dead_tup > 1000
	ORDER BY n_dead_tup DESC
	LIMIT 20;`

func collectNeedVacuum(ctx context.Context, conn *pgx.Conn, report *maintenanceReport) error {
	qctx, cancel := context.WithTimeout(ctx, indexQueryTimeout)
	defer cancel()

	rows, err := conn.Query(qctx, sqlNeedVacuum)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var t tableVacuum
		err = rows.Scan(
			&t.Schema,     // 1
			&t.Table,      // 2
			&t.LiveRows,   // 3
			&t.DeadRows,   // 4
			&t.DeadPct,    // 5
			&t.Size,       // 6
			&t.VacuumAgo,  // 7
			&t.AnalyzeAgo, // 8
		)
		if err != nil {
			return err
		}
		t.VacuumAgo = round2(t.VacuumAgo)
		t.AnalyzeAgo = round2(t.AnalyzeAgo)
		t.VacuumSQL = vacuumSQL(t.Schema, t.Table)
		report.NeedVacuum = append(report.NeedVacuum, t)
	}
	return rows.Err()
}

// sqlLongTx catches both a long-running query and the far more dangerous
// idle-in-transaction: either one holds back vacuum and keeps locks.
const sqlLongTx = `SELECT
		pid,                                            -- 1
		COALESCE(usename, ''),                          -- 2
		COALESCE(application_name, ''),                 -- 3
		COALESCE(state, ''),                            -- 4
		EXTRACT(epoch FROM (now() - xact_start)),       -- 5
		COALESCE(EXTRACT(epoch FROM (now() - state_change)), 0), -- 6
		COALESCE(query, '')                             -- 7
	FROM pg_stat_activity
	WHERE xact_start IS NOT NULL
	AND now() - xact_start > interval '1 minute'
	AND pid <> pg_backend_pid()
	ORDER BY xact_start;`

func collectLongTx(ctx context.Context, conn *pgx.Conn, report *maintenanceReport) error {
	qctx, cancel := context.WithTimeout(ctx, indexQueryTimeout)
	defer cancel()

	rows, err := conn.Query(qctx, sqlLongTx)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var t longTx
		err = rows.Scan(
			&t.PID,          // 1
			&t.User,         // 2
			&t.App,          // 3
			&t.State,        // 4
			&t.XactSeconds,  // 5
			&t.StateSeconds, // 6
			&t.Query,        // 7
		)
		if err != nil {
			return err
		}
		t.XactSeconds = round2(t.XactSeconds)
		t.StateSeconds = round2(t.StateSeconds)
		t.Query = normalizeQuery(t.Query)
		t.CancelSQL = cancelSQL(t.PID)
		t.TerminateSQL = terminateSQL(t.PID)
		report.LongTx = append(report.LongTx, t)
	}
	return rows.Err()
}

const sqlSequences = `SELECT
		schemaname,                                   -- 1
		sequencename,                                 -- 2
		last_value,                                   -- 3
		max_value,                                    -- 4
		round(100.0 * last_value / max_value, 2)      -- 5
	FROM pg_sequences
	WHERE last_value IS NOT NULL
	AND max_value > 0
	ORDER BY last_value::numeric / max_value DESC
	LIMIT 10;`

func collectSequences(ctx context.Context, conn *pgx.Conn, report *maintenanceReport) error {
	qctx, cancel := context.WithTimeout(ctx, indexQueryTimeout)
	defer cancel()

	rows, err := conn.Query(qctx, sqlSequences)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var s seqUsage
		err = rows.Scan(
			&s.Schema,    // 1
			&s.Name,      // 2
			&s.LastValue, // 3
			&s.MaxValue,  // 4
			&s.Pct,       // 5
		)
		if err != nil {
			return err
		}
		report.Sequences = append(report.Sequences, s)
	}
	return rows.Err()
}

// vacuumTracker remembers the backend PID of the vacuum this app started, so
// progress is attributed to it and never to an autovacuum of the same table.
var vacuumTracker struct {
	mu      sync.Mutex
	running bool
	pid     uint32
	schema  string
	table   string
	started time.Time
}

// vacuumProgressOut reports what the server says about the running vacuum.
// Phase names come straight from PostgreSQL ("scanning heap", "vacuuming
// indexes", ...), so the screen never invents progress it cannot see.
type vacuumProgressOut struct {
	Running        bool    `json:"running"`
	Schema         string  `json:"schema"`
	Table          string  `json:"table"`
	Phase          string  `json:"phase"`
	Pct            float64 `json:"pct"`
	HeapBlksTotal  int64   `json:"heapBlksTotal"`
	HeapBlksDone   int64   `json:"heapBlksDone"`
	IndexPasses    int64   `json:"indexPasses"`
	ElapsedSeconds float64 `json:"elapsedSeconds"`
}

// runVacuum executes the same VACUUM (ANALYZE) the UI displayed. It is not
// destructive, but it can run for a long time on a big table, hence the wide
// timeout; the server keeps working throughout.
func runVacuum(ctx context.Context, dbURL, schema, table string) error {
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

	vacuumTracker.mu.Lock()
	vacuumTracker.running = true
	vacuumTracker.pid = conn.PgConn().PID()
	vacuumTracker.schema = schema
	vacuumTracker.table = table
	vacuumTracker.started = time.Now()
	vacuumTracker.mu.Unlock()
	defer func() {
		vacuumTracker.mu.Lock()
		vacuumTracker.running = false
		vacuumTracker.mu.Unlock()
	}()

	qctx, cancel := context.WithTimeout(ctx, vacuumTimeout)
	defer cancel()
	start := time.Now()
	_, err = conn.Exec(qctx, vacuumSQL(schema, table))
	if err != nil {
		return fmt.Errorf("vacuum %s.%s: %w", schema, table, err)
	}
	debugf("event=vacuum_done schema=%s table=%s seconds=%.1f",
		schema, table, time.Since(start).Seconds())
	return nil
}

const sqlVacuumProgress = `SELECT
		phase,             -- 1
		heap_blks_total,   -- 2
		heap_blks_scanned, -- 3
		index_vacuum_count -- 4
	FROM pg_stat_progress_vacuum
	WHERE pid = $1;` // 1

// collectVacuumProgress asks the server how far the vacuum this app started
// has got. A vacuum that is running but not yet visible in the progress view
// still reports Running with the elapsed time, so the screen always shows the
// action is alive.
func collectVacuumProgress(ctx context.Context, dbURL string) vacuumProgressOut {
	vacuumTracker.mu.Lock()
	out := vacuumProgressOut{
		Running: vacuumTracker.running,
		Schema:  vacuumTracker.schema,
		Table:   vacuumTracker.table,
	}
	pid := vacuumTracker.pid
	started := vacuumTracker.started
	vacuumTracker.mu.Unlock()

	if !out.Running {
		return out
	}
	out.ElapsedSeconds = round2(time.Since(started).Seconds())

	connCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	conn, err := pgx.Connect(connCtx, dbURL)
	cancel()
	if err != nil {
		return out
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), sampleTimeout)
		_ = conn.Close(closeCtx)
		cancel()
	}()

	qctx, cancel := context.WithTimeout(ctx, sampleTimeout)
	defer cancel()
	err = conn.QueryRow(qctx, sqlVacuumProgress, pid).Scan(
		&out.Phase,         // 1
		&out.HeapBlksTotal, // 2
		&out.HeapBlksDone,  // 3
		&out.IndexPasses,   // 4
	)
	if err != nil {
		// No row yet (or already gone): the elapsed time still tells the user
		// the vacuum is alive.
		return out
	}
	if out.HeapBlksTotal > 0 {
		out.Pct = round2(100 * float64(out.HeapBlksDone) / float64(out.HeapBlksTotal))
	}
	return out
}
