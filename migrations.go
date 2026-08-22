package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/crgimenes/filo"
	"github.com/crgimenes/migration/core"
	"github.com/crgimenes/migration/drift"
	"github.com/crgimenes/migration/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

// The Migrations screen embeds the migration project's library packages
// (core, drift, gen) instead of shelling out to its binary — crg's chosen
// direction. The two apps stay independent: keikiban only READS migration's
// config to find the directory for the connected database, and the library
// packages register no SQL driver, so the connection here is keikiban's own
// pgx one wrapped for sqlx.
//
// Read paths (status, drift, previews) never write anything — not even the
// schema_migrations tracking table, which the migration CLI creates on first
// contact. Opening a screen must not alter a production database; the table
// appears when the user runs the first migration, as part of that consented
// action.

const migRunTimeout = 5 * time.Minute

// migDrift is the live-schema-vs-snapshot comparison. Its own error field
// keeps a missing snapshot (a normal state) from hiding the rest.
type migDrift struct {
	SnapVersion int      `json:"snapVersion"`
	Changes     []string `json:"changes"`
	Error       string   `json:"error,omitempty"`
}

type migStatusOut struct {
	Error string `json:"error,omitempty"`
	Dir   string `json:"dir"`
	// DirSource says where Dir came from: "migration config" when matched
	// from migration's own saved connections, "manual" when typed in the
	// screen. Empty Dir means neither produced one.
	DirSource   string    `json:"dirSource"`
	TableExists bool      `json:"tableExists"`
	Applied     int       `json:"applied"`
	Pending     []string  `json:"pending"`
	Drift       *migDrift `json:"drift,omitempty"`
}

type migPreviewOut struct {
	Error string `json:"error,omitempty"`
	// Note carries a non-error outcome such as "no drift to capture".
	Note  string     `json:"note,omitempty"`
	Files []namedDef `json:"files"`
}

type migApplyOut struct {
	Error   string       `json:"error,omitempty"`
	Message string       `json:"message,omitempty"`
	Status  migStatusOut `json:"status"`
}

// migrationConfigPath mirrors the migration app's own resolution exactly
// (migration_init.filo in the current directory, else os.UserConfigDir),
// because the point is reading THE file that app maintains. keikiban's own
// config deliberately uses XDG instead; the difference is theirs to keep.
func migrationConfigPath() (string, error) {
	local := "migration_init.filo"
	_, err := os.Stat(local)
	if err == nil {
		return local, nil
	}

	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve migration config path: %w", err)
	}
	return filepath.Join(base, "migration", "init.filo"), nil
}

// migrationDirFor finds the migrations directory migration has saved for
// this database, by exact URL match — the same handoff contract migration's
// own resolveGUITarget applies. Read-only: that file belongs to migration.
func migrationDirFor(dbURL string) (string, error) {
	path, err := migrationConfigPath()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read migration config %s: %w", path, err)
	}
	return migrationDirInConfig(string(b), dbURL)
}

// migrationDirInConfig parses migration's (connection url dir [title]) forms
// and returns the dir saved for dbURL. Split from migrationDirFor so tests
// exercise the parsing without touching the real config.
func migrationDirInConfig(src, dbURL string) (string, error) {
	if !hasCode(src) {
		return "", nil
	}

	f := filo.New()
	defer f.Close()

	dir := ""
	err := f.RegisterBuiltin("connection", func(_ context.Context, args []filo.Value) (filo.Value, error) {
		if len(args) < 2 {
			return filo.VBool(false), errors.New("connection: url and dir are required")
		}
		u, err := args[0].AsString()
		if err != nil {
			return filo.VBool(false), err
		}
		d, err := args[1].AsString()
		if err != nil {
			return filo.VBool(false), err
		}
		if u == dbURL && dir == "" {
			dir = d
		}
		return filo.VBool(true), nil
	})
	if err != nil {
		return "", err
	}

	err = f.DoString(src)
	if err != nil {
		return "", fmt.Errorf("parse migration config: %w", err)
	}
	return dir, nil
}

