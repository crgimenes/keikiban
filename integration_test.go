package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crgimenes/migration/introspect"
	"github.com/crgimenes/migration/snapshot"
	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// These tests exercise the SQL collectors against a real server, because a
// broken catalog query cannot be caught by any amount of unit testing: the
// report simply comes back empty and looks like a healthy database.
//
// SAFETY: every test here connects ONLY to the throwaway container started by
// startPostgres, and some of them run DDL, VACUUM and pg_cancel_backend. No
// test in this package may read the user's configuration file, which names
// production databases. TestMain enforces that below.

const (
	containerStartTimeout = 3 * time.Minute
	// statsWait bounds the polling for numbers that the statistics collector
	// publishes asynchronously (dead rows, sequential scans): they are never
	// visible in the same instant the work happens.
	statsWait = 45 * time.Second
	pgImage   = "postgres:17-alpine"
)

var (
	pgOnce      sync.Once
	pgContainer *postgres.PostgresContainer
	pgConnURL   string
	pgStartErr  error
)

// TestMain points the config lookup at an empty temporary directory, so that
// no test in this package can reach the real $XDG_CONFIG_HOME/keikiban/
// init.filo. That file lists production databases; a test that picked it up
// would aim this file's DDL at them. The container URL is the only database
// any test here is allowed to know.
//
// Note the remaining hole, which the app's own lookup order creates: a
// keikiban_init.filo sitting in the package directory still wins over
// XDG_CONFIG_HOME. Keep dev configs out of the repo, and out of harm's way.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "keikiban-test-config")
	if err != nil {
		fmt.Fprintln(os.Stderr, "keikiban: cannot isolate the test config:", err)
		os.Exit(1)
	}
	err = os.Setenv("XDG_CONFIG_HOME", dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "keikiban: cannot isolate the test config:", err)
		os.Exit(1)
	}

	code := m.Run()

	if pgContainer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		_ = pgContainer.Terminate(ctx)
		cancel()
	}
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// startPostgres boots one throwaway PostgreSQL for the whole package: the
// container costs seconds to start and every test here only needs a server it
// is free to damage.
//
// A machine without a running Docker daemon skips these tests instead of
// failing them, per the house rule for environment-dependent tests. On macOS
// that means `colima start` (see ~/bin/colima-up); when the daemon is not on
// the default socket, point DOCKER_HOST at it.
func startPostgres(t *testing.T) string {
	t.Helper()

	pgOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), containerStartTimeout)
		defer cancel()

		pgContainer, pgStartErr = postgres.Run(ctx, pgImage,
			postgres.WithDatabase("keikiban_test"),
			postgres.WithUsername("keikiban"),
			postgres.WithPassword("keikiban"),
			postgres.BasicWaitStrategies(),
		)
		if pgStartErr != nil {
			return
		}
		pgConnURL, pgStartErr = pgContainer.ConnectionString(ctx, "sslmode=disable")
	})

	if pgStartErr != nil {
		t.Skipf("no PostgreSQL container available, skipping (is the Docker daemon up?): %v", pgStartErr)
	}
	return pgConnURL
}

// openConn returns a connection closed when the test ends.
func openConn(t *testing.T, url string) *pgx.Conn {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), sampleTimeout)
		_ = conn.Close(closeCtx)
		closeCancel()
	})
	return conn
}

func mustExec(t *testing.T, conn *pgx.Conn, sql string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	_, err := conn.Exec(ctx, sql)
	if err != nil {
		t.Fatalf("exec %.60q: %v", sql, err)
	}
}

func backendPID(t *testing.T, conn *pgx.Conn) int {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), sampleTimeout)
	defer cancel()

	pid := 0
	err := conn.QueryRow(ctx, `SELECT pg_backend_pid();`).Scan(&pid)
	if err != nil {
		t.Fatalf("pg_backend_pid: %v", err)
	}
	return pid
}

