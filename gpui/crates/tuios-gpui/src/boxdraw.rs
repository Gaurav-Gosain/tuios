//! Cell-filling characters drawn as shapes on the pixel grid instead of font
//! glyphs, so they join across cells with no gaps whatever the font: box
//! drawing, blocks and braille as rectangles, Powerline separators as
//! polygons (docs/design/FINAL.md section 9, rule 6). All sizes are device
//! pixels. Characters not listed here fall back to the font.

/// Line weight on one side of a cell: none, light, heavy or double.
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum W {
    N,
    L,
    H,
    D,
}

/// (up, right, down, left) for the line characters handled.
fn arms(ch: char) -> Option<[W; 4]> {
    use W::*;
    Some(match ch {
        '─' => [N, L, N, L],
        '━' => [N, H, N, H],
        '│' => [L, N, L, N],
        '┃' => [H, N, H, N],
        '┌' | '╭' => [N, L, L, N],
        '┐' | '╮' => [N, N, L, L],
        '└' | '╰' => [L, L, N, N],
        '┘' | '╯' => [L, N, N, L],
        '┏' => [N, H, H, N],
        '┓' => [N, N, H, H],
        '┗' => [H, H, N, N],
        '┛' => [H, N, N, H],
        '├' => [L, L, L, N],
        '┤' => [L, N, L, L],
        '┬' => [N, L, L, L],
        '┴' => [L, L, N, L],
        '┼' => [L, L, L, L],
        '┣' => [H, H, H, N],
        '┫' => [H, N, H, H],
        '┳' => [N, H, H, H],
        '┻' => [H, H, N, H],
        '╋' => [H, H, H, H],
        '═' => [N, D, N, D],
        '║' => [D, N, D, N],
        '╔' => [N, D, D, N],
        '╗' => [N, N, D, D],
        '╚' => [D, D, N, N],
        '╝' => [D, N, N, D],
        '╠' => [D, D, D, N],
        '╣' => [D, N, D, D],
        '╦' => [N, D, D, D],
        '╩' => [D, D, N, D],
        '╬' => [D, D, D, D],
        '╴' => [N, N, N, L],
        '╵' => [L, N, N, N],
        '╶' => [N, L, N, N],
        '╷' => [N, N, L, N],
        _ => return None,
    })
}

/// A rectangle in cell units (0..1 across the cell), with an alpha for shades.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Rect {
    pub x0: f32,
    pub y0: f32,
    pub x1: f32,
    pub y1: f32,
    pub alpha: f32,
}

/// Characters drawn as rectangles by [`rects`].
pub fn is_box(ch: char) -> bool {
    arms(ch).is_some() || block(ch).is_some() || is_braille(ch)
}

fn is_braille(ch: char) -> bool {
    ('\u{2801}'..='\u{28FF}').contains(&ch)
}

/// Powerline separators (U+E0B0 to U+E0BF), drawn by [`powerline`].
pub fn is_powerline(ch: char) -> bool {
    ('\u{E0B0}'..='\u{E0BF}').contains(&ch)
}

/// Braille: a 2 x 4 grid of square dots, each a whole number of pixels.
fn braille(ch: char, cw: f32, chh: f32) -> Vec<Rect> {
    let bits = ch as u32 - 0x2800;
    // Dot order of the Unicode braille block: 1 2 3 down the left, 4 5 6 down
    // the right, then 7 and 8 on the bottom row.
    const DOTS: [(u32, u32); 8] = [(0, 0), (0, 1), (0, 2), (1, 0), (1, 1), (1, 2), (0, 3), (1, 3)];
    let size = (cw / 4.).round().max(1.);
    let mut out = Vec::new();
    for (i, (col, row)) in DOTS.iter().enumerate() {
        if bits & (1 << i) == 0 {
            continue;
        }
        let x = (cw * (1. + 2. * *col as f32) / 4. - size / 2.).round();
        let y = (chh * (1. + 2. * *row as f32) / 8. - size / 2.).round();
        out.push(Rect { x0: x, y0: y, x1: x + size, y1: y + size, alpha: 1. });
    }
    out
}