// openMigDB wraps keikiban's own pgx connection for the migration packages.
// stdlib.OpenDB takes the parsed config directly, so no database/sql driver
// name is ever registered or looked up; the "postgres" passed to sqlx only
// selects $1 placeholders.
func openMigDB(ctx context.Context, dbURL string) (*sqlx.DB, *core.DatabaseConfig, func(), error) {
	cfg, err := core.GetDatabaseConfig(dbURL)
	if err != nil {
		return nil, nil, nil, err
	}

	connCfg, err := pgx.ParseConfig(dbURL)
	if err != nil {
		return nil, nil, nil, err
	}

	db := sqlx.NewDb(stdlib.OpenDB(*connCfg), "postgres")
	closer := func() { _ = db.Close() }

	pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	err = db.PingContext(pingCtx)
	if err != nil {
		closer()
		return nil, nil, nil, err
	}
	return db, cfg, closer, nil
}

// migPendingFiles lists the up files newer than the applied version, in
// order. Base names only: the screen shows files, not the user's paths.
func migPendingFiles(dir string, applied int) ([]string, []string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(files)

	names := []string{}
	paths := []string{}
	for _, f := range files {
		if core.FileVersion(f) <= applied {
			continue
		}
		names = append(names, filepath.Base(f))
		paths = append(paths, f)
	}
	return names, paths, nil
}

// collectMigrationStatus is the screen's read path. dir == "" means "resolve
// it from migration's config"; a still-empty answer is a normal state the
// screen handles, not an error.
func collectMigrationStatus(ctx context.Context, dbURL, dir string) migStatusOut {
	out := migStatusOut{Pending: []string{}, DirSource: "manual"}

	if dir == "" {
		resolved, err := migrationDirFor(dbURL)
		if err != nil {
			out.Error = err.Error()
			return out
		}
		dir = resolved
		out.DirSource = "migration config"
	}
	out.Dir = dir
	if dir == "" {
		out.DirSource = ""
		return out
	}

	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		out.Error = "migrations directory not found: " + dir
		return out
	}

	db, cfg, closer, err := openMigDB(ctx, dbURL)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer closer()

	qctx, cancel := context.WithTimeout(ctx, indexQueryTimeout)
	defer cancel()

	// Existence is checked with the library's own read-only query; the
	// create-if-missing entry points stay out of the read path on purpose.
	count := 0
	err = db.GetContext(qctx, &count, cfg.CheckTableExistsSQL)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.TableExists = count > 0

	if out.TableExists {
		out.Applied, err = core.GetMigrationMax(qctx, db, cfg)
		if err != nil {
			out.Error = err.Error()
			return out
		}
	}

	out.Pending, _, err = migPendingFiles(dir, out.Applied)
	if err != nil {
		out.Error = err.Error()
		return out
	}

	d := &migDrift{Changes: []string{}}
	snapVersion, _, _, changes, err := drift.LiveDiff(qctx, db, dir)
	if err != nil {
		d.Error = err.Error()
	}
	if err == nil {
		d.SnapVersion = snapVersion
		for _, c := range changes {
			d.Changes = append(d.Changes, c.String())
		}
	}
	out.Drift = d
	return out
}

