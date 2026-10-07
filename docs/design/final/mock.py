#!/usr/bin/env python3
"""Final mockups: main (dark), palette (dark), main (light).

Usage: mock.py OUTDIR. Writes main-dark.svg, palette-dark.svg, main-light.svg.
Geometry and colours follow docs/design/FINAL.md at 1440x900, scale 1. The drawing
code is direction-b/mock.py with the FINAL.md changes: no header fill, solved
text tokens from final/tokens.py, a 2 px caret, text2 on the chosen palette row.
"""
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "..", "direction-b"))
import tokens as _b  # noqa: E402
from tokens import mix  # noqa: E402
import importlib.util  # noqa: E402

_spec = importlib.util.spec_from_file_location("final_tokens", os.path.join(HERE, "tokens.py"))
_f = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(_f)


def _merge(b, f):
    T = dict(b)
    for k in ("text", "text2", "text3", "need", "need_fill", "need_ink", "done", "done_fill",
              "err", "err_fill", "accent"):
        T[k] = f[k]
    T["header"] = T["stage"]       # the pane header has no fill of its own
    return T


DARK = _merge(_b.DARK, _f.DARK)
LIGHT = _merge(_b.LIGHT, _f.LIGHT)

W, H = 1440, 900
SB = 256          # sidebar column
BAND = 40         # title band
STAGE_M = 8       # stage margin right and bottom
CW, CH = 9, 20    # terminal cell at 15 px JetBrains Mono, line height 1.333
UI = "Inter, sans-serif"
MONO = "JetBrainsMonoNL Nerd Font Mono, monospace"

# Stage and grid geometry, computed the way the app must compute it.
SX, SY = SB, BAND
SW, SH = W - SB - STAGE_M, H - BAND - STAGE_M
COLS = (SW - 24) // CW
ROWS = (SH - 24) // CH
GX = SX + (SW - COLS * CW) // 2
GY = SY + (SH - ROWS * CH) // 2


def esc(t):
    return t.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def tw(t, size, weight=400):
    """Rough Inter advance, for placing chips after text."""
    k = 0.56 if weight < 500 else 0.58 if weight < 600 else 0.6
    narrow = sum(1 for c in t if c in "iljtf.,:;|!' ()")
    wide = sum(1 for c in t if c in "mwMW")
    return size * (k * len(t) - 0.28 * narrow + 0.3 * wide)


