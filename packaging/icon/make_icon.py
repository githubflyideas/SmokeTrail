#!/usr/bin/env python3
"""Generate the pingping icon set.

The mark is a latency trace: a baseline and two spikes. Two, because the name is
doubled and because one spike is a reading while two are a comparison — the
second is taller and red, which is the whole product in one picture. A link that
looks the same on average but spikes here and not there.

An earlier version drew expanding echo arcs. It was the right idea and the wrong
picture: at any size it read as a Wi-Fi symbol, which says "signal strength" —
the opposite of what this program measures.

Three renderings, because the same picture cannot serve every size. Only the
stroke weight changes; the geometry is identical, so the mark never becomes a
different shape as it shrinks:
  detailed  (>=48px)
  bold      (24-32px)
  tiny      (<=20px)  heavy enough that two 1px spikes do not vanish

The alert state turns the whole mark red. At 16px in a taskbar, colour is all
the eye resolves, and a shape change there is wasted detail.

Outputs pingping.ico, pingping-512.png, tray-ok.ico and tray-alert.ico, so the
application icon and the two notification-area states cannot drift apart. The
console header draws the same geometry as inline SVG (static/index.html), where
it picks up each theme's colours instead of these.

Run: python3 make_icon.py
"""
from PIL import Image, ImageDraw

BG_TOP, BG_BOT = (30, 39, 51), (13, 16, 21)
CYAN = (34, 211, 238)   # --phos, the console's trace colour
RED = (251, 59, 83)     # --alert, reserved for failure everywhere in this project

S = 1024

BASE = 0.70          # baseline height, 0 = top
X0, X1 = 0.12, 0.88  # baseline extent
SPIKES = [(0.36, 0.30, CYAN), (0.63, 0.44, RED)]
STROKE = {"detailed": 0.050, "bold": 0.072, "tiny": 0.105}


def rounded_mask(size, r=0.22):
    m = Image.new("L", (size, size), 0)
    ImageDraw.Draw(m).rounded_rectangle([0, 0, size - 1, size - 1],
                                        radius=int(size * r), fill=255)
    return m


def background(size):
    img = Image.new("RGB", (size, size))
    d = ImageDraw.Draw(img)
    for y in range(size):
        t = y / max(size - 1, 1)
        d.line([(0, y), (size, y)],
               fill=tuple(int(a + (b - a) * t) for a, b in zip(BG_TOP, BG_BOT)))
    return img


def render(tier, alert=False):
    img = background(S).convert("RGBA")
    layer = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    d = ImageDraw.Draw(layer)
    w = S * STROKE[tier]

    baseline = RED if alert else CYAN
    d.rectangle([X0 * S, BASE * S - w / 2, X1 * S, BASE * S + w / 2],
                fill=baseline + (255,))
    for cx, h, colour in SPIKES:
        if alert:
            colour = RED
        d.rectangle([cx * S - w / 2, (BASE - h) * S, cx * S + w / 2, BASE * S],
                    fill=colour + (255,))

    img = Image.alpha_composite(img, layer)
    img.putalpha(rounded_mask(S))
    return img


def tier_for(px):
    return "detailed" if px >= 48 else "bold" if px >= 24 else "tiny"


def save_ico(path, art, sizes):
    frames = [art[tier_for(px)].resize((px, px), Image.LANCZOS) for px in sizes]
    frames[0].save(path, format="ICO",
                   sizes=[(f.width, f.height) for f in frames],
                   append_images=frames[1:])


ok = {t: render(t) for t in STROKE}
al = {t: render(t, alert=True) for t in STROKE}

save_ico("pingping.ico", ok, (256, 128, 64, 48, 32, 24, 16))
# The notification area never asks for more than 32px, so these carry only the
# sizes Windows actually requests — a 256px frame in a tray icon is dead weight
# compiled into every binary.
save_ico("tray-ok.ico", ok, (32, 24, 20, 16))
save_ico("tray-alert.ico", al, (32, 24, 20, 16))
ok["detailed"].resize((512, 512), Image.LANCZOS).save("pingping-512.png")

print("wrote pingping.ico, tray-ok.ico, tray-alert.ico, pingping-512.png")
