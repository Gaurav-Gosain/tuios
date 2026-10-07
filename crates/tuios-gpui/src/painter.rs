//! Paints terminal panes with GPUI's primitives.
//!
//! Each row is planned ([`crate::rowplan`]) and shaped once: the row's words
//! go through the platform shaper (so font fallback works), and every glyph
//! is pinned to its cell's column so the grid never drifts. The result, a
//! list of glyphs, background spans, decorations and cell-filling shapes, is
//! cached per row. A row whose generation is unchanged replays its cache. A
//! row that changed but draws the same as one built before (a line that
//! scrolled up) takes that build from a pool keyed by the row's content hash.
//!
//! Every position is a whole device pixel: the cell is a whole number of
//! device pixels wide and tall at every scale, glyphs sit at their cell's
//! column with a rounded offset, and the baseline is one rounded value for
//! the whole grid (docs/design/FINAL.md section 9).
//!
//! Glyphs are painted with `Window::paint_glyph` directly inside
//! `Window::paint_layer`, as herdr-gpui does (Apache-2.0), which skips the
//! per-call layer that `ShapedLine::paint` pushes.

use crate::boxdraw;
use crate::rowplan::{DecoKind, PlanColors, plan_row};
use crate::theme::{Theme, hsla};
use ghostty_vt::{CursorShape, Rgb, Row, Screen, UnderlineKind};
use gpui::{
    App, Bounds, Font, FontFallbacks, FontFeatures, FontId, FontStyle, FontWeight, GlyphId, Hsla, PathBuilder, Pixels,
    Point, SharedString, TextRun, Window, fill, point, px, size,
};
use std::cell::RefCell;
use std::collections::HashMap;
use std::rc::Rc;

/// Font and cell geometry shared by every pane.
#[derive(Clone)]
pub struct Metrics {
    pub fonts: [Font; 4],
    pub font_size: Pixels,
    /// The window's scale factor these metrics were built for.
    pub scale: f32,
    /// The cell in logical pixels; a whole number of device pixels.
    pub cell_w: Pixels,
    pub cell_h: Pixels,
    /// The cell in device pixels.
    pub cell_w_dev: u32,
    pub cell_h_dev: u32,
    /// Distance from the cell top to the baseline, a whole device pixel.
    pub baseline: Pixels,
    /// Offset that centres a glyph in its cell when the font's advance is
    /// not a whole number of device pixels, rounded.
    pub glyph_dx: f32,
    /// Underline and strikethrough thickness, whole device pixels.
    pub underline_thickness: Pixels,
    pub ligatures: bool,
    /// Changes whenever the font, size, scale or theme changes; row caches
    /// compare it.
    pub epoch: u64,
    pub shapes: Rc<RefCell<ShapeCache>>,
}

/// The cell size for a font: `advance` and `natural` (ascent plus descent)
/// in logical pixels at `size`. Returns device pixels (width, height).
pub fn cell_device(advance: f32, natural: f32, font_size: f32, line_height: f32, scale: f32) -> (u32, u32) {
    let w = (advance * scale).round().max(1.);
    let h = (natural.max(font_size * line_height) * scale).round().max(1.);
    (w as u32, h as u32)
}

