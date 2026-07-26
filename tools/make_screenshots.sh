#!/bin/sh
# Capture the screenshots used by the README and the project site.
#
#   sh tools/make_screenshots.sh
#
# Shots come from the mocked UI (tools/uimock), not from a live database: they
# are reproducible, they never leak a real server's data, and they show a
# busy database instead of whatever the developer's test box is doing.

set -eu

cd "$(dirname "$0")/.."
PORT=8791
CHROME="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
[ -x "$CHROME" ] || { echo "Chrome not found at $CHROME" >&2; exit 1; }

sh tools/uimock/serve.sh "$PORT" >/dev/null 2>&1 &
SERVER=$!
trap 'kill $SERVER 2>/dev/null || true' EXIT
sleep 2

mkdir -p web
shot() { # shot <file> <query> <height>
    "$CHROME" --headless=new --disable-gpu --hide-scrollbars \
        --window-size=1280,"$3" --screenshot="web/$1" \
        "http://localhost:$PORT/?$2" 2>/dev/null
    echo "wrote web/$1"
}

shot dashboard-dark.png "theme=dark" 1000
shot dashboard-light.png "theme=light" 1000
shot indexes-dark.png "theme=dark&screen=indexes" 900
# Shorter frame for the README: GitHub scales an image to the column width,
# and a full-page capture arrives with unreadable text.
shot readme-dark.png "theme=dark" 655
