//! Plans how one terminal row is drawn, without touching GPUI: which text
//! runs to shape, which background spans to fill, which decorations to draw
//! and which cells are box-drawing characters drawn as rectangles.
//!
//! Kept pure so it can be tested cell by cell.

use crate::boxdraw;
use ghostty_vt::{Cell, CellFlags, Rgb, Row, UnderlineKind};

/// A run of cells shaped together: one font style, consecutive columns.
#[derive(Debug, Clone, PartialEq)]
pub struct Run {
    /// Bit 0 bold, bit 1 italic.
    pub style: usize,
    pub text: String,
    /// For each cell in the run: the byte offset of its text in `text`, its
    /// column and its colour.
    pub cells: Vec<RunCell>,
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct RunCell {
    pub byte: u32,
    pub col: u16,
    pub fg: Rgb,
    pub wide: bool,
}

impl Run {
    /// The run cell a glyph at byte `index` belongs to.
    pub fn cell_at(&self, index: usize) -> Option<&RunCell> {
        let i = self.cells.partition_point(|c| c.byte as usize <= index);
        i.checked_sub(1).map(|i| &self.cells[i])
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Span {
    pub start: u16,
    pub end: u16,
    pub color: Rgb,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum DecoKind {
    Underline(UnderlineKind),
    Strike,
    Overline,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Deco {
    pub start: u16,
    pub end: u16,
    pub kind: DecoKind,
    pub color: Rgb,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct BoxCell {
    pub col: u16,
    pub ch: char,
    pub color: Rgb,
}

#[derive(Debug, Clone, Default, PartialEq)]
pub struct RowPlan {
    pub runs: Vec<Run>,
    pub bgs: Vec<Span>,
    pub decos: Vec<Deco>,
    pub boxes: Vec<BoxCell>,
}

/// Colours a plan needs that the row does not carry.
#[derive(Debug, Clone, Copy)]
pub struct PlanColors {
    pub default_bg: Rgb,
    pub selection: Rgb,
}

fn fg_of(c: &Cell, colors: &PlanColors) -> Rgb {
    if c.flags.contains(CellFlags::FAINT) {
        crate::theme::mix(c.fg, c.bg.unwrap_or(colors.default_bg), 0.45)
    } else {
        c.fg
    }
}

/// The background a cell shows, or None for the pane's own background.
fn bg_of(c: &Cell, colors: &PlanColors) -> Option<Rgb> {
    if c.flags.contains(CellFlags::SELECTED) {
        Some(colors.selection)
    } else {
        c.bg
    }
}

pub fn plan_row(row: &Row, colors: &PlanColors) -> RowPlan {
    let mut plan = RowPlan::default();
    let mut run: Option<Run> = None;
    let flush = |run: &mut Option<Run>, plan: &mut RowPlan| {
        if let Some(r) = run.take() {
            // A run of nothing but blanks draws nothing.
            if r.text.bytes().any(|b| b != b' ') {
                plan.runs.push(r);
            }
        }
    };

    let mut bg: Option<Span> = None;
    let mut decos: Vec<Deco> = Vec::new();

    for (x, c) in row.cells.iter().enumerate() {
        let col = x as u16;

        // Backgrounds: merge equal neighbours. A wide character's spacer takes
        // the colour of the cell it belongs to.
        let cbg = if c.flags.contains(CellFlags::SPACER) && x > 0 {
            bg_of(&row.cells[x - 1], colors)
        } else {
            bg_of(c, colors)
        };
        match (cbg, bg.as_mut()) {
            (Some(color), Some(s)) if s.color == color && s.end == col => s.end = col + 1,
            (Some(color), _) => {
                if let Some(s) = bg.take() {
                    plan.bgs.push(s);
                }
                bg = Some(Span { start: col, end: col + 1, color });
            }
            (None, _) => {
                if let Some(s) = bg.take() {
                    plan.bgs.push(s);
                }
            }
        }

        if c.flags.contains(CellFlags::SPACER) {
            continue;
        }
        let fg = fg_of(c, colors);
        let width = if c.flags.contains(CellFlags::WIDE) { 2 } else { 1 };

        // Decorations, merged with an identical one directly to the left.
        let mut add = |kind: DecoKind, color: Rgb| {
            if let Some(d) = decos.iter_mut().rev().find(|d| d.kind == kind) {
                if d.end == col && d.color == color {
                    d.end = col + width;
                    return;
                }
            }
            decos.push(Deco { start: col, end: col + width, kind, color });
        };
        if c.underline != UnderlineKind::None {
            add(DecoKind::Underline(c.underline), c.ul_color.unwrap_or(fg));
        }
        if c.flags.contains(CellFlags::STRIKE) {
            add(DecoKind::Strike, fg);
        }
        if c.flags.contains(CellFlags::OVERLINE) {
            add(DecoKind::Overline, fg);
        }

        let text = row.cell_text(c);
        if c.flags.contains(CellFlags::INVISIBLE) || text.is_empty() || text == " " {
            // Blank cells end nothing: a run can carry on over them, which
            // keeps a line of words in one shaping call.
            if let Some(r) = run.as_mut() {
                r.cells.push(RunCell { byte: r.text.len() as u32, col, fg, wide: false });
                r.text.push(' ');
            }
            continue;
        }
        let mut chars = text.chars();
        if let (Some(ch), None) = (chars.next(), chars.next()) {
            if boxdraw::is_box(ch) {
                plan.boxes.push(BoxCell { col, ch, color: fg });
                flush(&mut run, &mut plan);
                continue;
            }
        }
        let style = c.flags.font_style();
        if run.as_ref().is_some_and(|r| r.style != style) {
            flush(&mut run, &mut plan);
        }
        let r = run.get_or_insert_with(|| Run { style, text: String::new(), cells: Vec::new() });
        r.cells.push(RunCell { byte: r.text.len() as u32, col, fg, wide: width == 2 });
        r.text.push_str(text);
    }
    flush(&mut run, &mut plan);
    if let Some(s) = bg.take() {
        plan.bgs.push(s);
    }
    // Trailing blanks carried by a run are not drawn; trim them so the shaped
    // string is no longer than it needs to be.
    for r in &mut plan.runs {
        let keep = r.text.trim_end_matches(' ').len();
        r.text.truncate(keep);
        r.cells.retain(|c| (c.byte as usize) < keep);
    }
    decos.sort_by_key(|d| d.start);
    plan.decos = decos;
    plan
}

#[cfg(test)]
mod tests {
    use super::*;
    use ghostty_vt::Terminal;

    fn row(bytes: &[u8]) -> Row {
        let mut t = Terminal::new(20, 1, 0).unwrap();
        t.write(bytes);
        t.snapshot().rows[0].clone()
    }

    const C: PlanColors = PlanColors { default_bg: Rgb(0, 0, 0), selection: Rgb(1, 2, 3) };

    #[test]
    fn words_share_a_run_across_spaces() {
        let p = plan_row(&row(b"ab cd"), &C);
        assert_eq!(p.runs.len(), 1);
        assert_eq!(p.runs[0].text, "ab cd");
        assert_eq!(p.runs[0].cell_at(3).unwrap().col, 3);
        assert!(p.bgs.is_empty());
    }

    #[test]
    fn style_changes_split_runs_and_colours_do_not() {
        let p = plan_row(&row(b"a\x1b[31mb\x1b[1mc"), &C);
        assert_eq!(p.runs.len(), 2);
        assert_eq!(p.runs[0].text, "ab");
        assert_ne!(p.runs[0].cells[0].fg, p.runs[0].cells[1].fg);
        assert_eq!(p.runs[1].style, 1);
    }

    #[test]
    fn wide_characters_keep_their_columns() {
        let p = plan_row(&row("漢x".as_bytes()), &C);
        let r = &p.runs[0];
        assert_eq!(r.cells[0].col, 0);
        assert!(r.cells[0].wide);
        assert_eq!(r.cells[1].col, 2);
        assert_eq!(r.cell_at(3).unwrap().col, 2, "byte 3 is the x");
    }

    #[test]
    fn backgrounds_merge_and_cover_wide_spacers() {
        let p = plan_row(&row("\x1b[41mab漢\x1b[0m c\x1b[42md".as_bytes()), &C);
        assert_eq!(p.bgs.len(), 2);
        assert_eq!((p.bgs[0].start, p.bgs[0].end), (0, 4));
        assert_eq!((p.bgs[1].start, p.bgs[1].end), (6, 7));
    }

    #[test]
    fn box_drawing_leaves_the_run() {
        let p = plan_row(&row("a─b".as_bytes()), &C);
        assert_eq!(p.boxes.len(), 1);
        assert_eq!(p.boxes[0].col, 1);
        assert_eq!(p.runs.len(), 2);
    }

    #[test]
    fn underlines_merge_and_keep_their_kind() {
        let p = plan_row(&row(b"\x1b[4mab\x1b[4:3mc"), &C);
        assert_eq!(p.decos.len(), 2);
        assert_eq!((p.decos[0].start, p.decos[0].end), (0, 2));
        assert_eq!(p.decos[1].kind, DecoKind::Underline(UnderlineKind::Curly));
    }

    #[test]
    fn selection_paints_the_selection_colour() {
        let mut t = Terminal::new(20, 1, 0).unwrap();
        t.write(b"hello");
        t.select((1, 0), (2, 0), false);
        let r = t.snapshot().rows[0].clone();
        let p = plan_row(&r, &C);
        assert_eq!(p.bgs, vec![Span { start: 1, end: 3, color: Rgb(1, 2, 3) }]);
    }
}