// eventually polls until want reports success, for numbers the statistics
// collector publishes on its own schedule. It fails the test with what was
// being waited for, so a timeout names the missing condition.
func eventually(t *testing.T, timeout time.Duration, what string, want func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if want() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

func findIndex(entries []indexEntry, name string) (indexEntry, bool) {
	for _, e := range entries {
		if e.Name == name {
			return e, true
		}
	}
	return indexEntry{}, false
}

// TestIndexReportSeesEveryCategory builds one table carrying a known instance
// of each problem the index screen reports, and checks the report finds them.
func TestIndexReportSeesEveryCategory(t *testing.T) {
	url := startPostgres(t)
	conn := openConn(t, url)
	ctx := context.Background()

	mustExec(t, conn, `CREATE TABLE idx_fixture (
		id bigserial PRIMARY KEY,
		a int,
		b int,
		c int);`)
	mustExec(t, conn, `INSERT INTO idx_fixture (a, b, c)
		SELECT g, g, g FROM generate_series(1, 20000) g;`)
	// idx_fixture_a is a leading prefix of idx_fixture_ab: redundant.
	mustExec(t, conn, `CREATE INDEX idx_fixture_ab ON idx_fixture (a, b);`)
	mustExec(t, conn, `CREATE INDEX idx_fixture_a ON idx_fixture (a);`)
	// Never scanned by anyone: the unused case.
	mustExec(t, conn, `CREATE INDEX idx_fixture_c ON idx_fixture (c);`)
	// A failed CREATE INDEX CONCURRENTLY is what leaves an invalid index
	// behind, and it is not reproducible on demand; flipping the catalog flag
	// produces the same state the report has to notice. Safe here, and only
	// here, because the whole server is thrown away with the container.
	mustExec(t, conn, `CREATE INDEX idx_fixture_bad ON idx_fixture (b);`)
	mustExec(t, conn, `UPDATE pg_index SET indisvalid = false
		WHERE indexrelid = 'idx_fixture_bad'::regclass;`)
	// A sequential scan over a table well past the report's 10000-row floor.
	mustExec(t, conn, `ANALYZE idx_fixture;`)
	mustExec(t, conn, `SELECT count(*) FROM idx_fixture;`)

	report := indexReport{}
	eventually(t, statsWait, "idx_fixture to show up as a seq-scan offender", func() bool {
		report = collectIndexReport(ctx, url)
		for _, s := range report.SeqScans {
			if s.Table == "idx_fixture" {
				return true
			}
		}
		return false
	})

	if report.Error != "" {
		t.Fatalf("report error: %s", report.Error)
	}

	invalid, ok := findIndex(report.Invalid, "idx_fixture_bad")
	if !ok {
		t.Errorf("invalid index idx_fixture_bad missing from the report")
	}
	// An invalid index is not also reported as unused: one problem, one place.
	_, ok = findIndex(report.Unused, "idx_fixture_bad")
	if ok {
		t.Errorf("invalid index idx_fixture_bad also listed as unused")
	}

	unused, ok := findIndex(report.Unused, "idx_fixture_c")
	if !ok {
		t.Fatalf("unused index idx_fixture_c missing from the report")
	}
	if unused.Scans != 0 {
		t.Errorf("unused index reports %d scans, want 0", unused.Scans)
	}
	if unused.SizeBytes <= 0 {
		t.Errorf("unused index reports size %d bytes, want a positive size", unused.SizeBytes)
	}

	// The DDL the UI shows, copies and later executes has to be exact.
	wantDDL := `DROP INDEX CONCURRENTLY "public"."idx_fixture_c";`
	if unused.DropDDL != wantDDL {
		t.Errorf("drop DDL = %q, want %q", unused.DropDDL, wantDDL)
	}
	if invalid.DropDDL == "" {
		t.Errorf("invalid index carries no drop DDL")
	}

	dup, ok := findIndex(report.Duplicates, "idx_fixture_a")
	if !ok {
		t.Fatalf("redundant index idx_fixture_a missing from the report")
	}
	if dup.CoveredBy != "idx_fixture_ab" {
		t.Errorf("idx_fixture_a covered by %q, want idx_fixture_ab", dup.CoveredBy)
	}
	// The wider index is the one to keep: it must not be reported itself.
	_, ok = findIndex(report.Duplicates, "idx_fixture_ab")
	if ok {
		t.Errorf("the wider index idx_fixture_ab was reported as redundant")
	}
	// The primary key is never a removal candidate.
	_, ok = findIndex(report.Duplicates, "idx_fixture_pkey")
	if ok {
		t.Errorf("primary key reported as redundant")
	}

	if report.IndexCacheHitPct < 0 || report.IndexCacheHitPct > 100 {
		t.Errorf("index cache hit = %v, want a percentage", report.IndexCacheHitPct)
	}
	// StatsReset is deliberately allowed to be empty: a cluster that has never
	// had its statistics reset carries NULL, and the report passes that through
	// instead of inventing a date. The screen spells it out as "statistics
	// never reset".
}

// TestDropIndexRemovesIt runs the destructive path end to end: the statement
// the screen displays is the statement that reaches the server.
func TestDropIndexRemovesIt(t *testing.T) {
	url := startPostgres(t)
	conn := openConn(t, url)
	ctx := context.Background()

	mustExec(t, conn, `CREATE TABLE drop_fixture (id int, v int);`)
	mustExec(t, conn, `CREATE INDEX drop_fixture_v ON drop_fixture (v);`)

	err := dropIndex(ctx, url, "public", "drop_fixture_v")
	if err != nil {
		t.Fatalf("dropIndex: %v", err)
	}

	qctx, cancel := context.WithTimeout(ctx, sampleTimeout)
	defer cancel()

	n := 0
	err = conn.QueryRow(qctx,
		`SELECT count(*) FROM pg_class WHERE relname = 'drop_fixture_v';`).Scan(&n)
	if err != nil {
		t.Fatalf("count index: %v", err)
	}
	if n != 0 {
		t.Errorf("index still present after dropIndex")
	}

	// Dropping what is no longer there fails loudly rather than silently
	// reporting success.
	err = dropIndex(ctx, url, "public", "drop_fixture_v")
	if err == nil {
		t.Errorf("dropping a missing index returned no error")
	}
}

// TestBlockingTreeSeesRealLock creates an actual lock wait and checks the
// locks screen names both ends of it, then cancels the waiter.
func TestBlockingTreeSeesRealLock(t *testing.T) {
	url := startPostgres(t)
	ctx := context.Background()

	holder := openConn(t, url)
	waiter := openConn(t, url)

	mustExec(t, holder, `CREATE TABLE lock_fixture (id int);`)
	mustExec(t, holder, `INSERT INTO lock_fixture VALUES (1);`)

	holderPID := backendPID(t, holder)
	waiterPID := backendPID(t, waiter)

	mustExec(t, holder, `BEGIN;`)
	mustExec(t, holder, `LOCK TABLE lock_fixture IN ACCESS EXCLUSIVE MODE;`)
	t.Cleanup(func() {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), sampleTimeout)
		_, _ = holder.Exec(rollbackCtx, `ROLLBACK;`)
		cancel()
	})

	// The waiter blocks until someone cancels it; the bounded context keeps a
	// failure here from hanging the suite.
	blocked := make(chan error, 1)
	go func() {
		qctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		n := 0
		blocked <- waiter.QueryRow(qctx, `SELECT count(*) FROM lock_fixture;`).Scan(&n)
	}()

	var tree []blockerOut
	eventually(t, statsWait, "the lock wait to appear in the blocking tree", func() bool {
		var err error
		tree, err = blockingSnapshot(ctx, url)
		if err != nil {
			t.Fatalf("blockingSnapshot: %v", err)
		}
		return len(tree) > 0
	})

	found := false
	for _, b := range tree {
		if b.PID != holderPID {
			continue
		}
		found = true
		if b.CancelSQL != cancelSQL(holderPID) {
			t.Errorf("cancel SQL = %q, want %q", b.CancelSQL, cancelSQL(holderPID))
		}
		if b.TerminateSQL != terminateSQL(holderPID) {
			t.Errorf("terminate SQL = %q, want %q", b.TerminateSQL, terminateSQL(holderPID))
		}
		// The holder sits idle in transaction: the classic invisible culprit.
		if b.AlsoBlocked {
			t.Errorf("the root blocker was reported as itself blocked")
		}

		waiterFound := false
		for _, bd := range b.Blocked {
			if bd.PID != waiterPID {
				continue
			}
			waiterFound = true
			if bd.LockMode == "" {
				t.Errorf("blocked session reports no lock mode")
			}
			if bd.WaitSeconds < 0 {
				t.Errorf("blocked session waited %v seconds", bd.WaitSeconds)
			}
		}
		if !waiterFound {
			t.Errorf("blocker %d does not list the waiting session %d", holderPID, waiterPID)
		}
	}
	if !found {
		t.Fatalf("blocking tree does not name the holder %d: %+v", holderPID, tree)
	}

	// Cancelling the waiter is the action the screen offers; it has to work.
	err := cancelBackend(ctx, url, waiterPID, false)
	if err != nil {
		t.Fatalf("cancelBackend: %v", err)
	}

	err = <-blocked
	if err == nil {
		t.Errorf("the blocked query completed although it was cancelled")
	}
}

