package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	sampleEvery     = time.Second
	sampleKeep      = time.Hour
	sampleTimeout   = 3 * time.Second
	connectTimeout  = 8 * time.Second
	reconnectAfter  = 5 * time.Second
	chartBuckets    = 100
	chartClassLimit = 10
	topSQLLimit     = 10
	maxQueryRunes   = 300
	pgssEvery       = 10 // pg_stat_statements snapshot every N samples
)

// requiredExtensions enrich the dashboard but are optional; when absent the
// UI shows the yellow badge with install hints instead of failing.
var requiredExtensions = []string{"pg_stat_statements"}

// activeRow is one active session captured in a sample. Besides the wait
// class, it carries the dimensions the chart can be sliced by.
type activeRow struct {
	class   string // wait event key (waitKey), or CPU when not waiting
	query   string
	queryID int64 // pg_stat_activity.query_id (PG14+), 0 when unavailable
	user    string
	app     string
	host    string
	db      string
}

// sliceKey returns the grouping key of a row for the chosen dimension. The
// menu of dimensions lives in the page; anything unknown falls back to waits.
func sliceKey(r activeRow, sliceBy string) string {
	value := ""
	switch sliceBy {
	case "sql":
		value = r.query
	case "users":
		value = r.user
	case "hosts":
		value = r.host
	case "applications":
		value = r.app
	case "databases":
		value = r.db
	default:
		value = r.class
	}
	if value == "" {
		return "(unset)"
	}
	return value
}

// sample is one capture of pg_stat_activity plus the cluster counters taken
// in the same second: connection counts by state and transaction rates.
type sample struct {
	at   time.Time
	rows []activeRow

	connActive int
	connIdleTx int // idle in transaction (incl. aborted)
	connIdle   int
	connOther  int

	commitsPS   float64
	rollbacksPS float64
	ratesValid  bool

	// Buffer traffic per second: volume, not a ratio. A hit ratio is pegged
	// at 100% whenever the only activity is our own sampling queries, which
	// read nothing from disk; the two rates always tell the truth, and the
	// ratio stays visually readable as the proportion between them.
	blksHitPS  float64
	blksReadPS float64
	ioValid    bool
}

// pgssSnap is one periodic capture of pg_stat_statements counters, keyed by
// queryid; window deltas between two snaps become calls/s, rows/call and
// ms/call for the Top SQL table.
type pgssSnap struct {
	at    time.Time
	stats map[int64]pgssStat
}

type pgssStat struct {
	calls   int64
	rows    int64
	totalMS float64
}

// Sampler keeps a sliding in-memory window of activity samples for one
// database, Performance Insights style: average active sessions over time,
// sliced by wait class. History lives only while the app runs, by design.
type Sampler struct {
	mu        sync.Mutex
	samples   []sample
	status    string
	missing   []string
	preloaded []string // subset of missing already in shared_preload_libraries
	maxConns  int
	dbCount   int
	cancel    context.CancelFunc
	done      chan struct{}

	pgss     []pgssSnap
	blocking []blockerOut

	// previous cumulative counters, for the per-second rates. Touched only by
	// the sampling goroutine.
	prevAt        time.Time
	prevCommits   int64
	prevRollbacks int64
	prevBlksRead  int64
	prevBlksHit   int64
	prevValid     bool
}

