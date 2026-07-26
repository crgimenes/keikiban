#!/usr/bin/env python3
"""Draw the GitHub social preview card (1280x640).

    python3 tools/make_social.py

GitHub scales the card down hard in timelines, so this stays deliberately
plain: icon, name, one line of what it is. Nothing that needs to be read at
thumbnail size beyond the name.
"""

import os

from PIL import Image, ImageDraw, ImageFont

W, H = 1280, 640
BG_TOP = (32, 43, 61)
BG_BOTTOM = (17, 24, 35)
TEXT = (240, 244, 248)
MUTED = (150, 163, 180)
ACCENT = (38, 166, 154)

HERE = os.path.dirname(__file__)
ICON = os.path.join(HERE, "..", "assets", "keikiban.png")
OUT = os.path.join(HERE, "..", "web", "social-preview.png")

FONTS = [
    "/System/Library/Fonts/SFNS.ttf",
    "/System/Library/Fonts/Helvetica.ttc",
    "/Library/Fonts/Arial.ttf",
]


def font(size, bold=False):
    for path in FONTS:
        if os.path.exists(path):
            try:
                return ImageFont.truetype(path, size, index=1 if bold else 0)
            except OSError:
                continue
    return ImageFont.load_default()


def lerp(a, b, t):
    return tuple(round(x + (y - x) * t) for x, y in zip(a, b))


def main():
    img = Image.new("RGB", (W, H))
    d = ImageDraw.Draw(img)
    for y in range(H):
        d.line([(0, y), (W, y)], fill=lerp(BG_TOP, BG_BOTTOM, y / H))

    icon = Image.open(ICON).convert("RGBA").resize((260, 260), Image.LANCZOS)
    img.paste(icon, (110, 190), icon)

    x = 430
    d.text((x, 216), "keikiban", font=font(96, bold=True), fill=TEXT)
    d.text((x, 330), "A PostgreSQL dashboard that stays up",
           font=font(38), fill=MUTED)
    d.text((x, 380), "when the database does not.",
           font=font(38), fill=MUTED)
    d.text((x, 452), "Database load  ·  Top SQL  ·  Locks  ·  Index health",
           font=font(28), fill=ACCENT)

    os.makedirs(os.path.dirname(OUT), exist_ok=True)
    img.save(OUT)
    print("wrote", os.path.normpath(OUT))


if __name__ == "__main__":
    main()