// TestSessionsListsIdleInTransaction checks the sessions screen sees the state
// that matters most, on the session that is actually in it.
func TestSessionsListsIdleInTransaction(t *testing.T) {
	url := startPostgres(t)
	ctx := context.Background()

	conn := openConn(t, url)
	pid := backendPID(t, conn)

	mustExec(t, conn, `BEGIN;`)
	mustExec(t, conn, `SELECT 1;`)
	t.Cleanup(func() {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), sampleTimeout)
		_, _ = conn.Exec(rollbackCtx, `ROLLBACK;`)
		cancel()
	})

	out := collectSessions(ctx, url)
	if out.Error != "" {
		t.Fatalf("collectSessions: %s", out.Error)
	}

	found := false
	for _, s := range out.Sessions {
		if s.PID != pid {
			continue
		}
		found = true
		if s.State != "idle in transaction" {
			t.Errorf("state = %q, want idle in transaction", s.State)
		}
		if s.Database != "keikiban_test" {
			t.Errorf("database = %q, want keikiban_test", s.Database)
		}
		if s.User != "keikiban" {
			t.Errorf("user = %q, want keikiban", s.User)
		}
		if s.CancelSQL != cancelSQL(pid) {
			t.Errorf("cancel SQL = %q, want %q", s.CancelSQL, cancelSQL(pid))
		}
		if s.Blocked {
			t.Errorf("an idle session was reported as blocked")
		}
	}
	if !found {
		t.Errorf("session %d missing from the list of %d sessions", pid, len(out.Sessions))
	}
}

// TestMaintenanceReportAndVacuum builds a table with dead rows, checks the
// maintenance screen flags it, then runs the VACUUM the screen offers and
// checks the flag clears.
func TestMaintenanceReportAndVacuum(t *testing.T) {
	url := startPostgres(t)
	conn := openConn(t, url)
	ctx := context.Background()

	// autovacuum is off for this table only, so the fixture stays dirty until
	// the test itself cleans it; otherwise the daemon races the assertions.
	mustExec(t, conn, `CREATE TABLE vac_fixture (
		id bigserial PRIMARY KEY,
		v int) WITH (autovacuum_enabled = false);`)
	mustExec(t, conn, `INSERT INTO vac_fixture (v)
		SELECT g FROM generate_series(1, 5000) g;`)
	mustExec(t, conn, `DELETE FROM vac_fixture WHERE id <= 3000;`)

	report := maintenanceReport{}
	hasFixture := func(r maintenanceReport) bool {
		for _, tv := range r.NeedVacuum {
			if tv.Table == "vac_fixture" {
				return true
			}
		}
		return false
	}

	eventually(t, statsWait, "vac_fixture to be flagged as needing vacuum", func() bool {
		report = collectMaintenance(ctx, url)
		return hasFixture(report)
	})

	if report.Error != "" {
		t.Fatalf("report error: %s", report.Error)
	}
	if report.FreezeMaxAge <= 0 {
		t.Errorf("freeze max age = %d, want the server setting", report.FreezeMaxAge)
	}
	if report.WraparoundPct < 0 {
		t.Errorf("wraparound = %v%%, want a non-negative percentage", report.WraparoundPct)
	}

	for _, tv := range report.NeedVacuum {
		if tv.Table != "vac_fixture" {
			continue
		}
		if tv.DeadRows < 1000 {
			t.Errorf("dead rows = %d, want the 3000 deleted rows", tv.DeadRows)
		}
		wantSQL := `VACUUM (ANALYZE) "public"."vac_fixture";`
		if tv.VacuumSQL != wantSQL {
			t.Errorf("vacuum SQL = %q, want %q", tv.VacuumSQL, wantSQL)
		}
		// Nothing has vacuumed this table yet, and the report says so rather
		// than pretending it happened at time zero.
		if tv.VacuumAgo != -1 {
			t.Errorf("vacuum ago = %v, want -1 for never vacuumed", tv.VacuumAgo)
		}
	}

	// The sequence behind the bigserial column is visible to the overflow check.
	foundSeq := false
	for _, s := range report.Sequences {
		if s.Name == "vac_fixture_id_seq" {
			foundSeq = true
			if s.LastValue <= 0 {
				t.Errorf("sequence last value = %d, want the 5000 inserts", s.LastValue)
			}
			if s.MaxValue <= 0 {
				t.Errorf("sequence max value = %d, want a positive ceiling", s.MaxValue)
			}
		}
	}
	if !foundSeq {
		t.Errorf("sequence vac_fixture_id_seq missing from the report")
	}

	err := runVacuum(ctx, url, "public", "vac_fixture")
	if err != nil {
		t.Fatalf("runVacuum: %v", err)
	}

	eventually(t, statsWait, "vac_fixture to drop off the vacuum list", func() bool {
		return !hasFixture(collectMaintenance(ctx, url))
	})
}

