//! Icons and fonts compiled into the binary, so the app looks the same on
//! every machine. The icons are Lucide (ISC, assets/icons/LICENSE-lucide.txt)
//! and the app's own state glyphs; the UI font is Inter (OFL,
//! assets/fonts/Inter-LICENSE.txt).

use gpui::{AssetSource, SharedString};
use std::borrow::Cow;

macro_rules! icons {
    ($($name:literal),* $(,)?) => {
        const ICONS: &[(&str, &[u8])] = &[$(($name, include_bytes!(concat!("../assets/icons/", $name)))),*];
    };
}

icons!(
    "state-needs.svg",
    "state-error.svg",
    "state-working.svg",
    "state-ring.svg",
    "state-done.svg",
    "state-idle.svg",
    "state-terminal.svg",
    "search.svg",
    "plus.svg",
    "columns-2.svg",
    "rows-2.svg",
    "maximize-2.svg",
    "panel-left.svg",
    "chevron-right.svg",
    "palette.svg",
    "layers.svg",
    "type.svg",
    "x.svg",
);

pub const FONTS: [&[u8]; 3] = [
    include_bytes!("../assets/fonts/Inter-Regular.ttf"),
    include_bytes!("../assets/fonts/Inter-Medium.ttf"),
    include_bytes!("../assets/fonts/Inter-SemiBold.ttf"),
];

/// The family name of the bundled UI font.
pub const UI_FONT: &str = "Inter";

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
