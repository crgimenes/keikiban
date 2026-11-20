package main

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// sessionOut is one client session as the Sessions screen shows it.
type sessionOut struct {
	PID          int     `json:"pid"`
	State        string  `json:"state"`
	User         string  `json:"user"`
	App          string  `json:"app"`
	Host         string  `json:"host"`
	Database     string  `json:"database"`
	WaitEvent    string  `json:"waitEvent"`
	QuerySeconds float64 `json:"querySeconds"`
	XactSeconds  float64 `json:"xactSeconds"`
	StateSeconds float64 `json:"stateSeconds"`
	Query        string  `json:"query"`
	Blocked      bool    `json:"blocked"`
	CancelSQL    string  `json:"cancelSQL"`
	TerminateSQL string  `json:"terminateSQL"`
}

// sessionsOut is the Sessions screen payload.
type sessionsOut struct {
	Error    string       `json:"error,omitempty"`
	Sessions []sessionOut `json:"sessions"`
}

// sqlSessions lists client backends. Ordering puts the sessions worth acting
// on first: active ones by how long the current query has been running, then
// idle-in-transaction, then plain idle.
const sqlSessions = `SELECT
		pid,                                                     -- 1
		COALESCE(state, ''),                                     -- 2
		COALESCE(usename, ''),                                   -- 3
		COALESCE(application_name, ''),                          -- 4
		COALESCE(host(client_addr), 'local'),                    -- 5
		COALESCE(datname, ''),                                   -- 6
		CASE WHEN wait_event_type IS NULL THEN ''
			ELSE wait_event_type || COALESCE(':' || wait_event, '')
			END,                                                 -- 7
		COALESCE(EXTRACT(epoch FROM (now() - query_start)), 0),  -- 8
		COALESCE(EXTRACT(epoch FROM (now() - xact_start)), 0),   -- 9
		COALESCE(EXTRACT(epoch FROM (now() - state_change)), 0), -- 10
		COALESCE(query, ''),                                     -- 11
		cardinality(pg_blocking_pids(pid)) > 0                   -- 12
	FROM pg_stat_activity
	WHERE backend_type = 'client backend'
	AND pid <> pg_backend_pid()
	ORDER BY
		CASE state
			WHEN 'active' THEN 0
			WHEN 'idle in transaction' THEN 1
			WHEN 'idle in transaction (aborted)' THEN 1
			ELSE 2
		END,
		query_start NULLS LAST
	LIMIT 200;`

// collectSessions lists the current client sessions. Failure is data, never a
// blank screen.
func collectSessions(ctx context.Context, dbURL string) sessionsOut {
	out := sessionsOut{Sessions: []sessionOut{}}

	connCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	conn, err := pgx.Connect(connCtx, dbURL)
	cancel()
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), sampleTimeout)
		_ = conn.Close(closeCtx)
		cancel()
	}()

	qctx, cancel := context.WithTimeout(ctx, indexQueryTimeout)
	defer cancel()

	rows, err := conn.Query(qctx, sqlSessions)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer rows.Close()

	for rows.Next() {
		var s sessionOut
		err = rows.Scan(
			&s.PID,          // 1
			&s.State,        // 2
			&s.User,         // 3
			&s.App,          // 4
			&s.Host,         // 5
			&s.Database,     // 6
			&s.WaitEvent,    // 7
			&s.QuerySeconds, // 8
			&s.XactSeconds,  // 9
			&s.StateSeconds, // 10
			&s.Query,        // 11
			&s.Blocked,      // 12
		)
		if err != nil {
			out.Error = err.Error()
			return out
		}
		s.QuerySeconds = round2(s.QuerySeconds)
		s.XactSeconds = round2(s.XactSeconds)
		s.StateSeconds = round2(s.StateSeconds)
		s.Query = normalizeQuery(s.Query)
		s.CancelSQL = cancelSQL(s.PID)
		s.TerminateSQL = terminateSQL(s.PID)
		out.Sessions = append(out.Sessions, s)
	}
	if rows.Err() != nil {
		out.Error = rows.Err().Error()
	}
	return out
}
