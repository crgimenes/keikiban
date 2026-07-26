#!/bin/sh
# Derive every icon format from the master PNG.
#
#   python3 tools/make_icon.py   # draws assets/keikiban.png (1024px)
#   sh tools/make_icon.sh        # this script, macOS only (needs iconutil)
#
# Produces:
#   assets/keikiban.icns   macOS app bundle icon
#   ui/favicon.png         the icon the window itself shows on Windows/Linux
#   web/favicon.png        the project site

set -eu

cd "$(dirname "$0")/.."
MASTER=assets/keikiban.png
SET=$(mktemp -d)/keikiban.iconset
mkdir -p "$SET"

for size in 16 32 128 256 512; do
    sips -z $size $size "$MASTER" --out "$SET/icon_${size}x${size}.png" >/dev/null
    double=$((size * 2))
    sips -z $double $double "$MASTER" \
        --out "$SET/icon_${size}x${size}@2x.png" >/dev/null
done

iconutil -c icns "$SET" -o assets/keikiban.icns
sips -z 256 256 "$MASTER" --out ui/favicon.png >/dev/null
mkdir -p web
cp ui/favicon.png web/favicon.png

echo "wrote assets/keikiban.icns, ui/favicon.png, web/favicon.png"
