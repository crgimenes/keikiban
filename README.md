# keikiban

A PostgreSQL dashboard that stays up when the database does not.

![keikiban dashboard](web/dashboard-dark.png)

The tools I had were built for browsing schemas. When something is wrong at
3am, browsing a schema is not what I need. I need to know which query is
eating the server, who is blocking whom, and whether that index nobody
scanned in six months is worth its gigabyte.

pgAdmin's dashboard falls over exactly when the database is busy, which is
the only time I open it. DBeaver keeps several connections live at once, so
the fastest way to run a command against production is to think you are on
staging. keikiban is my answer to both.

## What it shows

**Database load over time**, the way RDS Performance Insights draws it:
average active sessions stacked by wait event, sliced by SQL, user, host,
application, or database. Sampled once a second from `pg_stat_activity` while
the app is open. No agent on the server, no extension required.

**Top SQL**, ranked by its share of that load, each statement broken down by
what it waited on. With `pg_stat_statements` installed it also carries
calls/s, rows/call and ms/call. Without it the ranking still works, and a
yellow badge says what installing it would add.

**Blocking**, live: who holds the lock, who waits on it, the SQL on both
sides, the lock mode, and how long the wait has been going. Chains are
detected, so the middle link is labelled instead of being mistaken for the
culprit.

**Sessions**, with cancel and terminate. **Index health**: unused, duplicate
and invalid indexes, sequential-scan offenders, cache hit ratios.
**Maintenance**: transaction ID wraparound, dead rows, long-running
transactions, sequences approaching overflow.

Every destructive action shows the exact statement first, and lets you copy it
and run it somewhere else instead.

## Install

Requires PostgreSQL 12 or newer. Nothing to install on the server.

    brew install crgimenes/tap/keikiban

Or take a binary from the [releases](https://github.com/crgimenes/keikiban/releases):

| System | File |
| --- | --- |
| macOS (Intel and Apple Silicon) | `keikiban-darwin-universal.zip` |
| Windows x64 | `keikiban-windows-amd64.zip` |
| Linux x64 / arm64 | `keikiban-linux-amd64.gz`, `keikiban-linux-arm64.gz` |

The macOS build is signed and notarized, so it opens normally. If you build it
yourself and Gatekeeper complains, `xattr -dr com.apple.quarantine keikiban`
clears it. Windows SmartScreen may want "More info" then "Run anyway".

## Configure

Connections live in a [Filo](https://github.com/crgimenes/filo) file at
`~/.config/keikiban/init.filo`, or `./keikiban_init.filo` if you keep one per
project. On first run the app asks for a connection and writes the file.

```lisp
; keikiban configuration
(database "postgres://user:pass@db.internal:5432/app" "Production")
(database "postgres://user:pass@localhost:5432/app" "Local")
```

The first entry is the default and loads when the app opens. URL form and
`host=... dbname=...` form both work. keikiban only appends to this file, so
whatever else you keep in there survives.

**One connection at a time.** Connecting to one closes the other. That is
deliberate: the name of the attached database sits in the sidebar and in the
window title, so a command cannot land on a server you were not looking at.

## Command line

The GUI is the product. The flags exist so an AI agent can drive it:

    keikiban -json connections     configured connections
    keikiban -json sessions        current sessions
    keikiban -json locks           blocking tree
    keikiban -json indexes         index health report
    keikiban -json maintenance     vacuum, wraparound, sequences

Each prints one JSON document to stdout and exits. Passwords are masked
everywhere, including in `-debug`, which writes `key=value` diagnostic lines
to stderr.

## Build

    go build

Go 1.26, no cgo. Runtime dependencies are
[glaze](https://github.com/crgimenes/glaze) for the window,
[filo](https://github.com/crgimenes/filo) for the config file and
[pgx](https://github.com/jackc/pgx) for the wire protocol. The interface is
HTML drawn by the system webview, so it uses the platform's own controls and
follows the system light and dark theme.

## License

MIT
