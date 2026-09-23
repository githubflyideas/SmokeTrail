#!/usr/bin/env python3
"""Generate SmokeTrail.ico.

The mark is the product's thesis in one picture: a median latency line with the
percentile "smoke" opening around it, and one spike where the link goes bad. That
envelope is what SmokeTrail keeps and what an averaging monitor throws away.

Two renderings, because a taskbar icon is 16px and anything subtle dies there:
  detailed  (>=48px)  full envelope plus median line
  bold      (<48px)   the spike silhouette alone, heavy enough to read at 16px

Run: python3 make_icon.py
"""
import math
from PIL import Image, ImageDraw, ImageFilter

BG_TOP, BG_BOT = (30, 39, 51), (13, 16, 21)
GREEN = (63, 185, 80)
RED = (248, 81, 73)

S = 1024


def median(u):
    """p50 as a fraction of height, 0 = top. Latency spikes UP, so subtract."""
    calm = 0.62 + 0.030 * math.sin(u * 6.5 + 0.4)
    return calm - 0.34 * math.exp(-((u - 0.58) ** 2) / 0.0042)


def half(u):
    """Half-width of the percentile envelope."""
    return (0.045
            + 0.022 * math.sin(u * 8.0 + 1.1)
            + 0.135 * math.exp(-((u - 0.58) ** 2) / 0.0055))


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


def curve(fn, x0, x1, ua=0.0, ub=1.0, steps=260):
    """Sample fn over u in [ua,ub], placed at the matching x. Keeping one
    parametrisation for every layer is what stops the envelope drifting off the
    line it is supposed to wrap."""
    out = []
    for i in range(steps + 1):
        u = ua + (ub - ua) * i / steps
        out.append(((x0 + (x1 - x0) * u) * S, fn(u) * S))
    return out


def render(detailed):
    img = background(S).convert("RGBA")
    x0, x1 = 0.10, 0.90

    if detailed:
        # Envelope as one filled polygon, blurred so it reads as smoke rather
        # than a shape with an outline.
        env = Image.new("RGBA", (S, S), (0, 0, 0, 0))
        top = curve(lambda u: median(u) - half(u), x0, x1)
        bot = curve(lambda u: median(u) + half(u), x0, x1)
        ImageDraw.Draw(env).polygon(top + bot[::-1], fill=GREEN + (95,))
        env = env.filter(ImageFilter.GaussianBlur(S * 0.014))
        img = Image.alpha_composite(img, env)

        # The bad stretch, tinted red over the green envelope. Same u-range as
        # the red segment of the median line below, so the two agree.
        HOT_A, HOT_B = 0.46, 0.71
        hot = Image.new("RGBA", (S, S), (0, 0, 0, 0))
        ht = curve(lambda u: median(u) - half(u), x0, x1, HOT_A, HOT_B)
        hb = curve(lambda u: median(u) + half(u), x0, x1, HOT_A, HOT_B)
        ImageDraw.Draw(hot).polygon(ht + hb[::-1], fill=RED + (120,))
        hot = hot.filter(ImageFilter.GaussianBlur(S * 0.022))
        img = Image.alpha_composite(img, hot)

        # The median is drawn as a filled band rather than a thick polyline:
        # PIL's round joints scallop the edge of a wide stroke, which at icon
        # scale reads as hatching on the line.
        LW = 0.020
        line = Image.new("RGBA", (S, S), (0, 0, 0, 0))
        d = ImageDraw.Draw(line)
        for colour, a, b in ((GREEN, 0.0, 1.0), (RED, HOT_A, HOT_B)):
            up = curve(lambda u: median(u) - LW, x0, x1, a, b)
            dn = curve(lambda u: median(u) + LW, x0, x1, a, b)
            d.polygon(up + dn[::-1], fill=colour)
        img = Image.alpha_composite(img, line)
    else:
        # Small sizes: the spike alone, thick, with a red tip. A silhouette, not
        # a chart.
        line = Image.new("RGBA", (S, S), (0, 0, 0, 0))
        d = ImageDraw.Draw(line)
        spike = lambda u: 0.66 - 0.40 * math.exp(-((u - 0.5) ** 2) / 0.010)
        LW = 0.058
        for colour, a, b in ((GREEN, 0.0, 1.0), (RED, 0.38, 0.62)):
            up = curve(lambda u: spike(u) - LW, 0.08, 0.92, a, b, steps=160)
            dn = curve(lambda u: spike(u) + LW, 0.08, 0.92, a, b, steps=160)
            d.polygon(up + dn[::-1], fill=colour)
        img = Image.alpha_composite(img, line)

    img.putalpha(rounded_mask(S))
    return img


big, small = render(True), render(False)
frames = [(big if px >= 48 else small).resize((px, px), Image.LANCZOS)
          for px in (256, 128, 64, 48, 32, 24, 16)]
frames[0].save("SmokeTrail.ico", format="ICO",
               sizes=[(f.width, f.height) for f in frames],
               append_images=frames[1:])
big.resize((512, 512), Image.LANCZOS).save("SmokeTrail-512.png")
print("wrote SmokeTrail.ico and SmokeTrail-512.png")
