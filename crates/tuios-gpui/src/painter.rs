//! Paints terminal panes with GPUI's primitives.
//!
//! Each row is planned ([`crate::rowplan`]) and shaped once per change: the
//! row's text runs go through the platform shaper (so ligatures and font
//! fallback work), and every glyph is pinned to its cell's column so the grid
//! never drifts. The result, a list of glyphs, background spans, decorations
//! and box-drawing rectangles, is cached per row and keyed by the row's
//! generation, which the emulator bumps only when the row changes. A frame
//! then only replays cached rows: no shaping, no allocation per cell.
//!
//! Glyphs are painted with `Window::paint_glyph` directly inside
//! `Window::paint_layer`, as herdr-gpui does (Apache-2.0), which skips the
//! per-call layer that `ShapedLine::paint` pushes.

use crate::boxdraw;
use std::cell::RefCell;
use std::collections::HashMap;
use std::rc::Rc;
use crate::rowplan::{DecoKind, PlanColors, plan_row};
use crate::theme::{Theme, hsla};
use ghostty_vt::{CursorShape, Rgb, Row, Screen, UnderlineKind};
use gpui::{
    App, Bounds, Font, FontFallbacks, FontFeatures, FontId, FontStyle, FontWeight, GlyphId, Hsla, Pixels, Point,
    SharedString, TextRun, Window, fill, point, px, size,
};

/// Font and cell geometry shared by every pane.
#[derive(Clone)]
pub struct Metrics {
    pub fonts: [Font; 4],
    pub font_size: Pixels,
    pub cell_w: Pixels,
    pub cell_h: Pixels,
    /// Distance from the cell top to the baseline.
    pub baseline: Pixels,
    pub underline_thickness: Pixels,
    /// Changes whenever the font or size changes; row caches compare it.
    pub epoch: u64,
    pub shapes: Rc<RefCell<ShapeCache>>,
}

