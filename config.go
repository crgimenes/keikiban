package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/crgimenes/filo"
	"github.com/jackc/pgx/v5"
)

// Connection is one database declared in the config file.
type Connection struct {
	URL   string `json:"-"`
	Title string `json:"title"`
	// MaskedURL is URL with the password hidden; the only form that ever
	// reaches logs, JSON output, or the UI.
	MaskedURL string `json:"url"`
}

// Config is the loaded configuration plus where it came from.
type Config struct {
	Path        string       `json:"path"`
	Exists      bool         `json:"exists"`
	Connections []Connection `json:"connections"`
}

// configPath resolves the config file location: a keikiban_init.filo in the
// current directory wins, otherwise $XDG_CONFIG_HOME/keikiban/init.filo
// (defaulting XDG_CONFIG_HOME to ~/.config).
func configPath() (string, error) {
	local := "keikiban_init.filo"
	_, err := os.Stat(local)
	if err == nil {
		return local, nil
	}

	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve config path: %w", err)
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "keikiban", "init.filo"), nil
}

// loadConfig reads and parses the config file. A missing file is not an
// error: the returned Config has Exists=false and the caller decides what to
// do (the GUI opens the first-run setup screen).
func loadConfig() (Config, error) {
	path, err := configPath()
	if err != nil {
		return Config{}, err
	}

	cfg := Config{Path: path}
	b, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg.Exists = true
	cfg.Connections, err = parseConfig(string(b))
	if err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

// parseConfig runs the Filo source and collects the connections it declares
// through the (database url [title]) builtin, same shape as dbv.
func parseConfig(src string) ([]Connection, error) {
	// Filo rejects a script with no forms ("empty script"), but a config file
	// holding only comments is legitimate: it is what deleting the last
	// connection leaves behind.
	if !hasCode(src) {
		return nil, nil
	}

	f := filo.New()
	defer f.Close()

	var conns []Connection
	err := f.RegisterBuiltin("database", func(_ context.Context, args []filo.Value) (filo.Value, error) {
		if len(args) < 1 {
			return filo.VBool(false), fmt.Errorf("database: url is required")
		}

		dbURL, err := args[0].AsString()
		if err != nil {
			return filo.VBool(false), fmt.Errorf("database: url must be a string: %w", err)
		}
		if dbURL == "" {
			return filo.VBool(false), fmt.Errorf("database: url must not be empty")
		}

		title := ""
		if len(args) >= 2 {
			title, err = args[1].AsString()
			if err != nil {
				return filo.VBool(false), fmt.Errorf("database: title must be a string: %w", err)
			}
		}

		if title == "" {
			title = defaultTitle(dbURL)
		}

		conns = append(conns, Connection{
			URL:       dbURL,
			Title:     title,
			MaskedURL: maskURL(dbURL),
		})
		return filo.VBool(true), nil
	})
	if err != nil {
		return nil, err
	}

	err = f.DoString(src)
	if err != nil {
		return nil, err
	}
	return conns, nil
}

// appendConnection adds one (database ...) form to the config file, creating
// the file and its directory on first use. It only ever appends; the rest of
// the file belongs to the user and is never rewritten.
func appendConnection(path, dbURL, title string) error {
	err := os.MkdirAll(filepath.Dir(path), 0o700)
	if err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	_, statErr := os.Stat(path)
	isNew := errors.Is(statErr, os.ErrNotExist)

	f, err := os.OpenFile(filepath.Clean(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open config %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var b strings.Builder
	if isNew {
		b.WriteString("; keikiban configuration\n")
	}
	b.WriteString("(database ")
	b.WriteString(filoQuote(dbURL))
	if title != "" {
		b.WriteString(" ")
		b.WriteString(filoQuote(title))
	}
	b.WriteString(")\n")

	_, err = f.WriteString(b.String())
	if err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return f.Close()
}

// updateConnection replaces the index-th (database ...) form in the config
// file, keeping the line's indentation and every other line untouched.
func updateConnection(path string, index int, dbURL, title string) error {
	form := "(database " + filoQuote(dbURL)
	if title != "" {
		form += " " + filoQuote(title)
	}
	form += ")"
	return rewriteConnectionLine(path, index, form)
}

// deleteConnection removes the index-th (database ...) form from the config
// file, leaving every other line untouched.
func deleteConnection(path string, index int) error {
	return rewriteConnectionLine(path, index, "")
}

// makeDefaultConnection moves the index-th (database ...) form to where the
// first one sits. The default connection is by definition the first declared,
// so promoting one is a line move: the line travels whole, carrying its
// indentation and any same-line comment, and nothing else in the file moves.
func makeDefaultConnection(path string, index int) error {
	lines, dbLines, err := readConnectionLines(path)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(dbLines) {
		return fmt.Errorf("connection %d does not exist", index)
	}
	if index == 0 {
		return nil
	}

	// index > 0, so the source line is always below the destination and
	// removing it cannot shift where the first one sits.
	from, to := dbLines[index], dbLines[0]
	line := lines[from]
	lines = slices.Insert(slices.Delete(lines, from, from+1), to, line)
	return writeConfigLines(path, lines)
}

// rewriteConnectionLine performs the surgical edit behind update/delete: it
// replaces (or, with an empty form, removes) the index-th (database ...) line.
// Everything else in the file, comments and unrelated settings included, is
// preserved verbatim.
func rewriteConnectionLine(path string, index int, form string) error {
	lines, dbLines, err := readConnectionLines(path)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(dbLines) {
		return fmt.Errorf("connection %d does not exist", index)
	}

	n := dbLines[index]
	if form == "" {
		lines = slices.Delete(lines, n, n+1)
	} else {
		trimmed := strings.TrimLeft(lines[n], " \t")
		indent := lines[n][:len(lines[n])-len(trimmed)]
		// Keep anything after the form, e.g. a same-line ; comment.
		suffix := trimmed[formEnd(trimmed):]
		lines[n] = indent + form + suffix
	}
	return writeConfigLines(path, lines)
}

// readConnectionLines reads the config and reports which of its lines hold
// exactly one complete (database ...) form each, in declaration order. When the
// file states connections in a shape line surgery cannot edit safely
// (multi-line or computed forms), it refuses with an error instead of guessing;
// the user edits the file directly.
func readConnectionLines(path string) (lines []string, dbLines []int, err error) {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, nil, fmt.Errorf("read config %s: %w", path, err)
	}

	conns, err := parseConfig(string(b))
	if err != nil {
		return nil, nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	lines = strings.Split(string(b), "\n")
	for i, line := range lines {
		if isDatabaseLine(line) {
			dbLines = append(dbLines, i)
		}
	}
	if len(dbLines) != len(conns) {
		return nil, nil, fmt.Errorf("config %s declares connections in a shape keikiban cannot edit safely (multi-line or computed forms); edit the file directly", path)
	}
	return lines, dbLines, nil
}

func writeConfigLines(path string, lines []string) error {
	err := os.WriteFile(filepath.Clean(path), []byte(strings.Join(lines, "\n")), 0o600) // #nosec G703 -- path comes from configPath (env/home), not untrusted input
	if err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}

// defaultTitle names an untitled connection without ever exposing the URL on
// screen (a URL can carry a password): the database name when the connection
// string states one, otherwise the host.
func defaultTitle(dbURL string) string {
	cc, err := pgx.ParseConfig(dbURL)
	if err != nil {
		return "connection"
	}
	if cc.Database != "" {
		return cc.Database
	}
	if cc.Host != "" {
		return cc.Host
	}
	return "connection"
}

// hasCode reports whether the Filo source contains anything beyond whitespace
// and ; comments.
func hasCode(src string) bool {
	src = strings.TrimPrefix(src, "\uFEFF")
	for line := range strings.SplitSeq(src, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, ";") {
			continue
		}
		return true
	}
	return false
}

// isDatabaseLine reports whether line starts a (database ...) form that is
// complete on that same line.
func isDatabaseLine(line string) bool {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "(database ") {
		return false
	}
	return formEnd(t) > 0
}

// formEnd scans a line that starts with "(" and returns the index just past
// the parenthesis that closes the first form (string literals and their
// escapes respected), or 0 when the form does not close on this line.
func formEnd(t string) int {
	depth := 0
	inString := false
	escaped := false
	for i, r := range t {
		if inString {
			switch {
			case escaped:
				escaped = false
			case r == '\\':
				escaped = true
			case r == '"':
				inString = false
			}
			continue
		}
		switch r {
		case '"':
			inString = true
		case ';':
			return 0
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return 0
}

// filoQuote renders s as a Filo string literal.
func filoQuote(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\n", `\n`,
		"\t", `\t`,
		"\r", `\r`,
	)
	return `"` + r.Replace(s) + `"`
}

// maskURL hides the password in a connection string so it can be shown or
// logged. It handles both URL form (postgres://user:pass@host/db) and
// key=value DSN form (host=... password=...). A string it cannot parse is
// fully redacted rather than leaked.
func maskURL(dbURL string) string {
	if !strings.Contains(dbURL, "://") {
		fields := strings.Fields(dbURL)
		for i, field := range fields {
			if strings.HasPrefix(field, "password=") {
				fields[i] = "password=..."
			}
		}
		return strings.Join(fields, " ")
	}

	u, err := url.Parse(dbURL)
	if err != nil {
		return "(unparseable connection string)"
	}
	if u.User != nil {
		username := u.User.Username()
		_, hasPassword := u.User.Password()
		if hasPassword {
			u.User = url.UserPassword(username, "...")
		}
	}
	return u.String()
}