func newSampler(url string) *Sampler {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Sampler{
		status: "connecting",
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go s.run(ctx, url)
	return s
}

// Stop ends the sampling goroutine and waits for it to close its connection.
func (s *Sampler) Stop() {
	s.cancel()
	<-s.done
}

func (s *Sampler) setStatus(status string) {
	s.mu.Lock()
	s.status = status
	s.mu.Unlock()
}

// run is the sampling loop: connect, detect optional extensions, then one
// activity sample per second. Any error degrades to a visible status and a
// reconnect with backoff; a monitoring tool must not die with its patient.
func (s *Sampler) run(ctx context.Context, url string) {
	defer close(s.done)
	for {
		err := s.sampleUntilError(ctx, url)
		if ctx.Err() != nil {
			return
		}
		s.setStatus(fmt.Sprintf("reconnecting: %v", err))
		debugf("event=sampler_reconnect err=%q", err)

		select {
		case <-ctx.Done():
			return
		case <-time.After(reconnectAfter):
		}
	}
}

func (s *Sampler) sampleUntilError(ctx context.Context, url string) error {
	connCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	conn, err := pgx.Connect(connCtx, url)
	cancel()
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), sampleTimeout)
		_ = conn.Close(closeCtx)
		cancel()
	}()

	missing, preloaded, err := missingExtensions(ctx, conn)
	if err != nil {
		return err
	}
	maxConns, err := maxConnections(ctx, conn)
	if err != nil {
		return err
	}
	dbCount, err := databaseCount(ctx, conn)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.missing = missing
	s.preloaded = preloaded
	s.maxConns = maxConns
	s.dbCount = dbCount
	s.status = "sampling"
	s.mu.Unlock()
	debugf("event=sampler_connected missing_extensions=%q preloaded=%q max_connections=%d",
		strings.Join(missing, ","), strings.Join(preloaded, ","), maxConns)

	// pg_stat_activity.query_id needs PG14+; older servers sample without it.
	version, err := serverVersionNum(ctx, conn)
	if err != nil {
		return err
	}
	activityQuery := sqlActivity
	if version < 140000 {
		activityQuery = sqlActivityNoQueryID
	}
	pgssAvailable := len(missing) == 0

	ticker := time.NewTicker(sampleEvery)
	defer ticker.Stop()
	tick := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}

		smp, err := sampleActivity(ctx, conn, activityQuery)
		if err != nil {
			return err
		}
		err = s.sampleCounters(ctx, conn, &smp)
		if err != nil {
			return err
		}
		if len(smp.rows) > 0 {
			debugf("event=sample active=%d", len(smp.rows))
		}
		s.push(smp)

		// pg_blocking_pids is expensive, so it only runs when the sample just
		// showed someone waiting on a lock.
		err = s.sampleBlocking(ctx, conn, waitingOnLock(smp.rows))
		if err != nil {
			return err
		}

		tick++
		if pgssAvailable && tick%pgssEvery == 0 {
			err = s.samplePGSS(ctx, conn)
			if err != nil {
				return err
			}
		}
	}
}

// waitingOnLock reports whether any sampled session is waiting on a lock.
func waitingOnLock(rows []activeRow) bool {
	for _, r := range rows {
		if strings.HasPrefix(r.class, "Lock:") || r.class == "Lock" {
			return true
		}
	}
	return false
}

// sampleBlocking refreshes the blocking tree, or clears it when nothing is
// waiting on a lock (the common case, at no query cost).
func (s *Sampler) sampleBlocking(ctx context.Context, conn *pgx.Conn, waiting bool) error {
	var tree []blockerOut
	if waiting {
		var err error
		tree, err = collectBlocking(ctx, conn)
		if err != nil {
			return err
		}
		if len(tree) > 0 {
			debugf("event=blocking blockers=%d", len(tree))
		}
	}
	s.mu.Lock()
	s.blocking = tree
	s.mu.Unlock()
	return nil
}

func serverVersionNum(ctx context.Context, conn *pgx.Conn) (int, error) {
	qctx, cancel := context.WithTimeout(ctx, sampleTimeout)
	defer cancel()

	v := 0
	err := conn.QueryRow(qctx,
		`SELECT current_setting('server_version_num')::int;`).Scan(&v)
	return v, err
}

