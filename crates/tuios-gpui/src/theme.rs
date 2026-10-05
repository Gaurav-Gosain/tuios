//! Colours for the terminal and the chrome around it.

use ghostty_vt::Rgb;
use gpui::{Hsla, Rgba, rgb, rgba};

/// One theme: the terminal's default colours and 16-colour palette, and the
/// chrome's surfaces. The 256-colour palette is derived from the 16.
#[derive(Clone, Debug)]
pub struct Theme {
    pub name: &'static str,
    pub fg: u32,
    pub bg: u32,
    pub cursor: u32,
    pub selection: u32,
    pub ansi: [u32; 16],
    // chrome
    pub sidebar: u32,
    pub surface: u32,
    pub border: u32,
    pub muted: u32,
    pub text: u32,
    pub accent: u32,
    pub working: u32,
    pub needs_input: u32,
    pub done: u32,
    pub errored: u32,
}

pub const NIGHT: Theme = Theme {
    name: "Night",
    fg: 0xc8d0e0,
    bg: 0x14161c,
    cursor: 0xe6c07b,
    selection: 0x3a4a6b,
    ansi: [
        0x1c1f26, 0xe06c75, 0x98c379, 0xe5c07b, 0x61afef, 0xc678dd, 0x56b6c2, 0xabb2bf, //
        0x5c6370, 0xef7d87, 0xa9d48a, 0xf0d08c, 0x7cbff7, 0xd28fe6, 0x6fcad5, 0xe6e9ef,
    ],
    sidebar: 0x101217,
    surface: 0x1b1e26,
    border: 0x262a34,
    muted: 0x6b7385,
    text: 0xd5dae5,
    accent: 0x7aa2f7,
    working: 0x7aa2f7,
    needs_input: 0xe5c07b,
    done: 0x98c379,
    errored: 0xe06c75,
};

pub const DAY: Theme = Theme {
    name: "Day",
    fg: 0x2b2f38,
    bg: 0xfbfbfa,
    cursor: 0x3a6ed8,
    selection: 0xc9d8f5,
    ansi: [
        0x2b2f38, 0xc4393f, 0x3f8f3a, 0x9a6a00, 0x2d64c8, 0x8a3fb8, 0x1d8592, 0xb9bcc4, //
        0x6b7080, 0xd9484e, 0x4ea648, 0xb37c00, 0x3b76e0, 0xa04fd0, 0x26a0ae, 0xffffff,
    ],
    sidebar: 0xf0f0ee,
    surface: 0xe8e8e5,
    border: 0xdcdcd8,
    muted: 0x868a94,
    text: 0x2b2f38,
    accent: 0x3a6ed8,
    working: 0x3a6ed8,
    needs_input: 0xb37c00,
    done: 0x3f8f3a,
    errored: 0xc4393f,
};

impl Theme {
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

    pub fn rgb(c: u32) -> Rgba {
        rgb(c)
    }

    /// `c` with alpha `a` (0-255).
    pub fn alpha(c: u32, a: u8) -> Rgba {
        rgba((c << 8) | a as u32)
    }

    pub fn agent_color(&self, state: &str) -> u32 {
        match state {
            "working" => self.working,
            "needs_input" => self.needs_input,
            "done" | "idle" => self.done,
            "errored" => self.errored,
            _ => self.muted,
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
