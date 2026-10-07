#!/usr/bin/env python3
"""Draws the Direction A mockups (SVG) with the numbers in direction-a.md.

    python3 mock.py OUTDIR

Text widths are measured with the bundled Inter, so ellipses and gaps land
where the app would put them. render.sh turns the SVGs into PNGs.
"""
import os
import sys

from fontTools.ttLib import TTFont

from tokens import TOKYO, TOKYO_DAY, contrast, over, tokens

HERE = os.path.dirname(os.path.abspath(__file__))
FONTS = os.path.join(HERE, "../../../crates/tuios-gpui/assets/fonts")
OUT = sys.argv[1] if len(sys.argv) > 1 else HERE

W, H = 1440, 900
SB = 248          # sidebar width
BAND = 40         # top band height
HDR = 28          # pane header height (with bridge insets)
PAD = 12          # pane inner padding
CW, CH, FS = 9, 20, 15   # terminal cell and font size
UI = "Inter"
MONO = "JetBrainsMonoNL Nerd Font Mono"

_fonts = {w: TTFont(os.path.join(FONTS, f"Inter-{n}.ttf")) for w, n in ((400, "Regular"), (500, "Medium"), (600, "SemiBold"))}


def tw(text, size, weight=400):
    f = _fonts[weight]
    cmap, hmtx, upm = f.getBestCmap(), f["hmtx"], f["head"].unitsPerEm
    return sum(hmtx[cmap.get(ord(c), cmap[ord("?")])][0] for c in text) * size / upm


def fit(text, size, weight, width):
    if tw(text, size, weight) <= width:
        return text
    while text and tw(text + "…", size, weight) > width:
        text = text[:-1]
    return text.rstrip() + "…"


TERM_DARK = dict(fg="#c0caf5", com="#565f89", green="#9ece6a", cyan="#7dcfff", blue="#7aa2f7", mag="#bb9af7",
                 red="#f7768e", yellow="#e0af68", dim="#a9b1d6", cursor="#c0caf5")
TERM_LIGHT = dict(fg="#3760bf", com="#848cb5", green="#587539", cyan="#007197", blue="#2e7de9", mag="#9854f1",
                  red="#f52a65", yellow="#8c6c3e", dim="#6172b0", cursor="#3760bf")


def theme(dark):
    t = tokens(**(TOKYO if dark else TOKYO_DAY))
    t["term"] = TERM_DARK if dark else TERM_LIGHT
    t["dark"] = dark
    t["scrim_a"] = 0.45 if dark else 0.18
    return t


