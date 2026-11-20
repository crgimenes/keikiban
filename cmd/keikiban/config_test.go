package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseConfig(t *testing.T) {
	src := `; comment
(database "postgres://alice:secret@db.example.com:5432/app" "Production")
(database "postgres://localhost/dev")
`
	conns, err := parseConfig(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(conns) != 2 {
		t.Fatalf("got %d connections, want 2", len(conns))
	}

	if conns[0].Title != "Production" {
		t.Errorf("title = %q, want Production", conns[0].Title)
	}
	if conns[0].URL != "postgres://alice:secret@db.example.com:5432/app" {
		t.Errorf("url = %q", conns[0].URL)
	}
	if strings.Contains(conns[0].MaskedURL, "secret") {
		t.Errorf("masked url leaks password: %q", conns[0].MaskedURL)
	}

	// No title: defaults to the database name, never the URL (a URL can carry
	// a password and must not reach the screen).
	if conns[1].Title != "dev" {
		t.Errorf("default title = %q, want dev", conns[1].Title)
	}
}

func TestDefaultTitle(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"postgres://alice:secret@host:5432/app", "app"},
		{"host=localhost dbname=app password=x", "app"},
		{"postgres://alice@host:5432/", "host"}, // no dbname stated: host
	}
	for _, c := range cases {
		got := defaultTitle(c.in)
		if got != c.want {
			t.Errorf("defaultTitle(%q) = %q, want %q", c.in, got, c.want)
		}
		if strings.Contains(got, "secret") || strings.Contains(got, "password") {
			t.Errorf("defaultTitle(%q) leaks credentials: %q", c.in, got)
		}
	}
}

func TestParseConfigCommentsOnly(t *testing.T) {
	// Deleting the last connection leaves the file with only the header
	// comment; that must load as an empty config, not a parse error.
	cases := []string{
		"",
		"\n\n",
		"; keikiban configuration\n",
		"\uFEFF; comment after BOM\n",
	}
	for _, src := range cases {
		conns, err := parseConfig(src)
		if err != nil {
			t.Errorf("parseConfig(%q) error: %v", src, err)
		}
		if len(conns) != 0 {
			t.Errorf("parseConfig(%q) = %v, want none", src, conns)
		}
	}
}

func TestParseConfigErrors(t *testing.T) {
	cases := []string{
		`(database)`,
		`(database "")`,
		`(database 42)`,
		`(database "postgres://x" 42)`,
	}
	for _, src := range cases {
		_, err := parseConfig(src)
		if err == nil {
			t.Errorf("parseConfig(%q) = nil error, want error", src)
		}
	}
}

func TestAppendConnectionRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keikiban", "init.filo")

	err := appendConnection(path, "postgres://bob:p4ss@host/db", "")
	if err != nil {
		t.Fatal(err)
	}
	// Titles with characters that need Filo escaping must survive the trip.
	err = appendConnection(path, "postgres://localhost/dev", `quo"ted \ title`)
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	conns, err := parseConfig(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if len(conns) != 2 {
		t.Fatalf("got %d connections, want 2", len(conns))
	}
	if conns[0].URL != "postgres://bob:p4ss@host/db" {
		t.Errorf("url = %q", conns[0].URL)
	}
	if conns[1].Title != `quo"ted \ title` {
		t.Errorf("title = %q", conns[1].Title)
	}
}

func TestUpdateAndDeleteConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "init.filo")
	src := `; keikiban configuration
(database "postgres://a@h1/db1" "One") ; prod note
(set Unrelated 42)
  (database "postgres://b@h2/db2" "Two")
`
	err := os.WriteFile(path, []byte(src), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// Update keeps indentation, the same-line comment, and unrelated lines.
	err = updateConnection(path, 0, "postgres://a@h1/db1new", "One!")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{
		"; keikiban configuration",
		`(database "postgres://a@h1/db1new" "One!") ; prod note`,
		"(set Unrelated 42)",
		`  (database "postgres://b@h2/db2" "Two")`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("after update, config lost %q:\n%s", want, got)
		}
	}

	err = deleteConnection(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	conns, err := parseConfig(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if len(conns) != 1 || conns[0].Title != "Two" {
		t.Errorf("after delete, connections = %+v", conns)
	}
	if !strings.Contains(string(b), "(set Unrelated 42)") {
		t.Error("delete touched an unrelated line")
	}
}

func TestMakeDefaultConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "init.filo")
	src := `; keikiban configuration
(database "postgres://a@h1/db1" "One")
(set Unrelated 42)
  (database "postgres://b@h2/db2" "Two") ; staging note
(database "postgres://c@h3/db3" "Three")
`
	err := os.WriteFile(path, []byte(src), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = makeDefaultConnection(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	conns, err := parseConfig(string(b))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Two", "One", "Three"}
	for i, title := range want {
		if conns[i].Title != title {
			t.Errorf("connection %d = %q, want %q\n%s", i, conns[i].Title, title, b)
		}
	}
	// The line travels whole: its indentation and same-line comment come along.
	if !strings.Contains(string(b), `  (database "postgres://b@h2/db2" "Two") ; staging note`) {
		t.Errorf("promoted line lost its indentation or comment:\n%s", b)
	}
	if !strings.Contains(string(b), "(set Unrelated 42)") {
		t.Errorf("promotion touched an unrelated line:\n%s", b)
	}
	// The header comment stays on top; the promoted form goes where the first
	// connection was, not above everything.
	if !strings.HasPrefix(string(b), "; keikiban configuration\n  (database") {
		t.Errorf("promotion did not land on the first connection's line:\n%s", b)
	}

	// Promoting the one that is already default is a no-op, not an error.
	before := string(b)
	err = makeDefaultConnection(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != before {
		t.Errorf("promoting the default rewrote the file:\n%s", b)
	}

	err = makeDefaultConnection(path, 3)
	if err == nil {
		t.Error("promoting a connection that does not exist must fail")
	}
}

func TestRewriteRefusesUneditableShapes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "init.filo")
	// A multi-line form parses fine but cannot be edited line-surgically.
	src := "(database\n  \"postgres://a@h/db\")\n"
	err := os.WriteFile(path, []byte(src), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = deleteConnection(path, 0)
	if err == nil {
		t.Fatal("delete on a multi-line form must refuse, got nil error")
	}
	err = updateConnection(path, 0, "postgres://x@h/db", "")
	if err == nil {
		t.Fatal("update on a multi-line form must refuse, got nil error")
	}
	err = makeDefaultConnection(path, 0)
	if err == nil {
		t.Fatal("promotion on a multi-line form must refuse, got nil error")
	}
	// And the refusal must not have touched the file.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != src {
		t.Error("refusal modified the file")
	}
}

func TestIsDatabaseLine(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{`(database "postgres://a@h/db")`, true},
		{`  (database "u" "t") ; note`, true},
		{`(database "has ) paren and \" quote")`, true},
		{`(database`, false},
		{`; (database "commented out")`, false},
		{`(set X 1)`, false},
	}
	for _, c := range cases {
		got := isDatabaseLine(c.in)
		if got != c.want {
			t.Errorf("isDatabaseLine(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestMaskURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"postgres://alice:secret@host:5432/app", "postgres://alice:...@host:5432/app"},
		{"postgres://host/app", "postgres://host/app"},
		{"postgres://alice@host/app", "postgres://alice@host/app"},
		{"host=localhost password=hunter2 dbname=app", "host=localhost password=... dbname=app"},
		{"host=localhost dbname=app", "host=localhost dbname=app"},
	}
	for _, c := range cases {
		got := maskURL(c.in)
		if got != c.want {
			t.Errorf("maskURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAssetName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"app://keikiban/", "index.html"},
		{"app://keikiban/app.js", "app.js"},
		{"app://keikiban/../etc/passwd", "etc/passwd"},
		{"/index.html", "index.html"},
	}
	for _, c := range cases {
		got := assetName(c.in)
		if got != c.want {
			t.Errorf("assetName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
