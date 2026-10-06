//! Colours, taken from tuios.
//!
//! tuios works a theme out in Go (internal/theme): the terminal colours from
//! the theme file, and the chrome from two derived palettes, one for dialogs
//! (theme.UI) and one for the rail and dock (theme.GroundUI), mixed in OKLab
//! and checked for contrast. The bridge runs that same code and sends the
//! result (internal/guibridge/theme.go), so the GUI does not reimplement it
//! and a theme name looks the same in both clients. This module maps the
//! exported palettes onto the GUI's surfaces.

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
    pub selection: u32,
    pub ansi: [u32; 16],
    // Chrome, derived from the terminal's own background and foreground so a
    // theme change recolours everything at once (docs/DESIGN-RESEARCH.md,
    // "Palette rules").
    /// The sidebar ground.
    pub sidebar: u32,
    /// A row under the pointer, a keycap, a quiet field.
    pub hover: u32,
    /// The focused pane's row, the palette's chosen row, the active tab.
    pub selected: u32,
    /// The palette panel.
    pub raised: u32,
    /// Hairlines between regions.
    pub hairline: u32,
    /// Primary, secondary and tertiary chrome text.
    pub text: u32,
    pub text2: u32,
    pub text3: u32,
    pub accent: u32,
    // Agent states, as tuios works them out for contrast.
    pub working: u32,
    pub needs_input: u32,
    pub idle: u32,
    pub done: u32,
    pub errored: u32,
}

/// Pulls a colour toward its own grey by `keep` (1 keeps it), so chrome text
/// in a strongly tinted theme reads as text, not as a colour.
fn desaturate(c: u32, keep: f32) -> u32 {
    let (r, g, b) = (((c >> 16) & 0xff) as f32, ((c >> 8) & 0xff) as f32, (c & 0xff) as f32);
    let y = 0.2126 * r + 0.7152 * g + 0.0722 * b;
    let f = |v: f32| (y + (v - y) * keep).round().clamp(0., 255.) as u32;
    (f(r) << 16) | (f(g) << 8) | f(b)
}

/// Mixes two 0xRRGGBB colours; `t` 0 keeps `a`.
pub fn mix32(a: u32, b: u32, t: f32) -> u32 {
    mix(Rgb::from_u32(a), Rgb::from_u32(b), t).to_u32()
}

impl Theme {
    /// Maps an exported tuios theme onto the GUI's surfaces.
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
        let accent = ui("Accent", 0x6b50ff);
        let light = e.light;
        let ink = desaturate(fg, 0.45);
        // Light grounds step toward black, dark ones toward the foreground.
        let toward = if light { 0x000000 } else { fg };
        let step = |t: f32| mix32(bg, toward, t);
        Theme {
            name: e.name.clone(),
            light,
            fg,
            bg,
            cursor: c(&e.terminal.cursor, fg),
            selection: ui("AccentTint", 0x464479),
            ansi,
            sidebar: step(if light { 0.03 } else { 0.035 }),
            hover: step(if light { 0.05 } else { 0.06 }),
            selected: step(if light { 0.085 } else { 0.095 }),
            raised: if light { bg } else { step(0.06) },
            hairline: mix32(bg, fg, if light { 0.13 } else { 0.09 }),
            text: ink,
            text2: mix32(ink, bg, if light { 0.32 } else { 0.36 }),
            text3: mix32(ink, bg, if light { 0.5 } else { 0.56 }),
            accent,
            working: ag("working", accent),
            needs_input: ag("needs_input", 0xe0af68),
            idle: ag("idle", 0x858392),
            done: ag("done", 0x9ece69),
            errored: ag("errored", 0xf7768e),
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

    /// `c` with alpha `a` (0-255).
    pub fn alpha(c: u32, a: u8) -> Rgba {
        rgba((c << 8) | a as u32)
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

    #[test]
    fn fallback_is_tuios_without_a_theme() {
        let t = Theme::fallback();
        assert_eq!(t.fg, 0xe5e5e5);
        assert_eq!(t.bg, 0x000000);
        assert_eq!(t.ansi, XTERM);
        assert!(!t.light);
    }

    #[test]
    fn export_fields_land_on_their_surfaces() {
        let mut e = ThemeExport::default();
        e.terminal.bg = "#282a36".into();
        e.terminal.ansi = vec!["#21222c".into(); 16];
        e.agent.insert("needs_input".into(), "#ffb86c".into());
        let t = Theme::from_export(&e);
        assert_eq!(t.bg, 0x282a36);
        assert_eq!(t.ansi[15], 0x21222c);
        assert_eq!(t.needs_input, 0xffb86c);
    }
}
