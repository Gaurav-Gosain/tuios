#!/usr/bin/env python3
"""Direction A tokens: derives every chrome colour from a theme's bg and fg.

Run it to print the token table for tokyonight and tokyonight_day. mock.py
imports it to draw the mockups, so the pictures use the spec's numbers.
"""
import math


def hx(c):
    return tuple(int(c[i:i + 2], 16) / 255 for i in (1, 3, 5))


def to_hex(rgb):
    return "#%02x%02x%02x" % tuple(max(0, min(255, round(v * 255))) for v in rgb)


def lin(v):
    return v / 12.92 if v <= 0.04045 else ((v + 0.055) / 1.055) ** 2.4


def delin(v):
    v = max(0.0, min(1.0, v))
    return 12.92 * v if v <= 0.0031308 else 1.055 * v ** (1 / 2.4) - 0.055


def oklab(c):
    r, g, b = (lin(v) for v in hx(c))
    l = 0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b
    m = 0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b
    s = 0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b
    l, m, s = (math.copysign(abs(x) ** (1 / 3), x) for x in (l, m, s))
    return (0.2104542553 * l + 0.7936177850 * m - 0.0040720468 * s,
            1.9779984951 * l - 2.4285922050 * m + 0.4505937099 * s,
            0.0259040371 * l + 0.7827717662 * m - 0.8086757660 * s)


def from_oklab(L, a, b):
    l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3
    m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3
    s = (L - 0.0894841775 * a - 1.2914855480 * b) ** 3
    r = 4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s
    g = -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s
    bb = -0.0041960863 * l - 0.7034186147 * m + 1.7076147010 * s
    return to_hex((delin(r), delin(g), delin(bb)))


def mix(a, b, t):
    """OKLab mix; t = 0 keeps a."""
    A, B = oklab(a), oklab(b)
    return from_oklab(*(x + (y - x) * t for x, y in zip(A, B)))


def over(c, ground, alpha):
    """c at alpha composited on ground, in sRGB (what the GPU blend does)."""
    return to_hex(tuple(g + (x - g) * alpha for x, g in zip(hx(c), hx(ground))))


def lum(c):
    r, g, b = (lin(v) for v in hx(c))
    return 0.2126 * r + 0.7152 * g + 0.0722 * b


def contrast(a, b):
    x, y = sorted((lum(a), lum(b)), reverse=True)
    return (x + 0.05) / (y + 0.05)


def chroma_cap(c, cap):
    L, a, b = oklab(c)
    C = math.hypot(a, b)
    if C <= cap or C == 0:
        return c
    k = cap / C
    return from_oklab(L, a * k, b * k)


def solve(ink, ground, target):
    """The colour on the ink-to-ground line with contrast `target` on ground."""
    lo, hi = 0.0, 1.0
    for _ in range(40):
        mid = (lo + hi) / 2
        if contrast(mix(ink, ground, mid), ground) >= target:
            lo = mid
        else:
            hi = mid
    return mix(ink, ground, lo), lo


def state_for(c, pill_ground, light):
    """Light themes: keep the hue, raise chroma to at least 0.13, then lower
    OKLab L from 0.78 until the colour reads at 3:1 on its own 14 % pill.
    Dark themes keep the theme colour."""
    if not light:
        return c
    _, a, b = oklab(c)
    C = math.hypot(a, b)
    k = max(C, 0.13) / C if C else 1
    a, b = a * k, b * k
    L = 0.78
    while L > 0.2:
        cand = from_oklab(L, a, b)
        if contrast(cand, over(cand, pill_ground, 0.14)) >= 3.0:
            return cand
        L -= 0.005
    return from_oklab(L, a, b)


def tokens(bg, fg, accent, need, done, err, light):
    t = {"canvas": bg}
    if light:
        t["chrome"] = mix(bg, "#ffffff", 0.45)
        step = lambda k: mix(t["chrome"], "#000000", k)
        t["hover"], t["selected"] = step(0.035), step(0.065)
        t["raised"] = mix(bg, "#ffffff", 0.7)
        ink = from_oklab(0.27, *[v * 0.02 / max(1e-9, math.hypot(*oklab(fg)[1:])) for v in oklab(fg)[1:]])
        t["hair_a"] = 0.11
    else:
        t["chrome"] = mix(bg, fg, 0.03)
        step = lambda k: mix(bg, fg, k)
        t["hover"], t["selected"] = step(0.055), step(0.09)
        t["raised"] = step(0.045)
        ink = chroma_cap(fg, 0.035)
        t["hair_a"] = 0.09
    ink_ref = ink if light else fg
    t["hairline"] = over(ink_ref, t["chrome"], t["hair_a"])
    t["hairline_canvas"] = over(ink_ref, bg, t["hair_a"])
    # The ground with the least contrast for text decides: every ground
    # that carries chrome text, rows under the pointer and selected rows too.
    worst = min((t["chrome"], bg, t["hover"], t["selected"]), key=lambda g: contrast(ink, g))
    t["worst"] = worst
    t["text"] = ink
    t["text2"], _ = solve(ink, worst, 4.6)
    t["text3"], _ = solve(ink, worst, 3.1)
    t["accent"] = accent
    t["need"] = state_for(need, t["chrome"], light)
    t["done"] = state_for(done, t["chrome"], light)
    t["err"] = state_for(err, t["chrome"], light)
    t["sel_text"] = over(accent, bg, 0.28)
    t["dim_a"] = 0.20 if light else 0.30
    t["panel_border"] = over(ink, t["raised"], 0.14 if light else 0.10)
    return t


TOKYO = dict(bg="#1a1b26", fg="#c0caf5", accent="#7aa2f7", need="#e0af68", done="#9ece6a", err="#f7768e", light=False)
TOKYO_DAY = dict(bg="#e1e2e7", fg="#3760bf", accent="#2e7de9", need="#8c6c3e", done="#587539", err="#f52a65", light=True)

if __name__ == "__main__":
    for name, th in (("tokyonight", TOKYO), ("tokyonight_day", TOKYO_DAY)):
        t = tokens(**th)
        print(f"## {name}")
        for k, v in t.items():
            if isinstance(v, str) and v.startswith("#"):
                print(f"{k:16} {v}  on chrome {contrast(v, t['chrome']):5.2f}  on canvas {contrast(v, t['canvas']):5.2f}")
            else:
                print(k, v)
        for s in ("need", "done", "err"):
            print(s, "on own pill", round(contrast(t[s], over(t[s], t["chrome"], 0.14)), 2))
