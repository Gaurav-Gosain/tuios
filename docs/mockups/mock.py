#!/usr/bin/env python3
"""Generates the design mockups (SVG) for docs/DESIGN-RESEARCH.md."""
import sys, os

OUT = sys.argv[1]
W, H = 1440, 900
SB = 264
TOP = 38
UI = "Inter, Adwaita Sans, sans-serif"
MONO = "JetBrainsMono Nerd Font Mono, monospace"


def hx(c):
    return tuple(int(c[i:i + 2], 16) for i in (1, 3, 5))


def mix(a, b, t):
    a, b = hx(a), hx(b)
    return "#%02x%02x%02x" % tuple(round(x + (y - x) * t) for x, y in zip(a, b))


def theme(dark):
    if dark:
        bg, fg = "#1a1b26", "#c0caf5"
        return dict(bg=bg, fg=fg, sidebar=mix(bg, fg, .035), hover=mix(bg, fg, .06), sel=mix(bg, fg, .095),
                    raised=mix(bg, fg, .06), hair=mix(bg, fg, .08), t2=mix(fg, bg, .38), t3=mix(fg, bg, .58),
                    accent="#7aa2f7", need="#e0af68", done="#9ece6a", err="#f7768e", green="#9ece6a",
                    blue="#7aa2f7", mag="#bb9af7", cyan="#7dcfff", dim=.22, scrim="#000000", scrima=.20)
    bg, fg = "#e1e2e7", "#3760bf"
    return dict(bg=bg, fg=fg, sidebar=mix(bg, "#000000", .03), hover=mix(bg, "#000000", .05), sel=mix(bg, "#000000", .08),
                raised=bg, hair=mix(bg, fg, .11), t2=mix(fg, bg, .35), t3=mix(fg, bg, .52),
                accent="#2e7de9", need="#8c6c3e", done="#587539", err="#f52a65", green="#587539",
                blue="#2e7de9", mag="#9854f1", cyan="#007197", dim=.22, scrim="#000000", scrima=.08)