class S:
    def __init__(s):
        s.o = []

    def rect(s, x, y, w, h, fill, r=0, op=1, stroke=None, sw=1):
        st = f' stroke="{stroke}" stroke-width="{sw}"' if stroke else ""
        s.o.append(f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{r}" fill="{fill}" fill-opacity="{op}"{st}/>')

    def text(s, x, y, t, fill, size=13, weight=400, font=UI, anchor="start", op=1, tnum=False):
        t = t.replace("&", "&amp;").replace("<", "&lt;")
        feat = ' style="font-feature-settings:\'tnum\'"' if tnum else ""
        s.o.append(f'<text x="{x}" y="{y}" fill="{fill}" fill-opacity="{op}" font-family="{font}" font-size="{size}" '
                   f'font-weight="{weight}" text-anchor="{anchor}" xml:space="preserve"{feat}>{t}</text>')

    def raw(s, x):
        s.o.append(x)


def icon(s, x, y, kind, T):
    """The state family: 14 px in a 16 px box, 1.5 px strokes, centred at x, y."""
    mark = T["canvas"]
    if kind == "idle":
        s.raw(f'<circle cx="{x}" cy="{y}" r="6.25" fill="none" stroke="{T["text3"]}" stroke-width="1.5"/>')
    elif kind == "work":
        s.raw(f'<circle cx="{x}" cy="{y}" r="6.25" fill="none" stroke="{T["text3"]}" stroke-opacity=".5" stroke-width="1.5"/>'
              f'<path d="M{x} {y-6.25}A6.25 6.25 0 0 1 {x+5.413} {y+3.125}" fill="none" stroke="{T["text"]}" stroke-width="1.5" stroke-linecap="round"/>')
    elif kind == "need":
        s.raw(f'<circle cx="{x}" cy="{y}" r="6.25" fill="none" stroke="{T["need"]}" stroke-width="1.5"/>'
              f'<circle cx="{x}" cy="{y}" r="2.5" fill="{T["need"]}"/>')
    elif kind == "err":
        s.raw(f'<circle cx="{x}" cy="{y}" r="7" fill="{T["err"]}"/>'
              f'<path d="M{x-2.4} {y-2.4}L{x+2.4} {y+2.4}M{x+2.4} {y-2.4}L{x-2.4} {y+2.4}" stroke="{mark}" stroke-width="1.5" stroke-linecap="round"/>')
    elif kind == "done":
        s.raw(f'<circle cx="{x}" cy="{y}" r="7" fill="{T["done"]}"/>'
              f'<path d="M{x-3} {y+0.2}L{x-0.9} {y+2.3}L{x+3.1} {y-2}" fill="none" stroke="{mark}" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>')
    elif kind == "term":
        s.raw(f'<circle cx="{x}" cy="{y}" r="2.5" fill="{T["text3"]}"/>')


def chip(s, xr, y, label, T, ground):
    """A keycap chip, right edge at xr, top at y: 11 px, 18 tall, 1 px hairline."""
    w = tw(label, 11, 500) + 12
    s.rect(xr - w + 0.5, y + 0.5, w - 1, 17, "none", 4, stroke=over(T["text"], ground, T["hair_a"] + 0.04))
    s.text(xr - w / 2, y + 13, label, T["text3"], 11, 500, anchor="middle")
    return w


def sidebar(s, T, needs_rows, sessions):
    s.rect(0, 0, SB, H, T["chrome"])
    s.rect(SB - 1, 0, 1, H, T["hairline"])
    # Search field, centred in the 40 px band.
    s.rect(8, 6, SB - 16, 28, T["hover"], 6)
    s.raw(f'<circle cx="22.5" cy="19.5" r="4.75" fill="none" stroke="{T["text3"]}" stroke-width="1.5"/>'
          f'<path d="M26 23L28.5 25.5" stroke="{T["text3"]}" stroke-width="1.5" stroke-linecap="round"/>')
    s.text(36, 24.5, "Search", T["text3"], 13)
    chip(s, SB - 14, 11, "Ctrl+Shift+P", T, T["hover"])

    y = BAND + 12

    def header(y, label, extra=None, count=None):
        s.text(16, y + 18, label, T["text2"], 12, 600)
        if extra:
            s.text(16 + tw(label, 12, 600) + 6, y + 18, extra, T["text3"], 12)
        if count:
            s.text(SB - 16, y + 18, count, T["text3"], 11, 500, anchor="end", tnum=True)
        return y + 28

    def row(y, kind, title, harness, line2, age, sel=False, seen=False, ws=None):
        if sel:
            s.rect(6, y, SB - 12, 44, T["selected"], 6)
        icon(s, 24, y + 14.5, kind, T)
        right = SB - 16
        if age:
            s.text(right, y + 19, age, T["text3"], 11, 500, anchor="end", tnum=True)
            right -= tw(age, 11, 500) + 8
        if ws:
            s.text(right, y + 19, ws, T["text3"], 11, 500, anchor="end", tnum=True)
            right -= tw(ws, 11, 500) + 8
        s.text(40, y + 19, fit(title, 13, 500, right - 40), T["text2"] if seen else T["text"], 13, 500)
        x = 40
        if harness:
            s.text(x, y + 35, harness + " · ", T["text3"], 12)
            x += tw(harness + " · ", 12)
        s.text(x, y + 35, fit(line2, 12, 400, SB - 16 - x), T["text2"] if kind in ("need", "err") else T["text3"], 12)
        return y + 46

    if needs_rows:
        y = header(y, "Needs you", count=str(len(needs_rows)))
        for r in needs_rows:
            y = row(y, *r)
        y += 12
    for name, extra, rows in sessions:
        y = header(y, name, extra)
        for r in rows:
            y = row(y, *r)
        y += 12
    # New session, at the foot.
    s.rect(0, H - 41, SB - 1, 1, T["hairline"])
    s.raw(f'<path d="M18 {H-20.5}H28M23 {H-25.5}V{H-15.5}" stroke="{T["text2"]}" stroke-width="1.5" stroke-linecap="round"/>')
    s.text(38, H - 16, "New session", T["text2"], 13)


def band(s, T, tabs):
    x0 = SB
    s.rect(x0, 0, W - x0, BAND, T["canvas"])
    s.text(x0 + 16, 25, "tuios", T["text"], 13, 600)
    x = x0 + 16 + tw("tuios", 13, 600) + 8
    s.text(x, 25, "/", T["text3"], 13)
    x += tw("/", 13) + 8
    for n, name, active, need in tabs:
        w = 10 + tw(n, 13) + 6 + tw(name, 13, 500) + 10 + (12 if need else 0)
        if active:
            s.rect(x, 6, w, 28, T["selected"], 6)
        s.text(x + 10, 25, n, T["text3"], 13, tnum=True)
        s.text(x + 10 + tw(n, 13) + 6, 25, name, T["text"] if active else T["text2"], 13, 500)
        if need:
            s.raw(f'<circle cx="{x + w - 13}" cy="20" r="3" fill="{T["need"]}"/>')
        x += w + 2
    c = T["text3"]
    for i, kind in enumerate(("split-r", "split-d", "zoom")):
        bx = W - 8 - 28 * (3 - i)
        ox, oy = bx + 6, 12
        if kind == "split-r":
            s.raw(f'<rect x="{ox+0.75}" y="{oy+0.75}" width="14.5" height="14.5" rx="3" fill="none" stroke="{c}" stroke-width="1.5"/><path d="M{ox+8} {oy+1}V{oy+15}" stroke="{c}" stroke-width="1.5"/>')
        elif kind == "split-d":
            s.raw(f'<rect x="{ox+0.75}" y="{oy+0.75}" width="14.5" height="14.5" rx="3" fill="none" stroke="{c}" stroke-width="1.5"/><path d="M{ox+1} {oy+8}H{ox+15}" stroke="{c}" stroke-width="1.5"/>')
        else:
            s.raw(f'<path d="M{ox+10} {oy+1.5}H{ox+14.5}V{oy+6}M{ox+6} {oy+14.5}H{ox+1.5}V{oy+10}M{ox+14.5} {oy+1.5}L{ox+9.5} {oy+6.5}M{ox+1.5} {oy+14.5}L{ox+6.5} {oy+9.5}" fill="none" stroke="{c}" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>')
    s.rect(0, BAND, W, 1, T["hairline"])


def term(s, x, y, lines, T, cols, sel=None, cursor=None):
    P = T["term"]
    if sel:
        r, c0, c1 = sel
        s.rect(x + c0 * CW, y + r * CH, (c1 - c0) * CW, CH, T["sel_text"])
    if cursor:
        r, c = cursor
        s.rect(x + c * CW, y + r * CH, CW, CH, P["cursor"])
    for i, line in enumerate(lines):
        cx = 0
        for seg, col, *bold in line:
            seg = seg[:max(0, cols - cx)]
            if seg:
                s.text(x + cx * CW, y + i * CH + 15, seg, P.get(col, col), FS, 700 if bold else 400, MONO)
            cx += len(seg)


def pane(s, T, x, y, w, h, kind, name, detail, lines, focus=False, need=False, sel=None, cursor=None, status=None):
    s.rect(x, y, w, h, T["canvas"])
    icon(s, x + PAD + 7, y + 14, kind, T)
    right = x + w - PAD
    if need:
        pw = tw("Needs you", 11, 600) + 16
        s.rect(right - pw, y + 5, pw, 18, T["need"], 9, op=0.14)
        s.text(right - pw / 2, y + 18, "Needs you", T["need"], 11, 600, anchor="middle")
        right -= pw + 8
    nx = x + PAD + 20
    s.text(nx, y + 18, fit(name, 12, 500, right - nx), T["text"] if focus else T["text2"], 12, 500)
    dx = nx + tw(name, 12, 500) + 8
    if dx < right - 40:
        s.text(dx, y + 18, fit(detail, 12, 400, right - dx), T["text3"], 12)
    cols = int((w - 2 * PAD) // CW)
    rows = int((h - HDR - 4 - PAD) // CH)
    term(s, x + PAD, y + HDR + 4, lines[:rows], T, cols, sel, cursor)
    if status:
        r = rows - 1
        s.rect(x + PAD, y + HDR + 4 + r * CH, cols * CW, CH, over(T["term"]["fg"], T["canvas"], 0.08))
        term(s, x + PAD, y + HDR + 4 + r * CH, [status], T, cols)
    if not focus:
        s.rect(x, y, w, h, T["canvas"], op=T["dim_a"])
    if need:
        s.rect(x + 2.5, y + 2.5, w - 5, h - 5, "none", 6, stroke=T["need"], sw=1)


LEFT = [
    [("› ", "com"), ("Cache the shaped rows per generation", "fg")], [],
    [("• ", "blue"), ("Read ", "fg"), ("crates/tuios-gpui/src/painter.rs", "cyan")],
    [("• ", "blue"), ("Read ", "fg"), ("crates/tuios-gpui/src/rowplan.rs", "cyan")],
    [("• ", "blue"), ("Edit ", "fg"), ("painter.rs ", "cyan"), ("+42 ", "green"), ("-17", "red")],
    [("  │ ", "com"), ("fn prepare(&mut self, screen: &Screen) {", "dim")],
    [("  │ ", "com"), ("+    let dirty = screen.rows.iter()", "green")],
    [("  │ ", "com"), ("+        .filter(|r| r.gen != self.drawn[r.y]);", "green")],
    [("  │ ", "com"), ("-    for row in &screen.rows {", "red")],
    [],
    [("• ", "blue"), ("Ran ", "fg"), ("cargo test -p tuios-gpui", "fg", 1)],
    [("  └ ", "com"), ("test result: ok. 41 passed; 0 failed", "green")],
    [],
    [("• ", "blue"), ("Ran ", "fg"), ("cargo run --release -- --perf", "fg", 1)],
    [("  └ ", "com"), ("scroll p50 1.9 ms  p95 3.1 ms  ", "fg"), ("(was 4.8 ms)", "com")],
    [],
    [("Working ", "blue"), ("(5m 03s · esc to interrupt)", "com")],
] + [[]] * 18 + [
    [("─" * 80, "com")],
    [("› ", "fg")],
    [("─" * 80, "com")],
    [("  gpt-5.5 high · 61% context left", "com")],
]

NEED = [
    [("● ", "green"), ("Read ", "fg"), ("src/client.ts", "cyan")],
    [("● ", "green"), ("Read ", "fg"), ("src/errors.ts", "cyan")],
    [("● ", "green"), ("Search ", "fg"), ('"status === 429"', "yellow"), (" · 3 matches", "com")], [],
    [("The client throws on any non-2xx response", "fg")],
    [("today, so a rate limit surfaces as a generic", "fg")],
    [("HttpError. Two options:", "fg")], [],
    [(" 1. ", "com"), ("Retry 429 with backoff, honour Retry-After", "fg")],
    [(" 2. ", "com"), ("Surface a typed RateLimitError", "fg")], [],
    [("Should the client retry on 429, or surface", "fg", 1)],
    [("the error?", "fg", 1)], [],
    [("❯ 1. Retry with backoff", "blue")],
    [("  2. Surface the error", "dim")],
    [("  3. Type something else", "dim")], [],
    [("Enter to select · ↑↓ to navigate · Esc to cancel", "com")],
]

NVIM = [
    [("  1 ", "com"), ("//! The fleet: every pane in every session.", "com")],
    [("  2 ", "com")],
    [("  3 ", "com"), ("use ", "mag"), ("serde::Deserialize;", "fg")],
    [("  4 ", "com"), ("use ", "mag"), ("std::cmp::Ordering;", "fg")],
    [("  5 ", "com")],
    [("  6 ", "com"), ("/// What a pane is doing, most urgent first.", "com")],
    [("  7 ", "com"), ("#[derive(Clone, Copy, Debug, PartialEq, Eq)]", "dim")],
    [("  8 ", "com"), ("pub enum ", "mag"), ("Status", "cyan"), (" {", "fg")],
    [("  9 ", "com"), ("    NeedsYou,", "fg")],
    [(" 10 ", "com"), ("    Errored,", "fg")],
    [(" 11 ", "com"), ("    Working,", "fg")],
    [(" 12 ", "com"), ("    Done,", "fg")],
    [(" 13 ", "com"), ("    Idle,", "fg")],
    [(" 14 ", "com"), ("}", "fg")],
    [(" 15 ", "com")],
    [(" 16 ", "com"), ("impl ", "mag"), ("Status", "cyan"), (" {", "fg")],
    [(" 17 ", "com"), ("    pub fn ", "mag"), ("rank", "blue"), ("(self) -> ", "fg"), ("u8", "cyan"), (" {", "fg")],
    [(" 18 ", "com"), ("        self ", "fg"), ("as ", "mag"), ("u8", "cyan")],
    [(" 19 ", "com"), ("    }", "fg")],
    [(" 20 ", "com"), ("}", "fg")],
] + [[]] * 6
NVIM_STATUS = [(" fleet.rs", "fg"), ("                              1,1   Top", "com")]


def main(s, T):
    tabs = [("1", "build", True, False), ("2", "monitor", False, False), ("3", "review", False, True)]
    needs = [
        ("need", "api retries", "claude", "Retry on 429, or surface the error?", "1m", False, False, None),
        ("err", "migrations", "codex", "Migration failed: relation users already exists", "4m", False, False, None),
    ]
    sessions = [
        ("tuios", "· attached", [
            ("work", "paint cache", "codex", "Editing painter.rs", "5m", True, False, None),
            ("done", "flaky test", "claude", "Fixed the race in attach. 3 files changed", "12m", False, False, None),
            ("term", "nvim", None, "~/dev/tuios-gpui · main", "", False, True, None),
            ("term", "htop", None, "review · ~/dev/tuios-gpui", "", False, True, None),
        ]),
        ("api", "· 1 terminal", [
            ("work", "docs pass", "codex", "Reading docs/KEYBINDINGS.md", "2m", False, False, None),
            ("done", "readme", "claude", "Updated 4 docs pages with the new flags", "1h", False, True, None),
        ]),
        ("dotfiles", "· 2 terminals", []),
    ]
    sidebar(s, T, needs, sessions)
    band(s, T, tabs)
    y0 = BAND + 1
    split_x = SB + 596
    split_y = y0 + 430
    pane(s, T, SB, y0, split_x - SB, H - y0, "work", "paint cache", "codex · ~/dev/tuios-gpui · main", LEFT,
         focus=True, sel=(4, 7, 25), cursor=(36, 2))
    pane(s, T, split_x + 1, y0, W - split_x - 1, split_y - y0, "need", "api retries", "claude · ~/dev/api · retry-429", NEED,
         need=True)
    pane(s, T, split_x + 1, split_y + 1, W - split_x - 1, H - split_y - 1, "term", "nvim", "~/dev/tuios-gpui · main", NVIM,
         status=NVIM_STATUS)
    s.rect(split_x, y0, 1, H - y0, T["hairline_canvas"])
    s.rect(split_x + 1, split_y, W - split_x - 1, 1, T["hairline_canvas"])


def palette(s, T):
    s.rect(0, 0, W, H, "#000000", op=T["scrim_a"])
    pw = 640
    px, py = (W - pw) / 2, round(H * 0.14)
    rows = [
        ("Needs you", None),
        ("need", [("api ", 0), ("re", 1), ("tries", 0)], "Retry on 429, or surface the error?", None, True),
        ("Panes", None),
        ("done", [("", 0), ("re", 1), ("adme", 0)], "api", None, False),
        ("term", [("", 0), ("re", 1), ("pl", 0)], "dotfiles · ~/dev/dotfiles", None, False),
        ("Commands", None),
        ("cmd", [("", 0), ("Re", 1), ("name session", 0)], "", None, False),
        ("cmd", [("", 0), ("Re", 1), ("start pane", 0)], "", None, False),
        ("cmd", [("Go to ", 0), ("re", 1), ("view", 0)], "Workspace 3", "Alt+3", False),
    ]
    h = 52 + 1 + 6 + sum(28 if r[1] is None else 36 for r in rows) + 6 + 1 + 36
    s.raw('<defs><filter id="sh" x="-20%" y="-20%" width="140%" height="160%">'
          f'<feDropShadow dx="0" dy="12" stdDeviation="16" flood-color="#000" flood-opacity="{.32 if T["dark"] else .12}"/>'
          f'<feDropShadow dx="0" dy="2" stdDeviation="2" flood-color="#000" flood-opacity="{.20 if T["dark"] else .06}"/></filter></defs>')
    s.raw(f'<rect x="{px}" y="{py}" width="{pw}" height="{h}" rx="10" fill="{T["raised"]}" filter="url(#sh)"/>')
    s.rect(px + 0.5, py + 0.5, pw - 1, h - 1, "none", 10, stroke=T["panel_border"])
    s.raw(f'<circle cx="{px+22.5}" cy="{py+25.5}" r="5.25" fill="none" stroke="{T["text3"]}" stroke-width="1.5"/>'
          f'<path d="M{px+26.5} {py+29.5}L{px+29.5} {py+32.5}" stroke="{T["text3"]}" stroke-width="1.5" stroke-linecap="round"/>')
    s.text(px + 44, py + 31, "re", T["text"], 15)
    s.rect(px + 44 + tw("re", 15) + 1, py + 17, 2, 18, T["accent"], 1)
    s.rect(px, py + 52, pw, 1, T["panel_border"])
    y = py + 59
    first = True
    for r in rows:
        if r[1] is None:
            s.text(px + 16, y + 18, r[0], T["text2"], 12, 600)
            y += 28
            continue
        kind, title, sub, keys, sel = r
        if sel:
            s.rect(px + 6, y, pw - 12, 36, T["selected"] if T["dark"] else T["hover"], 6)
        if kind == "cmd":
            s.raw(f'<path d="M{px+21} {y+13.5}L{px+25} {y+18}L{px+21} {y+22.5}" fill="none" stroke="{T["text3"]}" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>')
        else:
            icon(s, px + 23, y + 18, kind, T)
        x = px + 44
        for seg, hit in title:
            if seg:
                s.text(x, y + 23, seg, T["text"] if hit else T["text2"] if not sel else T["text"], 13, 600 if hit else 400)
                x += tw(seg, 13, 600 if hit else 400)
        if sub:
            s.text(x + 8, y + 23, fit(sub, 12, 400, px + pw - 120 - x), T["text3"], 12)
        if keys:
            chip(s, px + pw - 16, y + 9, keys, T, T["raised"])
        y += 36
    y += 6
    s.rect(px, y, pw, 1, T["panel_border"])
    s.text(px + 16, y + 23, "6 results", T["text3"], 12)
    xr = px + pw - 16
    xr -= chip(s, xr, y + 9, "Esc", T, T["raised"]) + 6
    s.text(xr, y + 22, "Close", T["text2"], 12, anchor="end")
    xr -= tw("Close", 12) + 16
    xr -= chip(s, xr, y + 9, "Enter", T, T["raised"]) + 6
    s.text(xr, y + 22, "Open", T["text2"], 12, anchor="end")


def build(name, dark, pal=False):
    T = theme(dark)
    s = S()
    main(s, T)
    if pal:
        palette(s, T)
    svg = (f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" viewBox="0 0 {W} {H}" '
           f'text-rendering="geometricPrecision">' + "".join(s.o) + "</svg>")
    with open(os.path.join(OUT, name + ".svg"), "w") as f:
        f.write(svg)


if __name__ == "__main__":
    build("main-dark", True)
    build("palette-dark", True, True)
    build("main-light", False)
