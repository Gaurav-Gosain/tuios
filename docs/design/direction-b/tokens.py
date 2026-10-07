#!/usr/bin/env python3
"""Direction B tokens: derives every chrome colour from a tuios theme.

This is the reference implementation of the formulas in
docs/design/direction-b.md. mock.py imports it. Run it to print the table.
"""


def hx(c):
    return tuple(int(c[i:i + 2], 16) for i in (1, 3, 5))


def h(t):
    return "#%02x%02x%02x" % tuple(max(0, min(255, round(v))) for v in t)


def mix(a, b, t):
    """sRGB mix; t = 0 keeps a."""
    a, b = hx(a), hx(b)
    return h([x + (y - x) * t for x, y in zip(a, b)])


def lum(c):
    def ch(v):
        v /= 255
        return v / 12.92 if v <= 0.04045 else ((v + 0.055) / 1.055) ** 2.4
    r, g, b = (ch(v) for v in hx(c))
    return 0.2126 * r + 0.7152 * g + 0.0722 * b


def contrast(a, b):
    la, lb = sorted((lum(a), lum(b)), reverse=True)
    return (la + 0.05) / (lb + 0.05)


def desaturate(c, keep):
    r, g, b = hx(c)
    y = 0.2126 * r + 0.7152 * g + 0.0722 * b
    return h([y + (v - y) * keep for v in (r, g, b)])


def toward_target(ink, ground, target):
    """Largest t such that mix(ink, ground, t) still has `target` contrast on ground."""
    lo, hi = 0.0, 1.0
    if contrast(ink, ground) < target:
        return ink
    for _ in range(20):
        mid = (lo + hi) / 2
        if contrast(mix(ink, ground, mid), ground) >= target:
            lo = mid
        else:
            hi = mid
    return mix(ink, ground, lo)


def darken_until(c, grounds, target, toward="#000000"):
    """Smallest step toward `toward` so c reaches target on every ground."""
    t = 0.0
    while t < 1 and min(contrast(mix(c, toward, t), g(mix(c, toward, t))) for g in grounds) < target:
        t += 0.01
    return mix(c, toward, t)


def _lin(v):
    v /= 255
    return v / 12.92 if v <= 0.04045 else ((v + 0.055) / 1.055) ** 2.4


def _gam(v):
    v = v * 12.92 if v <= 0.0031308 else 1.055 * v ** (1 / 2.4) - 0.055
    return v * 255


def to_oklch(c):
    import math
    r, g, b = (_lin(v) for v in hx(c))
    l = (0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b) ** (1 / 3)
    m = (0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b) ** (1 / 3)
    s = (0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b) ** (1 / 3)
    L = 0.2104542553 * l + 0.7936177850 * m - 0.0040720468 * s
    A = 1.9779984951 * l - 2.4285922050 * m + 0.4505937099 * s
    B = 0.0259040371 * l + 0.7827717662 * m - 0.8086757660 * s
    return L, math.hypot(A, B), math.atan2(B, A)


def from_oklch(L, C, H):
    import math
    while True:
        A, B = C * math.cos(H), C * math.sin(H)
        l = (L + 0.3963377774 * A + 0.2158037573 * B) ** 3
        m = (L - 0.1055613458 * A - 0.0638541728 * B) ** 3
        s = (L - 0.0894841775 * A - 1.2914855480 * B) ** 3
        rgb = (4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
               -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
               -0.0041960863 * l - 0.7034186147 * m + 1.7076147010 * s)
        if all(-1e-4 <= v <= 1 + 1e-4 for v in rgb) or C < 0.005:
            return h([_gam(min(1, max(0, v))) for v in rgb])
        C -= 0.005


def light_state(c, grounds, target=3.0, min_chroma=0.13):
    """Light themes: keep the hue, hold chroma up, lower OKLCH L until target."""
    L, C, H = to_oklch(c)
    C = max(C, min_chroma)
    out = from_oklch(L, C, H)
    while L > 0.2 and min(contrast(out, g(out)) for g in grounds) < target:
        L -= 0.005
        out = from_oklch(L, C, H)
    return out