/// The polygons for a Powerline separator, in pixels from the cell's
/// top-left corner. `t` is the stroke of the thin variants.
pub fn powerline(ch: char, w: f32, h: f32, t: f32) -> Vec<Vec<(f32, f32)>> {
    let mid = h / 2.;
    // A line from a to b of width t, as a quad.
    let line = |a: (f32, f32), b: (f32, f32)| {
        let (dx, dy) = (b.0 - a.0, b.1 - a.1);
        let len = (dx * dx + dy * dy).sqrt().max(1e-3);
        let (nx, ny) = (-dy / len * t / 2., dx / len * t / 2.);
        vec![(a.0 + nx, a.1 + ny), (b.0 + nx, b.1 + ny), (b.0 - nx, b.1 - ny), (a.0 - nx, a.1 - ny)]
    };
    // Half a disc: the flat side at x0, bulging toward x1.
    let half_disc = |x0: f32, x1: f32| {
        let mut p = vec![(x0, 0.)];
        for i in 0..=16 {
            let a = std::f32::consts::PI * (i as f32 / 16. - 0.5);
            p.push((x0 + (x1 - x0) * a.cos(), mid + mid * a.sin()));
        }
        p.push((x0, h));
        p
    };
    match ch {
        '\u{E0B0}' => vec![vec![(0., 0.), (w, mid), (0., h)]],
        '\u{E0B2}' => vec![vec![(w, 0.), (0., mid), (w, h)]],
        '\u{E0B1}' => vec![line((0., 0.), (w, mid)), line((w, mid), (0., h))],
        '\u{E0B3}' => vec![line((w, 0.), (0., mid)), line((0., mid), (w, h))],
        '\u{E0B4}' => vec![half_disc(0., w)],
        '\u{E0B6}' => vec![half_disc(w, 0.)],
        '\u{E0B5}' => vec![line((0., 0.), (w, mid)), line((w, mid), (0., h))],
        '\u{E0B7}' => vec![line((w, 0.), (0., mid)), line((0., mid), (w, h))],
        '\u{E0B8}' => vec![vec![(0., 0.), (w, h), (0., h)]],
        '\u{E0BA}' => vec![vec![(w, 0.), (w, h), (0., h)]],
        '\u{E0BC}' => vec![vec![(0., 0.), (w, 0.), (0., h)]],
        '\u{E0BE}' => vec![vec![(0., 0.), (w, 0.), (w, h)]],
        '\u{E0B9}' | '\u{E0BF}' => vec![line((0., 0.), (w, h))],
        '\u{E0BB}' | '\u{E0BD}' => vec![line((0., h), (w, 0.))],
        _ => Vec::new(),
    }
}

fn block(ch: char) -> Option<Vec<Rect>> {
    let r = |x0: f32, y0: f32, x1: f32, y1: f32| Rect { x0, y0, x1, y1, alpha: 1. };
    let shade = |a: f32| vec![Rect { x0: 0., y0: 0., x1: 1., y1: 1., alpha: a }];
    Some(match ch {
        '█' => vec![r(0., 0., 1., 1.)],
        '▀' => vec![r(0., 0., 1., 0.5)],
        '▄' => vec![r(0., 0.5, 1., 1.)],
        '▌' => vec![r(0., 0., 0.5, 1.)],
        '▐' => vec![r(0.5, 0., 1., 1.)],
        '░' => shade(0.25),
        '▒' => shade(0.5),
        '▓' => shade(0.75),
        '▔' => vec![r(0., 0., 1., 0.125)],
        '▕' => vec![r(0.875, 0., 1., 1.)],
        '▖' => vec![r(0., 0.5, 0.5, 1.)],
        '▗' => vec![r(0.5, 0.5, 1., 1.)],
        '▘' => vec![r(0., 0., 0.5, 0.5)],
        '▝' => vec![r(0.5, 0., 1., 0.5)],
        '▚' => vec![r(0., 0., 0.5, 0.5), r(0.5, 0.5, 1., 1.)],
        '▞' => vec![r(0.5, 0., 1., 0.5), r(0., 0.5, 0.5, 1.)],
        '▙' => vec![r(0., 0., 0.5, 1.), r(0.5, 0.5, 1., 1.)],
        '▛' => vec![r(0., 0., 1., 0.5), r(0., 0.5, 0.5, 1.)],
        '▜' => vec![r(0., 0., 1., 0.5), r(0.5, 0.5, 1., 1.)],
        '▟' => vec![r(0.5, 0., 1., 1.), r(0., 0.5, 0.5, 1.)],
        '\u{2581}'..='\u{2587}' => {
            let n = ch as u32 - 0x2580;
            vec![r(0., 1. - n as f32 / 8., 1., 1.)]
        }
        '\u{2589}'..='\u{258F}' => {
            let n = 8 - (ch as u32 - 0x2588);
            vec![r(0., 0., n as f32 / 8., 1.)]
        }
        _ => return None,
    })
}