// TestSamplerCollectsAgainstRealServer runs the sampler loop against the
// container: it has to connect, report itself as sampling, and produce a
// snapshot with the sessions it saw.
func TestSamplerCollectsAgainstRealServer(t *testing.T) {
	url := startPostgres(t)

	// A session that stays active gives the sampler something to count.
	busy := openConn(t, url)
	busyCtx, stopBusy := context.WithCancel(context.Background())
	busyDone := make(chan struct{})
	go func() {
		defer close(busyDone)
		_, _ = busy.Exec(busyCtx, `SELECT pg_sleep(20);`)
	}()
	t.Cleanup(func() {
		stopBusy()
		<-busyDone
	})

	s := newSampler(url)
	t.Cleanup(s.Stop)

	snap := dashOut{}
	eventually(t, statsWait, "the sampler to capture an active session", func() bool {
		snap = s.Snapshot(time.Now(), 300, "waits", "sql")
		if !snap.Connected {
			return false
		}
		return len(snap.TopSQL) > 0
	})

	if snap.Status != "sampling" {
		t.Errorf("status = %q, want sampling", snap.Status)
	}
	if snap.Samples <= 0 {
		t.Errorf("samples = %d, want at least one capture", snap.Samples)
	}
	if snap.MaxConnections <= 0 {
		t.Errorf("max connections = %d, want the server setting", snap.MaxConnections)
	}
	if snap.DatabaseCount <= 0 {
		t.Errorf("database count = %d, want at least one database", snap.DatabaseCount)
	}
	if len(snap.Buckets) == 0 {
		t.Errorf("the chart has no buckets")
	}

	// A stock container has no pg_stat_statements, and the dashboard says so
	// instead of quietly dropping the enrichment.
	found := false
	for _, m := range snap.Missing {
		if m == "pg_stat_statements" {
			found = true
		}
	}
	if !found {
		t.Errorf("missing extensions = %v, want pg_stat_statements listed", snap.Missing)
	}

	// The sleeping session is what the sampler should be ranking.
	ranked := false
	for _, top := range snap.TopSQL {
		if strings.Contains(top.Query, "pg_sleep") {
			ranked = true
			if top.AAS <= 0 {
				t.Errorf("pg_sleep ranks with AAS %v, want a positive load", top.AAS)
			}
		}
	}
	if !ranked {
		t.Errorf("the sleeping session is missing from Top SQL: %+v", snap.TopSQL)
	}
}

// browserFixture builds one schema holding an instance of every object kind
// the browser tree shows. It runs once: the tests below only read.
func browserFixture(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	mustExec(t, conn, `CREATE SCHEMA IF NOT EXISTS br_fixture;`)
	mustExec(t, conn, `CREATE TABLE IF NOT EXISTS br_fixture.authors (
		id bigserial PRIMARY KEY,
		name text NOT NULL);`)
	mustExec(t, conn, `CREATE TABLE IF NOT EXISTS br_fixture.books (
		id bigserial PRIMARY KEY,
		author_id bigint NOT NULL REFERENCES br_fixture.authors (id),
		title text NOT NULL,
		edition int DEFAULT 1,
		note text);`)
	mustExec(t, conn, `COMMENT ON TABLE br_fixture.books IS 'one row per book';`)
	mustExec(t, conn, `CREATE INDEX IF NOT EXISTS books_title_idx
		ON br_fixture.books (title);`)
	mustExec(t, conn, `CREATE OR REPLACE VIEW br_fixture.recent_books AS
		SELECT id, title FROM br_fixture.books WHERE edition > 1;`)
	mustExec(t, conn, `CREATE MATERIALIZED VIEW IF NOT EXISTS br_fixture.book_count AS
		SELECT count(*) AS total FROM br_fixture.books;`)
	mustExec(t, conn, `CREATE SEQUENCE IF NOT EXISTS br_fixture.ticket_seq
		START 10 INCREMENT 5 MAXVALUE 1000;`)
}

// TestBrowserTreeCountsAndLists checks the root of the tree and one expansion:
// the counts shown on group nodes have to match what expanding them yields.
func TestBrowserTreeCountsAndLists(t *testing.T) {
	url := startPostgres(t)
	conn := openConn(t, url)
	ctx := context.Background()
	browserFixture(t, conn)

	tree := collectBrowserTree(ctx, url)
	if tree.Error != "" {
		t.Fatalf("tree error: %s", tree.Error)
	}

	var fixture schemaNode
	found := false
	for _, s := range tree.Schemas {
		if s.Name == "br_fixture" {
			fixture = s
			found = true
		}
		// System schemas are noise in a browser and must stay out.
		if s.Name == "pg_catalog" || s.Name == "information_schema" {
			t.Errorf("system schema %q reached the tree", s.Name)
		}
	}
	if !found {
		t.Fatalf("br_fixture missing from the tree: %+v", tree.Schemas)
	}

	// authors + books; the bigserial sequences are counted as sequences, and
	// the materialized view must not be counted as a table.
	if fixture.Tables != 2 {
		t.Errorf("tables = %d, want 2", fixture.Tables)
	}
	if fixture.Views != 1 {
		t.Errorf("views = %d, want 1", fixture.Views)
	}
	if fixture.MatViews != 1 {
		t.Errorf("materialized views = %d, want 1", fixture.MatViews)
	}
	// Two bigserial columns own a sequence each, plus the explicit one.
	if fixture.Sequences != 3 {
		t.Errorf("sequences = %d, want 3", fixture.Sequences)
	}

	list := collectObjects(ctx, url, "br_fixture", "tables")
	if list.Error != "" {
		t.Fatalf("objects error: %s", list.Error)
	}
	if list.Total != fixture.Tables {
		t.Errorf("listing total = %d, but the tree node said %d",
			list.Total, fixture.Tables)
	}
	if list.Truncated {
		t.Errorf("a two-table schema came back truncated")
	}

	names := []string{}
	for _, o := range list.Objects {
		names = append(names, o.Name)
		if o.Schema != "br_fixture" {
			t.Errorf("object %q carries schema %q", o.Name, o.Schema)
		}
	}
	if len(names) != 2 || names[0] != "authors" || names[1] != "books" {
		t.Errorf("tables = %v, want [authors books] in name order", names)
	}

	for _, o := range list.Objects {
		if o.Name == "books" && o.Comment != "one row per book" {
			t.Errorf("comment = %q, want the table comment", o.Comment)
		}
	}

	// An unknown group is a programming error in the page, and it says so
	// rather than quietly returning an empty list that reads as "nothing here".
	bad := collectObjects(ctx, url, "br_fixture", "nonsense")
	if bad.Error == "" {
		t.Errorf("unknown object group returned no error")
	}
}