impl Metrics {
    pub fn new(family: &str, font_size: f32, line_height: f32, ligatures: bool, window: &mut Window, epoch: u64) -> Self {
        let base = Font {
            family: SharedString::from(family.to_string()),
            features: if ligatures { FontFeatures::default() } else { FontFeatures::disable_ligatures() },
            // CJK and symbol fonts only. Emoji must not be named here: GPUI's
            // Linux text system drops any font without an "m" glyph from its
            // database when it is loaded by name, which takes Noto Color
            // Emoji away from the shaper's own fallback, the one path that
            // finds it. A text font with outline emoji (DejaVu) would win
            // over colour emoji the same way.
            fallbacks: Some(FontFallbacks::from_fonts(vec![
                "Noto Sans CJK SC".into(),
                "Noto Sans CJK TC".into(),
                "Symbols Nerd Font Mono".into(),
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
        if std::env::var_os("TUIOS_GPUI_DEBUG").is_some() {
            for (label, f) in [("chain", fonts[0].clone()), ("plain", gpui::font("FiraCode Nerd Font Mono"))] {
                let l = window.text_system().shape_line(
                    "a🚀👍漢".into(),
                    size_px,
                    &[TextRun { len: "a🚀👍漢".len(), font: f, color: Hsla::default(), background_color: None, underline: None, strikethrough: None }],
                    None,
                );
                for r in &l.runs {
                    eprintln!("[gpui] font {label}: font_id {:?} glyphs {:?}", r.font_id, r.glyphs.iter().map(|g| (g.index, g.id, g.is_emoji)).collect::<Vec<_>>());
                }
            }
        }
        let scale = window.scale_factor();
        // Cells are whole device pixels wide and tall, so backgrounds tile
        // without seams and glyphs land on the same subpixel offset in every
        // cell of a column.
        let snap = |v: f32| (v * scale).round().max(1.) / scale;
        let cell_w = px(snap(f32::from(m.width)));
        let natural = f32::from(m.ascent + m.descent);
        let cell_h = px(snap(natural.max(font_size * line_height)));
        let baseline = px(snap((f32::from(cell_h) - natural) / 2. + f32::from(m.ascent)));
        Metrics {
            fonts,
            font_size: size_px,
            cell_w,
            cell_h,
            baseline,
            underline_thickness: px(snap((font_size / 14.).max(1.))),
            epoch,
            shapes: Rc::new(RefCell::new(ShapeCache::default())),
        }
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

/// Shaped words, keyed by font style and text. Shared by every pane through
/// [`Metrics`]; a font change builds new metrics and so a new cache.
#[derive(Default)]
pub struct ShapeCache {
    words: HashMap<(u8, Box<str>), Rc<[WordGlyph]>>,
    pub hits: u64,
    pub misses: u64,
}

/// Entries kept before the cache starts over. A screen of distinct words is
/// a few thousand; this holds many screens.
const SHAPE_CACHE_LIMIT: usize = 32_768;

impl ShapeCache {
    fn get(&mut self, style: usize, word: &str, m: &Metrics, window: &mut Window, stats: &mut PaintStats) -> Rc<[WordGlyph]> {
        if let Some(g) = self.words.get(&(style as u8, Box::from(word))) {
            self.hits += 1;
            return g.clone();
        }
        self.misses += 1;
        stats.runs_shaped += 1;
        let shaped = window.text_system().shape_line(
            SharedString::from(word.to_string()),
            m.font_size,
            &[TextRun { len: word.len(), font: m.fonts[style].clone(), color: Hsla::default(), background_color: None, underline: None, strikethrough: None }],
            None,
        );
        let glyphs: Rc<[WordGlyph]> = shaped
            .runs
            .iter()
            .flat_map(|r| {
                r.glyphs.iter().map(move |g| WordGlyph { index: g.index as u32, x: f32::from(g.position.x), font_id: r.font_id, glyph: g.id, emoji: g.is_emoji })
            })
            .collect();
        if self.words.len() >= SHAPE_CACHE_LIMIT {
            self.words.clear();
        }
        self.words.insert((style as u8, Box::from(word)), glyphs.clone());
        glyphs
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
    epoch: u64,
    glyphs: Vec<GlyphInst>,
    bgs: Vec<QuadInst>,
    /// Decorations and box-drawing rectangles, drawn above the text.
    over: Vec<QuadInst>,
}

/// Counters for the performance harness.
#[derive(Default, Clone, Copy, Debug)]
pub struct PaintStats {
    pub rows_shaped: u64,
    pub runs_shaped: u64,
    pub glyphs: u64,
    pub quads: u64,
}

/// The per-pane painter: owns the row caches.
#[derive(Default)]
pub struct PanePainter {
    rows: Vec<RowCache>,
    above: Option<RowCache>,
    pub stats: PaintStats,
}

/// How the cursor is drawn this frame.
#[derive(Clone, Copy, Debug)]
pub struct CursorPaint {
    pub visible: bool,
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
        for (y, row) in screen.rows.iter().enumerate() {
            let cache = &mut self.rows[y];
            if cache.generation == row.generation && cache.epoch == metrics.epoch && row.generation != 0 {
                continue;
            }
            build_row(cache, row, &colors, metrics, window, &mut self.stats);
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
    }

    /// Paints the pane at `origin`. `y_offset` shifts every row (smooth
    /// scrolling); rows are clipped to `clip` by the caller's content mask.
    #[allow(clippy::too_many_arguments)]
    pub fn paint(
        &mut self,
        screen: &Screen,
        origin: Point<Pixels>,
        metrics: &Metrics,
        cursor: CursorPaint,
        y_offset: f32,
        window: &mut Window,
        _cx: &mut App,
    ) {
        let cw = f32::from(metrics.cell_w);
        let chh = f32::from(metrics.cell_h);
        let pane_size = size(px(screen.cols as f32 * cw), px(screen.rows.len() as f32 * chh));
        let bounds = Bounds::new(origin, pane_size);
        let cur = screen.cursor;
        let show_cursor = cursor.visible && cur.visible && (cur.y as usize) < self.rows.len();
        let cursor_color = cur.color.unwrap_or(cursor.color);
        let block = show_cursor && cursor.focused && cur.shape == CursorShape::Block;
        let mut glyphs = 0u64;
        let mut quads = 0u64;
        // The rows to draw with their row index; the row above the viewport is
        // index -1 and only shows while the content is pulled down.
        let above = self.above.as_ref().filter(|_| y_offset > 0.);
        let rows: Vec<(f32, &RowCache)> =
            above.map(|r| (-1., r)).into_iter().chain(self.rows.iter().enumerate().map(|(y, r)| (y as f32, r))).collect();

        window.paint_layer(bounds, |window| {
            window.paint_quad(fill(bounds, hsla(screen.bg)));
            quads += 1;
            for &(y, row) in &rows {
                let oy = f32::from(origin.y) + y * chh + y_offset;
                for q in &row.bgs {
                    window.paint_quad(fill(
                        Bounds::new(point(px(f32::from(origin.x) + q.x0), px(oy + q.y0)), size(px(q.x1 - q.x0), px(q.y1 - q.y0))),
                        q.color,
                    ));
                    quads += 1;
                }
            }
            if block {
                let w = if cur.wide { 2. } else { 1. };
                window.paint_quad(fill(
                    Bounds::new(
                        point(px(f32::from(origin.x) + cur.x as f32 * cw), px(f32::from(origin.y) + cur.y as f32 * chh + y_offset)),
                        size(px(cw * w), px(chh)),
                    ),
                    hsla(cursor_color),
                ));
                quads += 1;
            }
            let under_cursor = hsla(screen.bg);
            for &(y, row) in &rows {
                let base = f32::from(origin.y) + y * chh + y_offset + f32::from(metrics.baseline);
                let cursor_row = block && y == cur.y as f32;
                for g in &row.glyphs {
                    let pos = point(px(f32::from(origin.x) + g.x), px(base));
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
            for &(y, row) in &rows {
                let oy = f32::from(origin.y) + y * chh + y_offset;
                for q in &row.over {
                    window.paint_quad(fill(
                        Bounds::new(point(px(f32::from(origin.x) + q.x0), px(oy + q.y0)), size(px(q.x1 - q.x0), px(q.y1 - q.y0))),
                        q.color,
                    ));
                    quads += 1;
                }
            }
            if show_cursor && !block {
                let x = f32::from(origin.x) + cur.x as f32 * cw;
                let y = f32::from(origin.y) + cur.y as f32 * chh + y_offset;
                let w = if cur.wide { 2. * cw } else { cw };
                let t = f32::from(metrics.underline_thickness).max(1.);
                let color = hsla(cursor_color);
                let rect = |x: f32, y: f32, w: f32, h: f32| fill(Bounds::new(point(px(x), px(y)), size(px(w), px(h))), color);
                match (cursor.focused, cur.shape) {
                    (true, CursorShape::Bar) => window.paint_quad(rect(x, y, (2. * t).max(2.), chh)),
                    (true, CursorShape::Underline) => window.paint_quad(rect(x, y + chh - 2. * t, w, 2. * t)),
                    _ => {
                        // Hollow: the cursor of a pane without focus.
                        window.paint_quad(rect(x, y, w, t));
                        window.paint_quad(rect(x, y + chh - t, w, t));
                        window.paint_quad(rect(x, y, t, chh));
                        window.paint_quad(rect(x + w - t, y, t, chh));
                    }
                }
                quads += 4;
            }
        });
        self.stats.glyphs += glyphs;
        self.stats.quads += quads;
    }

    /// Drops every cached row, for a font change.
    pub fn clear(&mut self) {
        self.rows.clear();
    }
}

fn build_row(cache: &mut RowCache, row: &Row, colors: &PlanColors, metrics: &Metrics, window: &mut Window, stats: &mut PaintStats) {
    let cw = f32::from(metrics.cell_w);
    let chh = f32::from(metrics.cell_h);
    cache.generation = row.generation;
    cache.epoch = metrics.epoch;
    cache.glyphs.clear();
    cache.bgs.clear();
    cache.over.clear();
    stats.rows_shaped += 1;
    let plan = plan_row(row, colors);
    for s in &plan.bgs {
        cache.bgs.push(QuadInst { x0: s.start as f32 * cw, y0: 0., x1: s.end as f32 * cw, y1: chh, color: hsla(s.color) });
    }
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
            let glyphs = metrics.shapes.borrow_mut().get(run.style, word, metrics, window, stats);
            // Each glyph sits at its cell's column; within a cell (a base
            // and its combining marks) the shaper's own offsets are kept.
            let mut cluster_col = u16::MAX;
            let mut cluster_x = 0f32;
            for g in glyphs.iter() {
                let Some(cell) = run.cell_at(start + g.index as usize) else { continue };
                if cell.col != cluster_col {
                    cluster_col = cell.col;
                    cluster_x = g.x;
                }
                cache.glyphs.push(GlyphInst {
                    x: cell.col as f32 * cw + (g.x - cluster_x),
                    font_id: g.font_id,
                    glyph: g.glyph,
                    color: hsla(cell.fg),
                    col: cell.col,
                    emoji: g.emoji,
                });
            }
        }
    }
    let t = f32::from(metrics.underline_thickness);
    for d in &plan.decos {
        let x0 = d.start as f32 * cw;
        let x1 = d.end as f32 * cw;
        let color = hsla(d.color);
        let base = f32::from(metrics.baseline);
        let ul_y = (base + (chh - base) * 0.45).min(chh - t).round();
        match d.kind {
            DecoKind::Underline(UnderlineKind::Double) => {
                cache.over.push(QuadInst { x0, y0: ul_y - t, x1, y1: ul_y, color });
                cache.over.push(QuadInst { x0, y0: ul_y + t, x1, y1: (ul_y + 2. * t).min(chh), color });
            }
            DecoKind::Underline(UnderlineKind::Curly) => {
                // A wave of short steps, one per half cell.
                let step = cw / 2.;
                let mut x = x0;
                let mut up = true;
                while x < x1 {
                    let y = if up { ul_y - t } else { ul_y + t / 2. };
                    cache.over.push(QuadInst { x0: x, y0: y, x1: (x + step).min(x1), y1: y + t, color });
                    x += step;
                    up = !up;
                }
            }
            DecoKind::Underline(UnderlineKind::Dotted) | DecoKind::Underline(UnderlineKind::Dashed) => {
                let (on, off) = if matches!(d.kind, DecoKind::Underline(UnderlineKind::Dotted)) { (t.max(1.), t.max(1.) * 2.) } else { (cw / 2., cw / 4.) };
                let mut x = x0;
                while x < x1 {
                    cache.over.push(QuadInst { x0: x, y0: ul_y, x1: (x + on).min(x1), y1: ul_y + t, color });
                    x += on + off;
                }
            }
            DecoKind::Underline(_) => cache.over.push(QuadInst { x0, y0: ul_y, x1, y1: ul_y + t, color }),
            DecoKind::Strike => {
                let y = (chh / 2.).round();
                cache.over.push(QuadInst { x0, y0: y, x1, y1: y + t, color });
            }
            DecoKind::Overline => cache.over.push(QuadInst { x0, y0: 0., x1, y1: t, color }),
        }
    }
    for b in &plan.boxes {
        let ox = b.col as f32 * cw;
        for r in boxdraw::rects(b.ch, cw, chh, t) {
            let mut color = hsla(b.color);
            color.a *= r.alpha;
            cache.over.push(QuadInst { x0: ox + r.x0, y0: r.y0, x1: ox + r.x1, y1: r.y1, color });
        }
    }
}
