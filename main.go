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
	"strings"
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
	default:
		_ = enc.Encode(map[string]any{
			"error":    fmt.Sprintf("unknown command %q", command),
			"commands": []string{"connections"},
		})
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
}

// testResult reports a connection attempt. Failure is data, not a rejected
// promise: the page always gets something to show.
type testResult struct {
	OK      bool   `json:"ok"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
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

	// Sized for the setup/list screens; user-resizable with a sane floor.
	w.SetTitle("keikiban")
	w.SetSize(680, 500, glaze.HintNone)
	w.SetSize(480, 380, glaze.HintMin)

	state := func() uiState {
		if cfg.Connections == nil {
			cfg.Connections = []Connection{}
		}
		return uiState{
			Path:        cfg.Path,
			Exists:      cfg.Exists,
			Error:       configErr,
			Connections: cfg.Connections,
		}
	}

	err = w.Bind("configState", func() (uiState, error) {
		return state(), nil
	})
	if err != nil {
		log.Fatal(err)
	}

	err = w.Bind("testConnection", func(dbURL string) (testResult, error) {
		return testConnection(dbURL), nil
	})
	if err != nil {
		log.Fatal(err)
	}

	// reload re-reads the config after a mutation so the UI always reflects
	// the file, the single source of truth.
	reload := func() (uiState, error) {
		reloaded, err := loadConfig()
		if err != nil {
			return uiState{}, err
		}
		cfg = reloaded
		configErr = ""
		return state(), nil
	}

	validateURL := func(dbURL string) error {
		_, err := pgx.ParseConfig(dbURL)
		if err != nil {
			return fmt.Errorf("invalid connection string: %w", err)
		}
		return nil
	}

	err = w.Bind("addConnection", func(dbURL, title string) (uiState, error) {
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
	if err != nil {
		log.Fatal(err)
	}

	err = w.Bind("updateConnection", func(index int, dbURL, title string) (uiState, error) {
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
	if err != nil {
		log.Fatal(err)
	}

	err = w.Bind("deleteConnection", func(index int) (uiState, error) {
		err := deleteConnection(cfg.Path, index)
		if err != nil {
			return uiState{}, err
		}
		debugf("event=connection_deleted index=%d", index)
		return reload()
	})
	if err != nil {
		log.Fatal(err)
	}

	// connectionURL hands the real URL (password included) to the edit form
	// only; every displayed or logged URL stays masked.
	err = w.Bind("connectionURL", func(index int) (string, error) {
		if index < 0 || index >= len(cfg.Connections) {
			return "", fmt.Errorf("connection %d does not exist", index)
		}
		return cfg.Connections[index].URL, nil
	})
	if err != nil {
		log.Fatal(err)
	}

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
