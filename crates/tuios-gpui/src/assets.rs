//! Icons and fonts compiled into the binary, so the app looks the same on
//! every machine. The icons are Lucide (ISC, assets/icons/LICENSE-lucide.txt)
//! and the app's own state icons. The UI font is Inter and the terminal font
//! is JetBrains Mono 2.304 (both SIL OFL 1.1, assets/fonts/*-LICENSE.txt).

use gpui::{AssetSource, SharedString};
use std::borrow::Cow;

macro_rules! icons {
    ($($name:literal),* $(,)?) => {
        const ICONS: &[(&str, &[u8])] = &[$(($name, include_bytes!(concat!("../assets/icons/", $name)))),*];
    };
}

icons!(
    "state-needs.svg",
    "state-ring.svg",
    "state-arc.svg",
    "state-disc.svg",
    "state-dot.svg",
    "mark-x.svg",
    "mark-check.svg",
    "search.svg",
    "plus.svg",
    "columns-2.svg",
    "rows-2.svg",
    "maximize-2.svg",
    "panel-left.svg",
    "chevron-right.svg",
    "chevron-down.svg",
    "palette.svg",
    "layers.svg",
    "x.svg",
    "win-minus.svg",
    "win-square.svg",
    "win-close.svg",
);

pub const FONTS: [&[u8]; 7] = [
    include_bytes!("../assets/fonts/Inter-Regular.ttf"),
    include_bytes!("../assets/fonts/Inter-Medium.ttf"),
    include_bytes!("../assets/fonts/Inter-SemiBold.ttf"),
    include_bytes!("../assets/fonts/JetBrainsMono-Regular.ttf"),
    include_bytes!("../assets/fonts/JetBrainsMono-Bold.ttf"),
    include_bytes!("../assets/fonts/JetBrainsMono-Italic.ttf"),
    include_bytes!("../assets/fonts/JetBrainsMono-BoldItalic.ttf"),
];

/// The family name of the bundled UI font.
pub const UI_FONT: &str = "Inter";
/// The family name of the bundled terminal font.
pub const TERMINAL_FONT: &str = "JetBrains Mono";

pub struct Assets;

impl AssetSource for Assets {
    fn load(&self, path: &str) -> anyhow::Result<Option<Cow<'static, [u8]>>> {
        let name = path.strip_prefix("icons/").unwrap_or(path);
        Ok(ICONS.iter().find(|(n, _)| *n == name).map(|(_, b)| Cow::Borrowed(*b)))
    }

    fn list(&self, _path: &str) -> anyhow::Result<Vec<SharedString>> {
        Ok(ICONS.iter().map(|(n, _)| SharedString::from(format!("icons/{n}"))).collect())
    }
}
