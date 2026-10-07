#!/usr/bin/env python3
"""Final tokens: the reference implementation of docs/design/FINAL.md section 3.

It reuses the colour maths of direction-b/tokens.py and changes two things:
the pane header has no fill of its own, and text is solved on every ground it
sits on (shell, stage, hover, selected, field, palette panel).
Run it to print the resolved table for tokyonight and tokyonight_day.
"""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "direction-b"))
from tokens import mix, contrast, desaturate, toward_target, light_state  # noqa: E402


def over(c, g, a):
    """c at alpha a over ground g, as the GPU blend does it."""
    return mix(g, c, a)


def tokens(bg, fg, accent, need, done, err, light):
    T = dict(bg=bg, fg=fg)
    if not light:
        T["base"] = mix(bg, "#000000", 0.30)
        T["stage"] = bg
        T["hover"] = mix(T["base"], fg, 0.04)
        T["selected"] = mix(T["base"], fg, 0.09)
        T["field"] = mix(T["base"], fg, 0.045)
        T["raised"] = mix(bg, fg, 0.045)
        T["raised_sel"] = mix(T["raised"], fg, 0.075)
        border_c, border_a, hair_a, scrim_a = fg, 0.13, 0.12, 0.45
        ink = desaturate(fg, 0.45)
    else:
        # The stage is the brightest surface: the shell is a step darker.
        T["base"] = mix(bg, "#000000", 0.06)
        T["stage"] = bg
        T["hover"] = mix(T["base"], "#000000", 0.03)
        T["selected"] = mix(T["base"], "#000000", 0.07)
        T["field"] = mix(T["base"], "#ffffff", 0.5)
        T["raised"] = mix(bg, "#ffffff", 0.85)
        T["raised_sel"] = mix(T["raised"], "#000000", 0.06)
        border_c, border_a, hair_a, scrim_a = "#000000", 0.18, 0.14, 0.18
        ink = desaturate(fg, 0.12)
    T["border"] = over(border_c, T["stage"], border_a)
    T["border_raised"] = over(border_c, T["raised"], border_a)
    T["hairline"] = over(border_c, T["stage"], hair_a)
    T["border_alpha"], T["hairline_alpha"], T["scrim_alpha"] = border_a, hair_a, scrim_a
    grounds = [T[k] for k in ("base", "stage", "hover", "selected", "field", "raised")]
    worst = min(grounds, key=lambda g: contrast(ink, g))
    if light:
        # 10.5, not 11: the darker shell leaves `selected` too dark for 11
        # with a near-neutral ink.
        while contrast(ink, worst) < 10.5 and ink != "#000000":
            nxt = mix(ink, "#000000", 0.02)
            if nxt == ink:
                # Near black a 2 % step rounds back to the same colour.
                nxt = "#" + "".join(f"{max(0, int(ink[i:i + 2], 16) - 1):02x}" for i in (1, 3, 5))
            ink = nxt
    T["text"] = ink
    T["text2"] = toward_target(ink, worst, 4.6)
    T["text3"] = toward_target(ink, worst, 3.1)
    T["worst"] = worst
    for k, c in dict(accent=accent, need=need, done=done, err=err).items():
        if light:
            c = light_state(c, [lambda x: mix(T["base"], x, 0.14), lambda x: worst])
        T[k] = c
        T[k + "_fill"] = mix(T["base"], c, 0.14)
        T[k + "_ink"] = c if not light else light_state(c, [lambda x, f=T[k + "_fill"]: f], 4.5)
    T["selection"] = over(T["accent"], bg, 0.28 if not light else 0.22)
    T["on_state"] = "#ffffff" if light else bg
    return T


DARK = tokens("#1a1b26", "#c0caf5", "#7aa2f7", "#e0af68", "#9ece6a", "#f7768e", False)
LIGHT = tokens("#e1e2e7", "#3760bf", "#2e7de9", "#e0af68", "#9ece6a", "#f7768e", True)

if __name__ == "__main__":
    for k in DARK:
        d, l = DARK[k], LIGHT[k]
        extra = ""
        if k in ("text", "text2", "text3"):
            extra = f"  {contrast(d, DARK['worst']):.1f} / {contrast(l, LIGHT['worst']):.1f} on worst"
        if k in ("need", "done", "err", "accent"):
            extra = f"  {contrast(d, DARK['worst']):.1f} / {contrast(l, LIGHT['worst']):.1f} on worst"
        if k.endswith("_ink"):
            extra = f"  {contrast(d, DARK[k[:-4]+'_fill']):.1f} / {contrast(l, LIGHT[k[:-4]+'_fill']):.1f} on fill"
        print(f"{k:14} {d}  {l}{extra}")