// samplePGSS captures the heaviest pg_stat_statements rows. The full view can
// hold thousands of entries; the top by total time is what the ranking needs.
func (s *Sampler) samplePGSS(ctx context.Context, conn *pgx.Conn) error {
	qctx, cancel := context.WithTimeout(ctx, sampleTimeout)
	defer cancel()

	rows, err := conn.Query(qctx, `SELECT
			queryid,        -- 1
			calls,          -- 2
			rows,           -- 3
			total_exec_time -- 4
		FROM pg_stat_statements
		WHERE queryid IS NOT NULL
		ORDER BY total_exec_time DESC
		LIMIT 500;`)
	if err != nil {
		return err
	}
	defer rows.Close()

	snap := pgssSnap{at: time.Now(), stats: map[int64]pgssStat{}}
	for rows.Next() {
		var id int64
		var st pgssStat
		err = rows.Scan(
			&id,         // 1
			&st.calls,   // 2
			&st.rows,    // 3
			&st.totalMS, // 4
		)
		if err != nil {
			return err
		}
		snap.stats[id] = st
	}
	if rows.Err() != nil {
		return rows.Err()
	}

	s.mu.Lock()
	s.pgss = append(s.pgss, snap)
	cutoff := snap.at.Add(-sampleKeep)
	first := 0
	for first < len(s.pgss) && s.pgss[first].at.Before(cutoff) {
		first++
	}
	s.pgss = s.pgss[first:]
	s.mu.Unlock()
	return nil
}

// sampleCounters fills smp with the transaction rates derived from the
// cluster-wide cumulative counters. A negative delta (stats reset) just marks
// the rates invalid for this sample.
func (s *Sampler) sampleCounters(ctx context.Context, conn *pgx.Conn, smp *sample) error {
	qctx, cancel := context.WithTimeout(ctx, sampleTimeout)
	defer cancel()

	var commits, rollbacks, blksRead, blksHit int64
	err := conn.QueryRow(qctx, `SELECT
			COALESCE(sum(xact_commit), 0)::bigint,   -- 1
			COALESCE(sum(xact_rollback), 0)::bigint, -- 2
			COALESCE(sum(blks_read), 0)::bigint,     -- 3
			COALESCE(sum(blks_hit), 0)::bigint       -- 4
		FROM pg_stat_database;`).Scan(
		&commits,   // 1
		&rollbacks, // 2
		&blksRead,  // 3
		&blksHit,   // 4
	)
	if err != nil {
		return err
	}

	dt := smp.at.Sub(s.prevAt).Seconds()
	if s.prevValid && dt > 0 && commits >= s.prevCommits && rollbacks >= s.prevRollbacks {
		smp.commitsPS = round2(float64(commits-s.prevCommits) / dt)
		smp.rollbacksPS = round2(float64(rollbacks-s.prevRollbacks) / dt)
		smp.ratesValid = true
	}
	dRead := blksRead - s.prevBlksRead
	dHit := blksHit - s.prevBlksHit
	if s.prevValid && dt > 0 && dRead >= 0 && dHit >= 0 {
		smp.blksHitPS = round2(float64(dHit) / dt)
		smp.blksReadPS = round2(float64(dRead) / dt)
		smp.ioValid = true
	}
	s.prevAt = smp.at
	s.prevCommits = commits
	s.prevRollbacks = rollbacks
	s.prevBlksRead = blksRead
	s.prevBlksHit = blksHit
	s.prevValid = true
	return nil
}

func databaseCount(ctx context.Context, conn *pgx.Conn) (int, error) {
	qctx, cancel := context.WithTimeout(ctx, sampleTimeout)
	defer cancel()

	n := 0
	err := conn.QueryRow(qctx,
		`SELECT count(*) FROM pg_database WHERE NOT datistemplate;`).Scan(&n)
	return n, err
}

func maxConnections(ctx context.Context, conn *pgx.Conn) (int, error) {
	qctx, cancel := context.WithTimeout(ctx, sampleTimeout)
	defer cancel()

	var v string
	err := conn.QueryRow(qctx, `SHOW max_connections;`).Scan(&v)
	if err != nil {
		return 0, err
	}
	n := 0
	_, err = fmt.Sscanf(v, "%d", &n)
	if err != nil {
		return 0, nil
	}
	return n, nil
}

