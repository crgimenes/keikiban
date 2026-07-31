#!/bin/sh
# Serve the interface with mocked data, no database and no window needed.
#
#   sh tools/uimock/serve.sh [port]     then open http://localhost:8791
#
# Useful for screenshots and for working on the UI offline. It copies ui/ to a
# temporary directory and injects mock.js ahead of app.js, so the real files
# are never touched.

set -eu

cd "$(dirname "$0")/../.."
PORT="${1:-8791}"
DIR=$(mktemp -d)

cp ui/index.html ui/app.js ui/style.css ui/icons.js ui/favicon.png "$DIR/"
cp ui/object.html ui/object.js "$DIR/"
cp tools/uimock/mock.js "$DIR/"
cp -R tools/uimock/recorded "$DIR/recorded"
# The mock must define window.* before the page script runs, in both windows:
# the object window is a page of its own and has its own binds.
sed -i '' 's|<script src="app.js"></script>|<script src="mock.js"></script>\n  <script src="app.js"></script>|' "$DIR/index.html"
sed -i '' 's|<script src="object.js"></script>|<script src="mock.js"></script>\n  <script src="object.js"></script>|' "$DIR/object.html"

echo "serving mocked UI on http://localhost:$PORT (Ctrl+C to stop)"
cd "$DIR" && exec python3 -m http.server "$PORT" --bind 127.0.0.1