class S:
    def __init__(s):
        s.o = []

    def rect(s, x, y, w, h, fill, r=0, op=1, stroke=None, sw=1):
        st = f' stroke="{stroke}" stroke-width="{sw}"' if stroke else ""
        s.o.append(f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{r}" fill="{fill}" fill-opacity="{op}"{st}/>')

    def text(s, x, y, t, fill, size=13, weight=400, font=UI, anchor="start", op=1):
        t = t.replace("&", "&amp;").replace("<", "&lt;")
        s.o.append(f'<text x="{x}" y="{y}" fill="{fill}" fill-opacity="{op}" font-family="{font}" font-size="{size}" '
                   f'font-weight="{weight}" text-anchor="{anchor}" xml:space="preserve">{t}</text>')

    def raw(s, x):
        s.o.append(x)


def glyph(s, x, y, kind, T):
    """A 16 px state glyph centred at x, y."""
    if kind == "need":
        s.raw(f'<circle cx="{x}" cy="{y}" r="6" fill="{T["need"]}"/>'
              f'<rect x="{x-1}" y="{y-3.5}" width="2" height="4.5" rx="1" fill="{T["bg"]}"/>'
              f'<circle cx="{x}" cy="{y+2.6}" r="1.1" fill="{T["bg"]}"/>')
    elif kind == "err":
        s.raw(f'<circle cx="{x}" cy="{y}" r="5.5" fill="none" stroke="{T["err"]}" stroke-width="1.6"/>'
              f'<path d="M{x-2.2} {y-2.2}L{x+2.2} {y+2.2}M{x+2.2} {y-2.2}L{x-2.2} {y+2.2}" stroke="{T["err"]}" stroke-width="1.6" stroke-linecap="round"/>')
    elif kind == "work":
        s.raw(f'<circle cx="{x}" cy="{y}" r="5.5" fill="none" stroke="{T["accent"]}" stroke-opacity=".25" stroke-width="1.8"/>'
              f'<path d="M{x} {y-5.5}A5.5 5.5 0 0 1 {x+5.5} {y}" fill="none" stroke="{T["accent"]}" stroke-width="1.8" stroke-linecap="round"/>')
    elif kind == "done":
        s.raw(f'<path d="M{x-4} {y}L{x-1.3} {y+2.8}L{x+4.2} {y-3}" fill="none" stroke="{T["done"]}" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>')
    elif kind == "idle":
        s.raw(f'<circle cx="{x}" cy="{y}" r="4.5" fill="none" stroke="{T["t3"]}" stroke-width="1.5"/>')
    elif kind == "term":
        s.raw(f'<path d="M{x-4} {y-3.5}L{x-0.5} {y}L{x-4} {y+3.5}M{x+0.5} {y+4}H{x+4.5}" fill="none" stroke="{T["t3"]}" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>')


def sidebar(s, T):
    s.rect(0, 0, SB, H, T["sidebar"])
    s.rect(SB - 1, 0, 1, H, T["hair"])
    # search field
    s.rect(10, 10, SB - 20, 30, T["hover"], 7)
    s.raw(f'<circle cx="26" cy="24" r="4.5" fill="none" stroke="{T["t3"]}" stroke-width="1.5"/><path d="M29.5 27.5L32 30" stroke="{T["t3"]}" stroke-width="1.5" stroke-linecap="round"/>')
    s.text(40, 29, "Search", T["t3"], 12.5)
    s.text(SB - 18, 29, "Ctrl Shift P", T["t3"], 10.5, 500, anchor="end")
    # count line
    y = 66
    s.text(16, y, "1", T["need"], 12, 600)
    s.text(25, y, "needs you", T["t2"], 12)
    s.text(87, y, "·", T["t3"], 12)
    s.text(96, y, "2", T["accent"], 12, 600)
    s.text(105, y, "working", T["t2"], 12)
    s.text(156, y, "·", T["t3"], 12)
    s.text(165, y, "2", T["done"], 12, 600)
    s.text(174, y, "done", T["t2"], 12)

    def section(y, label, extra=None):
        s.text(16, y, label, T["t3"], 11, 500)
        if extra:
            s.text(SB - 16, y, extra, T["t3"], 11, 500, anchor="end")

    def row(y, kind, title, sub, age, sel=False, ws=None, unread=True):
        if sel:
            s.rect(6, y, SB - 12, 46, T["sel"], 6)
        glyph(s, 24, y + 15, kind, T)
        s.text(38, y + 19, title, T["fg"] if (sel or unread) else T["t2"], 13, 500)
        s.text(38, y + 36, sub, T["t3"], 12)
        if age:
            s.text(SB - 16, y + 19, age, T["t3"], 11, 500, anchor="end")
        if ws:
            s.rect(SB - 16 - 30 - 14, y + 8, 14, 14, T["hover"], 3)
            s.text(SB - 16 - 30 - 7, y + 19, ws, T["t3"], 10, 600, anchor="middle")

    y = 96
    section(y, "Needs you")
    row(y + 10, "need", "claude · api retries", "Retry on 429, or surface the error?", "12m")
    y = 172
    section(y, "tuios", "attached")
    row(y + 10, "work", "codex · paint cache", "Editing crates/painter.rs", "3m", sel=True)
    row(y + 58, "work", "claude · docs pass", "Reading docs/KEYBINDINGS.md", "8m")
    row(y + 106, "done", "claude · flaky test", "Fixed the race in attach. 3 files", "40m")
    row(y + 154, "term", "nvim", "~/dev/tuios · fix/rail-header-gap", "", unread=False)
    row(y + 202, "term", "htop", "~", "", ws="2", unread=False)
    y = 440
    section(y, "api", "1 needs you")
    row(y + 10, "err", "codex · migrations", "Command failed: cargo sqlx migrate", "1h")
    row(y + 58, "done", "claude · readme", "Updated 4 pages with the new flags", "2h", unread=False)
    s.text(38, y + 124, "3 terminals", T["t3"], 12)
    y = 590
    section(y, "dotfiles")
    s.text(38, y + 26, "2 terminals", T["t3"], 12)
    # footer
    s.rect(0, H - 40, SB - 1, 1, T["hair"])
    s.raw(f'<path d="M20 {H-20}H30M25 {H-25}V{H-15}" stroke="{T["t2"]}" stroke-width="1.5" stroke-linecap="round"/>')
    s.text(38, H - 16, "New session", T["t2"], 12.5)
    s.text(SB - 16, H - 16, "Ctrl Shift J  next", T["t3"], 10.5, 500, anchor="end")


def terminal_lines(s, x, y, lines, T, cw=8.4, ch=18):
    for i, line in enumerate(lines):
        cx = x
        for seg, col in line:
            s.text(cx, y + i * ch + 13, seg, T.get(col, col), 13.5, 400, MONO)
            cx += len(seg) * cw


def main_area(s, T, need_ring=True):
    X0 = SB
    s.rect(X0, 0, W - X0, H, T["bg"])
    # top bar
    s.text(X0 + 16, 24, "tuios", T["fg"], 13, 600)
    s.text(X0 + 56, 24, "/", T["t3"], 13)
    tabs = [("1", "build", True, "work"), ("2", "monitor", False, None), ("3", "notes", False, "need")]
    tx = X0 + 70
    for n, name, act, st in tabs:
        w = 16 + len(name) * 7.2 + 16 + (12 if st else 0)
        if act:
            s.rect(tx, 7, w, 24, T["sel"], 6)
        s.text(tx + 10, 24, n, T["t3"], 12, 600)
        s.text(tx + 22, 24, name, T["fg"] if act else T["t2"], 12.5, 500 if act else 400)
        if st:
            s.raw(f'<circle cx="{tx + w - 12}" cy="19" r="3" fill="{T["accent"] if st == "work" else T["need"]}"/>')
        tx += w + 4
    for i, ic in enumerate(["split-r", "split-d", "zoom"]):
        bx = W - 16 - 28 * (3 - i)
        if ic == "split-r":
            s.raw(f'<rect x="{bx+7}" y="12" width="14" height="14" rx="3" fill="none" stroke="{T["t2"]}" stroke-width="1.4"/><path d="M{bx+14} 12V26" stroke="{T["t2"]}" stroke-width="1.4"/>')
        elif ic == "split-d":
            s.raw(f'<rect x="{bx+7}" y="12" width="14" height="14" rx="3" fill="none" stroke="{T["t2"]}" stroke-width="1.4"/><path d="M{bx+7} 19H{bx+21}" stroke="{T["t2"]}" stroke-width="1.4"/>')
        else:
            s.raw(f'<path d="M{bx+16} 12H{bx+21}V17M{bx+12} 26H{bx+7}V21M{bx+21} 12L{bx+15.5} 17.5M{bx+7} 26L{bx+12.5} 20.5" fill="none" stroke="{T["t2"]}" stroke-width="1.4" stroke-linecap="round"/>')
    s.rect(X0, TOP, W - X0, 1, T["hair"], op=.6)

    gx, gy = X0 + 4, TOP + 2
    split_x = X0 + 4 + 8.4 * 76
    split_y = TOP + 2 + 18 * 25
    # pane rects (content areas)
    panes = [
        dict(x=gx, y=gy, w=split_x - gx, h=H - gy, title="codex · paint cache", ctx="~/dev/tuios-gpui · main", kind="work", focus=True,
             lines=[[("› ", "t3"), ("Cache the shaped rows per generation", "fg")], [],
                    [("• ", "accent"), ("Read ", "fg"), ("crates/tuios-gpui/src/painter.rs", "cyan")],
                    [("• ", "accent"), ("Read ", "fg"), ("crates/tuios-gpui/src/rowplan.rs", "cyan")],
                    [("• ", "accent"), ("Edit ", "fg"), ("painter.rs ", "cyan"), ("+42 ", "green"), ("-17", "err")],
                    [("  │ ", "t3"), ("fn paint(&mut self, screen: &Screen) {", "t2")],
                    [("  │ ", "t3"), ("    let rows = self.rows.iter()", "t2")],
                    [("  │ ", "t3"), ("        .filter(|r| r.gen != r.drawn);", "t2")],
                    [],
                    [("• ", "accent"), ("Run ", "fg"), ("cargo test -p tuios-gpui", "fg")],
                    [("  └ ", "t3"), ("test result: ok. 41 passed; 0 failed", "green")],
                    [],
                    [("Working ", "accent"), ("(3m 12s · esc to interrupt)", "t3")],
                    [], [], [], [], [], [], [], [],
                    [("────────────────────────────────────────────────────────────────────────", "t3")],
                    [("› ", "fg"), ("█", "fg")],
                    [("────────────────────────────────────────────────────────────────────────", "t3")],
                    [("  gpt-5.5 high · 61% context left", "t3")]]),
        dict(x=split_x, y=gy, w=W - split_x, h=split_y - gy, title="claude · api retries", ctx="~/dev/api · retry-429", kind="need", focus=False,
             lines=[[("● ", "green"), ("Read ", "fg"), ("src/client.ts", "cyan")],
                    [("● ", "green"), ("Read ", "fg"), ("src/errors.ts", "cyan")], [],
                    [("The client throws on any non-2xx today. Two options:", "fg")], [],
                    [(" 1. ", "t2"), ("Retry 429 with backoff (honour Retry-After)", "fg")],
                    [(" 2. ", "t2"), ("Surface RateLimitError to the caller", "fg")], [],
                    [("Should the API client retry on 429, or surface", "fg")],
                    [("the error to the caller?", "fg")], [],
                    [("❯ ", "need"), ("1. Retry with backoff", "need")],
                    [("  2. Surface the error", "t2")],
                    [("  3. Type something else", "t2")], [],
                    [("Enter to select · ↑↓ to navigate · Esc to cancel", "t3")]]),
        dict(x=split_x, y=split_y, w=W - split_x, h=H - split_y, title="nvim", ctx="~/dev/tuios · fix/rail-header-gap", kind="term", focus=False,
             lines=[[("  1 ", "t3"), ("package", "mag"), (" guibridge", "fg")], [("  2 ", "t3")],
                    [("  3 ", "t3"), ("import", "mag"), (" (", "fg")],
                    [("  4 ", "t3"), ('    "encoding/json"', "green")],
                    [("  5 ", "t3"), ('    "time"', "green")],
                    [("  6 ", "t3"), (")", "fg")], [("  7 ", "t3")],
                    [("  8 ", "t3"), ("// gitEntry is a cached reading.", "t3")],
                    [("  9 ", "t3"), ("type", "mag"), (" gitEntry ", "cyan"), ("struct", "mag"), (" {", "fg")],
                    [(" 10 ", "t3"), ("    repo, branch ", "fg"), ("string", "cyan")],
                    [(" 11 ", "t3"), ("    at           time.", "fg"), ("Time", "cyan")],
                    [(" 12 ", "t3"), ("}", "fg")], [], [], [], [], [],
                    [("         bridge.go                       12,1  Top", "t2")]]),
    ]
    for p in panes:
        # header row
        hy = p["y"]
        glyph(s, p["x"] + 14, hy + 10, p["kind"], T)
        s.text(p["x"] + 26, hy + 14, p["title"], T["fg"] if p["focus"] else T["t2"], 12, 500 if p["focus"] else 400)
        tw = len(p["title"]) * 6.9
        s.text(p["x"] + 26 + tw + 10, hy + 14, p["ctx"], T["t3"], 12)
        if p["kind"] == "need":
            px = p["x"] + p["w"] - 86
            s.rect(px, hy + 2, 74, 17, T["need"], 8.5, op=.16)
            s.text(px + 37, hy + 14, "Needs you", T["need"], 11, 600, anchor="middle")
        if p["kind"] == "work":
            s.text(p["x"] + p["w"] - 14, hy + 14, "3m", T["t3"], 11, 500, anchor="end")
        if p["title"] == "nvim":
            s.rect(p["x"] + 8, hy + 22 + 18 * 17, 8.4 * 8, 18, T["green"])
        terminal_lines(s, p["x"] + 8, hy + 22, p["lines"], T)
        if p["title"] == "nvim":
            s.text(p["x"] + 8 + 8.4 * 0, hy + 22 + 17 * 18 + 13, " NORMAL ", T["bg"], 13.5, 700, MONO)
        if not p["focus"]:
            s.rect(p["x"], p["y"], p["w"], p["h"], T["bg"], op=T["dim"])
        if p["kind"] == "need" and need_ring:
            s.rect(p["x"] + 3, p["y"] + 2, p["w"] - 7, p["h"] - 5, "none", 6, stroke=T["need"], sw=1.5)
    # dividers
    s.rect(split_x - 4, TOP + 6, 1, H - TOP - 12, T["hair"])
    s.rect(split_x + 4, split_y - 5, W - split_x - 12, 1, T["hair"])


def palette(s, T):
    s.rect(0, 0, W, H, T["scrim"], op=T["scrima"])
    pw, px0, py0 = 640, (W - 640) / 2, 108
    rows = [("Needs you", None), ("need", "claude · api retries", "api · Retry on 429, or surface the error?", ""),
            ("Panes", None), ("work", "codex · paint cache", "tuios · workspace 1", ""),
            ("term", "nvim", "tuios · ~/dev/tuios", ""),
            ("Commands", None), ("cmd", "Split right", "", "Ctrl Shift D"), ("cmd", "Split down", "", "Ctrl Shift E"),
            ("cmd", "Jump to the next pane that needs you", "", "Ctrl Shift J"),
            ("cmd", "New session", "", "")]
    h = 52 + 8 + sum(28 if r[1] is None else 36 for r in rows) + 8 + 34
    s.raw(f'<defs><filter id="sh" x="-20%" y="-20%" width="140%" height="160%"><feDropShadow dx="0" dy="16" stdDeviation="20" flood-color="#000" flood-opacity=".35"/>'
          f'<feDropShadow dx="0" dy="2" stdDeviation="3" flood-color="#000" flood-opacity=".2"/></filter></defs>')
    s.raw(f'<rect x="{px0}" y="{py0}" width="{pw}" height="{h}" rx="12" fill="{T["raised"]}" stroke="{T["hair"]}" filter="url(#sh)"/>')
    s.raw(f'<circle cx="{px0+24}" cy="{py0+25}" r="6" fill="none" stroke="{T["t3"]}" stroke-width="1.6"/><path d="M{px0+28.5} {py0+29.5}L{px0+32} {py0+33}" stroke="{T["t3"]}" stroke-width="1.6" stroke-linecap="round"/>')
    s.text(px0 + 44, py0 + 31, "sp", T["fg"], 15)
    s.rect(px0 + 62, py0 + 17, 1.5, 18, T["accent"])
    s.rect(px0, py0 + 52, pw, 1, T["hair"])
    y = py0 + 60
    first = True
    for r in rows:
        if r[1] is None:
            s.text(px0 + 18, y + 19, r[0], T["t3"], 11, 500)
            y += 28
            continue
        kind, title, sub, keys = r
        if title == "Split right":
            s.rect(px0 + 8, y, pw - 16, 36, T["sel"], 7)
            s.rect(px0 + 8, y + 10, 2, 16, T["accent"], 1)
        if kind == "cmd":
            s.raw(f'<path d="M{px0+20} {y+14}L{px0+24} {y+18}L{px0+20} {y+22}" fill="none" stroke="{T["t3"]}" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>')
        else:
            glyph(s, px0 + 24, y + 18, kind, T)
        s.text(px0 + 40, y + 23, title, T["fg"], 13, 500 if title == "Split right" else 400)
        if sub:
            s.text(px0 + 40 + len(title) * 7 + 10, y + 23, sub, T["t3"], 12)
        if keys:
            kx = px0 + pw - 18
            for k in reversed(keys.split()):
                kw = 8 + len(k) * 6.4
                kx -= kw
                s.rect(kx, y + 9, kw, 18, T["hover"], 4)
                s.text(kx + kw / 2, y + 22, k, T["t2"], 10.5, 500, anchor="middle")
                kx -= 4
        y += 36
    y += 8
    s.rect(px0, y, pw, 1, T["hair"])
    s.text(px0 + 18, y + 22, "9 results", T["t3"], 11.5)
    s.text(px0 + pw - 120, y + 22, "Run", T["t2"], 11.5, anchor="end")
    s.rect(px0 + pw - 114, y + 9, 20, 17, T["hover"], 4)
    s.text(px0 + pw - 104, y + 22, "↵", T["t2"], 11, anchor="middle")
    s.text(px0 + pw - 52, y + 22, "Close", T["t2"], 11.5, anchor="end")
    s.rect(px0 + pw - 46, y + 9, 30, 17, T["hover"], 4)
    s.text(px0 + pw - 31, y + 22, "esc", T["t2"], 10.5, anchor="middle")


def build(name, dark, pal=False):
    T = theme(dark)
    s = S()
    sidebar(s, T)
    main_area(s, T)
    if pal:
        palette(s, T)
    svg = f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" viewBox="0 0 {W} {H}">' + "".join(s.o) + "</svg>"
    open(os.path.join(OUT, name + ".svg"), "w").write(svg)


build("main-dark", True)
build("palette-dark", True, True)
build("main-light", False)