impl Metrics {
    pub fn new(family: &str, font_size: f32, line_height: f32, ligatures: bool, window: &mut Window, epoch: u64) -> Self {
        let base = Font {
            family: SharedString::from(family.to_string()),
            features: if ligatures { FontFeatures::default() } else { FontFeatures::disable_ligatures() },
            // The icon and CJK fonts. Emoji must not be named here: GPUI's
            // Linux text system drops any font without an "m" glyph from its
            // database when it is loaded by name, which takes Noto Color
            // Emoji away from the shaper's own fallback, the one path that
            // finds it. "JetBrainsMono Nerd Font Mono" keeps the icons of
            // configs written for the old default.
            fallbacks: Some(FontFallbacks::from_fonts(vec![
                "Symbols Nerd Font Mono".into(),
                "JetBrainsMono Nerd Font Mono".into(),
                "Noto Sans Mono CJK SC".into(),
                "Noto Sans CJK SC".into(),
            ])),
            weight: FontWeight::NORMAL,
            style: FontStyle::Normal,
        };
        let mk = |bold: bool, italic: bool| {
            let mut f = base.clone();
            if bold {
                f.weight = FontWeight::BOLD;
            }
            if italic {
                f.style = FontStyle::Italic;
            }
            f
        };
        let fonts = [mk(false, false), mk(true, false), mk(false, true), mk(true, true)];
        let size_px = px(font_size);
        let m = window.text_system().shape_line(
            "M".into(),
            size_px,
            &[TextRun { len: 1, font: fonts[0].clone(), color: Hsla::default(), background_color: None, underline: None, strikethrough: None }],
            None,
        );
        let scale = window.scale_factor();
        let advance = f32::from(m.width);
        let (ascent, descent) = (f32::from(m.ascent), f32::from(m.descent));
        let (wd, hd) = cell_device(advance, ascent + descent, font_size, line_height, scale);
        let baseline_dev = ((hd as f32 - (ascent + descent) * scale) / 2. + ascent * scale).round();
        let glyph_dx_dev = ((wd as f32 - advance * scale) / 2.).round();
        let underline_dev = (font_size * scale / 15.).round().max(1.);
        Metrics {
            fonts,
            font_size: size_px,
            scale,
            cell_w: px(wd as f32 / scale),
            cell_h: px(hd as f32 / scale),
            cell_w_dev: wd,
            cell_h_dev: hd,
            baseline: px(baseline_dev / scale),
            glyph_dx: glyph_dx_dev / scale,
            underline_thickness: px(underline_dev / scale),
            ligatures,
            epoch,
            shapes: Rc::new(RefCell::new(ShapeCache::default())),
        }
    }

    /// Rounds a logical position to a whole device pixel.
    pub fn snap(&self, v: f32) -> f32 {
        (v * self.scale).round() / self.scale
    }
}

/// One shaped glyph of a word: its byte index in the word and its pen
/// position from the word's start.
#[derive(Clone, Copy, Debug)]
pub struct WordGlyph {
    index: u32,
    x: f32,
    font_id: FontId,
    glyph: GlyphId,
    emoji: bool,
}

/// Words in one generation of the shape cache, one map per font style.
#[derive(Default)]
struct Words([HashMap<Box<str>, Rc<[WordGlyph]>>; 4]);

impl Words {
    fn len(&self) -> usize {
        self.0.iter().map(HashMap::len).sum()
    }
}

/// Shaped words, keyed by font style and text, and with ligatures off, one
/// glyph per printable ASCII character. Shared by every pane through
/// [`Metrics`]; a font change builds new metrics and so a new cache.
///
/// Words live in two generations. When the current one fills up it becomes
/// the old one, and a hit in the old one moves the word back, so the hot
/// words survive and only the cold ones go.
pub struct ShapeCache {
    current: Words,
    old: Words,
    /// Printable ASCII, per font style: (font, glyph), or none yet.
    ascii: [[Option<(FontId, GlyphId)>; 95]; 4],
    pub hits: u64,
    pub misses: u64,
}

impl Default for ShapeCache {
    fn default() -> Self {
        ShapeCache { current: Words::default(), old: Words::default(), ascii: [[None; 95]; 4], hits: 0, misses: 0 }
    }
}

/// Words kept in one generation. A screen of distinct words is a few
/// thousand; this holds many screens.
const SHAPE_GENERATION: usize = 16_384;

impl ShapeCache {
    fn shape(&mut self, style: usize, word: &str, m: &Metrics, window: &mut Window, stats: &mut PaintStats) -> Rc<[WordGlyph]> {
        self.misses += 1;
        stats.runs_shaped += 1;
        let shaped = window.text_system().shape_line(
            SharedString::from(word.to_string()),
            m.font_size,
            &[TextRun { len: word.len(), font: m.fonts[style].clone(), color: Hsla::default(), background_color: None, underline: None, strikethrough: None }],
            None,
        );
        shaped
            .runs
            .iter()
            .flat_map(|r| r.glyphs.iter().map(move |g| WordGlyph { index: g.index as u32, x: f32::from(g.position.x), font_id: r.font_id, glyph: g.id, emoji: g.is_emoji }))
            .collect()
    }

