//! Colours.
//!
//! The terminal colours come from tuios: the bridge runs tuios's own theme
//! code and sends the result (internal/guibridge/theme.go), so a theme name
//! looks the same in both clients. The chrome is derived from the terminal's
//! background and foreground, the accent and the agent colours, once per
//! theme change, by the formulas in docs/design/FINAL.md section 3.
//! `docs/design/final/tokens.py` is the reference implementation, and the
//! tests below check this port against the values it prints.

use ghostty_vt::Rgb;
use gpui::{Hsla, Rgba, rgb, rgba};
use tuios_proto::{ThemeExport, parse_hex};

/// The theme tuios uses when the config names none, exported from the bridge
/// at build time. Shown until the bridge sends the real one.
const DEFAULT_EXPORT: &str = include_str!("../assets/default-theme.json");

/// xterm's 16 colours, which tuios leaves in place when no theme is set.
const XTERM: [u32; 16] = [
    0x000000, 0xcd0000, 0x00cd00, 0xcdcd00, 0x0000ee, 0xcd00cd, 0x00cdcd, 0xe5e5e5, //
    0x7f7f7f, 0xff0000, 0x00ff00, 0xffff00, 0x5c5cff, 0xff00ff, 0x00ffff, 0xffffff,
];

#[derive(Clone, Debug, PartialEq)]
pub struct Theme {
    pub name: String,
    pub light: bool,
    // Terminal panes.
    pub fg: u32,
    pub bg: u32,
    pub cursor: u32,
    pub ansi: [u32; 16],
    /// The panes and their headers: the terminal background.
    pub stage: u32,
    /// The window shell: sidebar and title band.
    pub base: u32,
    /// The search field and the segmented control.
    pub field: u32,
    /// A row or button under the pointer.
    pub hover: u32,
    /// The focused pane's row, a pressed button.
    pub selected: u32,
    /// The palette panel, and its chosen row.
    pub raised: u32,
    pub raised_sel: u32,
    /// The colour and alpha of the stage edge, chips and buttons.
    pub border: Rgba,
    /// Splits and the palette's dividers.
    pub hairline: Rgba,
    pub scrim: Rgba,
    /// Alpha of the `stage` quad over an unfocused pane's content.
    pub dim: f32,
    pub text: u32,
    pub text2: u32,
    pub text3: u32,
    pub accent: u32,
    pub need: u32,
    pub need_fill: u32,
    pub need_ink: u32,
    pub done: u32,
    pub done_fill: u32,
    pub err: u32,
    pub err_fill: u32,
    /// Text selection behind the cells.
    pub selection: u32,
    /// The mark on a filled state disc.
    pub on_state: u32,
}

/// Pulls a colour toward its own grey by `keep` (1 keeps it), so chrome text
/// in a strongly tinted theme reads as text, not as a colour.
fn desaturate(c: u32, keep: f32) -> u32 {
    let (r, g, b) = split(c);
    let y = 0.2126 * r + 0.7152 * g + 0.0722 * b;
    let f = |v: f32| (y + (v - y) * keep).round().clamp(0., 255.) as u32;
    (f(r) << 16) | (f(g) << 8) | f(b)
}

fn split(c: u32) -> (f32, f32, f32) {
    (((c >> 16) & 0xff) as f32, ((c >> 8) & 0xff) as f32, (c & 0xff) as f32)
}

/// Mixes two 0xRRGGBB colours in sRGB; `t` 0 keeps `a`.
pub fn mix32(a: u32, b: u32, t: f32) -> u32 {
    mix(Rgb::from_u32(a), Rgb::from_u32(b), t).to_u32()
}

fn lin(v: f32) -> f32 {
    let v = v / 255.;
    if v <= 0.04045 { v / 12.92 } else { ((v + 0.055) / 1.055).powf(2.4) }
}

fn gam(v: f32) -> f32 {
    let v = if v <= 0.003_130_8 { v * 12.92 } else { 1.055 * v.powf(1. / 2.4) - 0.055 };
    v * 255.
}

/// WCAG 2 relative luminance.
fn lum(c: u32) -> f32 {
    let (r, g, b) = split(c);
    0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b)
}

/// WCAG 2 contrast ratio.
pub fn contrast(a: u32, b: u32) -> f32 {
    let (la, lb) = (lum(a), lum(b));
    let (hi, lo) = if la > lb { (la, lb) } else { (lb, la) };
    (hi + 0.05) / (lo + 0.05)
}