const sqlActivity = `SELECT
		COALESCE(state, ''),                    -- 1
		COALESCE(wait_event_type, ''),          -- 2
		COALESCE(wait_event, ''),               -- 3
		COALESCE(query, ''),                    -- 4
		backend_type,                           -- 5
		COALESCE(query_id, 0),                  -- 6
		COALESCE(usename, ''),                  -- 7
		COALESCE(application_name, ''),         -- 8
		COALESCE(host(client_addr), 'local'),   -- 9
		COALESCE(datname, '')                   -- 10
	FROM pg_stat_activity
	WHERE pid <> pg_backend_pid()
	AND backend_type IN ('client backend', 'parallel worker');`

// sqlActivityNoQueryID is the PG13-and-older variant: no query_id column.
const sqlActivityNoQueryID = `SELECT
		COALESCE(state, ''),                    -- 1
		COALESCE(wait_event_type, ''),          -- 2
		COALESCE(wait_event, ''),               -- 3
		COALESCE(query, ''),                    -- 4
		backend_type,                           -- 5
		0::bigint,                              -- 6 (query_id placeholder)
		COALESCE(usename, ''),                  -- 7
		COALESCE(application_name, ''),         -- 8
		COALESCE(host(client_addr), 'local'),   -- 9
		COALESCE(datname, '')                   -- 10
	FROM pg_stat_activity
	WHERE pid <> pg_backend_pid()
	AND backend_type IN ('client backend', 'parallel worker');`

// sampleActivity captures one second of pg_stat_activity: the active sessions
// feed the load chart, and every client backend is counted by state for the
// connections chart.
func sampleActivity(ctx context.Context, conn *pgx.Conn, query string) (sample, error) {
	qctx, cancel := context.WithTimeout(ctx, sampleTimeout)
	defer cancel()

	smp := sample{at: time.Now()}
	rows, err := conn.Query(qctx, query)
	if err != nil {
		return smp, err
	}
	defer rows.Close()

	for rows.Next() {
		var state, typ, event, queryText, backend string
		var user, app, host, db string
		var queryID int64
		err = rows.Scan(
			&state,     // 1
			&typ,       // 2
			&event,     // 3
			&queryText, // 4
			&backend,   // 5
			&queryID,   // 6
			&user,      // 7
			&app,       // 8
			&host,      // 9
			&db,        // 10
		)
		if err != nil {
			return smp, err
		}

		if state == "active" {
			smp.rows = append(smp.rows, activeRow{
				class:   waitKey(typ, event),
				query:   queryText,
				queryID: queryID,
				user:    user,
				app:     app,
				host:    host,
				db:      db,
			})
		}
		if backend != "client backend" {
			continue
		}
		switch state {
		case "active":
			smp.connActive++
		case "idle in transaction", "idle in transaction (aborted)":
			smp.connIdleTx++
		case "idle":
			smp.connIdle++
		default:
			smp.connOther++
		}
	}
	return smp, rows.Err()
}

// waitKey labels one sampled session the way Performance Insights does for
// PostgreSQL: the specific wait event prefixed by its class ("IO:WALSync",
// "LWLock:WALWrite"), and plain "CPU" for a session that is not waiting.
func waitKey(typ, event string) string {
	if typ == "" {
		return "CPU"
	}
	if event == "" {
		return typ
	}
	return typ + ":" + event
}

// missingExtensions reports which optional extensions are not created in the
// CONNECTED database (pg_extension is per-database), and which of those are
// already in shared_preload_libraries — for them a plain CREATE EXTENSION is
// enough, no restart.
func missingExtensions(ctx context.Context, conn *pgx.Conn) (missing, preloaded []string, err error) {
	qctx, cancel := context.WithTimeout(ctx, sampleTimeout)
	defer cancel()

	installed := map[string]bool{}
	rows, err := conn.Query(qctx, `SELECT extname FROM pg_extension;`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		err = rows.Scan(&name)
		if err != nil {
			return nil, nil, err
		}
		installed[name] = true
	}
	if rows.Err() != nil {
		return nil, nil, rows.Err()
	}

	var spl string
	err = conn.QueryRow(qctx, `SHOW shared_preload_libraries;`).Scan(&spl)
	if err != nil {
		return nil, nil, err
	}
	loaded := map[string]bool{}
	for lib := range strings.SplitSeq(spl, ",") {
		loaded[strings.TrimSpace(lib)] = true
	}

	for _, want := range requiredExtensions {
		if installed[want] {
			continue
		}
		missing = append(missing, want)
		if loaded[want] {
			preloaded = append(preloaded, want)
		}
	}
	return missing, preloaded, nil
}