    fn word(&mut self, style: usize, word: &str, m: &Metrics, window: &mut Window, stats: &mut PaintStats) -> Rc<[WordGlyph]> {
        if let Some(g) = self.current.0[style].get(word) {
            self.hits += 1;
            return g.clone();
        }
        let glyphs = match self.old.0[style].remove_entry(word) {
            Some((k, g)) => {
                self.hits += 1;
                self.current.0[style].insert(k, g.clone());
                return g;
            }
            None => self.shape(style, word, m, window, stats),
        };
        if self.current.len() >= SHAPE_GENERATION {
            self.old = std::mem::take(&mut self.current);
        }
        self.current.0[style].insert(Box::from(word), glyphs.clone());
        glyphs
    }

    /// The glyph of one printable ASCII character. With ligatures off a
    /// character shapes the same alone as in a word, and a monospace font
    /// has no kerning, so unique tokens (timestamps, counters, hashes) cost
    /// no shaping at all.
    fn ascii(&mut self, style: usize, b: u8, m: &Metrics, window: &mut Window, stats: &mut PaintStats) -> Option<(FontId, GlyphId)> {
        let slot = (b - 0x20) as usize;
        if let Some(g) = self.ascii[style][slot] {
            self.hits += 1;
            return Some(g);
        }
        let s = [b];
        let word = std::str::from_utf8(&s).ok()?;
        let g = self.shape(style, word, m, window, stats).first().map(|g| (g.font_id, g.glyph))?;
        self.ascii[style][slot] = Some(g);
        Some(g)
    }
}

#[derive(Clone, Copy, Debug)]
struct GlyphInst {
    x: f32,
    font_id: FontId,
    glyph: GlyphId,
    color: Hsla,
    col: u16,
    emoji: bool,
}

#[derive(Clone, Copy, Debug)]
struct QuadInst {
    x0: f32,
    y0: f32,
    x1: f32,
    y1: f32,
    color: Hsla,
}

#[derive(Default)]
struct RowCache {
    generation: u64,
    hash: u64,
    epoch: u64,
    glyphs: Vec<GlyphInst>,
    bgs: Vec<QuadInst>,
    /// Decorations and cell-filling shapes, drawn above the text.
    over: Vec<QuadInst>,
    /// Powerline separators, drawn as paths: column, character, colour.
    paths: Vec<(u16, char, Hsla)>,
}

/// Counters for the performance harness.
#[derive(Default, Clone, Copy, Debug)]
pub struct PaintStats {
    pub rows_shaped: u64,
    pub rows_reused: u64,
    pub runs_shaped: u64,
    pub glyphs: u64,
    pub quads: u64,
}

/// The per-pane painter: owns the row caches.
#[derive(Default)]
pub struct PanePainter {
    rows: Vec<RowCache>,
    /// Built rows that left the screen, by content hash, for rows that come
    /// back with the same content.
    pool: HashMap<u64, RowCache>,
    above: Option<RowCache>,
    pub stats: PaintStats,
}

/// How the cursor is drawn this frame.
#[derive(Clone, Copy, Debug)]
pub struct CursorPaint {
    pub visible: bool,
    /// Solid in a focused pane of an active window, hollow otherwise.
    pub focused: bool,
    pub color: Rgb,
}

impl PanePainter {
    /// Brings the row caches up to date with `screen`. Rows whose generation
    /// is unchanged are left alone.
    pub fn prepare(&mut self, screen: &Screen, metrics: &Metrics, theme: &Theme, window: &mut Window) {
        if self.rows.len() != screen.rows.len() {
            self.rows.resize_with(screen.rows.len(), RowCache::default);
        }
        let colors = PlanColors { default_bg: screen.bg, selection: Rgb::from_u32(theme.selection) };
        let keep = 2 * screen.rows.len() + 8;
        for (y, row) in screen.rows.iter().enumerate() {
            let cache = &self.rows[y];
            if cache.generation == row.generation && cache.epoch == metrics.epoch && row.generation != 0 {
                continue;
            }
            if cache.hash == row.hash && cache.epoch == metrics.epoch && row.generation != 0 {
                self.rows[y].generation = row.generation;
                self.stats.rows_reused += 1;
                continue;
            }
            let old = std::mem::take(&mut self.rows[y]);
            if old.epoch == metrics.epoch && old.generation != 0 {
                self.pool.insert(old.hash, old);
            }
            let mut next = match self.pool.remove(&row.hash).filter(|c| c.epoch == metrics.epoch) {
                Some(c) => {
                    self.stats.rows_reused += 1;
                    c
                }
                None => {
                    // Recycle an evicted row's buffers when the pool is full.
                    let mut spare = RowCache::default();
                    if self.pool.len() > keep {
                        if let Some(k) = self.pool.keys().next().copied() {
                            spare = self.pool.remove(&k).unwrap_or_default();
                        }
                    }
                    build_row(&mut spare, row, &colors, metrics, window, &mut self.stats);
                    spare
                }
            };
            next.generation = row.generation;
            self.rows[y] = next;
        }
    }

