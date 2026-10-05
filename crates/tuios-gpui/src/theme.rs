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
    // The rail: sidebar, workspace strip and status bar (GroundUI).
    pub rail: u32,
    pub rail_rule: u32,
    pub rail_fg: u32,
    pub rail_dim: u32,
    pub rail_mute: u32,
    pub rail_row: u32,
    pub rail_hover: u32,
    // Dialogs: the command palette (UI).
    pub dlg_surface: u32,
    pub dlg_edge: u32,
    pub dlg_card: u32,
    pub dlg_fg: u32,
    pub dlg_dim: u32,
    pub dlg_mute: u32,
    pub dlg_row: u32,
    pub accent: u32,
    pub accent_bright: u32,
    pub accent_tint: u32,
    // Pane frames.
    pub border_focused: u32,
    pub border_unfocused: u32,
    // Agent states.
    pub working: u32,
    pub needs_input: u32,
    pub idle: u32,
    pub done: u32,
    pub errored: u32,
}

impl Theme {
    /// Maps an exported tuios theme onto the GUI's surfaces.
    pub fn from_export(e: &ThemeExport) -> Theme {
        let c = |s: &str, d: u32| parse_hex(s).unwrap_or(d);
        let ui = |k: &str, d: u32| e.ui.get(k).and_then(|v| parse_hex(v)).unwrap_or(d);
        let gr = |k: &str, d: u32| e.ground.get(k).and_then(|v| parse_hex(v)).unwrap_or_else(|| ui(k, d));
        let ag = |k: &str, d: u32| e.agent.get(k).and_then(|v| parse_hex(v)).unwrap_or(d);
        let mut ansi = XTERM;
        for (i, v) in e.terminal.ansi.iter().take(16).enumerate() {
            if let Some(x) = parse_hex(v) {
                ansi[i] = x;
            }
        }
        let fg = c(&e.terminal.fg, 0xe5e5e5);
        let bg = c(&e.terminal.bg, 0x000000);
        let rail = c(&e.rail_ground, gr("Canvas", 0x201f26));
        let accent = ui("Accent", 0x6b50ff);
        Theme {
            name: e.name.clone(),
            light: e.light,
            fg,
            bg,
            cursor: c(&e.terminal.cursor, fg),
            selection: ui("AccentTint", 0x464479),
            ansi,
            rail,
            rail_rule: c(&e.rail_rule, gr("Edge", 0x484851)),
            rail_fg: gr("Fg", 0xfffaf1),
            rail_dim: gr("FgDim", 0xbfbcc8),
            rail_mute: gr("FgMute", 0x858392),
            rail_row: gr("RowSel", 0x2d2c36),
            rail_hover: gr("Hover", 0x44434c),
            dlg_surface: ui("Surface", 0x3a3943),
            dlg_edge: ui("Edge", 0x646269),
            dlg_card: ui("Card", 0x4d4c57),
            dlg_fg: ui("Fg", 0xfffaf1),
            dlg_dim: ui("FgDim", 0xbfbcc8),
            dlg_mute: ui("FgMute", 0x858392),
            dlg_row: ui("RowSel", 0x2d2c36),
            accent,
            accent_bright: ui("AccentBright", accent),
            accent_tint: ui("AccentTint", 0x464479),
            border_focused: c(&e.border_focused_terminal, accent),
            border_unfocused: c(&e.border_unfocused, 0x585858),
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

    pub fn agent_color(&self, state: &str) -> u32 {
        match state {
            "working" => self.working,
            "needs_input" => self.needs_input,
            "done" => self.done,
            "errored" => self.errored,
            _ => self.idle,
        }
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
        assert_eq!(t.rail, 0x201f26, "the rail sits on charmtone Pepper");
        assert!(!t.light);
    }

    #[test]
    fn export_fields_land_on_their_surfaces() {
        let mut e = ThemeExport::default();
        e.terminal.bg = "#282a36".into();
        e.terminal.ansi = vec!["#21222c".into(); 16];
        e.ground.insert("Fg".into(), "#010203".into());
        e.ui.insert("Surface".into(), "#0a0b0c".into());
        e.agent.insert("needs_input".into(), "#ffb86c".into());
        let t = Theme::from_export(&e);
        assert_eq!(t.bg, 0x282a36);
        assert_eq!(t.ansi[15], 0x21222c);
        assert_eq!(t.rail_fg, 0x010203);
        assert_eq!(t.dlg_surface, 0x0a0b0c);
        assert_eq!(t.agent_color("needs_input"), 0xffb86c);
    }
}
