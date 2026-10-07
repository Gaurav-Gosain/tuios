//! Settings: the GUI's own file, and the theme from the user's tuios config.
//!
//! The GUI reads `$XDG_CONFIG_HOME/tuios-gpui/config.toml` (all keys
//! optional):
//!
//! ```toml
//! font_family = "JetBrains Mono"   # bundled
//! font_size = 15                   # pixels
//! line_height = 1.333
//! ui_font_family = "Inter"         # bundled
//! ligatures = false
//! text_antialias = "grayscale"     # or "subpixel"
//! text_contrast = 2.0              # 0 to 4, extra stem weight for grayscale
//! scrollback = 3000                # lines of history per pane
//! reduce_motion = false
//! gpu = "auto"                     # or "integrated": the integrated GPU's
//!                                  # Vulkan driver only, which saves memory
//! theme = "tokyonight"             # overrides tuios's own [appearance] theme
//! ```
//!
//! The theme otherwise comes from `[appearance] theme` in tuios's
//! `config.toml`, the same file and key the terminal client reads. Neither
//! file is ever written.

use serde::Deserialize;
use std::path::PathBuf;

/// Terminal font families tried in order when the configured one is missing.
/// The first is bundled, so it is always there.
pub const TERMINAL_FONTS: [&str; 3] = [crate::assets::TERMINAL_FONT, "JetBrainsMono Nerd Font Mono", "DejaVu Sans Mono"];
/// Chrome font families tried in order.
pub const UI_FONTS: [&str; 4] = [crate::assets::UI_FONT, "Adwaita Sans", "Cantarell", "DejaVu Sans"];

#[derive(Debug, Clone, Default, Deserialize, PartialEq)]
pub struct GuiFile {
    pub font_family: Option<String>,
    pub font_size: Option<f32>,
    pub line_height: Option<f32>,
    pub ui_font_family: Option<String>,
    pub ligatures: Option<bool>,
    pub theme: Option<String>,
    pub text_antialias: Option<String>,
    pub text_contrast: Option<f32>,
    pub scrollback: Option<usize>,
    pub reduce_motion: Option<bool>,
    pub gpu: Option<String>,
}

/// The Vulkan driver manifests of integrated GPUs on this machine, for
/// `gpu = "integrated"`: Intel's and AMD's open drivers. On a machine with
/// a discrete GPU, loading only these keeps the discrete driver (about
/// 100 MB of mappings) out of the process.
pub fn integrated_vulkan_drivers() -> Vec<std::path::PathBuf> {
    let mut out = Vec::new();
    for dir in ["/usr/share/vulkan/icd.d", "/etc/vulkan/icd.d"] {
        let Ok(entries) = std::fs::read_dir(dir) else { continue };
        for e in entries.flatten() {
            let name = e.file_name().to_string_lossy().to_lowercase();
            // Intel's current driver, not the old hasvk one for pre-Gen8.
            if (name.starts_with("intel_icd") || name.starts_with("radeon_icd")) && name.ends_with(".json") {
                out.push(e.path());
            }
        }
    }
    out.sort();
    out
}

#[derive(Debug, Default, Deserialize)]
struct TuiosFile {
    appearance: Option<Appearance>,
}

#[derive(Debug, Default, Deserialize)]
struct Appearance {
    theme: Option<String>,
}

fn config_home() -> Option<PathBuf> {
    std::env::var_os("XDG_CONFIG_HOME")
        .map(PathBuf::from)
        .filter(|p| p.is_absolute())
        .or_else(|| std::env::var_os("HOME").map(|h| PathBuf::from(h).join(".config")))
}

pub fn load_gui() -> GuiFile {
    let Some(path) = config_home().map(|h| h.join("tuios-gpui/config.toml")) else { return GuiFile::default() };
    parse_gui(&std::fs::read_to_string(path).unwrap_or_default())
}

pub fn parse_gui(text: &str) -> GuiFile {
    toml::from_str(text).unwrap_or_else(|e| {
        eprintln!("tuios-gpui: ignoring config.toml: {e}");
        GuiFile::default()
    })
}

/// The theme the user's tuios config names, if any.
pub fn tuios_theme() -> Option<String> {
    let path = config_home()?.join("tuios/config.toml");
    parse_tuios_theme(&std::fs::read_to_string(path).ok()?)
}

pub fn parse_tuios_theme(text: &str) -> Option<String> {
    let f: TuiosFile = toml::from_str(text).ok()?;
    f.appearance?.theme.filter(|t| !t.is_empty())
}

/// The first family of `wanted` that is installed, else the first installed
/// family whose name says it is monospace, else the first of `wanted`.
pub fn pick_font(wanted: &[&str], installed: &[String], want_mono: bool) -> String {
    for w in wanted {
        if installed.iter().any(|i| i == w) {
            return w.to_string();
        }
    }
    if want_mono {
        if let Some(m) = installed.iter().find(|i| i.contains("Mono")) {
            return m.clone();
        }
    }
    wanted.first().map(|s| s.to_string()).unwrap_or_default()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn reads_the_tuios_theme_and_ignores_the_rest() {
        let t = "[appearance]\ntheme = 'dracula'\nborder_style = 'rounded'\n[keybindings]\nleader = 'ctrl+b'\n";
        assert_eq!(parse_tuios_theme(t).as_deref(), Some("dracula"));
        assert_eq!(parse_tuios_theme("[appearance]\ntheme = ''\n"), None);
        assert_eq!(parse_tuios_theme("not toml ["), None);
    }

    #[test]
    fn gui_file_keys_are_optional() {
        let g = parse_gui("font_size = 15\nligatures = false\nscrollback = 500\ntext_antialias = 'subpixel'\n");
        assert_eq!(g.font_size, Some(15.));
        assert_eq!(g.ligatures, Some(false));
        assert_eq!(g.scrollback, Some(500));
        assert_eq!(g.text_antialias.as_deref(), Some("subpixel"));
        assert_eq!(g.font_family, None);
    }

    #[test]
    fn font_fallback_order() {
        let installed: Vec<String> = ["Adwaita Mono", "JetBrains Mono"].iter().map(|s| s.to_string()).collect();
        assert_eq!(pick_font(&TERMINAL_FONTS, &installed, true), "JetBrains Mono");
        let installed: Vec<String> = ["Adwaita Mono", "JetBrainsMono Nerd Font Mono"].iter().map(|s| s.to_string()).collect();
        assert_eq!(pick_font(&TERMINAL_FONTS, &installed, true), "JetBrainsMono Nerd Font Mono");
        let none: Vec<String> = vec!["Foo Mono".into()];
        assert_eq!(pick_font(&TERMINAL_FONTS, &none, true), "Foo Mono");
        assert_eq!(pick_font(&["Wanted"], &[], true), "Wanted");
    }
}
