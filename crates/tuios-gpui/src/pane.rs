//! One pane on the GUI side: its emulator, its painter caches and the local
//! view state (scroll position, selection) the daemon does not know about.

use crate::painter::PanePainter;
use crate::theme::Theme;
use ghostty_vt::{Rgb, Terminal};

pub const SCROLLBACK: usize = 10_000;

pub struct Pane {
    pub term: Terminal,
    pub painter: PanePainter,
    /// Pixels the content is pulled down past the viewport's top row, for
    /// smooth scrolling. Always in [0, cell height).
    pub scroll_px: f32,
    /// Pixels still to scroll, for animated wheel steps; positive goes back
    /// into history.
    pub scroll_pending: f32,
    /// Bytes received, for the status bar.
    pub bytes_in: u64,
}

impl Pane {
    pub fn new(cols: u16, rows: u16, theme: &Theme) -> Self {
        let mut term = Terminal::new(cols.max(1), rows.max(1), SCROLLBACK).expect("ghostty terminal");
        apply_theme(&mut term, theme);
        Pane { term, painter: PanePainter::default(), scroll_px: 0., scroll_pending: 0., bytes_in: 0 }
    }

    /// Starts over from a snapshot: a blank emulator of the given size, then
    /// the bytes that rebuild the screen. A zero size keeps the current one.
    pub fn restore(&mut self, cols: u16, rows: u16, bytes: &[u8], theme: &Theme, cell: (u32, u32)) {
        let (c, r) = if cols == 0 || rows == 0 { (self.term.cols(), self.term.rows()) } else { (cols, rows) };
        let mut term = Terminal::new(c, r, SCROLLBACK).expect("ghostty terminal");
        apply_theme(&mut term, theme);
        let _ = cell;
        term.write(bytes);
        self.term = term;
        self.painter.clear();
        self.scroll_px = 0.;
        self.scroll_pending = 0.;
    }

    pub fn write(&mut self, bytes: &[u8]) {
        self.bytes_in += bytes.len() as u64;
        self.term.write(bytes);
    }

    /// Scrolls by `px` pixels (positive goes back into history). Whole rows
    /// move ghostty's viewport; the rest stays as a pixel offset.
    pub fn scroll_pixels(&mut self, px: f32, cell_h: f32) {
        if cell_h <= 0. {
            return;
        }
        if self.term.alt_screen() {
            self.scroll_px = 0.;
            return;
        }
        let total = self.scroll_px + px;
        let rows = (total / cell_h).floor();
        let mut rest = total - rows * cell_h;
        if rows != 0. {
            self.term.scroll_delta(-(rows as isize));
        }
        let (_, offset, _) = self.term.scrollbar();
        if offset == 0 {
            // At the top of the history nothing is above to show.
            rest = 0.;
            if px > 0. {
                self.scroll_pending = 0.;
            }
        }
        if self.term.at_bottom() && total < 0. {
            rest = 0.;
            self.scroll_pending = 0.;
        }
        self.scroll_px = rest;
    }

    /// Returns to the live screen, for typing.
    pub fn snap_to_bottom(&mut self) {
        if !self.term.at_bottom() || self.scroll_px != 0. {
            self.term.scroll_to_bottom();
        }
        self.scroll_px = 0.;
        self.scroll_pending = 0.;
    }

    pub fn scrolled(&self) -> bool {
        !self.term.at_bottom() || self.scroll_px > 0.
    }
}

pub fn apply_theme(term: &mut Terminal, theme: &Theme) {
    term.set_theme(Rgb::from_u32(theme.fg), Rgb::from_u32(theme.bg), Rgb::from_u32(theme.cursor), &theme.palette());
}