/// The colour nearest `ground` on the line from `ink` that still keeps
/// `target` contrast on it.
fn toward_target(ink: u32, ground: u32, target: f32) -> u32 {
    if contrast(ink, ground) < target {
        return ink;
    }
    let (mut lo, mut hi) = (0f32, 1f32);
    for _ in 0..20 {
        let mid = (lo + hi) / 2.;
        if contrast(mix32(ink, ground, mid), ground) >= target {
            lo = mid;
        } else {
            hi = mid;
        }
    }
    mix32(ink, ground, lo)
}

fn to_oklch(c: u32) -> (f32, f32, f32) {
    let (r, g, b) = split(c);
    let (r, g, b) = (lin(r), lin(g), lin(b));
    let l = (0.412_221_46 * r + 0.536_332_55 * g + 0.051_445_995 * b).cbrt();
    let m = (0.211_903_5 * r + 0.680_699_5 * g + 0.107_396_96 * b).cbrt();
    let s = (0.088_302_46 * r + 0.281_718_85 * g + 0.629_978_7 * b).cbrt();
    let ll = 0.210_454_26 * l + 0.793_617_8 * m - 0.004_072_047 * s;
    let a = 1.977_998_5 * l - 2.428_592_2 * m + 0.450_593_7 * s;
    let bb = 0.025_904_037 * l + 0.782_771_77 * m - 0.808_675_77 * s;
    (ll, a.hypot(bb), bb.atan2(a))
}

fn from_oklch(l0: f32, mut c: f32, h: f32) -> u32 {
    loop {
        let (a, b) = (c * h.cos(), c * h.sin());
        let l = (l0 + 0.396_337_78 * a + 0.215_803_76 * b).powi(3);
        let m = (l0 - 0.105_561_346 * a - 0.063_854_17 * b).powi(3);
        let s = (l0 - 0.089_484_18 * a - 1.291_485_5 * b).powi(3);
        let rgb = [
            4.076_741_7 * l - 3.307_711_6 * m + 0.230_969_94 * s,
            -1.268_438 * l + 2.609_757_4 * m - 0.341_319_38 * s,
            -0.004_196_086_3 * l - 0.703_418_6 * m + 1.707_614_7 * s,
        ];
        if rgb.iter().all(|v| (-1e-4..=1. + 1e-4).contains(v)) || c < 0.005 {
            let f = |v: f32| gam(v.clamp(0., 1.)).round().clamp(0., 255.) as u32;
            return (f(rgb[0]) << 16) | (f(rgb[1]) << 8) | f(rgb[2]);
        }
        c -= 0.005;
    }
}

/// Light themes: keeps the hue, holds the chroma up, and lowers OKLCH
/// lightness from a bright start until the colour reaches `target` on every
/// ground `grounds` gives for it. Starting bright keeps amber amber where a
/// theme's own darkened colour would turn brown.
fn light_state(c: u32, grounds: &dyn Fn(u32) -> Vec<u32>, target: f32) -> u32 {
    let (l, ch, h) = to_oklch(c);
    let ch = ch.max(0.13);
    let mut l = l.max(0.8);
    let mut out = from_oklch(l, ch, h);
    while l > 0.2 && grounds(out).iter().map(|g| contrast(out, *g)).fold(f32::MAX, f32::min) < target {
        l -= 0.005;
        out = from_oklch(l, ch, h);
    }
    out
}

/// `c` at alpha `a` on ground `g`, as the GPU blends it.
fn over(c: u32, g: u32, a: f32) -> u32 {
    mix32(g, c, a)
}

/// The colour `c` with alpha `a` (0 to 1).
pub fn with_alpha(c: u32, a: f32) -> Rgba {
    rgba((c << 8) | (a * 255.).round().clamp(0., 255.) as u32)
}

impl Theme {
    /// Derives every chrome colour from an exported tuios theme.
    pub fn from_export(e: &ThemeExport) -> Theme {
        let c = |s: &str, d: u32| parse_hex(s).unwrap_or(d);
        let ui = |k: &str, d: u32| e.ui.get(k).and_then(|v| parse_hex(v)).unwrap_or(d);
        let ag = |k: &str, d: u32| e.agent.get(k).and_then(|v| parse_hex(v)).unwrap_or(d);
        let mut ansi = XTERM;
        for (i, v) in e.terminal.ansi.iter().take(16).enumerate() {
            if let Some(x) = parse_hex(v) {
                ansi[i] = x;
            }
        }
        let fg = c(&e.terminal.fg, 0xe5e5e5);
        let bg = c(&e.terminal.bg, 0x000000);
        Theme::derive(
            &e.name,
            e.light,
            fg,
            bg,
            c(&e.terminal.cursor, fg),
            ansi,
            ui("Accent", 0x6b50ff),
            ag("needs_input", 0xe0af68),
            ag("done", 0x9ece6a),
            ag("errored", 0xf7768e),
        )
    }

