package main

import (
	"fmt"
	"testing"
	"time"
)

func TestAggregate(t *testing.T) {
	// 12:00:02: the forming bucket [12:00:00, 12:00:03) is excluded, so the
	// last charted bucket spans [11:59:57, 12:00:00).
	now := time.Date(2026, 7, 25, 12, 0, 2, 0, time.UTC)
	window := 300 // 5 min, 100 buckets of 3s

	// Two samples in the last charted bucket: one with two active sessions
	// (CPU + Lock), one with a single CPU session running the same query.
	samples := []sample{
		{at: now.Add(-5 * time.Second), rows: []activeRow{
			{class: "CPU", query: "SELECT 1"},
			{class: "Lock", query: "UPDATE t SET x = 1"},
		}, connActive: 2, connIdle: 4, commitsPS: 10, ratesValid: true},
		{at: now.Add(-3 * time.Second), rows: []activeRow{
			{class: "CPU", query: "SELECT 1"},
		}, connActive: 1, connIdle: 4},
		// Outside the window: ignored.
		{at: now.Add(-10 * time.Minute), rows: []activeRow{
			{class: "IO", query: "COPY big FROM stdin"},
		}},
	}

	out := aggregate(samples, now, window, "waits")

	if out.BucketSeconds != 3 {
		t.Errorf("bucketSeconds = %d, want 3", out.BucketSeconds)
	}
	if len(out.Buckets) != chartBuckets {
		t.Fatalf("buckets = %d, want %d", len(out.Buckets), chartBuckets)
	}
	if len(out.Classes) != 2 || out.Classes[0] != "CPU" || out.Classes[1] != "Lock" {
		t.Errorf("classes = %v, want [CPU Lock]", out.Classes)
	}

	last := out.Buckets[chartBuckets-1]
	// Two samples in that bucket, 3 CPU+Lock rows total: CPU 1.0, Lock 0.5.
	if last.V["CPU"] != 1.0 {
		t.Errorf("last bucket CPU = %v, want 1.0", last.V["CPU"])
	}
	if last.V["Lock"] != 0.5 {
		t.Errorf("last bucket Lock = %v, want 0.5", last.V["Lock"])
	}

	if len(out.TopSQL) != 2 {
		t.Fatalf("topSQL = %v, want 2 entries", out.TopSQL)
	}
	// SELECT 1 appears in 2 of 2 samples: AAS 1.0, 2 of 3 rows: ~66.7%.
	if out.TopSQL[0].Query != "SELECT 1" {
		t.Errorf("top query = %q, want SELECT 1", out.TopSQL[0].Query)
	}
	if out.TopSQL[0].AAS != 1.0 {
		t.Errorf("top AAS = %v, want 1.0", out.TopSQL[0].AAS)
	}
	if out.TopSQL[0].Pct < 66 || out.TopSQL[0].Pct > 67 {
		t.Errorf("top pct = %v, want ~66.7", out.TopSQL[0].Pct)
	}
	// SELECT 1 always ran on CPU: its load splits entirely into that class.
	if out.TopSQL[0].ByClass["CPU"] != 1.0 {
		t.Errorf("top byClass CPU = %v, want 1.0", out.TopSQL[0].ByClass["CPU"])
	}
	if out.TopSQL[1].ByClass["Lock"] != 0.5 {
		t.Errorf("update byClass Lock = %v, want 0.5", out.TopSQL[1].ByClass["Lock"])
	}

	// Counter charts share the bucket: connections average by state, and the
	// transaction rate averages only over rate-valid samples.
	lastConns := out.Conns[chartBuckets-1]
	if lastConns.V["active"] != 1.5 || lastConns.V["idle"] != 4.0 {
		t.Errorf("conns = %v, want active 1.5 idle 4.0", lastConns.V)
	}
	lastTPS := out.TPS[chartBuckets-1]
	if lastTPS.V["commits/s"] != 10.0 {
		t.Errorf("tps = %v, want commits/s 10.0", lastTPS.V)
	}
}

