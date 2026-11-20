package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"mime"
	"net/url"
	"os"
	"path"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/crgimenes/glaze"
	"github.com/crgimenes/glaze/menu"
	"github.com/jackc/pgx/v5"
)

//go:embed ui
var uiFS embed.FS

var debugMode bool

func init() { runtime.LockOSThread() }

// debugf prints one key=value diagnostic line to stderr when -debug is on.
// stderr keeps -json stdout clean for machine consumption.
func debugf(format string, args ...any) {
	if !debugMode {
		return
	}
	fmt.Fprintf(os.Stderr, "keikiban: "+format+"\n", args...)
}

func main() {
	debugFlag := flag.Bool("debug", false, "print key=value diagnostic lines to stderr")
	jsonFlag := flag.Bool("json", false, "one-shot mode: run a command, print JSON to stdout, exit")
	flag.Parse()
	debugMode = *debugFlag

	cfg, err := loadConfig()
	configErr := ""
	if err != nil {
		configErr = err.Error()
	}
	debugf("event=config_loaded path=%s exists=%v connections=%d err=%q",
		cfg.Path, cfg.Exists, len(cfg.Connections), configErr)

	if *jsonFlag {
		runJSON(cfg, configErr, flag.Args())
		return
	}
	runGUI(cfg, configErr)
}