    /// docs/design/FINAL.md section 3.2.
    #[allow(clippy::too_many_arguments)]
    pub fn derive(name: &str, light: bool, fg: u32, bg: u32, cursor: u32, ansi: [u32; 16], accent: u32, need: u32, done: u32, err: u32) -> Theme {
        let stage = bg;
        let (base, hover, selected, field, raised, raised_sel);
        let (border_c, border_a, hair_a, scrim_a, dim);
        let mut ink;
        if light {
            // The stage is the brightest surface: the shell is a step darker,
            // and the field and the palette are lighter still.
            base = mix32(bg, 0x000000, 0.04);
            hover = mix32(base, 0x000000, 0.03);
            selected = mix32(base, 0x000000, 0.07);
            field = mix32(base, 0xffffff, 0.5);
            raised = mix32(bg, 0xffffff, 0.85);
            raised_sel = mix32(raised, 0x000000, 0.06);
            (border_c, border_a, hair_a, scrim_a, dim) = (0x000000, 0.16, 0.14, 0.18, 0.12);
            ink = desaturate(fg, 0.12);
        } else {
            base = mix32(bg, 0x000000, 0.30);
            hover = mix32(base, fg, 0.04);
            selected = mix32(base, fg, 0.09);
            field = mix32(base, fg, 0.045);
            raised = mix32(bg, fg, 0.045);
            raised_sel = mix32(raised, fg, 0.075);
            (border_c, border_a, hair_a, scrim_a, dim) = (fg, 0.13, 0.12, 0.45, 0.30);
            ink = desaturate(fg, 0.45);
        }
        let grounds = [base, stage, hover, selected, field, raised];
        let worst = grounds.iter().copied().min_by(|a, b| contrast(ink, *a).total_cmp(&contrast(ink, *b))).unwrap_or(stage);
        if light {
            // 10.5: on the darker shell, 2 % steps stall just under 11.
            while contrast(ink, worst) < 10.5 {
                let next = mix32(ink, 0x000000, 0.02);
                if next == ink {
                    break;
                }
                ink = next;
            }
        }
        let fill = |c: u32| mix32(base, c, 0.14);
        let state = |c: u32| if light { light_state(c, &|x| vec![fill(x), worst], 3.0) } else { c };
        let (accent, need, done, err) = (state(accent), state(need), state(done), state(err));
        let need_ink = if light { light_state(need, &|_| vec![fill(need)], 4.5) } else { need };
        Theme {
            name: name.to_string(),
            light,
            fg,
            bg,
            cursor,
            ansi,
            stage,
            base,
            field,
            hover,
            selected,
            raised,
            raised_sel,
            border: with_alpha(border_c, border_a),
            hairline: with_alpha(border_c, hair_a),
            scrim: with_alpha(0x000000, scrim_a),
            dim,
            text: ink,
            text2: toward_target(ink, worst, 4.6),
            text3: toward_target(ink, worst, 3.1),
            accent,
            need,
            need_fill: fill(need),
            need_ink,
            done,
            done_fill: fill(done),
            err,
            err_fill: fill(err),
            selection: over(accent, bg, if light { 0.22 } else { 0.28 }),
            on_state: if light { 0xffffff } else { bg },
        }
    }

    /// tuios's colours with no theme set.
    pub fn fallback() -> Theme {
        let e: ThemeExport = serde_json::from_str(DEFAULT_EXPORT).unwrap_or_default();
        Theme::from_export(&e)
    }

    pub fn palette(&self) -> [Rgb; 256] {
        let mut p = [Rgb::default(); 256];
        for (i, c) in self.ansi.iter().enumerate() {
            p[i] = Rgb::from_u32(*c);
        }
        let steps = [0u8, 95, 135, 175, 215, 255];
        for i in 0..216 {
            p[16 + i] = Rgb(steps[i / 36], steps[(i / 6) % 6], steps[i % 6]);
        }
        for i in 0..24 {
            let v = 8 + i as u8 * 10;
            p[232 + i] = Rgb(v, v, v);
        }
        p
    }

