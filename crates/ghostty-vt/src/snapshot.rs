//! Plain-data copy of a terminal screen, ready for painting.

use crate::ffi::GhosttyColorRgb;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Default)]
pub struct Rgb(pub u8, pub u8, pub u8);

impl Rgb {
    pub const fn from_u32(v: u32) -> Self {
        Rgb((v >> 16) as u8, (v >> 8) as u8, v as u8)
    }
    pub const fn to_u32(self) -> u32 {
        ((self.0 as u32) << 16) | ((self.1 as u32) << 8) | self.2 as u32
    }
    pub(crate) fn from_ffi(c: GhosttyColorRgb) -> Self {
        Rgb(c.r, c.g, c.b)
    }
    pub(crate) fn into_ffi(self) -> GhosttyColorRgb {
        GhosttyColorRgb { r: self.0, g: self.1, b: self.2 }
    }
}

/// Cell attribute bits. A tiny hand-rolled bitflags type.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Default)]
pub struct CellFlags(pub u16);

impl CellFlags {
    pub const BOLD: Self = Self(1);
    pub const ITALIC: Self = Self(1 << 1);
    pub const FAINT: Self = Self(1 << 2);
    pub const STRIKE: Self = Self(1 << 3);
    pub const OVERLINE: Self = Self(1 << 4);
    pub const INVISIBLE: Self = Self(1 << 5);
    pub const BLINK: Self = Self(1 << 6);
    /// The first cell of a double-width character.
    pub const WIDE: Self = Self(1 << 7);
    /// The second half of a wide character (or a wrap spacer). No text.
    pub const SPACER: Self = Self(1 << 8);
    pub const SELECTED: Self = Self(1 << 9);

    pub const fn empty() -> Self {
        Self(0)
    }
    pub const fn contains(self, o: Self) -> bool {
        self.0 & o.0 == o.0
    }
    /// The font style index: bit 0 bold, bit 1 italic.
    pub const fn font_style(self) -> usize {
        (self.0 & 0b11) as usize
    }
}

impl std::ops::BitOr for CellFlags {
    type Output = Self;
    fn bitor(self, o: Self) -> Self {
        Self(self.0 | o.0)
    }
}

impl std::ops::BitOrAssign for CellFlags {
    fn bitor_assign(&mut self, o: Self) {
        self.0 |= o.0
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Default)]
pub enum UnderlineKind {
    #[default]
    None,
    Single,
    Double,
    Curly,
    Dotted,
    Dashed,
}

/// One cell. Colours are resolved (palette, inverse and bold-bright applied).
/// `bg == None` means the terminal's default background.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub struct Cell {
    /// Byte range of this cell's grapheme in [`Row::text`].
    pub text_start: u32,
    pub text_len: u8,
    pub fg: Rgb,
    pub bg: Option<Rgb>,
    pub ul_color: Option<Rgb>,
    pub flags: CellFlags,
    pub underline: UnderlineKind,
}

impl Cell {
    pub fn blank(fg: Rgb) -> Self {
        Cell {
            text_start: 0,
            text_len: 0,
            fg,
            bg: None,
            ul_color: None,
            flags: CellFlags::empty(),
            underline: UnderlineKind::None,
        }
    }
}

#[derive(Debug, Clone, Default)]
pub struct Row {
    pub cells: Vec<Cell>,
    /// Every cell's grapheme, concatenated.
    pub text: String,
    /// Bumped each time the row is rebuilt from the emulator.
    pub generation: u64,
    /// A hash of the row's cells and text. Two rows with the same hash draw
    /// the same, so a painter can reuse what it built for one on the other,
    /// for example when a row moves up the screen as it scrolls.
    pub hash: u64,
    pub wrapped: bool,
}

/// FxHash's mixing step: fast and good enough for a cache key.
#[derive(Default)]
pub(crate) struct FxHasher(u64);

impl std::hash::Hasher for FxHasher {
    fn finish(&self) -> u64 {
        self.0
    }
    fn write(&mut self, bytes: &[u8]) {
        for chunk in bytes.chunks(8) {
            let mut b = [0u8; 8];
            b[..chunk.len()].copy_from_slice(chunk);
            self.write_u64(u64::from_le_bytes(b));
        }
    }
    fn write_u64(&mut self, v: u64) {
        self.0 = (self.0.rotate_left(5) ^ v).wrapping_mul(0x51_7c_c1_b7_27_22_0a_95);
    }
    fn write_u8(&mut self, v: u8) {
        self.write_u64(v as u64)
    }
    fn write_u16(&mut self, v: u16) {
        self.write_u64(v as u64)
    }
    fn write_u32(&mut self, v: u32) {
        self.write_u64(v as u64)
    }
    fn write_usize(&mut self, v: usize) {
        self.write_u64(v as u64)
    }
}

impl Row {
    /// Hashes the cells and text into [`Row::hash`].
    pub(crate) fn rehash(&mut self) {
        use std::hash::{Hash, Hasher};
        let mut h = FxHasher::default();
        self.cells.hash(&mut h);
        h.write(self.text.as_bytes());
        self.hash = h.finish();
    }

    pub fn cell_text(&self, c: &Cell) -> &str {
        let s = c.text_start as usize;
        &self.text[s..s + c.text_len as usize]
    }

    /// The row as plain text, one char per cell (spaces for empty cells).
    pub fn plain(&self) -> String {
        let mut out = String::with_capacity(self.cells.len());
        for c in &self.cells {
            if c.flags.contains(CellFlags::SPACER) {
                continue;
            }
            let t = self.cell_text(c);
            if t.is_empty() {
                out.push(' ');
            } else {
                out.push_str(t);
            }
        }
        out
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum CursorShape {
    #[default]
    Block,
    Bar,
    Underline,
    Hollow,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub struct CursorState {
    pub visible: bool,
    pub x: u16,
    pub y: u16,
    pub wide: bool,
    pub blinking: bool,
    pub shape: CursorShape,
    pub color: Option<Rgb>,
}

#[derive(Debug, Clone)]
pub struct Screen {
    pub cols: u16,
    pub rows: Vec<Row>,
    pub fg: Rgb,
    pub bg: Rgb,
    pub cursor: CursorState,
    pub(crate) full_dirty: bool,
    generation: u64,
}

impl Screen {
    pub fn blank(cols: u16, rows: u16) -> Self {
        Screen {
            cols,
            rows: (0..rows).map(|_| Row::default()).collect(),
            fg: Rgb(0xcc, 0xcc, 0xcc),
            bg: Rgb(0, 0, 0),
            cursor: CursorState::default(),
            full_dirty: true,
            generation: 0,
        }
    }

    /// A new row generation. The counter is process-wide, so a generation
    /// never repeats even when a screen is rebuilt (resize, reset): a painter
    /// cache keyed by generation cannot mistake a new row for an old one.
    pub(crate) fn next_generation(&mut self) -> u64 {
        static NEXT: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(1);
        self.generation = NEXT.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
        self.generation
    }

    /// A monotonically increasing number that changes whenever any row does.
    pub fn generation(&self) -> u64 {
        self.generation
    }

    /// The visible screen as text, for tests and diagnostics.
    pub fn plain_text(&self) -> String {
        self.rows.iter().map(|r| r.plain().trim_end().to_string()).collect::<Vec<_>>().join("\n")
    }
}