def tokens(bg, fg, accent, need, done, err, light):
    T = dict(bg=bg, fg=fg, light=light)
    if not light:
        T["base"] = mix(bg, "#000000", 0.30)          # window shell: sidebar, title band
        T["stage"] = bg                                 # pane area, always the terminal bg
        T["header"] = mix(bg, fg, 0.03)                 # pane header ground
        T["hover"] = mix(T["base"], fg, 0.04)
        T["selected"] = mix(T["base"], fg, 0.09)
        T["field"] = mix(T["base"], fg, 0.045)
        T["raised"] = mix(bg, fg, 0.045)                # palette panel
        T["raised_sel"] = mix(T["raised"], fg, 0.075)
        T["border"] = (fg, 0.10)                        # stage and palette edge
        T["hairline"] = (fg, 0.07)                      # splits, band underline
        T["scrim"] = ("#000000", 0.45)
        ink = desaturate(fg, 0.45)
    else:
        T["base"] = mix(bg, "#ffffff", 0.55)
        T["stage"] = bg
        T["header"] = mix(bg, "#000000", 0.025)
        T["hover"] = mix(T["base"], "#000000", 0.03)
        T["selected"] = mix(T["base"], "#000000", 0.08)
        T["field"] = mix(T["base"], "#000000", 0.035)
        T["raised"] = mix(bg, "#ffffff", 0.75)
        T["raised_sel"] = mix(T["raised"], "#000000", 0.06)
        T["border"] = ("#000000", 0.12)
        T["hairline"] = ("#000000", 0.09)
        T["scrim"] = ("#000000", 0.18)
        ink = desaturate(fg, 0.12)
    # Text is measured on the worse of the two grounds it sits on.
    worst = min((T["base"], T["stage"], T["header"]), key=lambda g: contrast(ink, g))
    if light:
        while contrast(ink, worst) < 11:
            ink = mix(ink, "#000000", 0.02)
    T["text"] = ink
    T["text2"] = toward_target(ink, worst, 4.6)
    T["text3"] = toward_target(ink, worst, 3.1)
    T["worst"] = worst
    st = dict(accent=accent, need=need, done=done, err=err)
    for k, c in st.items():
        if light:
            c = light_state(c, [lambda x: mix(T["base"], x, 0.14), lambda x: worst])
        T[k] = c
        T[k + "_fill"] = mix(T["base"], c, 0.14)
        # Text inside a pill is small, so it needs 4.5:1 on the pill fill.
        T[k + "_ink"] = c if not light else light_state(c, [lambda x, f=T[k + "_fill"]: f], 4.5)
    T["selection"] = (T["accent"], 0.28 if not light else 0.22)
    T["on_state"] = "#ffffff" if light else bg  # mark drawn on a filled disc
    return T


DARK = tokens("#1a1b26", "#c0caf5", "#7aa2f7", "#e0af68", "#9ece6a", "#f7768e", False)
# Light states start from the vivid agent hues, not the theme's muddy ones.
LIGHT = tokens("#e1e2e7", "#3760bf", "#2e7de9", "#e0af68", "#9ece6a", "#f7768e", True)

if __name__ == "__main__":
    for name, T in (("dark (tokyonight)", DARK), ("light (tokyonight_day)", LIGHT)):
        print("##", name)
        for k, v in T.items():
            if k in ("light",):
                continue
            if isinstance(v, tuple):
                print(f"{k:11} {v[0]} at {round(v[1]*100)} %")
            else:
                extra = ""
                if k in ("text", "text2", "text3"):
                    extra = f"  {contrast(v, T['worst']):.2f}:1 on worst, {contrast(v, T['base']):.2f}:1 on base"
                if k in ("need", "done", "err", "accent"):
                    extra = f"  {contrast(v, T[k+'_fill']):.2f}:1 on fill, {contrast(v, T['worst']):.2f}:1 on worst"
                print(f"{k:11} {v}{extra}")