    /// An `Rgba` at a fraction of its own alpha.
    pub fn fade(c: Rgba, f: f32) -> Rgba {
        Rgba { a: c.a * f, ..c }
    }
}

pub fn hsla(c: Rgb) -> Hsla {
    rgb(c.to_u32()).into()
}

/// Mixes `a` toward `b` by `t` (0 keeps `a`).
pub fn mix(a: Rgb, b: Rgb, t: f32) -> Rgb {
    let m = |x: u8, y: u8| (x as f32 + (y as f32 - x as f32) * t).round().clamp(0., 255.) as u8;
    Rgb(m(a.0, b.0), m(a.1, b.1), m(a.2, b.2))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn dark() -> Theme {
        Theme::derive("tokyonight", false, 0xc0caf5, 0x1a1b26, 0xc0caf5, XTERM, 0x7aa2f7, 0xe0af68, 0x9ece6a, 0xf7768e)
    }

    fn light() -> Theme {
        Theme::derive("tokyonight_day", true, 0x3760bf, 0xe1e2e7, 0x3760bf, XTERM, 0x2e7de9, 0xe0af68, 0x9ece6a, 0xf7768e)
    }

    /// Within one step of each channel: the reference rounds through Python.
    fn near(a: u32, b: u32) -> bool {
        let (x, y) = (split(a), split(b));
        (x.0 - y.0).abs() <= 2. && (x.1 - y.1).abs() <= 2. && (x.2 - y.2).abs() <= 2.
    }

    #[test]
    fn fallback_is_tuios_without_a_theme() {
        let t = Theme::fallback();
        assert_eq!(t.fg, 0xe5e5e5);
        assert_eq!(t.bg, 0x000000);
        assert_eq!(t.ansi, XTERM);
        assert!(!t.light);
    }

    #[test]
    fn dark_tokens_match_the_spec() {
        let t = dark();
        for (got, want, name) in [
            (t.base, 0x12131b, "base"),
            (t.field, 0x1a1b25, "field"),
            (t.hover, 0x191a24, "hover"),
            (t.selected, 0x22232f, "selected"),
            (t.raised, 0x21232f, "raised"),
            (t.raised_sel, 0x2d303e, "raised_sel"),
            (t.text, 0xc6cbde, "text"),
            (t.text2, 0x888b9b, "text2"),
            (t.text3, 0x6c6f7e, "text3"),
            (t.need_fill, 0x2f2926, "need_fill"),
            (t.selection, 0x354161, "selection"),
        ] {
            assert!(near(got, want), "{name}: {got:06x}, spec {want:06x}");
        }
    }

    #[test]
    fn light_tokens_match_the_spec() {
        let t = light();
        for (got, want, name) in [
            (t.base, 0xd8d9de, "base"),
            (t.field, 0xececee, "field"),
            (t.selected, 0xc9cace, "selected"),
            (t.raised, 0xfafbfb, "raised"),
            (t.text, 0x191b21, "text"),
            (t.text2, 0x53545a, "text2"),
            (t.text3, 0x6d6e74, "text3"),
        ] {
            assert!(near(got, want), "{name}: {got:06x}, spec {want:06x}");
        }
        // Amber stays amber, and every state colour reads on every ground.
        let (_, c, _) = to_oklch(t.need);
        assert!(c >= 0.1, "need {:06x} lost its chroma", t.need);
        for g in [t.base, t.stage, t.hover, t.selected, t.field, t.raised] {
            for (c, n) in [(t.need, "need"), (t.done, "done"), (t.err, "err")] {
                assert!(contrast(c, g) >= 2.9, "{n} {c:06x} on {g:06x}");
            }
            assert!(contrast(t.text, g) >= 10.4);
            assert!(contrast(t.text2, g) >= 4.5);
        }
        assert!(contrast(t.need_ink, t.need_fill) >= 4.5);
        // The stage is the brightest surface the panes sit on.
        assert!(lum(t.stage) > lum(t.base) && lum(t.stage) > lum(t.selected));
    }

    #[test]
    fn export_fields_land_on_their_surfaces() {
        let mut e = ThemeExport::default();
        e.terminal.bg = "#282a36".into();
        e.terminal.ansi = vec!["#21222c".into(); 16];
        e.agent.insert("needs_input".into(), "#ffb86c".into());
        let t = Theme::from_export(&e);
        assert_eq!(t.bg, 0x282a36);
        assert_eq!(t.stage, 0x282a36);
        assert_eq!(t.ansi[15], 0x21222c);
        assert_eq!(t.need, 0xffb86c);
    }
}