func TestWaitKey(t *testing.T) {
	cases := []struct {
		typ, event, want string
	}{
		{"", "", "CPU"},
		{"IO", "WALSync", "IO:WALSync"},
		{"Lock", "", "Lock"},
	}
	for _, c := range cases {
		got := waitKey(c.typ, c.event)
		if got != c.want {
			t.Errorf("waitKey(%q, %q) = %q, want %q", c.typ, c.event, got, c.want)
		}
	}
}

func TestAggregateFoldsTailIntoOther(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 2, 0, time.UTC)

	// 12 distinct wait events with strictly decreasing weight: the two
	// lightest must fold into "Other".
	var rows []activeRow
	for i := 1; i <= 12; i++ {
		for range 13 - i {
			rows = append(rows, activeRow{
				class: fmt.Sprintf("IO:W%02d", i),
				query: "SELECT 1",
			})
		}
	}
	samples := []sample{{at: now.Add(-5 * time.Second), rows: rows}}

	out := aggregate(samples, now, 300, "waits")

	if len(out.Classes) != chartClassLimit+1 {
		t.Fatalf("classes = %d (%v), want %d + Other",
			len(out.Classes), out.Classes, chartClassLimit)
	}
	if out.Classes[len(out.Classes)-1] != "Other" {
		t.Errorf("last class = %q, want Other", out.Classes[len(out.Classes)-1])
	}
	if out.Classes[0] != "IO:W01" {
		t.Errorf("first class = %q, want the heaviest (IO:W01)", out.Classes[0])
	}

	// The folded tail: W11 (2 rows) + W12 (1 row) over 1 sample = 3.0, in the
	// last charted bucket [11:59:55, 12:00:00).
	last := out.Buckets[chartBuckets-1]
	if last.V["Other"] != 3.0 {
		t.Errorf("Other = %v, want 3.0", last.V["Other"])
	}
}

// TestAggregateStableBuckets is the regression for the "past keeps being
// rewritten" bug: as the window slides, a sample must stay in the same
// absolute-time bucket with the same value.
func TestAggregateStableBuckets(t *testing.T) {
	base := time.Date(2026, 7, 25, 12, 0, 4, 0, time.UTC)
	samples := []sample{{at: base.Add(-5 * time.Second), rows: []activeRow{
		{class: "CPU", query: "SELECT 1"},
	}}}

	a := aggregate(samples, base, 300, "waits")
	b := aggregate(samples, base.Add(2*time.Second), 300, "waits")

	find := func(out dashOut) *bucketOut {
		for i := range out.Buckets {
			if out.Buckets[i].V["CPU"] > 0 {
				return &out.Buckets[i]
			}
		}
		return nil
	}
	ba := find(a)
	bb := find(b)
	if ba == nil || bb == nil {
		t.Fatal("sample bucket not found in one of the aggregations")
	}
	if ba.T != bb.T {
		t.Errorf("bucket start moved: %d != %d", ba.T, bb.T)
	}
	if ba.V["CPU"] != bb.V["CPU"] {
		t.Errorf("bucket value changed: %v != %v", ba.V["CPU"], bb.V["CPU"])
	}
}