func (s *Sampler) push(smp sample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples = append(s.samples, smp)

	cutoff := smp.at.Add(-sampleKeep)
	first := 0
	for first < len(s.samples) && s.samples[first].at.Before(cutoff) {
		first++
	}
	s.samples = s.samples[first:]
}

// bucketOut is one time slice of the stacked chart: average active sessions
// per wait class.
type bucketOut struct {
	T int64              `json:"t"` // bucket start, unix milliseconds
	V map[string]float64 `json:"v"`
}

// topSQLOut ranks one entry of the selected Top dimension (a query, a user, a
// host, ...) by its share of the sampled load. ByClass splits that load across
// wait classes, Performance Insights style, so the bar reuses the chart colors.
type topSQLOut struct {
	Query   string             `json:"query"` // the entry key: query text, user name, ...
	AAS     float64            `json:"aas"`
	Pct     float64            `json:"pct"`
	ByClass map[string]float64 `json:"byClass"`

	// pg_stat_statements enrichment over the visible window; HasStats is
	// false when the extension is missing or the query was not matched.
	HasStats    bool    `json:"hasStats"`
	CallsPS     float64 `json:"callsPS"`
	RowsPerCall float64 `json:"rowsPerCall"`
	MSPerCall   float64 `json:"msPerCall"`

	queryID int64
}

// dashOut is the dashboard snapshot handed to the UI (and, later, to the IPC
// endpoint for AI agents: one place, one serialization).
type dashOut struct {
	Connected     bool   `json:"connected"`
	Status        string `json:"status"`
	Title         string `json:"title"`
	URL           string `json:"url"`
	WindowSeconds int    `json:"windowSeconds"`
	BucketSeconds int    `json:"bucketSeconds"`
	// Samples is how many activity captures landed in the window; it lets the
	// UI distinguish "sampling an idle database" from "not sampling at all".
	Samples int    `json:"samples"`
	SliceBy string `json:"sliceBy"`
	TopBy   string `json:"topBy"`
	// Classes are the chart series for the current slice dimension;
	// WaitClasses are always wait events, for the Top SQL breakdown.
	Classes     []string    `json:"classes"`
	WaitClasses []string    `json:"waitClasses"`
	Buckets     []bucketOut `json:"buckets"`
	TopSQL      []topSQLOut `json:"topSQL"`
	Missing     []string    `json:"missingExtensions"`
	// MissingPreloaded lists the missing extensions whose library is already
	// preloaded: installing them is one CREATE EXTENSION, no server restart.
	MissingPreloaded []string `json:"missingPreloaded"`

	// Counter charts, same closed-bucket timeline as the load chart.
	ConnClasses    []string    `json:"connClasses"`
	Conns          []bucketOut `json:"conns"`
	MaxConnections int         `json:"maxConnections"`
	// DatabaseCount tells the UI whether per-database views are worth
	// offering: a single-database cluster has nothing to compare.
	DatabaseCount int         `json:"databaseCount"`
	TPSClasses    []string    `json:"tpsClasses"`
	TPS           []bucketOut `json:"tps"`
	IOClasses     []string    `json:"ioClasses"`
	IO            []bucketOut `json:"io"`

	// Blocking is the live lock-wait tree (not bucketed): who blocks whom
	// right now. Empty means nobody is waiting on a lock.
	Blocking []blockerOut `json:"blocking"`
}

