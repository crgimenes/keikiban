#!/bin/sh
# Record the data the screenshots are drawn from.
#
#   sh tools/record_demo.sh <connection-url> [seconds]
#
# Runs the app's own one-shot commands against a live database and stores the
# answers in tools/uimock/recorded/. Those files are what the mocked UI
# serves, so every published screenshot shows a real measurement rather than
# a drawing of one.
#
# Point it at a scratch database with a real workload on it (pgbench does the
# job) and never at a database whose table names you would not publish: these
# files are committed.

set -eu

cd "$(dirname "$0")/.."
URL="${1:?usage: record_demo.sh <connection-url> [seconds]}"
SECONDS_TO_SAMPLE="${2:-60}"
OUT=tools/uimock/recorded
CFG=$(mktemp -d)

mkdir -p "$OUT" "$CFG/keikiban"
printf '(database "%s" "keikibench")\n' "$URL" > "$CFG/keikiban/init.filo"

go build -o /tmp/keikiban-record .
run() {
    XDG_CONFIG_HOME="$CFG" /tmp/keikiban-record -json "$@" > "$OUT/$1.json"
    echo "recorded $OUT/$1.json"
}

# The load chart needs a window of samples, so this one takes a while.
XDG_CONFIG_HOME="$CFG" /tmp/keikiban-record -json dashboard "$SECONDS_TO_SAMPLE" \
    > "$OUT/dashboard.json"
echo "recorded $OUT/dashboard.json"
run sessions
run indexes
run maintenance
run locks

# These files are committed, so the host they were recorded against does not
# travel with them. Only cosmetic fields are touched; every measurement stays
# exactly as the server reported it.
python3 - "$OUT" <<'PY'
import json
import pathlib
import sys

out = pathlib.Path(sys.argv[1])
HOST_FIELDS = {"host"}

def scrub(node):
    if isinstance(node, dict):
        for key, value in node.items():
            if key == "url" and isinstance(value, str):
                node[key] = "postgres://postgres:...@db.local:5432/keikibench"
            elif key in HOST_FIELDS and isinstance(value, str):
                node[key] = "10.0.0.10"
            else:
                scrub(value)
    elif isinstance(node, list):
        for item in node:
            scrub(item)

for path in sorted(out.glob("*.json")):
    data = json.loads(path.read_text())
    scrub(data)
    path.write_text(json.dumps(data, indent=2) + "\n")
    print("scrubbed", path)
PY