func TestEnrichTopSQL(t *testing.T) {
	base := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	snaps := []pgssSnap{
		// Before the window: must be ignored.
		{at: base.Add(-10 * time.Minute), stats: map[int64]pgssStat{
			42: {calls: 1, rows: 1, totalMS: 1},
		}},
		{at: base.Add(-60 * time.Second), stats: map[int64]pgssStat{
			42: {calls: 100, rows: 1000, totalMS: 500},
		}},
		{at: base.Add(-30 * time.Second), stats: map[int64]pgssStat{
			42: {calls: 160, rows: 1300, totalMS: 800},
			99: {calls: 30, rows: 30, totalMS: 90},
		}},
	}

	top := []topSQLOut{
		{Query: "SELECT a", queryID: 42},
		{Query: "SELECT new", queryID: 99}, // absent from the older snapshot
		{Query: "no id", queryID: 0},
	}
	enrichTopSQL(top, snaps, base.Add(-5*time.Minute))

	// 60 calls over 30s = 2/s; 300 rows / 60 calls = 5; 300ms / 60 = 5.
	if !top[0].HasStats || top[0].CallsPS != 2 || top[0].RowsPerCall != 5 || top[0].MSPerCall != 5 {
		t.Errorf("enriched = %+v, want 2 calls/s, 5 rows/call, 5 ms/call", top[0])
	}
	// Mid-window arrival: its whole count is the delta.
	if !top[1].HasStats || top[1].CallsPS != 1 {
		t.Errorf("new query = %+v, want 1 calls/s", top[1])
	}
	if top[2].HasStats {
		t.Error("query without id must not be enriched")
	}
}

// TestAggregateSliceBy checks the chart regroups by the chosen dimension
// while the Top SQL breakdown stays in wait classes.
func TestAggregateSliceBy(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 2, 0, time.UTC)
	samples := []sample{{at: now.Add(-5 * time.Second), rows: []activeRow{
		{class: "CPU", query: "SELECT 1", user: "alice", db: "app", host: "10.0.0.1"},
		{class: "Lock", query: "UPDATE t", user: "bob", db: "app", host: "10.0.0.2"},
		{class: "CPU", query: "SELECT 2", user: "alice", db: "other", host: ""},
	}}}

	byUser := aggregate(samples, now, 300, "users")
	if len(byUser.Classes) != 2 || byUser.Classes[0] != "alice" {
		t.Errorf("classes by user = %v, want alice first", byUser.Classes)
	}
	last := byUser.Buckets[chartBuckets-1]
	if last.V["alice"] != 2 || last.V["bob"] != 1 {
		t.Errorf("by user = %v, want alice 2 bob 1", last.V)
	}
	// The per-query bars keep speaking in wait classes.
	if len(byUser.WaitClasses) != 2 {
		t.Errorf("waitClasses = %v, want CPU and Lock", byUser.WaitClasses)
	}
	if byUser.TopSQL[0].ByClass["CPU"] == 0 {
		t.Errorf("top SQL lost its wait breakdown: %+v", byUser.TopSQL[0])
	}

	byDB := aggregate(samples, now, 300, "databases")
	if byDB.Buckets[chartBuckets-1].V["app"] != 2 {
		t.Errorf("by database = %v, want app 2", byDB.Buckets[chartBuckets-1].V)
	}

	// An empty dimension value is labelled, never dropped or blank.
	byHost := aggregate(samples, now, 300, "hosts")
	if byHost.Buckets[chartBuckets-1].V["(unset)"] != 1 {
		t.Errorf("by host = %v, want one (unset)", byHost.Buckets[chartBuckets-1].V)
	}
}

func TestAggregateEmpty(t *testing.T) {
	out := aggregate(nil, time.Now(), 300, "waits")
	if len(out.Buckets) != chartBuckets {
		t.Errorf("buckets = %d, want %d", len(out.Buckets), chartBuckets)
	}
	if len(out.TopSQL) != 0 || out.TopSQL == nil {
		t.Errorf("topSQL must be an empty non-nil slice, got %#v", out.TopSQL)
	}
	if len(out.Classes) != 0 {
		t.Errorf("classes = %v, want none", out.Classes)
	}
}

func TestNormalizeQuery(t *testing.T) {
	long := make([]rune, 0, 400)
	for range 400 {
		long = append(long, 'á') // multi-byte on purpose
	}
	got := normalizeQuery("  " + string(long) + "  ")
	r := []rune(got)
	if len(r) != maxQueryRunes+3 {
		t.Errorf("normalized length = %d runes, want %d", len(r), maxQueryRunes+3)
	}
	if got[len(got)-3:] != "..." {
		t.Errorf("normalized query must end with ellipsis")
	}
}