// TestObjectDetailDescribesTable checks the detail pane's payload: columns
// with their keys, and definitions taken verbatim from the server.
func TestObjectDetailDescribesTable(t *testing.T) {
	url := startPostgres(t)
	conn := openConn(t, url)
	ctx := context.Background()
	browserFixture(t, conn)

	d := collectObjectDetail(ctx, url, "br_fixture", "books")
	if d.Error != "" {
		t.Fatalf("detail error: %s", d.Error)
	}
	if d.Kind != "r" {
		t.Errorf("kind = %q, want r", d.Kind)
	}
	if d.Comment != "one row per book" {
		t.Errorf("comment = %q", d.Comment)
	}

	byName := map[string]columnInfo{}
	for _, c := range d.Columns {
		byName[c.Name] = c
	}
	if len(d.Columns) != 5 {
		t.Errorf("columns = %d, want 5: %+v", len(d.Columns), d.Columns)
	}

	id := byName["id"]
	if !id.PK {
		t.Errorf("id is not marked as primary key")
	}
	if !id.NotNull {
		t.Errorf("id is not marked not null")
	}
	if id.Default == "" {
		t.Errorf("id lost its bigserial default")
	}

	author := byName["author_id"]
	if author.FK != "br_fixture.authors" {
		t.Errorf("author_id FK = %q, want br_fixture.authors", author.FK)
	}
	if author.PK {
		t.Errorf("author_id wrongly marked as primary key")
	}

	edition := byName["edition"]
	if edition.Default != "1" {
		t.Errorf("edition default = %q, want 1", edition.Default)
	}
	if edition.Type != "integer" {
		t.Errorf("edition type = %q, want integer", edition.Type)
	}

	note := byName["note"]
	if note.NotNull {
		t.Errorf("note is nullable but came back not null")
	}

	// Definitions are the server's own text; the app never reassembles DDL it
	// cannot vouch for.
	foundIndex := false
	for _, i := range d.Indexes {
		if i.Name != "books_title_idx" {
			continue
		}
		foundIndex = true
		if !strings.HasPrefix(i.Def, "CREATE INDEX books_title_idx ON br_fixture.books") {
			t.Errorf("index def = %q", i.Def)
		}
	}
	if !foundIndex {
		t.Errorf("books_title_idx missing from the detail: %+v", d.Indexes)
	}

	foundFK := false
	for _, c := range d.Constraints {
		if strings.HasPrefix(c.Def, "FOREIGN KEY") {
			foundFK = true
		}
	}
	if !foundFK {
		t.Errorf("foreign key constraint missing: %+v", d.Constraints)
	}

	// A table is not a view and must not carry a view definition.
	if d.ViewDef != "" {
		t.Errorf("table came back with a view definition: %q", d.ViewDef)
	}
	if d.Sequence != nil {
		t.Errorf("table came back with sequence parameters")
	}
}

func TestObjectDetailViewAndSequence(t *testing.T) {
	url := startPostgres(t)
	conn := openConn(t, url)
	ctx := context.Background()
	browserFixture(t, conn)

	v := collectObjectDetail(ctx, url, "br_fixture", "recent_books")
	if v.Error != "" {
		t.Fatalf("view detail error: %s", v.Error)
	}
	if v.Kind != "v" {
		t.Errorf("kind = %q, want v", v.Kind)
	}
	if !strings.Contains(v.ViewDef, "FROM br_fixture.books") {
		t.Errorf("view definition does not look like the server's: %q", v.ViewDef)
	}
	if len(v.Columns) != 2 {
		t.Errorf("view columns = %d, want 2", len(v.Columns))
	}

	s := collectObjectDetail(ctx, url, "br_fixture", "ticket_seq")
	if s.Error != "" {
		t.Fatalf("sequence detail error: %s", s.Error)
	}
	if s.Sequence == nil {
		t.Fatalf("sequence came back without its parameters")
	}
	if s.Sequence.Start != 10 {
		t.Errorf("start = %d, want 10", s.Sequence.Start)
	}
	if s.Sequence.Increment != 5 {
		t.Errorf("increment = %d, want 5", s.Sequence.Increment)
	}
	if s.Sequence.Max != 1000 {
		t.Errorf("max = %d, want 1000", s.Sequence.Max)
	}
	// Nothing has drawn from it, and the report says so instead of showing a
	// last value that was never handed out.
	if s.Sequence.Called {
		t.Errorf("untouched sequence reported as already used")
	}

	missing := collectObjectDetail(ctx, url, "br_fixture", "no_such_object")
	if missing.Error == "" {
		t.Errorf("describing a missing object returned no error")
	}
}

