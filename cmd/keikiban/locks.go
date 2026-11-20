package main

import (
	"context"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
)

// blockedOut is one session waiting on a lock.
type blockedOut struct {
	PID         int     `json:"pid"`
	Query       string  `json:"query"`
	User        string  `json:"user"`
	App         string  `json:"app"`
	WaitEvent   string  `json:"waitEvent"`
	WaitSeconds float64 `json:"waitSeconds"`
	LockMode    string  `json:"lockMode"`
}

// blockerOut is one session holding a lock others wait for, with everything
// it blocks. AlsoBlocked marks a middle link: it blocks someone while waiting
// on someone else, so the real culprit is further up the chain.
type blockerOut struct {
	PID          int          `json:"pid"`
	Query        string       `json:"query"`
	State        string       `json:"state"`
	StateSeconds float64      `json:"stateSeconds"`
	User         string       `json:"user"`
	App          string       `json:"app"`
	AlsoBlocked  bool         `json:"alsoBlocked"`
	CancelSQL    string       `json:"cancelSQL"`
	TerminateSQL string       `json:"terminateSQL"`
	Blocked      []blockedOut `json:"blocked"`
}

func cancelSQL(pid int) string {
	return fmt.Sprintf("SELECT pg_cancel_backend(%d);", pid)
}

func terminateSQL(pid int) string {
	return fmt.Sprintf("SELECT pg_terminate_backend(%d);", pid)
}

// sqlBlocking lists every lock-wait edge: one row per (blocked session,
// blocker). pg_blocking_pids is expensive, so the CTE narrows the candidates
// to sessions actually waiting on a Lock before calling it.
const sqlBlocking = `WITH blocked AS (
		SELECT
			pid,
			COALESCE(query, '') AS query,
			COALESCE(usename, '') AS usename,
			COALESCE(application_name, '') AS application_name,
			COALESCE(wait_event, '') AS wait_event,
			EXTRACT(epoch FROM (now() - state_change)) AS wait_seconds,
			pg_blocking_pids(pid) AS blockers
		FROM pg_stat_activity
		WHERE wait_event_type = 'Lock'
	)
	SELECT
		b.pid,                                        -- 1
		b.query,                                      -- 2
		b.usename,                                    -- 3
		b.application_name,                           -- 4
		b.wait_event,                                 -- 5
		COALESCE(b.wait_seconds, 0),                  -- 6
		COALESCE((SELECT l.mode FROM pg_locks l
			WHERE l.pid = b.pid AND NOT l.granted LIMIT 1), ''), -- 7
		blocker.pid,                                  -- 8
		COALESCE(a.query, ''),                        -- 9
		COALESCE(a.state, ''),                        -- 10
		COALESCE(EXTRACT(epoch FROM (now() - a.state_change)), 0), -- 11
		COALESCE(a.usename, ''),                      -- 12
		COALESCE(a.application_name, ''),             -- 13
		COALESCE(a.wait_event_type, '') = 'Lock'      -- 14
	FROM blocked b
	CROSS JOIN LATERAL unnest(b.blockers) AS blocker(pid)
	LEFT JOIN pg_stat_activity a ON a.pid = blocker.pid;`

// collectBlocking returns the current blocking tree, grouped by blocker and
// ordered by the longest wait it causes.
func collectBlocking(ctx context.Context, conn *pgx.Conn) ([]blockerOut, error) {
	qctx, cancel := context.WithTimeout(ctx, sampleTimeout)
	defer cancel()

	rows, err := conn.Query(qctx, sqlBlocking)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byPID := map[int]*blockerOut{}
	for rows.Next() {
		var bd blockedOut
		var b blockerOut
		err = rows.Scan(
			&bd.PID,         // 1
			&bd.Query,       // 2
			&bd.User,        // 3
			&bd.App,         // 4
			&bd.WaitEvent,   // 5
			&bd.WaitSeconds, // 6
			&bd.LockMode,    // 7
			&b.PID,          // 8
			&b.Query,        // 9
			&b.State,        // 10
			&b.StateSeconds, // 11
			&b.User,         // 12
			&b.App,          // 13
			&b.AlsoBlocked,  // 14
		)
		if err != nil {
			return nil, err
		}

		bd.Query = normalizeQuery(bd.Query)
		bd.WaitSeconds = round2(bd.WaitSeconds)

		node, ok := byPID[b.PID]
		if !ok {
			b.Query = normalizeQuery(b.Query)
			b.StateSeconds = round2(b.StateSeconds)
			b.CancelSQL = cancelSQL(b.PID)
			b.TerminateSQL = terminateSQL(b.PID)
			node = &b
			byPID[b.PID] = node
		}
		node.Blocked = append(node.Blocked, bd)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}

	out := make([]blockerOut, 0, len(byPID))
	for _, node := range byPID {
		sort.Slice(node.Blocked, func(i, j int) bool {
			return node.Blocked[i].WaitSeconds > node.Blocked[j].WaitSeconds
		})
		out = append(out, *node)
	}
	// Root blockers first, then by the longest wait they cause.
	sort.Slice(out, func(i, j int) bool {
		if out[i].AlsoBlocked != out[j].AlsoBlocked {
			return !out[i].AlsoBlocked
		}
		return out[i].Blocked[0].WaitSeconds > out[j].Blocked[0].WaitSeconds
	})
	return out, nil
}

// blockingSnapshot connects and returns the blocking tree once, for the
// one-shot JSON mode.
func blockingSnapshot(ctx context.Context, dbURL string) ([]blockerOut, error) {
	connCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	conn, err := pgx.Connect(connCtx, dbURL)
	cancel()
	if err != nil {
		return nil, err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), sampleTimeout)
		_ = conn.Close(closeCtx)
		cancel()
	}()

	tree, err := collectBlocking(ctx, conn)
	if err != nil {
		return nil, err
	}
	if tree == nil {
		tree = []blockerOut{}
	}
	return tree, nil
}

// cancelBackend asks the server to cancel the running query of pid (or, with
// terminate, to close the whole connection). It runs the same statement the
// UI displayed.
func cancelBackend(ctx context.Context, dbURL string, pid int, terminate bool) error {
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

	stmt := cancelSQL(pid)
	if terminate {
		stmt = terminateSQL(pid)
	}

	qctx, cancel := context.WithTimeout(ctx, sampleTimeout)
	defer cancel()
	ok := false
	err = conn.QueryRow(qctx, stmt).Scan(&ok)
	if err != nil {
		return fmt.Errorf("%s: %w", stmt, err)
	}
	debugf("event=backend_signalled pid=%d terminate=%v accepted=%v", pid, terminate, ok)
	if !ok {
		return fmt.Errorf("server refused the request for pid %d (already gone?)", pid)
	}
	return nil
}