// Connection-state and transaction-rate series share fixed class names; the
// UI maps them to fixed colors.
var (
	connClasses = []string{"active", "idle in transaction", "idle", "other"}
	tpsClasses  = []string{"commits/s", "rollbacks/s"}
	ioClasses   = []string{"from cache/s", "from disk/s"}
)

// Snapshot aggregates the sliding window ending now into chart buckets and a
// top-SQL ranking.
func (s *Sampler) Snapshot(now time.Time, windowSeconds int, sliceBy, topBy string) dashOut {
	s.mu.Lock()
	samples := s.samples
	status := s.status
	missing := s.missing
	preloaded := s.preloaded
	pgss := s.pgss
	blocking := s.blocking
	s.mu.Unlock()

	out := aggregate(samples, now, windowSeconds, sliceBy, topBy)
	enrichTopSQL(out.TopSQL, pgss, now.Add(-time.Duration(out.WindowSeconds)*time.Second))
	out.Connected = status == "sampling"
	out.Status = status
	out.Missing = missing
	if out.Missing == nil {
		out.Missing = []string{}
	}
	out.MissingPreloaded = preloaded
	if out.MissingPreloaded == nil {
		out.MissingPreloaded = []string{}
	}
	out.Blocking = blocking
	if out.Blocking == nil {
		out.Blocking = []blockerOut{}
	}
	s.mu.Lock()
	out.MaxConnections = s.maxConns
	out.DatabaseCount = s.dbCount
	s.mu.Unlock()
	return out
}

// rankAndFold orders series by load, keeps the heaviest chartClassLimit and
// folds the tail into "Other". CPU, when present, is always kept and stacked
// first, the way Performance Insights draws it. It returns the ordered series
// and the mapping that sends a dropped series to "Other".
func rankAndFold(seen map[string]bool, total map[string]int) ([]string, func(string) string) {
	var ranked []string
	for c := range seen {
		ranked = append(ranked, c)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if total[ranked[i]] != total[ranked[j]] {
			return total[ranked[i]] > total[ranked[j]]
		}
		return ranked[i] < ranked[j]
	})

	kept := map[string]bool{}
	if seen["CPU"] {
		kept["CPU"] = true
	}
	for _, c := range ranked {
		if len(kept) >= chartClassLimit {
			break
		}
		kept[c] = true
	}

	var out []string
	hasOther := false
	if seen["CPU"] {
		out = append(out, "CPU")
	}
	for _, c := range ranked {
		if c == "CPU" {
			continue
		}
		if kept[c] {
			out = append(out, c)
			continue
		}
		hasOther = true
	}
	if hasOther {
		out = append(out, "Other")
	}

	mapTo := func(c string) string {
		if kept[c] {
			return c
		}
		return "Other"
	}
	return out, mapTo
}