// TestSearchFindsAcrossSchemas is the point of running search on the server:
// a match is found without anything being expanded first.
func TestSearchFindsAcrossSchemas(t *testing.T) {
	url := startPostgres(t)
	conn := openConn(t, url)
	ctx := context.Background()
	browserFixture(t, conn)

	got := searchObjects(ctx, url, "recent_books")
	if got.Error != "" {
		t.Fatalf("search error: %s", got.Error)
	}
	found := false
	for _, h := range got.Hits {
		if h.Schema == "br_fixture" && h.Name == "recent_books" {
			found = true
			if h.Kind != "v" {
				t.Errorf("kind = %q, want v", h.Kind)
			}
		}
	}
	if !found {
		t.Errorf("recent_books not found by search: %+v", got.Hits)
	}

	// An empty term is not a request to list the whole database.
	empty := searchObjects(ctx, url, "   ")
	if len(empty.Hits) != 0 {
		t.Errorf("blank search returned %d hits", len(empty.Hits))
	}
}

// TestSearchTreatsUnderscoreLiterally pins the escaping. Identifiers are full
// of underscores, and LIKE would read every one of them as "any character":
// searching book_count would then also match booksXcount.
func TestSearchTreatsUnderscoreLiterally(t *testing.T) {
	url := startPostgres(t)
	conn := openConn(t, url)
	ctx := context.Background()

	mustExec(t, conn, `CREATE SCHEMA IF NOT EXISTS br_like;`)
	mustExec(t, conn, `CREATE TABLE IF NOT EXISTS br_like.rate_limit (id int);`)
	mustExec(t, conn, `CREATE TABLE IF NOT EXISTS br_like.rateXlimit (id int);`)

	got := searchObjects(ctx, url, "rate_limit")
	if got.Error != "" {
		t.Fatalf("search error: %s", got.Error)
	}

	names := []string{}
	for _, h := range got.Hits {
		names = append(names, h.Name)
	}
	if len(names) != 1 || names[0] != "rate_limit" {
		t.Errorf("search for rate_limit matched %v, want only rate_limit", names)
	}

	// The percent sign is the other wildcard, and it is literal too.
	pct := searchObjects(ctx, url, "%")
	if len(pct.Hits) != 0 {
		t.Errorf("a literal %% matched %d objects, want none", len(pct.Hits))
	}
}

// TestInitialQueryIsRunnable checks that the statement the editor opens with
// is valid SQL for the object it belongs to, quoting included.
func TestInitialQueryIsRunnable(t *testing.T) {
	url := startPostgres(t)
	conn := openConn(t, url)
	ctx := context.Background()

	// A name needing quotes, and one that is a reserved word, are exactly the
	// cases a hand-built string gets wrong.
	mustExec(t, conn, `CREATE SCHEMA IF NOT EXISTS "Odd Schema";`)
	mustExec(t, conn, `CREATE TABLE IF NOT EXISTS "Odd Schema"."select" (id int);`)
	mustExec(t, conn, `INSERT INTO "Odd Schema"."select" VALUES (1), (2);`)

	sql := initialQuery("Odd Schema", "select")
	if !strings.Contains(sql, "LIMIT 100") || !strings.Contains(sql, "OFFSET 0") {
		t.Errorf("initial query lost its modest bounds: %q", sql)
	}

	out := runQuery(ctx, url, sql)
	if out.Error != "" {
		t.Fatalf("initial query did not run: %s (%q)", out.Error, sql)
	}
	if !out.ReturnsRows {
		t.Errorf("a SELECT reported that it returns no rows")
	}
	if len(out.Rows) != 2 {
		t.Errorf("rows = %d, want 2", len(out.Rows))
	}
}

// TestRunQueryDistinguishesNullFromEmpty is the honesty test for the grid: a
// NULL and an empty string must not arrive looking alike.
func TestRunQueryDistinguishesNullFromEmpty(t *testing.T) {
	url := startPostgres(t)
	ctx := context.Background()

	out := runQuery(ctx, url, `SELECT NULL::text AS a, '' AS b, 'x' AS c;`)
	if out.Error != "" {
		t.Fatalf("query error: %s", out.Error)
	}
	if len(out.Rows) != 1 || len(out.Rows[0]) != 3 {
		t.Fatalf("shape = %d rows, want one row of three", len(out.Rows))
	}

	row := out.Rows[0]
	if row[0] != nil {
		t.Errorf("SQL NULL came back as %q, want a null", *row[0])
	}
	if row[1] == nil || *row[1] != "" {
		t.Errorf("empty string came back as null, losing the difference")
	}
	if row[2] == nil || *row[2] != "x" {
		t.Errorf("plain value did not survive the trip")
	}

	if out.Columns[0].Name != "a" {
		t.Errorf("column name = %q, want a", out.Columns[0].Name)
	}
	if out.Columns[0].Type != "text" {
		t.Errorf("column type = %q, want text", out.Columns[0].Type)
	}
}

// TestRunQueryReportsWritesAndErrors covers the two answers that are not a
// grid: a statement that changes rows, and one the server rejects.
func TestRunQueryReportsWritesAndErrors(t *testing.T) {
	url := startPostgres(t)
	conn := openConn(t, url)
	ctx := context.Background()

	mustExec(t, conn, `CREATE TABLE IF NOT EXISTS q_fixture (id int, v text);`)
	mustExec(t, conn, `TRUNCATE q_fixture;`)
	mustExec(t, conn, `INSERT INTO q_fixture SELECT g, 'v' FROM generate_series(1, 5) g;`)

	out := runQuery(ctx, url, `UPDATE q_fixture SET v = 'w' WHERE id <= 3;`)
	if out.Error != "" {
		t.Fatalf("update error: %s", out.Error)
	}
	if out.ReturnsRows {
		t.Errorf("an UPDATE reported that it returns rows")
	}
	if out.Affected != 3 {
		t.Errorf("affected = %d, want 3", out.Affected)
	}
	if !strings.HasPrefix(out.Command, "UPDATE") {
		t.Errorf("command = %q, want the UPDATE tag", out.Command)
	}

	// A rejected statement reports the server's own words, and does not
	// pretend to be an empty result.
	bad := runQuery(ctx, url, `SELECT * FROM table_that_is_not_there;`)
	if bad.Error == "" {
		t.Errorf("a failing query came back without an error")
	}
	if bad.Rows == nil || bad.Columns == nil {
		t.Errorf("failed query returned nil collections, which break the page")
	}

	blank := runQuery(ctx, url, "   \n  ")
	if blank.Error == "" {
		t.Errorf("an empty editor was sent to the server")
	}
}