/// The rectangles for `ch` in pixels, given the cell size and the light line
/// thickness. Positions are relative to the cell's top-left corner and
/// rounded to whole pixels so neighbouring cells meet exactly.
pub fn rects(ch: char, cw: f32, ch_h: f32, light: f32) -> Vec<Rect> {
    if is_braille(ch) {
        return braille(ch, cw, ch_h);
    }
    if let Some(b) = block(ch) {
        return b
            .into_iter()
            .map(|r| Rect {
                x0: (r.x0 * cw).round(),
                y0: (r.y0 * ch_h).round(),
                x1: (r.x1 * cw).round(),
                y1: (r.y1 * ch_h).round(),
                alpha: r.alpha,
            })
            .collect();
    }
    let Some([up, right, down, left]) = arms(ch) else {
        return Vec::new();
    };
    let light = light.max(1.).round();
    let heavy = (light * 2.).max(2.);
    let cx = (cw / 2.).floor();
    let cy = (ch_h / 2.).floor();
    let mut out = Vec::new();
    let thick = |w: W| if w == W::H { heavy } else { light };
    let full = |x0: f32, y0: f32, x1: f32, y1: f32| Rect { x0, y0, x1, y1, alpha: 1. };
    // Vertical arms. A vertical arm reaches the centre plus half the width of
    // the horizontal line it meets, so corners close.
    let hthick = thick(if left != W::N { left } else { right });
    let vthick = thick(if up != W::N { up } else { down });
    let gap = light.max(2.);
    for (w, top) in [(up, true), (down, false)] {
        if w == W::N {
            continue;
        }
        let t = thick(w);
        let (y0, y1) = if top { (0., cy + (hthick / 2.).ceil()) } else { (cy - (hthick / 2.).floor(), ch_h) };
        if w == W::D {
            out.push(full(cx - gap - light / 2., y0, cx - gap + light / 2., y1));
            out.push(full(cx + gap - light / 2., y0, cx + gap + light / 2., y1));
        } else {
            let x0 = cx - (t / 2.).floor();
            out.push(full(x0, y0, x0 + t, y1));
        }
    }
    for (w, l) in [(left, true), (right, false)] {
        if w == W::N {
            continue;
        }
        let t = thick(w);
        let (x0, x1) = if l { (0., cx + (vthick / 2.).ceil()) } else { (cx - (vthick / 2.).floor(), cw) };
        if w == W::D {
            out.push(full(x0, cy - gap - light / 2., x1, cy - gap + light / 2.));
            out.push(full(x0, cy + gap - light / 2., x1, cy + gap + light / 2.));
        } else {
            let y0 = cy - (t / 2.).floor();
            out.push(full(x0, y0, x1, y0 + t));
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn horizontal_lines_span_the_cell() {
        let r = rects('─', 9., 18., 1.);
        assert_eq!(r.len(), 2);
        let min = r.iter().map(|r| r.x0).fold(f32::MAX, f32::min);
        let max = r.iter().map(|r| r.x1).fold(f32::MIN, f32::max);
        assert_eq!((min, max), (0., 9.));
    }

    #[test]
    fn corners_meet_at_the_centre() {
        let r = rects('┌', 9., 18., 1.);
        // One arm to the right, one down; both reach past the centre.
        assert!(r.iter().any(|r| r.x1 == 9. && r.x0 <= 4.));
        assert!(r.iter().any(|r| r.y1 == 18. && r.y0 <= 9.));
    }

    #[test]
    fn braille_dots_are_whole_pixels() {
        let r = rects('⣿', 9., 20., 1.);
        assert_eq!(r.len(), 8);
        for d in &r {
            assert_eq!(d.x0, d.x0.round());
            assert_eq!(d.y1 - d.y0, 2.);
            assert!(d.x1 <= 9. && d.y1 <= 20.);
        }
        assert_eq!(rects('⠁', 9., 20., 1.).len(), 1);
        assert!(is_box('⠁') && !is_box('\u{2800}'));
    }

    #[test]
    fn powerline_arrows_fill_the_cell() {
        let p = powerline('\u{E0B0}', 9., 20., 1.);
        assert_eq!(p, vec![vec![(0., 0.), (9., 10.), (0., 20.)]]);
        assert!(is_powerline('\u{E0B6}') && !is_powerline('a'));
        assert_eq!(powerline('\u{E0B4}', 9., 20., 1.)[0].len(), 19);
    }

    #[test]
    fn blocks_round_to_pixels() {
        let r = rects('▄', 9., 17., 1.);
        assert_eq!(r, vec![Rect { x0: 0., y0: 9., x1: 9., y1: 17., alpha: 1. }]);
        assert!(!is_box('a'));
        assert!(is_box('▒'));
    }
}