class S:
    def __init__(s):
        s.o = []

    def add(s, x):
        s.o.append(x)

    def rect(s, x, y, w, h, fill, r=0, op=1, stroke=None, sop=1, sw=1):
        st = f' stroke="{stroke}" stroke-opacity="{sop}" stroke-width="{sw}"' if stroke else ""
        s.add(f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{r}" fill="{fill}" fill-opacity="{op}"{st}/>')

    def text(s, x, y, t, fill, size=13, weight=400, font=UI, anchor="start", op=1, extra=""):
        s.add(f'<text x="{x}" y="{y}" fill="{fill}" fill-opacity="{op}" font-family="{font}" font-size="{size}" '
              f'font-weight="{weight}" text-anchor="{anchor}" xml:space="preserve" {extra}>{esc(t)}</text>')

    def spans(s, x, y, parts, size=13, font=UI, anchor="start", extra=""):
        """parts: list of (text, fill, weight)."""
        inner = "".join(f'<tspan fill="{f}" font-weight="{w}">{esc(t)}</tspan>' for t, f, w in parts)
        s.add(f'<text x="{x}" y="{y}" font-family="{font}" font-size="{size}" text-anchor="{anchor}" '
              f'xml:space="preserve" {extra}>{inner}</text>')

    def svg(s):
        return (f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" viewBox="0 0 {W} {H}" '
                f'shape-rendering="geometricPrecision" text-rendering="geometricPrecision">'
                '<defs><filter id="shadow" x="-20%" y="-20%" width="140%" height="160%">'
                '<feDropShadow dx="0" dy="2" stdDeviation="1.5" flood-color="#000" flood-opacity=".14"/>'
                '<feDropShadow dx="0" dy="8" stdDeviation="8" flood-color="#000" flood-opacity=".18"/>'
                '<feDropShadow dx="0" dy="24" stdDeviation="24" flood-color="#000" flood-opacity=".22"/>'
                '</filter>'
                '<filter id="seg" x="-10%" y="-30%" width="120%" height="180%">'
                '<feDropShadow dx="0" dy="1" stdDeviation=".5" flood-color="#000" flood-opacity=".25"/></filter>'
                f'<clipPath id="stage"><rect x="{SX}" y="{SY}" width="{SW}" height="{SH}" rx="10"/></clipPath>'
                '</defs>' + "".join(s.o) + "</svg>")


# ---------- icons: one family, 14 px in a 16 px box, 1.5 px strokes ----------

def glyph(s, cx, cy, kind, T):
    r = 6.25
    if kind == "idle":
        s.add(f'<circle cx="{cx}" cy="{cy}" r="{r}" fill="none" stroke="{T["text3"]}" stroke-width="1.5"/>')
    elif kind == "work":
        s.add(f'<circle cx="{cx}" cy="{cy}" r="{r}" fill="none" stroke="{T["accent"]}" stroke-opacity=".25" stroke-width="1.5"/>'
              f'<path d="M{cx} {cy - r}A{r} {r} 0 0 1 {cx + r * 0.866:.2f} {cy + r * 0.5:.2f}" fill="none" '
              f'stroke="{T["accent"]}" stroke-width="1.5" stroke-linecap="round"/>')
    elif kind == "need":
        s.add(f'<circle cx="{cx}" cy="{cy}" r="{r}" fill="none" stroke="{T["need"]}" stroke-width="1.5"/>'
              f'<circle cx="{cx}" cy="{cy}" r="2.5" fill="{T["need"]}"/>')
    elif kind == "err":
        d = 2.4
        s.add(f'<circle cx="{cx}" cy="{cy}" r="7" fill="{T["err"]}"/>'
              f'<path d="M{cx - d} {cy - d}L{cx + d} {cy + d}M{cx + d} {cy - d}L{cx - d} {cy + d}" '
              f'stroke="{T["on_state"]}" stroke-width="1.5" stroke-linecap="round"/>')
    elif kind == "done":
        s.add(f'<circle cx="{cx}" cy="{cy}" r="7" fill="{T["done"]}"/>'
              f'<path d="M{cx - 3} {cy + 0.2}L{cx - 0.8} {cy + 2.4}L{cx + 3.2} {cy - 2.2}" fill="none" '
              f'stroke="{T["on_state"]}" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>')
    elif kind == "term":
        s.add(f'<circle cx="{cx}" cy="{cy}" r="3" fill="{T["text3"]}"/>')


def icon(s, kind, cx, cy, col):
    st = f'fill="none" stroke="{col}" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"'
    if kind == "search":
        s.add(f'<circle cx="{cx - 1}" cy="{cy - 1}" r="4.75" {st}/><path d="M{cx + 2.6} {cy + 2.6}L{cx + 5.5} {cy + 5.5}" {st}/>')
    elif kind == "splitr":
        s.add(f'<rect x="{cx - 6.25}" y="{cy - 5.25}" width="12.5" height="10.5" rx="2" {st}/><path d="M{cx} {cy - 5}V{cy + 5}" {st}/>')
    elif kind == "splitd":
        s.add(f'<rect x="{cx - 6.25}" y="{cy - 5.25}" width="12.5" height="10.5" rx="2" {st}/><path d="M{cx - 6} {cy}H{cx + 6}" {st}/>')
    elif kind == "zoom":
        s.add(f'<path d="M{cx + 1.5} {cy - 5.5}H{cx + 5.5}V{cy - 1.5}M{cx - 1.5} {cy + 5.5}H{cx - 5.5}V{cy + 1.5}'
              f'M{cx + 5.5} {cy - 5.5}L{cx + 1} {cy - 1}M{cx - 5.5} {cy + 5.5}L{cx - 1} {cy + 1}" {st}/>')
    elif kind == "chev-d":
        s.add(f'<path d="M{cx - 3} {cy - 1.5}L{cx} {cy + 1.5}L{cx + 3} {cy - 1.5}" {st}/>')
    elif kind == "chev-r":
        s.add(f'<path d="M{cx - 1.5} {cy - 3}L{cx + 1.5} {cy}L{cx - 1.5} {cy + 3}" {st}/>')
    elif kind == "plus":
        s.add(f'<path d="M{cx} {cy - 5}V{cy + 5}M{cx - 5} {cy}H{cx + 5}" {st}/>')
    elif kind == "enter":
        s.add(f'<path d="M{cx + 4} {cy - 4}V{cy}H{cx - 4}M{cx - 1.5} {cy - 2.5}L{cx - 4} {cy}L{cx - 1.5} {cy + 2.5}" {st}/>')


def chip(s, x_right, cy, label, T, fill=None):
    """Keycap: one chip, 11/500, 18 tall, radius 4, 1 px border, no fill. Right aligned."""
    w = tw(label, 11, 500) + 12
    x = x_right - w
    a, op = T["border"]
    s.rect(x + .5, cy - 8.5, w - 1, 17, fill or "none", 4, 1 if fill else 0, a, op)
    s.text(x + w / 2, cy + 3.8, label, T["text3"], 11, 500, anchor="middle")
    return x


def pill(s, x_right, cy, label, T, k):
    w = tw(label, 11, 600) + 12
    x = x_right - w
    s.rect(x, cy - 8, w, 16, T[k + "_fill"], 8)
    s.text(x + w / 2, cy + 3.8, label, T[k + "_ink"], 11, 600, anchor="middle")
    return x


def badge(s, x, cy, n, T, k):
    w = max(16, tw(str(n), 11, 600) + 10)
    s.rect(x, cy - 8, w, 16, T[k + "_fill"], 8)
    s.text(x + w / 2, cy + 3.8, str(n), T[k + "_ink"], 11, 600, anchor="middle", extra='font-feature-settings="tnum"')
    return w


# ---------- sidebar ----------

def sidebar(s, T, focused="paint cache", hover="flaky test"):
    s.rect(0, 0, W, H, T["base"])
    # Search field, 28 px, centred in the 40 px band.
    a, op = T["border"]
    s.rect(12.5, 6.5, SB - 24, 27, T["field"], 6, 1, a, op * 0.7)
    icon(s, "search", 27, 20, T["text3"])
    s.text(40, 24.5, "Search", T["text3"], 13)
    chip(s, SB - 18, 20, "Ctrl+Shift+P", T)

    y = 52
    # Needs you: label plus one count badge.
    s.text(20, y + 12, "Needs you", T["text2"], 12, 600)
    badge(s, 20 + tw("Needs you", 12, 600) + 8, y + 8, 2, T, "need")
    y += 24
    y = row(s, T, y, "need", "api retries", "claude · Retry on 429, or surface the error?", "4m")
    y = row(s, T, y, "err", "migrations", "codex · Migration failed: relation users already exists", "9m", ws="api")
    y += 14
    y = group(s, T, y, "tuios", "4 panes")
    y = row(s, T, y, "work", "paint cache", "codex · Editing painter.rs", "1m", sel=(focused == "paint cache"))
    y = row(s, T, y, "need", "api retries", "claude · Retry on 429, or surface the error?", "4m")
    y = row(s, T, y, "done", "flaky test", "claude · Fixed the race in attach", "12m", ws="2", hov=(hover == "flaky test"))
    y = row(s, T, y, "term", "nvim", "~/dev/tuios-gpui · main", "")
    y += 14
    y = group(s, T, y, "api", "3 panes")
    y = row(s, T, y, "err", "migrations", "codex · Migration failed: relation users already exists", "9m")
    y = row(s, T, y, "work", "docs pass", "codex · Reading docs/KEYBINDINGS.md", "2m")
    y = row(s, T, y, "idle", "readme", "claude · Updated 4 docs pages", "1h")
    y += 14
    group(s, T, y, "dotfiles", "2 terminals", folded=True)

    # Footer: one quiet action.
    s.rect(12, H - 44, SB - 24, 32, T["hover"], 6, 0)
    icon(s, "plus", 27, H - 28, T["text2"])
    s.text(40, H - 23.5, "New session", T["text2"], 13, 500)


def group(s, T, y, name, meta, folded=False):
    icon(s, "chev-r" if folded else "chev-d", 22, y + 14, T["text3"])
    s.spans(32, y + 18, [(name, T["text2"], 600), ("  " + meta, T["text3"], 400)], 12)
    return y + 28


def row(s, T, y, kind, title, sub, age, sel=False, hov=False, ws=None):
    if sel:
        s.rect(8, y, SB - 16, 44, T["selected"], 6)
    elif hov:
        s.rect(8, y, SB - 16, 44, T["hover"], 6)
    glyph(s, 28, y + 15, kind, T)
    s.text(44, y + 19.5, title, T["text"], 13, 500)
    right = SB - 20
    if age:
        s.text(right, y + 19.5, age, T["text3"], 11, 500, anchor="end", extra='font-feature-settings="tnum"')
        right -= tw(age, 11, 500) + 6
    if ws:
        s.text(right, y + 19.5, ws, T["text3"], 11, 500, anchor="end")
    # Second line: harness in text3, message in text2, cut with an ellipsis.
    harness, _, msg = sub.partition(" · ")
    if not msg:
        s.text(44, y + 36, sub, T["text3"], 12)
    else:
        room = SB - 20 - 44 - tw(harness + " · ", 12)
        while tw(msg, 12) > room and len(msg) > 3:
            msg = msg[:-2].rstrip() + "…" if not msg.endswith("…") else msg[:-2] + "…"
        s.spans(44, y + 36, [(harness + " · ", T["text3"], 400), (msg, T["text2"], 400)], 12)
    return y + 46


# ---------- title band ----------

def band(s, T):
    x = GX
    s.text(x, 25, "tuios", T["text"], 13, 600)
    icon(s, "chev-d", x + tw("tuios", 13, 600) + 9, 20, T["text3"])
    x += tw("tuios", 13, 600) + 26
    # Segmented workspace control.
    segs = [("1", "build", True, False), ("2", "monitor", False, True), ("3", "review", False, False)]
    widths = [tw(n + "  " + l, 13, 500) + 20 + (10 if d else 0) for n, l, _, d in segs]
    a, op = T["border"]
    s.rect(x, 6, sum(widths) + 4, 28, T["field"], 7)
    sx = x + 2
    for (n, l, act, dot), w in zip(segs, widths):
        if act:
            seg_fill = T["stage"] if not T["light"] else "#ffffff"
            s.add(f'<rect x="{sx + .5}" y="8.5" width="{w - 1}" height="23" rx="5" fill="{seg_fill}" '
                  f'stroke="{a}" stroke-opacity="{op}" filter="url(#seg)"/>')
        s.spans(sx + 10, 24.5, [(n + "  ", T["text3"], 500), (l, T["text"] if act else T["text2"], 500)], 13,
                extra='font-feature-settings="tnum"')
        if dot:
            s.add(f'<circle cx="{sx + w - 11}" cy="20" r="3" fill="{T["need"]}"/>')
        sx += w
    # Actions, right aligned to the stage edge.
    for i, k in enumerate(("splitr", "splitd", "zoom")):
        cx = SX + SW - 14 - (2 - i) * 32
        icon(s, k, cx, 20, T["text2"])


# ---------- stage and panes ----------

def stage(s, T):
    a, op = T["border"]
    s.add(f'<rect x="{SX + .5}" y="{SY + .5}" width="{SW - 1}" height="{SH - 1}" rx="10" fill="{T["stage"]}" '
          f'stroke="{a}" stroke-opacity="{op}"/>')


def cx_(c):
    return GX + c * CW


def cy_(r):
    return GY + r * CH


def term_line(s, c, r, parts, T):
    """parts: (text, colour key or hex, weight)."""
    out = []
    for t, col, wgt in parts:
        out.append((t, T.get(col, col), wgt))
    s.spans(cx_(c), cy_(r) + 15, out, 15, MONO)


def header(s, T, c0, c1, r, kind, title, detail, focused, pill_state=None):
    """Pane header in the tuios border row: ground strip, glyph, title, detail, pill."""
    x0 = cx_(c0) - 4
    x1 = cx_(c1 + 1) + 4
    y = cy_(r)
    glyph(s, cx_(c0) + 8, y + 10, kind, T)
    tcol = T["text"] if focused else T["text2"]
    s.spans(cx_(c0) + 22, y + 14.2, [(title, tcol, 500 if focused else 400), ("   " + detail, T["text3"], 400)], 12)
    if pill_state:
        pill(s, x1 - 4, y + 10, "Needs you", T, pill_state)


def dim(s, T, c0, c1, r0, r1, amt=0.30):
    s.rect(cx_(c0) - 4, cy_(r0), cx_(c1 + 1) - cx_(c0) + 8, cy_(r1 + 1) - cy_(r0), T["stage"], 0, amt)


def panes(s, T, focus="A"):
    L = T["light"]
    ac = {k: v for k, v in zip(("blue", "green", "red", "yellow", "mag", "cyan", "fg", "com"),
          (("#2e7de9", "#587539", "#f52a65", "#8c6c3e", "#9854f1", "#007197", "#3760bf", "#848cb5") if L else
           ("#7aa2f7", "#9ece6a", "#f7768e", "#e0af68", "#bb9af7", "#7dcfff", "#c0caf5", "#565f89")))}
    TT = dict(T, **ac)
    split = 64                     # gap column between A and B
    a1, b0, b1 = split - 1, split + 1, COLS - 1
    rb = 21                        # header row of C
    hx_ = cx_(split) + CW / 2      # gap centre line
    hl, hop = T["hairline"]
    s.rect(hx_ - .5, cy_(0), 1, cy_(ROWS) - cy_(0), hl, 0, hop)

    # A: codex, working, focused.
    header(s, TT, 0, a1, 0, "work", "paint cache", "codex · Editing painter.rs", focus == "A")
    lines = [
        [("› ", "com", 400), ("Cache the shaped rows per generation in the painter", "fg", 400)],
        [],
        [("• ", "com", 400), ("Read ", "fg", 400), ("crates/tuios-gpui/src/painter.rs", "blue", 400)],
        [("• ", "com", 400), ("Read ", "fg", 400), ("crates/tuios-gpui/src/rowplan.rs", "blue", 400)],
        [("• ", "com", 400), ("Edit ", "fg", 400), ("painter.rs ", "blue", 400), ("+42 ", "green", 400), ("-17", "red", 400)],
        [("  │ ", "com", 400), ("fn prepare(&mut self, screen: &Screen) {", "com", 400)],
        [("  │ ", "com", 400), ("+    let dirty = screen.rows.iter()", "green", 400)],
        [("  │ ", "com", 400), ("+        .filter(|r| r.gen != self.drawn[r.y]);", "green", 400)],
        [("  │ ", "com", 400), ("-    for row in &screen.rows {", "red", 400)],
        [],
        [("• ", "com", 400), ("Ran ", "fg", 400), ("cargo test -p tuios-gpui", "fg", 700)],
        [("  └ ", "com", 400), ("test result: ok. 41 passed; 0 failed", "green", 400)],
        [],
        [("• ", "com", 400), ("Ran ", "fg", 400), ("cargo run --release -- --perf", "fg", 700)],
        [("  └ ", "com", 400), ("scroll p50 1.9 ms  p95 3.1 ms  ", "fg", 400), ("(was 4.8 ms)", "com", 400)],
        [],
        [("Working ", "blue", 400), ("(5m 03s · esc to interrupt)", "com", 400)],
    ]
    for i, p in enumerate(lines):
        if p:
            term_line(s, 0, 1 + i, p, TT)
    # Prompt box at the bottom of A, box drawing on the cell grid.
    rbx = ROWS - 4
    s.add(f'<rect x="{cx_(0) + 4.5}" y="{cy_(rbx) + 10.5}" width="{(a1 + 1) * CW - 9}" height="{2 * CH}" rx="4" '
          f'fill="none" stroke="{TT["com"]}" stroke-width="1"/>')
    term_line(s, 1, rbx + 1, [("> ", "com", 400)], TT)
    # Cursor: a block on whole cell pixels at the prompt.
    s.rect(cx_(3), cy_(rbx + 1) + 1, CW, CH - 2, T["fg"] if not L else "#3760bf", 0, .9)

    # B: claude, needs you.
    header(s, TT, b0, b1, 0, "need", "api retries", "claude · ~/dev/api · main", focus == "B", "need")
    bl = [
        [("● ", "green", 400), ("Read ", "fg", 400), ("src/client.ts", "blue", 400)],
        [("● ", "green", 400), ("Read ", "fg", 400), ("src/errors.ts", "blue", 400)],
        [("● ", "green", 400), ("Search ", "fg", 400), ('"status === 429"', "com", 400), (" · 3 matches", "com", 400)],
        [],
        [("The client throws on any non-2xx response today, so a", "fg", 400)],
        [("rate limit surfaces as a generic ", "fg", 400), ("HttpError", "fg", 700), (". Two options:", "fg", 400)],
        [],
        [("  1. Retry 429 with backoff and honour ", "fg", 400), ("Retry-After", "fg", 700)],
        [("  2. Surface a typed ", "fg", 400), ("RateLimitError", "fg", 700), (" to the caller", "fg", 400)],
        [],
        [("Should the client retry on 429, or surface the error?", "fg", 700)],
        [],
        [("› 1. Retry with backoff", "yellow", 400)],
        [("  2. Surface the error", "fg", 400)],
        [("  3. Type something else", "fg", 400)],
        [],
        [("Enter to select · ↑↓ to navigate · Esc to cancel", "com", 400)],
    ]
    for i, p in enumerate(bl):
        if p:
            term_line(s, b0, 1 + i, p, TT)

    # C: nvim, plain terminal. Its own background differs from the stage (OSC 11).
    nv_bg = "#16161e" if not L else "#d0d5e3"
    s.rect(cx_(b0) - 4, cy_(rb + 1), cx_(b1 + 1) - cx_(b0) + 8, cy_(ROWS) - cy_(rb + 1), nv_bg)
    header(s, TT, b0, b1, rb, "term", "nvim", "fleet.rs · ~/dev/tuios-gpui · main", focus == "C")
    cl = [
        [("//! ", "com", 400), ("The fleet: every pane in every session on the daemon.", "com", 400)],
        [("//!", "com", 400)],
        [("//! ", "com", 400), ("The attached session comes from the bridge's live", "com", 400)],
        [("//! ", "com", 400), ("state. Other sessions come from fleet events.", "com", 400)],
        [],
        [("use ", "mag", 700), ("serde::Deserialize;", "fg", 400)],
        [("use ", "mag", 700), ("std::cmp::Ordering;", "fg", 400)],
        [("use ", "mag", 700), ("tuios_proto::State;", "fg", 400)],
        [],
        [("/// ", "com", 400), ("What a pane is doing, most urgent first.", "com", 400)],
        [("#[derive(Clone, Copy, Debug, PartialEq, Eq, Ord)]", "cyan", 400)],
        [("pub enum ", "mag", 700), ("Status", "blue", 400), (" {", "fg", 400)],
        [("    NeedsYou,", "fg", 400)],
        [("    Errored,", "fg", 400)],
        [("    Working,", "fg", 400)],
        [("    Done,", "fg", 400)],
    ]
    for i, p in enumerate(cl):
        if p:
            term_line(s, b0, rb + 1 + i, p, TT)
    # nvim status line on the last row.
    s.rect(cx_(b0), cy_(ROWS - 1), cx_(b1 + 1) - cx_(b0), CH, "#292e42" if not L else "#c4c8da")
    term_line(s, b0, ROWS - 1, [(" fleet.rs", "fg", 400), (" " * 40 + "1,1      Top", "com", 400)], TT)

    # Focus: the content of unfocused panes sits back. Headers keep their marks at full strength.
    if focus != "B":
        dim(s, T, b0, b1, 1, rb - 1)
    if focus != "C":
        dim(s, T, b0, b1, rb + 1, ROWS - 1)
    if focus != "A":
        dim(s, T, 0, a1, 1, ROWS - 1)

    # Needs-you ring on B: 1 px on the gap centre line, a soft 3 px glow.
    x0, y0 = hx_, cy_(0) - 3
    x1, y1 = cx_(b1 + 1) + 4, cy_(rb) - 1
    s.add(f'<rect x="{x0 - 1.5}" y="{y0 - 1.5}" width="{x1 - x0 + 3}" height="{y1 - y0 + 3}" rx="7.5" fill="none" '
          f'stroke="{T["need"]}" stroke-opacity=".16" stroke-width="3"/>')
    s.add(f'<rect x="{x0}" y="{y0 + .5}" width="{x1 - x0 - .5}" height="{y1 - y0 - 1}" rx="6" fill="none" '
          f'stroke="{T["need"]}" stroke-width="1"/>')


def main_view(T, focus="A"):
    s = S()
    sidebar(s, T)
    band(s, T)
    stage(s, T)
    s.add('<g clip-path="url(#stage)">')
    panes(s, T, focus)
    s.add("</g>")
    return s


# ---------- palette ----------

def palette(T):
    s = main_view(T)
    sc, sa = T["scrim"]
    s.rect(0, 0, W, H, sc, 0, sa)
    PW = 680
    px = (W - PW) // 2
    py = round(H * 0.14)
    sections = [
        ("Needs you", [("need", [("api ", 400), ("re", 600), ("tries", 400)], "claude · Retry on 429, or surface the error?", "Ctrl+Shift+J")]),
        ("Panes", [("idle", [("", 400), ("re", 600), ("adme", 400)], "claude · api", "")]),
        ("Commands", [
            ("cmd", [("P", 400), ("re", 600), ("vious pane", 400)], "", "Ctrl+Shift+Tab"),
            ("cmd", [("P", 400), ("re", 600), ("vious session", 400)], "", "Ctrl+Shift+["),
            ("cmd", [("", 400), ("Re", 600), ("set text size", 400)], "", "Ctrl+0"),
        ]),
    ]
    rows = sum(len(r) for _, r in sections)
    ph = 52 + 1 + 6 + len(sections) * 28 + rows * 40 + 6 + 1 + 40
    a, op = T["border"]
    s.add(f'<g filter="url(#shadow)"><rect x="{px}" y="{py}" width="{PW}" height="{ph}" rx="12" fill="{T["raised"]}"/></g>')
    s.add(f'<rect x="{px + .5}" y="{py + .5}" width="{PW - 1}" height="{ph - 1}" rx="11.5" fill="none" stroke="{a}" stroke-opacity="{op}"/>')
    # Query row, 52 px.
    icon(s, "search", px + 24, py + 26, T["text3"])
    s.text(px + 44, py + 31.5, "re", T["text"], 16)
    s.rect(round(px + 44 + tw("re", 16)), py + 16, 2, 20, T["accent"], 0)
    chip(s, px + PW - 16, py + 26, "Esc", T)
    hl, hop = T["hairline"]
    s.rect(px, py + 52, PW, 1, hl, 0, hop)
    y = py + 53 + 6
    first = True
    for name, items in sections:
        s.text(px + 20, y + 18, name, T["text3"], 12, 600)
        y += 28
        for kind, title, sub, key in items:
            if first:
                s.rect(px + 6, y, PW - 12, 40, T["raised_sel"], 8)
            if kind == "cmd":
                s.add(f'<path d="M{px + 24} {y + 14}l4 6-4 6" fill="none" stroke="{T["text3"]}" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>')
            else:
                glyph(s, px + 26, y + 20, kind, T)
            # Unmatched characters in text, matched ones in text at 600; the weight carries the match.
            parts = [(t, T["text"], w) for t, w in title]
            if sub:
                parts.append(("   " + sub, T["text2"] if first else T["text3"], 400))
            s.spans(px + 44, y + 25, parts, 14)
            if key:
                chip(s, px + PW - 20, y + 20, key.replace("+", "+"), T)
            first = False
            y += 40
    y += 6
    s.rect(px, y, PW, 1, hl, 0, hop)
    s.text(px + 20, y + 25, "5 results", T["text3"], 12, 400)
    xr = chip(s, px + PW - 16, y + 20, "Esc", T)
    s.text(xr - 8, y + 24.5, "Close", T["text2"], 12, 500, anchor="end")
    xr2 = xr - 8 - tw("Close", 12, 500) - 20
    xr3 = chip(s, xr2, y + 20, "Enter", T)
    s.text(xr3 - 8, y + 24.5, "Open", T["text"], 12, 500, anchor="end")
    return s


if __name__ == "__main__":
    out = sys.argv[1]
    os.makedirs(out, exist_ok=True)
    for name, svg in (("main-dark", main_view(DARK).svg()),
                      ("palette-dark", palette(DARK).svg()),
                      ("main-light", main_view(LIGHT).svg())):
        with open(os.path.join(out, name + ".svg"), "w") as f:
            f.write(svg)
    print(f"grid {COLS}x{ROWS} at {GX},{GY}; stage {SX},{SY} {SW}x{SH}")