    /// Prepares the row above the viewport, shown while smooth scrolling.
    pub fn prepare_above(&mut self, row: Option<&Row>, bg: Rgb, metrics: &Metrics, theme: &Theme, window: &mut Window) {
        let Some(row) = row else {
            self.above = None;
            return;
        };
        let colors = PlanColors { default_bg: bg, selection: Rgb::from_u32(theme.selection) };
        let cache = self.above.get_or_insert_with(RowCache::default);
        if cache.generation == row.generation && cache.epoch == metrics.epoch {
            return;
        }
        build_row(cache, row, &colors, metrics, window, &mut self.stats);
        cache.generation = row.generation;
    }

    /// Paints the pane at `origin`, which must be a whole device pixel.
    /// `y_offset` shifts every row (smooth scrolling); rows are clipped to
    /// the pane by the caller's content mask.
    #[allow(clippy::too_many_arguments)]
    pub fn paint(&mut self, screen: &Screen, origin: Point<Pixels>, metrics: &Metrics, cursor: CursorPaint, y_offset: f32, window: &mut Window, _cx: &mut App) {
        let cw = f32::from(metrics.cell_w);
        let chh = f32::from(metrics.cell_h);
        let y_offset = metrics.snap(y_offset);
        let pane_size = size(px(screen.cols as f32 * cw), px(screen.rows.len() as f32 * chh));
        let bounds = Bounds::new(origin, pane_size);
        let cur = screen.cursor;
        let show_cursor = cursor.visible && cur.visible && (cur.y as usize) < self.rows.len();
        let cursor_color = cur.color.unwrap_or(cursor.color);
        let block = show_cursor && cursor.focused && cur.shape == CursorShape::Block;
        let (ox, oy) = (f32::from(origin.x), f32::from(origin.y));
        let mut glyphs = 0u64;
        let mut quads = 0u64;
        // The row above the viewport is index -1 and only shows while the
        // content is pulled down.
        let above = self.above.as_ref().filter(|_| y_offset > 0.);
        let rows = above.map(|r| (-1., r)).into_iter().chain(self.rows.iter().enumerate().map(|(y, r)| (y as f32, r)));

        window.paint_layer(bounds, |window| {
            window.paint_quad(fill(bounds, hsla(screen.bg)));
            quads += 1;
            for (y, row) in rows.clone() {
                let top = oy + y * chh + y_offset;
                for q in &row.bgs {
                    window.paint_quad(fill(Bounds::new(point(px(ox + q.x0), px(top + q.y0)), size(px(q.x1 - q.x0), px(q.y1 - q.y0))), q.color));
                    quads += 1;
                }
            }
            if block {
                let w = if cur.wide { 2. } else { 1. };
                window.paint_quad(fill(
                    Bounds::new(point(px(ox + cur.x as f32 * cw), px(oy + cur.y as f32 * chh + y_offset)), size(px(cw * w), px(chh))),
                    hsla(cursor_color),
                ));
                quads += 1;
            }
            let under_cursor = hsla(screen.bg);
            for (y, row) in rows.clone() {
                let base = oy + y * chh + y_offset + f32::from(metrics.baseline);
                let cursor_row = block && y == cur.y as f32;
                for g in &row.glyphs {
                    let pos = point(px(ox + g.x), px(base));
                    if g.emoji {
                        let _ = window.paint_emoji(pos, g.font_id, g.glyph, metrics.font_size);
                    } else {
                        let color = if cursor_row && g.col == cur.x { under_cursor } else { g.color };
                        let _ = window.paint_glyph(pos, g.font_id, g.glyph, metrics.font_size, color);
                    }
                    glyphs += 1;
                }
            }
        });
        window.paint_layer(bounds, |window| {
            for (y, row) in rows.clone() {
                let top = oy + y * chh + y_offset;
                for q in &row.over {
                    window.paint_quad(fill(Bounds::new(point(px(ox + q.x0), px(top + q.y0)), size(px(q.x1 - q.x0), px(q.y1 - q.y0))), q.color));
                    quads += 1;
                }
                for &(col, ch, color) in &row.paths {
                    let x0 = ox + col as f32 * cw;
                    let t = f32::from(metrics.underline_thickness);
                    for poly in boxdraw::powerline(ch, cw, chh, t) {
                        let mut b = PathBuilder::fill();
                        for (i, (x, y)) in poly.iter().enumerate() {
                            let p = point(px(x0 + x), px(top + y));
                            if i == 0 { b.move_to(p) } else { b.line_to(p) }
                        }
                        b.close();
                        if let Ok(path) = b.build() {
                            window.paint_path(path, color);
                        }
                    }
                }
            }
            if show_cursor && !block {
                let x = ox + cur.x as f32 * cw;
                let y = oy + cur.y as f32 * chh + y_offset;
                let w = if cur.wide { 2. * cw } else { cw };
                // Strokes are whole device pixels: 2 for a bar or underline,
                // 1 for the hollow block of a pane without focus.
                let two = 2. / metrics.scale;
                let one = 1. / metrics.scale;
                let color = hsla(cursor_color);
                let rect = |x: f32, y: f32, w: f32, h: f32| fill(Bounds::new(point(px(x), px(y)), size(px(w), px(h))), color);
                match (cursor.focused, cur.shape) {
                    (true, CursorShape::Bar) => window.paint_quad(rect(x, y, two, chh)),
                    (true, CursorShape::Underline) => window.paint_quad(rect(x, y + chh - two, w, two)),
                    _ => {
                        window.paint_quad(rect(x, y, w, one));
                        window.paint_quad(rect(x, y + chh - one, w, one));
                        window.paint_quad(rect(x, y, one, chh));
                        window.paint_quad(rect(x + w - one, y, one, chh));
                    }
                }
                quads += 4;
            }
        });
        self.stats.glyphs += glyphs;
        self.stats.quads += quads;
    }