// TestRunQueryStopsAtTheRowCeiling checks the guard against a SELECT with no
// LIMIT: it stops, and it says so.
func TestRunQueryStopsAtTheRowCeiling(t *testing.T) {
	url := startPostgres(t)
	ctx := context.Background()

	out := runQuery(ctx, url,
		`SELECT g FROM generate_series(1, `+strconv.Itoa(queryRowLimit*2)+`) g;`)
	if out.Error != "" {
		t.Fatalf("query error: %s", out.Error)
	}
	if len(out.Rows) != queryRowLimit {
		t.Errorf("rows = %d, want the %d ceiling", len(out.Rows), queryRowLimit)
	}
	if !out.Truncated {
		t.Errorf("the answer was cut short without saying so")
	}
}

// TestRunQueryCancels proves the promise the Cancel button makes: pgx has to
// carry the cancellation to the server, not just abandon the call.
func TestRunQueryCancels(t *testing.T) {
	url := startPostgres(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan queryOut, 1)
	go func() {
		done <- runQuery(ctx, url, `SELECT pg_sleep(30);`)
	}()

	// Long enough for the statement to be running on the server.
	time.Sleep(2 * time.Second)
	cancel()

	select {
	case out := <-done:
		if out.Error == "" {
			t.Errorf("a cancelled query reported success")
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("cancelling did not stop the query")
	}
}

// migFixtureDir writes a migrations directory with two known pairs.
func migFixtureDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	files := map[string]string{
		"001_users.up.sql":   "CREATE TABLE mig_users (id bigserial PRIMARY KEY, email text);",
		"001_users.down.sql": "DROP TABLE mig_users;",
		"002_notes.up.sql":   "CREATE TABLE mig_notes (id bigserial PRIMARY KEY, body text);",
		"002_notes.down.sql": "DROP TABLE mig_notes;",
	}
	for name, sql := range files {
		err := os.WriteFile(filepath.Join(dir, name), []byte(sql), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestMigrationStatusIsReadOnly pins the screen's core promise: looking at
// migration state must not create the tracking table on the server.
func TestMigrationStatusIsReadOnly(t *testing.T) {
	url := startPostgres(t)
	conn := openConn(t, url)
	ctx := context.Background()
	dir := migFixtureDir(t)

	mustExec(t, conn, `DROP TABLE IF EXISTS schema_migrations, mig_users, mig_notes;`)

	out := collectMigrationStatus(ctx, url, dir)
	if out.Error != "" {
		t.Fatalf("status error: %s", out.Error)
	}
	if out.TableExists {
		t.Errorf("tableExists = true on a clean database")
	}
	if out.Applied != 0 {
		t.Errorf("applied = %d, want 0", out.Applied)
	}
	if len(out.Pending) != 2 {
		t.Errorf("pending = %v, want the two fixtures", out.Pending)
	}
	if out.DirSource != "manual" {
		t.Errorf("dirSource = %q, want manual", out.DirSource)
	}
	// Drift needs a snapshot, and saying so is data, not failure.
	if out.Drift == nil || out.Drift.Error == "" {
		t.Errorf("drift without a snapshot should carry its own error, got %+v", out.Drift)
	}

	// The promise itself: status must not have created schema_migrations.
	qctx, cancel := context.WithTimeout(ctx, sampleTimeout)
	defer cancel()
	n := 0
	err := conn.QueryRow(qctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_name='schema_migrations';`).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("reading status CREATED schema_migrations — the read path wrote to the database")
	}
}

// TestMigrationUpDownCycle runs the fixtures up, checks the preview matches
// the files, reverts one, and checks the versions the whole way.
func TestMigrationUpDownCycle(t *testing.T) {
	url := startPostgres(t)
	conn := openConn(t, url)
	ctx := context.Background()
	dir := migFixtureDir(t)

	mustExec(t, conn, `DROP TABLE IF EXISTS schema_migrations, mig_users, mig_notes;`)

	preview := collectMigrationPreview(ctx, url, dir, "up", "")
	if preview.Error != "" {
		t.Fatalf("preview error: %s", preview.Error)
	}
	if len(preview.Files) != 2 {
		t.Fatalf("preview files = %d, want 2", len(preview.Files))
	}
	if !strings.Contains(preview.Files[0].Def, "CREATE TABLE mig_users") {
		t.Errorf("preview does not show the exact SQL: %q", preview.Files[0].Def)
	}

	out := applyMigrationAction(ctx, url, dir, "up", "")
	if out.Error != "" {
		t.Fatalf("up error: %s", out.Error)
	}
	if out.Status.Applied != 2 {
		t.Errorf("applied after up = %d, want 2", out.Status.Applied)
	}
	if len(out.Status.Pending) != 0 {
		t.Errorf("pending after up = %v, want none", out.Status.Pending)
	}

	// The revert path is one step, never the library's revert-everything
	// default.
	down := applyMigrationAction(ctx, url, dir, "down", "")
	if down.Error != "" {
		t.Fatalf("down error: %s", down.Error)
	}
	if down.Status.Applied != 1 {
		t.Errorf("applied after down = %d, want 1 (exactly one step)", down.Status.Applied)
	}
	if len(down.Status.Pending) != 1 {
		t.Errorf("pending after down = %v, want the reverted file", down.Status.Pending)
	}

	qctx, cancel := context.WithTimeout(ctx, sampleTimeout)
	defer cancel()
	n := 0
	err := conn.QueryRow(qctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_name='mig_notes';`).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("mig_notes still exists after reverting migration 2")
	}
}

// TestMigrationCaptureDrift snapshots the schema, alters it behind the
// tool's back, and checks Capture turns the difference into a pair.
func TestMigrationCaptureDrift(t *testing.T) {
	url := startPostgres(t)
	conn := openConn(t, url)
	ctx := context.Background()
	dir := migFixtureDir(t)

	mustExec(t, conn, `DROP TABLE IF EXISTS schema_migrations, mig_users, mig_notes;`)

	up := applyMigrationAction(ctx, url, dir, "up", "")
	if up.Error != "" {
		t.Fatalf("up error: %s", up.Error)
	}

	// Snapshot the current state the way migration itself does, then drift.
	db, _, closer, err := openMigDB(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer closer()
	live, err := introspect.Read(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = snapshot.Write(dir, 2, live)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, conn, `ALTER TABLE mig_users ADD COLUMN nickname text;`)

	status := collectMigrationStatus(ctx, url, dir)
	if status.Drift == nil || status.Drift.Error != "" {
		t.Fatalf("drift not detected: %+v", status.Drift)
	}
	if len(status.Drift.Changes) == 0 {
		t.Fatalf("no drift changes after an out-of-band ALTER")
	}

	// The preview shows the generated pair without writing anything.
	preview := collectMigrationPreview(ctx, url, dir, "capture", "nickname")
	if preview.Error != "" {
		t.Fatalf("capture preview error: %s", preview.Error)
	}
	if len(preview.Files) != 2 {
		t.Fatalf("capture preview files = %d, want the up/down pair", len(preview.Files))
	}
	if !strings.Contains(preview.Files[0].Def, "nickname") {
		t.Errorf("generated up SQL does not mention the drifted column: %q", preview.Files[0].Def)
	}
	entries, err := filepath.Glob(filepath.Join(dir, "003_*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("preview WROTE files: %v", entries)
	}

	// A hostile capture name must not escape the migrations directory.
	bad := collectMigrationPreview(ctx, url, dir, "capture", "../escape")
	if bad.Error == "" {
		t.Errorf("path-traversal capture name was accepted")
	}

	out := applyMigrationAction(ctx, url, dir, "capture", "nickname")
	if out.Error != "" {
		t.Fatalf("capture error: %s", out.Error)
	}
	if out.Status.Applied != 3 {
		t.Errorf("applied after capture = %d, want 3", out.Status.Applied)
	}
	if out.Status.Drift == nil || len(out.Status.Drift.Changes) != 0 {
		t.Errorf("drift should be clean after capture: %+v", out.Status.Drift)
	}
	pair, err := filepath.Glob(filepath.Join(dir, "003_nickname.*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pair) != 2 {
		t.Errorf("capture wrote %v, want the up/down pair", pair)
	}
}

// TestMigrationDirInConfig pins the read-only parsing of migration's own
// config: exact URL match, first match wins, missing is not an error.
func TestMigrationDirInConfig(t *testing.T) {
	src := `; migration configuration
(connection "postgres://a@h/db1" "/tmp/one" "first")
(connection "postgres://a@h/db2" "/tmp/two")
(connection "postgres://a@h/db1" "/tmp/shadowed")
`
	dir, err := migrationDirInConfig(src, "postgres://a@h/db2")
	if err != nil || dir != "/tmp/two" {
		t.Errorf("dir = %q err = %v, want /tmp/two", dir, err)
	}
	dir, err = migrationDirInConfig(src, "postgres://a@h/db1")
	if err != nil || dir != "/tmp/one" {
		t.Errorf("first match should win, got %q err = %v", dir, err)
	}
	dir, err = migrationDirInConfig(src, "postgres://a@h/other")
	if err != nil || dir != "" {
		t.Errorf("no match should be empty, got %q err = %v", dir, err)
	}
	dir, err = migrationDirInConfig("; only comments\n", "postgres://a@h/db1")
	if err != nil || dir != "" {
		t.Errorf("comments-only config should be empty, got %q err = %v", dir, err)
	}
}

// TestCollectorsReportConnectionFailureAsData checks the promise the whole UI
// rests on: a server that cannot be reached produces a visible message, never
// an empty screen that reads as a healthy database.
func TestCollectorsReportConnectionFailureAsData(t *testing.T) {
	// Port 1 is reserved and never listening; no container needed.
	const dead = "postgres://nobody:nobody@127.0.0.1:1/nothing?sslmode=disable&connect_timeout=2"
	ctx := context.Background()

	index := collectIndexReport(ctx, dead)
	if index.Error == "" {
		t.Errorf("index report hid a connection failure")
	}
	if index.Unused == nil || index.Duplicates == nil || index.Invalid == nil || index.SeqScans == nil {
		t.Errorf("index report returned nil collections, which serialize to null and break the page")
	}

	maint := collectMaintenance(ctx, dead)
	if maint.Error == "" {
		t.Errorf("maintenance report hid a connection failure")
	}
	if maint.NeedVacuum == nil || maint.LongTx == nil || maint.Sequences == nil || maint.OldestTables == nil {
		t.Errorf("maintenance report returned nil collections")
	}

	sessions := collectSessions(ctx, dead)
	if sessions.Error == "" {
		t.Errorf("sessions list hid a connection failure")
	}
	if sessions.Sessions == nil {
		t.Errorf("sessions list returned a nil collection")
	}

	_, err := blockingSnapshot(ctx, dead)
	if err == nil {
		t.Errorf("blocking snapshot hid a connection failure")
	}
}
