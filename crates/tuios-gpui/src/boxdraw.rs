//! Box-drawing and block characters drawn as rectangles snapped to the cell
//! grid, so lines join across cells with no gaps whatever the font. Characters
//! not listed here fall back to the font.

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

pub fn is_box(ch: char) -> bool {
    arms(ch).is_some() || block(ch).is_some()
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
    fn blocks_round_to_pixels() {
        let r = rects('▄', 9., 17., 1.);
        assert_eq!(r, vec![Rect { x0: 0., y0: 9., x1: 9., y1: 17., alpha: 1. }]);
        assert!(!is_box('a'));
        assert!(is_box('▒'));
    }
}
