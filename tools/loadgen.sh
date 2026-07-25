#!/bin/sh
# Random-load generator for the keikiban test database.
#
# Reads the first connection URL from the keikiban config and runs a mix of
# random queries (CPU burns, sleeps, sorts, catalog joins) until stopped, so
# the dashboard has something to show without a human driving DBeaver.
#
#   ./tools/loadgen.sh            run until Ctrl+C (or pkill -f loadgen.sh)
#   ./tools/loadgen.sh 120        run for 120 seconds and exit

set -eu

CONFIG="${XDG_CONFIG_HOME:-$HOME/.config}/keikiban/init.filo"
URL=$(grep -o '"postgres://[^"]*"' "$CONFIG" | head -1 | tr -d '"')
if [ -z "$URL" ]; then
    echo "loadgen: no connection URL found in $CONFIG" >&2
    exit 1
fi

DURATION="${1:-0}"
START=$(date +%s)

# -X everywhere: a decorated .psqlrc turns query output into box drawings.
BIG_TABLE=$(psql -X "$URL" -qtAc "SELECT quote_ident(schemaname) || '.' ||
    quote_ident(relname)
    FROM pg_stat_user_tables
    ORDER BY pg_total_relation_size(relid) DESC
    LIMIT 1;")
if [ -z "$BIG_TABLE" ]; then
    BIG_TABLE="pg_class"
fi

run_random_query() {
    case $(( $(od -An -N1 -tu1 /dev/urandom | tr -d ' ') % 7 )) in
    0) q="SELECT count(*) FROM generate_series(1, 20000000);" ;;
    1) q="SELECT pg_sleep(2 + random() * 3);" ;;
    2) q="SELECT sum(g), md5(g::text) FROM generate_series(1, 300000) g GROUP BY md5(g::text) ORDER BY 2 LIMIT 10;" ;;
    3) q="SELECT c1.relname, c2.relname FROM pg_class c1 CROSS JOIN pg_class c2 ORDER BY random() LIMIT 50000;" ;;
    4) q="SELECT g, random() FROM generate_series(1, 2000000) g ORDER BY 2 DESC LIMIT 5;" ;;
    5) q="SELECT pg_sleep(1);" ;;
    # Real disk traffic. TABLESAMPLE reads blocks scattered across the whole
    # table, so the pages are rarely cached; a plain LIMIT/OFFSET count gets
    # answered by an index-only scan and produces no reads at all.
    6) q="SELECT count(*) FROM $BIG_TABLE TABLESAMPLE SYSTEM (0.3);" ;;
    esac
    psql -X "$URL" -qAt -c "$q" >/dev/null 2>&1 &
}

echo "loadgen: running against $(echo "$URL" | sed 's#//.*@#//***@#') (stop with Ctrl+C)"
trap 'echo "loadgen: stopping"; exit 0' INT TERM

while :; do
    # 1 to 3 concurrent queries per round, then a short breather.
    n=$(( $(od -An -N1 -tu1 /dev/urandom | tr -d ' ') % 3 + 1 ))
    i=0
    while [ "$i" -lt "$n" ]; do
        run_random_query
        i=$((i + 1))
    done
    sleep 2
    if [ "$DURATION" -gt 0 ] && [ $(( $(date +%s) - START )) -ge "$DURATION" ]; then
        echo "loadgen: duration reached"
        exit 0
    fi
done