// aggregate is the pure core of Snapshot, separated for testing.
//
// Buckets align to ABSOLUTE time boundaries (multiples of the bucket size),
// not to "now": a sample lands in one bucket and stays there forever, so the
// past never redraws differently between refreshes. The in-progress bucket is
// not charted at all — its average wobbles as samples arrive, which reads as
// the chart rewriting itself; it appears once its period closes.
func aggregate(samples []sample, now time.Time, windowSeconds int, sliceBy, topBy string) dashOut {
	if windowSeconds < chartBuckets {
		windowSeconds = chartBuckets
	}
	bucketSeconds := windowSeconds / chartBuckets
	bs := time.Duration(bucketSeconds) * time.Second
	end := now.Truncate(bs) // exclusive: the forming bucket stays out
	start := end.Add(-time.Duration(chartBuckets) * bs)

	type agg struct {
		count   map[string]int
		samples int
		conns   [4]int     // sums by connClasses order
		rates   [2]float64 // sums by tpsClasses order
		rateN   int
		io      [2]float64 // sums by ioClasses order
		ioN     int
	}
	buckets := make([]agg, chartBuckets)
	for i := range buckets {
		buckets[i].count = map[string]int{}
	}

	classSeen := map[string]bool{}
	// waitSeen tracks wait classes regardless of the chart's dimension: the
	// Top SQL bars always break a query down by what it waited on.
	waitSeen := map[string]bool{}
	waitTotal := map[string]int{}
	// The Top list groups by its own dimension, independent of the chart's.
	topCount := map[string]int{}
	topByWait := map[string]map[string]int{}
	queryIDOf := map[string]int64{}
	// topDisplay maps a grouping key to the text shown for it, for the keys
	// that are not the text itself (a query id).
	topDisplay := map[string]string{}
	totalSamples := 0
	totalRows := 0

	for _, smp := range samples {
		if smp.at.Before(start) || !smp.at.Before(end) {
			continue
		}
		idx := int(smp.at.Sub(start) / bs)
		if idx >= chartBuckets {
			idx = chartBuckets - 1
		}
		buckets[idx].samples++
		totalSamples++
		buckets[idx].conns[0] += smp.connActive
		buckets[idx].conns[1] += smp.connIdleTx
		buckets[idx].conns[2] += smp.connIdle
		buckets[idx].conns[3] += smp.connOther
		if smp.ratesValid {
			buckets[idx].rates[0] += smp.commitsPS
			buckets[idx].rates[1] += smp.rollbacksPS
			buckets[idx].rateN++
		}
		if smp.ioValid {
			buckets[idx].io[0] += smp.blksHitPS
			buckets[idx].io[1] += smp.blksReadPS
			buckets[idx].ioN++
		}
		for _, row := range smp.rows {
			row.query = normalizeQuery(row.query)
			key := sliceKey(row, sliceBy)
			buckets[idx].count[key]++
			classSeen[key] = true
			waitSeen[row.class] = true
			waitTotal[row.class]++
			totalRows++

			topKey := sliceKey(row, topBy)
			// Ranking SQL, the query id is the better identity: the same
			// statement with different literals is one query, and
			// pg_stat_activity hands out the raw text, so grouping by text
			// would list it once per set of values. The text of the first
			// sample seen is what gets displayed.
			if topBy == "sql" && row.queryID != 0 {
				id := row.queryID
				topKey = "id:" + strconv.FormatInt(id, 10)
				_, seen := topDisplay[topKey]
				if !seen {
					topDisplay[topKey] = row.query
				}
				queryIDOf[topKey] = id
			}
			topCount[topKey]++
			if topByWait[topKey] == nil {
				topByWait[topKey] = map[string]int{}
			}
			topByWait[topKey][row.class]++
		}
	}

	// Performance Insights keeps the chart legible by showing the top series
	// and folding the tail into a gray "Other".
	classTotal := map[string]int{}
	for _, b := range buckets {
		for class, n := range b.count {
			classTotal[class] += n
		}
	}
	classes, mapClass := rankAndFold(classSeen, classTotal)
	// The Top SQL breakdown always speaks in wait classes, whatever the chart
	// is sliced by.
	waitClasses, mapWait := rankAndFold(waitSeen, waitTotal)
	if classes == nil {
		// nil marshals to JSON null and a null breaks the UI's iteration; an
		// idle window is an empty list, not an absence.
		classes = []string{}
	}

	outBuckets := make([]bucketOut, chartBuckets)
	outConns := make([]bucketOut, chartBuckets)
	outTPS := make([]bucketOut, chartBuckets)
	outIO := make([]bucketOut, chartBuckets)
	for i, b := range buckets {
		t := start.Add(time.Duration(i) * bs).UnixMilli()

		v := map[string]float64{}
		cv := map[string]float64{}
		tv := map[string]float64{}
		hv := map[string]float64{}
		if b.ioN > 0 {
			for j, name := range ioClasses {
				if b.io[j] > 0 {
					hv[name] = round2(b.io[j] / float64(b.ioN))
				}
			}
		}
		if b.samples > 0 {
			merged := map[string]int{}
			for class, n := range b.count {
				merged[mapClass(class)] += n
			}
			for class, n := range merged {
				v[class] = round2(float64(n) / float64(b.samples))
			}
			for j, name := range connClasses {
				if b.conns[j] > 0 {
					cv[name] = round2(float64(b.conns[j]) / float64(b.samples))
				}
			}
		}
		if b.rateN > 0 {
			for j, name := range tpsClasses {
				if b.rates[j] > 0 {
					tv[name] = round2(b.rates[j] / float64(b.rateN))
				}
			}
		}
		outBuckets[i] = bucketOut{T: t, V: v}
		outConns[i] = bucketOut{T: t, V: cv}
		outTPS[i] = bucketOut{T: t, V: tv}
		outIO[i] = bucketOut{T: t, V: hv}
	}

	var top []topSQLOut
	for q, n := range topCount {
		label := q
		text, ok := topDisplay[q]
		if ok {
			label = text
		}
		entry := topSQLOut{Query: label, ByClass: map[string]float64{}, queryID: queryIDOf[q]}
		if totalSamples > 0 {
			entry.AAS = round2(float64(n) / float64(totalSamples))
			merged := map[string]int{}
			for class, cn := range topByWait[q] {
				merged[mapWait(class)] += cn
			}
			for class, cn := range merged {
				entry.ByClass[class] = round2(float64(cn) / float64(totalSamples))
			}
		}
		if totalRows > 0 {
			entry.Pct = round2(100 * float64(n) / float64(totalRows))
		}
		top = append(top, entry)
	}
	sort.Slice(top, func(i, j int) bool {
		if top[i].AAS != top[j].AAS {
			return top[i].AAS > top[j].AAS
		}
		return top[i].Query < top[j].Query
	})
	if len(top) > topSQLLimit {
		top = top[:topSQLLimit]
	}
	if top == nil {
		top = []topSQLOut{}
	}

	if waitClasses == nil {
		waitClasses = []string{}
	}
	return dashOut{
		WindowSeconds: windowSeconds,
		BucketSeconds: bucketSeconds,
		SliceBy:       sliceBy,
		TopBy:         topBy,
		Samples:       totalSamples,
		Classes:       classes,
		WaitClasses:   waitClasses,
		Buckets:       outBuckets,
		TopSQL:        top,
		ConnClasses:   connClasses,
		Conns:         outConns,
		TPSClasses:    tpsClasses,
		TPS:           outTPS,
		IOClasses:     ioClasses,
		IO:            outIO,
	}
}