// runJSON is the AI-friendly one-shot mode: execute one command, print a JSON
// document to stdout, exit. Passwords never appear in the output.
func runJSON(cfg Config, configErr string, args []string) {
	command := ""
	if len(args) > 0 {
		command = args[0]
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")

	switch command {
	case "connections":
		if configErr != "" {
			_ = enc.Encode(map[string]string{"error": configErr})
			os.Exit(1)
		}
		if cfg.Connections == nil {
			cfg.Connections = []Connection{}
		}
		_ = enc.Encode(cfg)
	case "indexes":
		if configErr != "" {
			_ = enc.Encode(map[string]string{"error": configErr})
			os.Exit(1)
		}
		if len(cfg.Connections) == 0 {
			_ = enc.Encode(map[string]string{"error": "no connections configured"})
			os.Exit(1)
		}
		report := collectIndexReport(context.Background(), cfg.Connections[0].URL)
		_ = enc.Encode(report)
		if report.Error != "" {
			os.Exit(1)
		}
	case "locks":
		if configErr != "" {
			_ = enc.Encode(map[string]string{"error": configErr})
			os.Exit(1)
		}
		if len(cfg.Connections) == 0 {
			_ = enc.Encode(map[string]string{"error": "no connections configured"})
			os.Exit(1)
		}
		tree, err := blockingSnapshot(context.Background(), cfg.Connections[0].URL)
		if err != nil {
			_ = enc.Encode(map[string]string{"error": err.Error()})
			os.Exit(1)
		}
		_ = enc.Encode(map[string]any{"blocking": tree})
	case "dashboard":
		if configErr != "" {
			_ = enc.Encode(map[string]string{"error": configErr})
			os.Exit(1)
		}
		if len(cfg.Connections) == 0 {
			_ = enc.Encode(map[string]string{"error": "no connections configured"})
			os.Exit(1)
		}
		// The load chart is built from samples over time, so this command
		// samples for a while before answering. Default 30s; the caller can
		// ask for another duration.
		seconds := 30
		if len(args) > 1 {
			n, err := strconv.Atoi(args[1])
			if err != nil || n < 1 {
				_ = enc.Encode(map[string]string{
					"error": "usage: keikiban -json dashboard [seconds]",
				})
				os.Exit(1)
			}
			seconds = n
		}
		sampler := newSampler(cfg.Connections[0].URL)
		time.Sleep(time.Duration(seconds) * time.Second)
		out := sampler.Snapshot(time.Now(), seconds, "waits", "sql")
		sampler.Stop()
		out.Title = cfg.Connections[0].Title
		out.URL = cfg.Connections[0].MaskedURL
		_ = enc.Encode(out)
		if !out.Connected {
			os.Exit(1)
		}
	case "sessions":
		if configErr != "" {
			_ = enc.Encode(map[string]string{"error": configErr})
			os.Exit(1)
		}
		if len(cfg.Connections) == 0 {
			_ = enc.Encode(map[string]string{"error": "no connections configured"})
			os.Exit(1)
		}
		out := collectSessions(context.Background(), cfg.Connections[0].URL)
		_ = enc.Encode(out)
		if out.Error != "" {
			os.Exit(1)
		}
	case "maintenance":
		if configErr != "" {
			_ = enc.Encode(map[string]string{"error": configErr})
			os.Exit(1)
		}
		if len(cfg.Connections) == 0 {
			_ = enc.Encode(map[string]string{"error": "no connections configured"})
			os.Exit(1)
		}
		report := collectMaintenance(context.Background(), cfg.Connections[0].URL)
		_ = enc.Encode(report)
		if report.Error != "" {
			os.Exit(1)
		}
	case "migrations":
		if configErr != "" {
			_ = enc.Encode(map[string]string{"error": configErr})
			os.Exit(1)
		}
		if len(cfg.Connections) == 0 {
			_ = enc.Encode(map[string]string{"error": "no connections configured"})
			os.Exit(1)
		}
		// Status only, and the status path is strictly read-only: an agent
		// checking migration state must not be able to alter the database.
		// Running or capturing stays in the window, behind the confirmation.
		dir := ""
		if len(args) > 1 {
			dir = args[1]
		}
		out := collectMigrationStatus(context.Background(), cfg.Connections[0].URL, dir)
		_ = enc.Encode(out)
		if out.Error != "" {
			os.Exit(1)
		}
	// The browser commands share one preamble: they all need a database and
	// they all take arguments, unlike the report commands above.
	case "schemas", "objects", "describe", "search":
		if configErr != "" {
			_ = enc.Encode(map[string]string{"error": configErr})
			os.Exit(1)
		}
		if len(cfg.Connections) == 0 {
			_ = enc.Encode(map[string]string{"error": "no connections configured"})
			os.Exit(1)
		}
		runBrowserJSON(enc, cfg.Connections[0].URL, command, args[1:])
	default:
		_ = enc.Encode(map[string]any{
			"error": fmt.Sprintf("unknown command %q", command),
			"commands": []string{
				"connections", "dashboard", "describe", "indexes", "locks",
				"maintenance", "migrations", "objects", "schemas", "search",
				"sessions",
			},
		})
		os.Exit(1)
	}
}

// runBrowserJSON serves the object browser to an agent: the same collectors
// the window uses, so both see one database and one truth.
func runBrowserJSON(enc *json.Encoder, dbURL, command string, args []string) {
	fail := func(usage string) {
		_ = enc.Encode(map[string]string{"error": usage})
		os.Exit(1)
	}

	failed := false
	switch command {
	case "schemas":
		out := collectBrowserTree(context.Background(), dbURL)
		_ = enc.Encode(out)
		failed = out.Error != ""
	case "objects":
		if len(args) != 2 {
			fail("usage: keikiban -json objects <schema> <tables|views|matviews|sequences>")
		}
		out := collectObjects(context.Background(), dbURL, args[0], args[1])
		_ = enc.Encode(out)
		failed = out.Error != ""
	case "describe":
		if len(args) != 2 {
			fail("usage: keikiban -json describe <schema> <name>")
		}
		out := collectObjectDetail(context.Background(), dbURL, args[0], args[1])
		_ = enc.Encode(out)
		failed = out.Error != ""
	case "search":
		if len(args) != 1 {
			fail("usage: keikiban -json search <term>")
		}
		out := searchObjects(context.Background(), dbURL, args[0])
		_ = enc.Encode(out)
		failed = out.Error != ""
	}
	if failed {
		os.Exit(1)
	}
}

// uiState is what the page needs to render: where the config lives, what it
// declares, and any load error, stated plainly instead of hidden.
type uiState struct {
	Path        string       `json:"path"`
	Exists      bool         `json:"exists"`
	Error       string       `json:"error,omitempty"`
	Connections []Connection `json:"connections"`
	// Active is the index of the one connection in use; -1 when none.
	Active int `json:"active"`
}

// testResult reports a connection attempt. Failure is data, not a rejected
// promise: the page always gets something to show.
type testResult struct {
	OK      bool   `json:"ok"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

// mustBind registers a bind the window cannot work without. A failure here is
// a programming error (duplicate or reserved name), never a runtime condition,
// so it stops the app before a half-wired page could reach the user.
func mustBind(w glaze.WebView, name string, fn any) {
	err := w.Bind(name, fn)
	if err != nil {
		log.Fatal(err)
	}
}

func runGUI(cfg Config, configErr string) {
	w, err := glaze.NewWithOptions(glaze.Options{
		Debug:          debugMode,
		SchemeHandlers: map[string]glaze.SchemeHandler{"app": serveAsset},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer w.Destroy()

	// Dashboard-sized; user-resizable with a sane floor.
	w.SetTitle("keikiban")
	w.SetSize(1200, 860, glaze.HintNone)
	w.SetSize(560, 480, glaze.HintMin)

	// mu guards cfg, configErr, activeIndex and the sampler: Bind callbacks
	// run on background goroutines and may overlap.
	var mu sync.Mutex
	var sampler *Sampler
	samplerURL := ""
	// activeIndex is the one connection keikiban is attached to. Exactly one
	// at a time, on purpose: with several open at once it is too easy to run
	// a command against production while believing you are on staging.
	// A fresh start always attaches to the first (the default).
	activeIndex := 0

	// activeConn returns the connection in use. Callers hold mu.
	activeConn := func() (Connection, bool) {
		if activeIndex < 0 || activeIndex >= len(cfg.Connections) {
			return Connection{}, false
		}
		return cfg.Connections[activeIndex], true
	}

	// activeDB is the shortcut every screen uses to reach the server. Having
	// no connection at all and having deliberately closed the one you had are
	// different situations, and the screen says which. Callers hold mu.
	activeDB := func() (string, error) {
		conn, ok := activeConn()
		if ok {
			return conn.URL, nil
		}
		if len(cfg.Connections) == 0 {
			return "", errors.New("no connections configured")
		}
		return "", errors.New("not connected")
	}

	// ensureSampler keeps one sampler alive for the active connection and
	// restarts it whenever that connection changes. Callers hold mu.
	ensureSampler := func() {
		if activeIndex >= len(cfg.Connections) {
			activeIndex = 0
		}
		want := ""
		conn, ok := activeConn()
		if ok {
			want = conn.URL
		}
		if want == samplerURL {
			return
		}
		if sampler != nil {
			sampler.Stop()
			sampler = nil
		}
		samplerURL = want
		if want != "" {
			sampler = newSampler(want)
			debugf("event=connected index=%d title=%q url=%s",
				activeIndex, conn.Title, conn.MaskedURL)
		}
		// The window title names the attached database, so even the OS
		// window switcher says which server a keystroke would reach.
		title := "keikiban"
		if ok && conn.Title != "" {
			title = "keikiban - " + conn.Title
		}
		w.Dispatch(func() { w.SetTitle(title) })
	}
	ensureSampler()
	defer func() {
		if sampler != nil {
			sampler.Stop()
		}
	}()

	// state assumes mu is held.
	state := func() uiState {
		if cfg.Connections == nil {
			cfg.Connections = []Connection{}
		}
		active := activeIndex
		if len(cfg.Connections) == 0 {
			active = -1
		}
		return uiState{
			Path:        cfg.Path,
			Exists:      cfg.Exists,
			Error:       configErr,
			Connections: cfg.Connections,
			Active:      active,
		}
	}

	mustBind(w, "configState", func() (uiState, error) {
		mu.Lock()
		defer mu.Unlock()
		return state(), nil
	})

	// connectTo attaches to one connection and, by doing so, detaches from
	// the previous one: keikiban is never attached to two servers at once.
	mustBind(w, "connectTo", func(index int) (uiState, error) {
		mu.Lock()
		defer mu.Unlock()
		if index < 0 || index >= len(cfg.Connections) {
			return uiState{}, fmt.Errorf("connection %d does not exist", index)
		}
		activeIndex = index
		ensureSampler()
		return state(), nil
	})

	// disconnect closes the open connection and leaves keikiban attached to
	// nothing: the sampler stops polling and the window title stops naming a
	// server. Being attached to no database is a legitimate resting state, not
	// an error to recover from.
	mustBind(w, "disconnect", func() (uiState, error) {
		mu.Lock()
		defer mu.Unlock()
		activeIndex = -1
		ensureSampler()
		debugf("event=disconnected")
		return state(), nil
	})

	mustBind(w, "dashboardState", func(windowSeconds int, sliceBy, topBy string) (dashOut, error) {
		mu.Lock()
		defer mu.Unlock()
		if sampler == nil {
			status := "no connection configured"
			if len(cfg.Connections) > 0 {
				status = "not connected"
			}
			return dashOut{
				Status:           status,
				Classes:          []string{},
				WaitClasses:      []string{},
				Buckets:          []bucketOut{},
				TopSQL:           []topSQLOut{},
				Missing:          []string{},
				MissingPreloaded: []string{},
				ConnClasses:      []string{},
				Conns:            []bucketOut{},
				TPSClasses:       []string{},
				TPS:              []bucketOut{},
				IOClasses:        []string{},
				IO:               []bucketOut{},
				Blocking:         []blockerOut{},
			}, nil
		}
		out := sampler.Snapshot(time.Now(), windowSeconds, sliceBy, topBy)
		conn, _ := activeConn()
		out.Title = conn.Title
		out.URL = conn.MaskedURL
		return out, nil
	})

	mustBind(w, "testConnection", func(dbURL string) (testResult, error) {
		return testConnection(dbURL), nil
	})

	// A JS exception inside the webview is invisible from the terminal; the
	// page forwards them here so -debug shows UI failures too.
	mustBind(w, "logError", func(msg string) error {
		debugf("event=ui_error msg=%q", msg)
		return nil
	})

	mustBind(w, "indexReport", func() (indexReport, error) {
		mu.Lock()
		dbURL, err := activeDB()
		mu.Unlock()
		if err != nil {
			report := emptyIndexReport()
			report.Error = err.Error()
			return report, nil
		}
		return collectIndexReport(context.Background(), dbURL), nil
	})

	mustBind(w, "browserTree", func() (browserTreeOut, error) {
		mu.Lock()
		dbURL, err := activeDB()
		mu.Unlock()
		if err != nil {
			return browserTreeOut{
				Error:   err.Error(),
				Schemas: []schemaNode{},
			}, nil
		}
		return collectBrowserTree(context.Background(), dbURL), nil
	})

	mustBind(w, "browserObjects", func(schema, group string) (objectListOut, error) {
		mu.Lock()
		dbURL, err := activeDB()
		mu.Unlock()
		if err != nil {
			out := emptyObjectList()
			out.Error = err.Error()
			return out, nil
		}
		return collectObjects(context.Background(), dbURL, schema, group), nil
	})

	// openObjectWindow gives one database object its own native window, so
	// several can stay open side by side, each with its own tabs. glaze ends
	// the run loop only when the LAST window closes, so these are ordinary
	// windows of the same app rather than a second process.
	//
	// The window captures which object it shows, but never which database:
	// activeDB is consulted on every query instead. A window that kept the URL
	// it was born with would go on reading a server the user has since left,
	// which is exactly what the one-connection-at-a-time rule exists to
	// prevent.
	openObjectWindow := func(schema, name string) {
		ow, err := glaze.NewWithOptions(glaze.Options{
			Debug:          debugMode,
			SchemeHandlers: map[string]glaze.SchemeHandler{"app": serveAsset},
		})
		if err != nil {
			debugf("event=object_window_error schema=%s name=%s err=%q", schema, name, err)
			return
		}
		ow.SetTitle(schema + "." + name)
		ow.SetSize(900, 680, glaze.HintNone)
		ow.SetSize(420, 320, glaze.HintMin)

		err = ow.Bind("objectDetail", func() (objectDetailOut, error) {
			mu.Lock()
			dbURL, dbErr := activeDB()
			mu.Unlock()
			if dbErr != nil {
				out := emptyDetail()
				out.Schema = schema
				out.Name = name
				out.Error = dbErr.Error()
				return out, nil
			}
			return collectObjectDetail(context.Background(), dbURL, schema, name), nil
		})
		if err != nil {
			debugf("event=object_window_error schema=%s name=%s err=%q", schema, name, err)
			ow.Destroy()
			return
		}

		err = ow.Bind("initialQuery", func() (string, error) {
			return initialQuery(schema, name), nil
		})
		if err != nil {
			debugf("event=object_window_error schema=%s name=%s err=%q", schema, name, err)
			ow.Destroy()
			return
		}

		// One query at a time per window, and always cancellable: a tool that
		// cannot stop what it started is the complaint people file about the
		// ones this replaces. queryCancel guards the running statement.
		var queryMu sync.Mutex
		var queryCancel context.CancelFunc

		err = ow.Bind("runQuery", func(sql string) (queryOut, error) {
			mu.Lock()
			dbURL, dbErr := activeDB()
			mu.Unlock()
			if dbErr != nil {
				out := emptyQueryOut()
				out.SQL = sql
				out.Error = dbErr.Error()
				return out, nil
			}

			ctx, cancel := context.WithCancel(context.Background())
			queryMu.Lock()
			// A second Run replaces the first: the window shows one result, so
			// leaving the previous statement running would burn a backend
			// nobody is waiting on.
			if queryCancel != nil {
				queryCancel()
			}
			queryCancel = cancel
			queryMu.Unlock()

			defer func() {
				queryMu.Lock()
				if queryCancel != nil {
					queryCancel()
					queryCancel = nil
				}
				queryMu.Unlock()
			}()
			return runQuery(ctx, dbURL, sql), nil
		})
		if err != nil {
			debugf("event=object_window_error schema=%s name=%s err=%q", schema, name, err)
			ow.Destroy()
			return
		}

		err = ow.Bind("cancelQuery", func() error {
			queryMu.Lock()
			defer queryMu.Unlock()
			if queryCancel == nil {
				return nil
			}
			queryCancel()
			queryCancel = nil
			debugf("event=query_cancelled schema=%s name=%s", schema, name)
			return nil
		})
		if err != nil {
			debugf("event=object_window_error schema=%s name=%s err=%q", schema, name, err)
			ow.Destroy()
			return
		}

		// The grid's write path: preview first, then save. Same anatomy as
		// every other write in this app, and for the same reason — the
		// default connection may be production.
		err = ow.Bind("gridPreview", func(in gridSaveIn) (gridSaveOut, error) {
			mu.Lock()
			dbURL, dbErr := activeDB()
			mu.Unlock()
			if dbErr != nil {
				return gridSaveOut{Error: dbErr.Error()}, nil
			}
			return previewRowUpdate(context.Background(), dbURL, in), nil
		})
		if err != nil {
			debugf("event=object_window_error schema=%s name=%s err=%q", schema, name, err)
			ow.Destroy()
			return
		}

		err = ow.Bind("gridSave", func(in gridSaveIn) (gridSaveOut, error) {
			mu.Lock()
			dbURL, dbErr := activeDB()
			mu.Unlock()
			if dbErr != nil {
				return gridSaveOut{Error: dbErr.Error()}, nil
			}
			return saveRowUpdate(context.Background(), dbURL, in), nil
		})
		if err != nil {
			debugf("event=object_window_error schema=%s name=%s err=%q", schema, name, err)
			ow.Destroy()
			return
		}

		err = ow.Bind("logError", func(msg string) error {
			debugf("event=ui_error window=%s.%s msg=%q", schema, name, msg)
			return nil
		})
		if err != nil {
			debugf("event=object_window_error schema=%s name=%s err=%q", schema, name, err)
			ow.Destroy()
			return
		}

		debugf("event=object_window_open schema=%s name=%s", schema, name)
		ow.Navigate("app://keikiban/object.html")
	}

	mustBind(w, "openObject", func(schema, name string) error {
		mu.Lock()
		_, err := activeDB()
		mu.Unlock()
		if err != nil {
			return err
		}
		// Creating a window is native UI work and belongs on the UI thread;
		// bind callbacks run on their own goroutines.
		w.Dispatch(func() { openObjectWindow(schema, name) })
		return nil
	})

	mustBind(w, "browserSearch", func(term string) (searchOut, error) {
		mu.Lock()
		dbURL, err := activeDB()
		mu.Unlock()
		if err != nil {
			return searchOut{
				Error: err.Error(),
				Hits:  []searchHit{},
			}, nil
		}
		return searchObjects(context.Background(), dbURL, term), nil
	})

	// The Migrations screen. Like every bind, the database is resolved per
	// call, never captured; the dir travels from the page because the manual
	// fallback lives there.
	mustBind(w, "migrationStatus", func(dir string) (migStatusOut, error) {
		mu.Lock()
		dbURL, err := activeDB()
		mu.Unlock()
		if err != nil {
			return migStatusOut{Error: err.Error(), Pending: []string{}}, nil
		}
		return collectMigrationStatus(context.Background(), dbURL, dir), nil
	})

	mustBind(w, "migrationPreview", func(dir, action, name string) (migPreviewOut, error) {
		mu.Lock()
		dbURL, err := activeDB()
		mu.Unlock()
		if err != nil {
			return migPreviewOut{Error: err.Error(), Files: []namedDef{}}, nil
		}
		return collectMigrationPreview(context.Background(), dbURL, dir, action, name), nil
	})

	mustBind(w, "migrationApply", func(dir, action, name string) (migApplyOut, error) {
		mu.Lock()
		dbURL, err := activeDB()
		mu.Unlock()
		if err != nil {
			return migApplyOut{Error: err.Error()}, nil
		}
		return applyMigrationAction(context.Background(), dbURL, dir, action, name), nil
	})

	// signalBackend cancels a query or terminates a connection: the same
	// statement the confirmation dialog displayed.
	mustBind(w, "signalBackend", func(pid int, terminate bool) error {
		mu.Lock()
		dbURL, err := activeDB()
		mu.Unlock()
		if err != nil {
			return err
		}
		return cancelBackend(context.Background(), dbURL, pid, terminate)
	})

	mustBind(w, "sessionList", func() (sessionsOut, error) {
		mu.Lock()
		dbURL, err := activeDB()
		mu.Unlock()
		if err != nil {
			return sessionsOut{
				Error:    err.Error(),
				Sessions: []sessionOut{},
			}, nil
		}
		return collectSessions(context.Background(), dbURL), nil
	})

	mustBind(w, "maintenanceReport", func() (maintenanceReport, error) {
		mu.Lock()
		dbURL, err := activeDB()
		mu.Unlock()
		if err != nil {
			report := emptyMaintenanceReport()
			report.Error = err.Error()
			return report, nil
		}
		return collectMaintenance(context.Background(), dbURL), nil
	})

	mustBind(w, "vacuumProgress", func() (vacuumProgressOut, error) {
		mu.Lock()
		dbURL, err := activeDB()
		mu.Unlock()
		if err != nil {
			return vacuumProgressOut{}, nil
		}
		return collectVacuumProgress(context.Background(), dbURL), nil
	})

	mustBind(w, "vacuumTable", func(schema, table string) (maintenanceReport, error) {
		mu.Lock()
		dbURL, err := activeDB()
		mu.Unlock()
		if err != nil {
			return maintenanceReport{}, err
		}
		err = runVacuum(context.Background(), dbURL, schema, table)
		if err != nil {
			return maintenanceReport{}, err
		}
		return collectMaintenance(context.Background(), dbURL), nil
	})

	mustBind(w, "dropIndex", func(schema, name string) (indexReport, error) {
		mu.Lock()
		dbURL, err := activeDB()
		mu.Unlock()
		if err != nil {
			return indexReport{}, err
		}
		err = dropIndex(context.Background(), dbURL, schema, name)
		if err != nil {
			return indexReport{}, err
		}
		return collectIndexReport(context.Background(), dbURL), nil
	})

	// reload re-reads the config after a mutation so the UI always reflects
	// the file, the single source of truth. Assumes mu is held.
	reload := func() (uiState, error) {
		reloaded, err := loadConfig()
		if err != nil {
			return uiState{}, err
		}
		cfg = reloaded
		configErr = ""
		ensureSampler()
		return state(), nil
	}

	validateURL := func(dbURL string) error {
		_, err := pgx.ParseConfig(dbURL)
		if err != nil {
			return fmt.Errorf("invalid connection string: %w", err)
		}
		return nil
	}

	mustBind(w, "addConnection", func(dbURL, title string) (uiState, error) {
		mu.Lock()
		defer mu.Unlock()
		dbURL = strings.TrimSpace(dbURL)
		err := validateURL(dbURL)
		if err != nil {
			return uiState{}, err
		}
		err = appendConnection(cfg.Path, dbURL, strings.TrimSpace(title))
		if err != nil {
			return uiState{}, err
		}
		debugf("event=connection_added url=%s", maskURL(dbURL))
		return reload()
	})

	mustBind(w, "updateConnection", func(index int, dbURL, title string) (uiState, error) {
		mu.Lock()
		defer mu.Unlock()
		dbURL = strings.TrimSpace(dbURL)
		err := validateURL(dbURL)
		if err != nil {
			return uiState{}, err
		}
		err = updateConnection(cfg.Path, index, dbURL, strings.TrimSpace(title))
		if err != nil {
			return uiState{}, err
		}
		debugf("event=connection_updated index=%d url=%s", index, maskURL(dbURL))
		return reload()
	})

	mustBind(w, "deleteConnection", func(index int) (uiState, error) {
		mu.Lock()
		defer mu.Unlock()
		err := deleteConnection(cfg.Path, index)
		if err != nil {
			return uiState{}, err
		}
		// The list closes up under the attached connection: follow it, so the
		// entry keikiban is attached to is never silently swapped for its
		// neighbour. Deleting the open one leaves keikiban attached to nothing.
		switch {
		case activeIndex == index:
			activeIndex = -1
		case activeIndex > index:
			activeIndex--
		}
		debugf("event=connection_deleted index=%d", index)
		return reload()
	})

	// makeDefault promotes a connection to first in the file, which is what
	// "default" means here: the one keikiban attaches to when it opens.
	mustBind(w, "makeDefault", func(index int) (uiState, error) {
		mu.Lock()
		defer mu.Unlock()
		err := makeDefaultConnection(cfg.Path, index)
		if err != nil {
			return uiState{}, err
		}
		// Promoting reorders the list, so the attached index has to move with
		// the entry it names; the open server must not change behind your back.
		switch {
		case activeIndex == index:
			activeIndex = 0
		case activeIndex >= 0 && activeIndex < index:
			activeIndex++
		}
		debugf("event=connection_promoted index=%d", index)
		return reload()
	})

	// connectionURL hands the real URL (password included) to the edit form
	// only; every displayed or logged URL stays masked.
	mustBind(w, "connectionURL", func(index int) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if index < 0 || index >= len(cfg.Connections) {
			return "", fmt.Errorf("connection %d does not exist", index)
		}
		return cfg.Connections[index].URL, nil
	})

	// The Edit menu is not cosmetic on macOS: its native selectors are the only
	// route Cocoa gives Cmd+X/C/V/A into the WKWebView's editing commands.
	// Windows and Linux deliver those shortcuts to the focused control directly,
	// so the menu (where supported at all) carries only Quit.
	items := []menu.Item{
		{Title: "keikiban", Submenu: []menu.Item{
			{Title: "Quit keikiban", Shortcut: "cmd+q", OnClick: w.Terminate},
		}},
	}
	if runtime.GOOS == "darwin" {
		// This is a desktop app, and with one window per object it is a
		// multi-window one: Cmd+W closing the key window is basic macOS
		// hygiene. performClose: targets whichever window is key, so the same
		// menu item serves the main window and every object window.
		items = append(items, menu.Item{Title: "File", Submenu: []menu.Item{
			{Title: "Close Window", Shortcut: "cmd+w", Selector: "performClose:"},
		}})
		items = append(items, menu.Item{Title: "Edit", Submenu: []menu.Item{
			{Title: "Undo", Shortcut: "cmd+z", Selector: "undo:"},
			{Title: "Redo", Shortcut: "cmd+shift+z", Selector: "redo:"},
			{Separator: true},
			{Title: "Cut", Shortcut: "cmd+x", Selector: "cut:"},
			{Title: "Copy", Shortcut: "cmd+c", Selector: "copy:"},
			{Title: "Paste", Shortcut: "cmd+v", Selector: "paste:"},
			{Title: "Select All", Shortcut: "cmd+a", Selector: "selectAll:"},
		}})
	}
	_, err = menu.Set(items, menu.Options{Window: w.Window()})
	if err != nil && !errors.Is(err, menu.ErrUnsupported) {
		debugf("event=menu_error err=%q", err)
	}

	w.Navigate("app://keikiban/")
	w.Run()
}

// testConnection connects, asks the server its version, and disconnects.
// Bounded by a timeout so a dead host answers in seconds, not minutes.
func testConnection(dbURL string) testResult {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, strings.TrimSpace(dbURL))
	if err != nil {
		debugf("event=test_connection ok=false err=%q", err)
		return testResult{Error: err.Error()}
	}
	defer func() { _ = conn.Close(ctx) }()

	version := ""
	err = conn.QueryRow(ctx, "SELECT version()").Scan(&version)
	if err != nil {
		debugf("event=test_connection ok=false err=%q", err)
		return testResult{Error: err.Error()}
	}

	debugf("event=test_connection ok=true version=%q", version)
	return testResult{OK: true, Version: version}
}

// serveAsset maps an app:// request to the embedded ui/ directory, following
// glaze's examples/scheme shape: root falls back to index.html.
func serveAsset(req *glaze.SchemeRequest) *glaze.SchemeResponse {
	name := assetName(req.URL)
	data, err := uiFS.ReadFile("ui/" + name)
	if err != nil {
		return nil
	}
	ct := mime.TypeByExtension(path.Ext(name))
	if ct == "" {
		ct = "application/octet-stream"
	}
	return &glaze.SchemeResponse{Body: data, MIMEType: ct}
}

// assetName turns a request URL into a clean embedded-FS name, defaulting the
// root to index.html.
func assetName(reqURL string) string {
	p := reqURL
	u, err := url.Parse(reqURL)
	if err == nil && u.Path != "" {
		p = u.Path
	}
	p = strings.TrimPrefix(path.Clean("/"+p), "/")
	if p == "" || p == "." {
		return "index.html"
	}
	return p
}