    /// Drops every cached row, for a font change or a hidden pane.
    pub fn clear(&mut self) {
        self.rows.clear();
        self.pool.clear();
        self.above = None;
    }
}

fn build_row(cache: &mut RowCache, row: &Row, colors: &PlanColors, metrics: &Metrics, window: &mut Window, stats: &mut PaintStats) {
    let cw = f32::from(metrics.cell_w);
    let chh = f32::from(metrics.cell_h);
    let scale = metrics.scale;
    cache.generation = row.generation;
    cache.hash = row.hash;
    cache.epoch = metrics.epoch;
    cache.glyphs.clear();
    cache.bgs.clear();
    cache.over.clear();
    cache.paths.clear();
    stats.rows_shaped += 1;
    let plan = plan_row(row, colors);
    for s in &plan.bgs {
        cache.bgs.push(QuadInst { x0: s.start as f32 * cw, y0: 0., x1: s.end as f32 * cw, y1: chh, color: hsla(s.color) });
    }
    let mut shapes = metrics.shapes.borrow_mut();
    for run in &plan.runs {
        // Runs are shaped a word at a time through a cache shared by every
        // pane: words repeat across rows and frames, so a scrolled or redrawn
        // screen mostly reuses shapes. Ligatures sit inside words ("->",
        // "!="), and a space breaks them in every monospace font.
        let bytes = run.text.as_bytes();
        let mut i = 0usize;
        while i < bytes.len() {
            if bytes[i] == b' ' {
                i += 1;
                continue;
            }
            let start = i;
            while i < bytes.len() && bytes[i] != b' ' {
                i += 1;
            }
            let word = &run.text[start..i];
            if !metrics.ligatures && word.bytes().all(|b| (0x21..0x7f).contains(&b)) {
                for (k, b) in word.bytes().enumerate() {
                    let Some(cell) = run.cell_at(start + k) else { continue };
                    let Some((font_id, glyph)) = shapes.ascii(run.style, b, metrics, window, stats) else { continue };
                    cache.glyphs.push(GlyphInst { x: cell.col as f32 * cw + metrics.glyph_dx, font_id, glyph, color: hsla(cell.fg), col: cell.col, emoji: false });
                }
                continue;
            }
            let glyphs = shapes.word(run.style, word, metrics, window, stats);
            // Each glyph sits at its cell's column; within a cell (a base
            // and its combining marks) the shaper's own offsets are kept,
            // rounded to the device grid.
            let mut cluster_col = u16::MAX;
            let mut cluster_x = 0f32;
            for g in glyphs.iter() {
                let Some(cell) = run.cell_at(start + g.index as usize) else { continue };
                if cell.col != cluster_col {
                    cluster_col = cell.col;
                    cluster_x = g.x;
                }
                let dx = if cell.wide { 0. } else { metrics.glyph_dx };
                cache.glyphs.push(GlyphInst {
                    x: cell.col as f32 * cw + dx + metrics.snap(g.x - cluster_x),
                    font_id: g.font_id,
                    glyph: g.glyph,
                    color: hsla(cell.fg),
                    col: cell.col,
                    emoji: g.emoji,
                });
            }
        }
    }
    drop(shapes);
    // Decorations in device pixels, then back to logical.
    let t = (f32::from(metrics.underline_thickness) * scale).round().max(1.);
    let (cwd, chd) = (metrics.cell_w_dev as f32, metrics.cell_h_dev as f32);
    let base = f32::from(metrics.baseline) * scale;
    let ul_y = (base + (chd - base) * 0.45).round().min(chd - t);
    let mut quad = |x0: f32, y0: f32, x1: f32, y1: f32, color: Hsla| {
        cache.over.push(QuadInst { x0: x0 / scale, y0: y0 / scale, x1: x1 / scale, y1: y1 / scale, color });
    };
    for d in &plan.decos {
        let x0 = d.start as f32 * cwd;
        let x1 = d.end as f32 * cwd;
        let color = hsla(d.color);
        match d.kind {
            DecoKind::Underline(UnderlineKind::Double) => {
                quad(x0, ul_y - t, x1, ul_y, color);
                quad(x0, ul_y + t, x1, (ul_y + 2. * t).min(chd), color);
            }
            DecoKind::Underline(UnderlineKind::Curly) => {
                // A wave of short steps, one per half cell.
                let step = (cwd / 2.).round().max(1.);
                let mut x = x0;
                let mut up = true;
                while x < x1 {
                    let y = if up { ul_y - t } else { ul_y + (t / 2.).round() };
                    quad(x, y, (x + step).min(x1), y + t, color);
                    x += step;
                    up = !up;
                }
            }
            DecoKind::Underline(UnderlineKind::Dotted) | DecoKind::Underline(UnderlineKind::Dashed) => {
                let dotted = matches!(d.kind, DecoKind::Underline(UnderlineKind::Dotted));
                let (on, off) = if dotted { (t, t * 2.) } else { ((cwd / 2.).round(), (cwd / 4.).round().max(1.)) };
                let mut x = x0;
                while x < x1 {
                    quad(x, ul_y, (x + on).min(x1), ul_y + t, color);
                    x += on + off;
                }
            }
            DecoKind::Underline(_) => quad(x0, ul_y, x1, ul_y + t, color),
            DecoKind::Strike => {
                let y = ((chd - t) / 2.).round();
                quad(x0, y, x1, y + t, color);
            }
            DecoKind::Overline => quad(x0, 0., x1, t, color),
        }
    }
    for b in &plan.boxes {
        if boxdraw::is_powerline(b.ch) {
            cache.paths.push((b.col, b.ch, hsla(b.color)));
            continue;
        }
        let ox = b.col as f32 * cwd;
        for r in boxdraw::rects(b.ch, cwd, chd, t) {
            let mut color = hsla(b.color);
            color.a *= r.alpha;
            quad(ox + r.x0, r.y0, ox + r.x1, r.y1, color);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn cells_are_whole_device_pixels() {
        // JetBrains Mono: advance 600/1000 em, ascent plus descent 1.32 em.
        assert_eq!(cell_device(9., 19.8, 15., 1.333, 1.), (9, 20));
        assert_eq!(cell_device(9., 19.8, 15., 1.333, 1.25), (11, 25));
        assert_eq!(cell_device(9., 19.8, 15., 1.333, 2.), (18, 40));
    }
}