// enrichTopSQL fills the pg_stat_statements columns of each ranked query from
// the counter deltas between the oldest and newest snapshots inside the
// window. Pure, for testing.
func enrichTopSQL(top []topSQLOut, snaps []pgssSnap, windowStart time.Time) {
	var first, last *pgssSnap
	for i := range snaps {
		if snaps[i].at.Before(windowStart) {
			continue
		}
		if first == nil {
			first = &snaps[i]
		}
		last = &snaps[i]
	}
	if first == nil || last == nil || first == last {
		return
	}
	dt := last.at.Sub(first.at).Seconds()
	if dt <= 0 {
		return
	}

	for i := range top {
		id := top[i].queryID
		if id == 0 {
			continue
		}
		newer, ok := last.stats[id]
		if !ok {
			continue
		}
		// A query absent from the older snapshot started counting mid-window;
		// its delta is everything it has.
		older := first.stats[id]
		dCalls := newer.calls - older.calls
		if dCalls <= 0 {
			continue
		}
		top[i].HasStats = true
		top[i].CallsPS = round2(float64(dCalls) / dt)
		top[i].RowsPerCall = round2(float64(newer.rows-older.rows) / float64(dCalls))
		top[i].MSPerCall = round2((newer.totalMS - older.totalMS) / float64(dCalls))
	}
}

// normalizeQuery trims and caps a query for display grouping; runes, not
// bytes, so multi-byte characters are never split.
func normalizeQuery(q string) string {
	q = strings.TrimSpace(q)
	r := []rune(q)
	if len(r) > maxQueryRunes {
		return string(r[:maxQueryRunes]) + "..."
	}
	return q
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}