// migCaptureName keeps the capture name a plain filename fragment: it lands
// verbatim in the migration pair's filenames, so a path separator or a dot
// sequence would escape the migrations directory.
var migCaptureName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// collectMigrationPreview shows the EXACT SQL an action would run or write,
// before anything happens — the same DDL-exposed contract as every other
// destructive screen. It never mutates: capture's preview generates the pair
// in memory and shows it, without writing files or versions.
func collectMigrationPreview(ctx context.Context, dbURL, dir, action, name string) migPreviewOut {
	out := migPreviewOut{Files: []namedDef{}}
	if dir == "" {
		out.Error = "no migrations directory"
		return out
	}

	db, cfg, closer, err := openMigDB(ctx, dbURL)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer closer()

	qctx, cancel := context.WithTimeout(ctx, indexQueryTimeout)
	defer cancel()

	applied := 0
	count := 0
	err = db.GetContext(qctx, &count, cfg.CheckTableExistsSQL)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	if count > 0 {
		applied, err = core.GetMigrationMax(qctx, db, cfg)
		if err != nil {
			out.Error = err.Error()
			return out
		}
	}

	switch action {
	case "up":
		_, paths, err := migPendingFiles(dir, applied)
		if err != nil {
			out.Error = err.Error()
			return out
		}
		if len(paths) == 0 {
			out.Note = "nothing pending"
			return out
		}
		for _, p := range paths {
			sql, err := os.ReadFile(filepath.Clean(p))
			if err != nil {
				out.Error = err.Error()
				return out
			}
			out.Files = append(out.Files, namedDef{Name: filepath.Base(p), Def: string(sql)})
		}
	case "down":
		if applied == 0 {
			out.Note = "nothing applied, nothing to revert"
			return out
		}
		downs, err := filepath.Glob(filepath.Join(dir, "*.down.sql"))
		if err != nil {
			out.Error = err.Error()
			return out
		}
		for _, p := range downs {
			if core.FileVersion(p) != applied {
				continue
			}
			sql, err := os.ReadFile(filepath.Clean(p))
			if err != nil {
				out.Error = err.Error()
				return out
			}
			out.Files = append(out.Files, namedDef{Name: filepath.Base(p), Def: string(sql)})
		}
		if len(out.Files) == 0 {
			out.Error = fmt.Sprintf("no down file for version %d in %s", applied, dir)
		}
	case "capture":
		if !migCaptureName.MatchString(name) {
			out.Error = "capture name must be letters, digits, _ or - (max 64)"
			return out
		}
		_, snap, live, changes, err := drift.LiveDiff(qctx, db, dir)
		if err != nil {
			out.Error = err.Error()
			return out
		}
		if len(changes) == 0 {
			out.Note = "no drift to capture"
			return out
		}
		upSQL, downSQL := gen.Generate(changes, snap, live)
		next := applied + 1
		out.Files = append(out.Files,
			namedDef{Name: fmt.Sprintf("%03d_%s.up.sql", next, name), Def: upSQL},
			namedDef{Name: fmt.Sprintf("%03d_%s.down.sql", next, name), Def: downSQL},
		)
	default:
		out.Error = "unknown action: " + action
	}
	return out
}

// applyMigrationAction executes what the preview showed. "down" always means
// exactly one step: the library's bare "down" reverts EVERYTHING applied,
// which no button should ever say implicitly.
func applyMigrationAction(ctx context.Context, dbURL, dir, action, name string) migApplyOut {
	out := migApplyOut{}
	if dir == "" {
		out.Error = "no migrations directory"
		out.Status = collectMigrationStatus(ctx, dbURL, dir)
		return out
	}

	db, cfg, closer, err := openMigDB(ctx, dbURL)
	if err != nil {
		out.Error = err.Error()
		out.Status = collectMigrationStatus(ctx, dbURL, dir)
		return out
	}
	defer closer()

	runCtx, cancel := context.WithTimeout(ctx, migRunTimeout)
	defer cancel()

	switch action {
	case "up":
		n, executed, err := core.RunWithExistingDatabase(runCtx, dir, "up", db, cfg)
		out.Error = errText(err)
		if err == nil {
			out.Message = fmt.Sprintf("applied %d migration(s)%s", n, fileList(executed))
		}
	case "down":
		n, executed, err := core.RunWithExistingDatabase(runCtx, dir, "down 1", db, cfg)
		out.Error = errText(err)
		if err == nil {
			out.Message = fmt.Sprintf("reverted %d migration(s)%s", n, fileList(executed))
		}
	case "capture":
		if !migCaptureName.MatchString(name) {
			out.Error = "capture name must be letters, digits, _ or - (max 64)"
			break
		}
		o, err := drift.Capture(runCtx, db, cfg, dir, name)
		out.Error = errText(err)
		if err == nil && !o.Drift {
			out.Message = "no drift to capture"
		}
		if err == nil && o.Drift {
			out.Message = fmt.Sprintf("captured drift as version %d (%s, %s)",
				o.Version, filepath.Base(o.UpFile), filepath.Base(o.DownFile))
			debugf("event=migration_capture version=%d", o.Version)
		}
	default:
		out.Error = "unknown action: " + action
	}

	if out.Error == "" && action != "capture" {
		debugf("event=migration_%s ok=true", action)
	}
	out.Status = collectMigrationStatus(ctx, dbURL, dir)
	return out
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func fileList(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	names := make([]string, 0, len(paths))
	for _, p := range paths {
		names = append(names, filepath.Base(p))
	}
	return ": " + strings.Join(names, ", ")
}
