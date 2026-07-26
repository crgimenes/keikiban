#!/usr/bin/env python3
"""Draw the keikiban app icon: a gauge, the 計器盤 the name refers to.

Regenerates assets/keikiban.png (1024px master). The macOS .icns and the
platform icons are derived from that master by tools/make_icon.sh.

    python3 tools/make_icon.py
"""

import math
import os

from PIL import Image, ImageDraw

# Drawn at 4x and downscaled: PIL has no antialiasing of its own.
SCALE = 4
SIZE = 1024 * SCALE
OUT = os.path.join(os.path.dirname(__file__), "..", "assets", "keikiban.png")

BG_TOP = (32, 43, 61)
BG_BOTTOM = (17, 24, 35)
TRACK = (255, 255, 255, 38)
ARC_OK = (38, 166, 154)
ARC_HOT = (229, 57, 53)
NEEDLE = (240, 244, 248)

# The dial spans 240 degrees, the usual gauge sweep: 150 to 390 (=30).
START, END = 150, 390
VALUE = 0.78  # where the needle points, and where the hot band begins


def lerp(a, b, t):
    return tuple(round(x + (y - x) * t) for x, y in zip(a, b))


def main():
    img = Image.new("RGBA", (SIZE, SIZE), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)

    # Background: rounded square with a vertical gradient, drawn as rows.
    bg = Image.new("RGBA", (SIZE, SIZE))
    bd = ImageDraw.Draw(bg)
    for y in range(SIZE):
        bd.line([(0, y), (SIZE, y)], fill=lerp(BG_TOP, BG_BOTTOM, y / SIZE))
    mask = Image.new("L", (SIZE, SIZE), 0)
    ImageDraw.Draw(mask).rounded_rectangle(
        [0, 0, SIZE - 1, SIZE - 1], radius=int(SIZE * 0.22), fill=255)
    img.paste(bg, (0, 0), mask)

    # The dial opens downward, so its visual mass sits above the centre; the
    # centre drops a little to leave equal air above and below.
    cx = SIZE / 2
    cy = SIZE * 0.565
    radius = SIZE * 0.325
    width = int(SIZE * 0.085)
    box = [cx - radius, cy - radius, cx + radius, cy + radius]

    # Track, then the value arc: teal up to the value, red past it, so the
    # icon carries the same "green is fine, red is not" reading as the app.
    d.arc(box, START, END, fill=TRACK, width=width)
    hot_start = START + (END - START) * VALUE
    d.arc(box, START, hot_start, fill=ARC_OK, width=width)
    d.arc(box, hot_start, END, fill=ARC_HOT, width=width)

    # Tick marks inside the arc.
    for i in range(5):
        ang = math.radians(START + (END - START) * i / 4)
        r0 = radius - width * 0.75
        r1 = radius - width * 1.25
        d.line(
            [cx + r0 * math.cos(ang), cy + r0 * math.sin(ang),
             cx + r1 * math.cos(ang), cy + r1 * math.sin(ang)],
            fill=TRACK, width=int(SIZE * 0.016))

    # Needle plus hub.
    ang = math.radians(START + (END - START) * VALUE)
    tip = (cx + radius * 0.82 * math.cos(ang), cy + radius * 0.82 * math.sin(ang))
    back = (cx - radius * 0.16 * math.cos(ang), cy - radius * 0.16 * math.sin(ang))
    d.line([back, tip], fill=NEEDLE, width=int(SIZE * 0.028))
    hub = SIZE * 0.045
    d.ellipse([cx - hub, cy - hub, cx + hub, cy + hub], fill=NEEDLE)

    img = img.resize((1024, 1024), Image.LANCZOS)
    os.makedirs(os.path.dirname(OUT), exist_ok=True)
    img.save(OUT)
    print("wrote", os.path.normpath(OUT))


if __name__ == "__main__":
    main()
