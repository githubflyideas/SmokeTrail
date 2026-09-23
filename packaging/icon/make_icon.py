#!/usr/bin/env python3
"""Generate the pingping icon set.

The mark is an echo: a probe at the origin and the reply expanding away from it.
That is what the name says out loud — ping, and the ping back — and it is the one
thing this program does that a reader can recognise at 16 pixels.

The arcs are not evenly spaced. Their gaps widen outward the way a latency
distribution opens up under load, which is the product's actual thesis; at icon
scale nobody will read that as a chart, but it is why the spacing looks like
something rather than nothing.

Three renderings, because the same picture cannot serve every size:
  detailed  (>=48px)  origin dot, three arcs, the outermost thinning out
  bold      (24-32px) dot and two heavy arcs
  tiny      (<=20px)  dot and one arc; at 16px a second arc merges into the
                      first and the mark becomes a smudge

The alert state breaks the outer arc — a broken ring reads as a reply that never
came, where a colour change alone reads as merely a warning. Below 24px that gap
is two pixels wide and scatters into noise, so the tiny rendering drops it and
relies on colour, which is all the eye resolves in a taskbar at that size anyway.

Outputs pingping.ico, pingping-512.png, tray-ok.ico and tray-alert.ico, so the
application icon and the two notification-area states can never drift apart.

Run: python3 make_icon.py
"""
from PIL import Image, ImageDraw

BG_TOP, BG_BOT = (30, 39, 51), (13, 16, 21)
GREEN = (63, 185, 80)
RED = (248, 81, 73)

S = 1024

# Emission point, left of centre so the arcs have room to open to the right.
CX, CY = 0.32, 0.50
DOT = 0.072

# Radius, stroke width, alpha. Widening gaps, thinning strokes: the reply loses
# definition as it travels, which is also how the eye reads distance.
ARCS = {
    "detailed": [(0.175, 0.052, 255), (0.300, 0.044, 225), (0.440, 0.034, 180)],
    "bold":     [(0.205, 0.082, 255), (0.370, 0.070, 255)],
    "tiny":     [(0.290, 0.130, 255)],
}
DOT_SCALE = {"detailed": 1.0, "bold": 1.18, "tiny": 1.55}

SPAN = 54  # degrees either side of horizontal


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


def arc(d, r, width, colour, alpha, gap=0):
    """One echo arc. `gap` opens a wedge at the nose, which is how the alert
    state says a reply is missing rather than merely late — a broken ring reads
    as absence, where a colour change alone reads as a warning."""
    box = [(CX - r) * S, (CY - r) * S, (CX + r) * S, (CY + r) * S]
    w = max(1, int(width * S))
    if gap:
        d.arc(box, -SPAN, -gap, fill=colour + (alpha,), width=w)
        d.arc(box, gap, SPAN, fill=colour + (alpha,), width=w)
    else:
        d.arc(box, -SPAN, SPAN, fill=colour + (alpha,), width=w)


def render(tier, alert=False):
    img = background(S).convert("RGBA")
    layer = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    d = ImageDraw.Draw(layer)

    arcs = ARCS[tier]
    for i, (r, width, alpha) in enumerate(arcs):
        outermost = i == len(arcs) - 1
        if alert and outermost:
            # The reply that did not arrive: red, and broken at the nose — except
            # at tiny sizes, where the gap is sub-pixel and only colour survives.
            arc(d, r, width, RED, 255, gap=0 if tier == "tiny" else 20)
        else:
            arc(d, r, width, GREEN, alpha)

    # The origin dot last, so it sits on top of any arc that reaches back.
    dot = DOT * DOT_SCALE[tier]
    d.ellipse([(CX - dot) * S, (CY - dot) * S, (CX + dot) * S, (CY + dot) * S],
              fill=(RED if alert else GREEN) + (255,))

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


ok = {t: render(t) for t in ARCS}
al = {t: render(t, alert=True) for t in ARCS}

save_ico("pingping.ico", ok, (256, 128, 64, 48, 32, 24, 16))
# The notification area never asks for more than 32px, so these carry only the
# sizes Windows actually requests — a 256px frame in a tray icon is dead weight
# compiled into every binary.
save_ico("tray-ok.ico", ok, (32, 24, 20, 16))
save_ico("tray-alert.ico", al, (32, 24, 20, 16))
ok["detailed"].resize((512, 512), Image.LANCZOS).save("pingping-512.png")

print("wrote pingping.ico, tray-ok.ico, tray-alert.ico, pingping-512.png")
