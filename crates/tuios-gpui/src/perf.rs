//! The performance harness: a dense 160x50 pane driven through the same
//! emulator and painter as the app, inside a real window, with each phase of
//! a frame timed. Run with `tuios-gpui --perf`.
//!
//! Timed per frame:
//! - feed: writing the frame's bytes into the ghostty emulator;
//! - prepare: copying dirty rows out of ghostty, planning and shaping them;
//! - paint: building the GPUI scene for the pane (backgrounds, glyphs,
//!   decorations);
//! - interval: time between consecutive frames, which includes GPUI's own
//!   layout, the GPU submit and the wait for the compositor.

use crate::app::Config;
use crate::painter::{CursorPaint, Metrics};
use crate::pane::Pane;
use crate::stats::percentile;
use crate::theme;
use ghostty_vt::Rgb;
use gpui::*;
use std::path::PathBuf;
use std::time::Instant;

const COLS: u16 = 160;
const ROWS: u16 = 50;
const FRAMES: usize = 240;
const WARMUP: usize = 20;

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum Phase {
    /// Every row rewritten with new text each frame.
    Full,
    /// Five new lines per frame, so the whole screen scrolls (like `cat`).
    Stream,
    /// One row changes per frame (typing).
    Typing,
    /// Nothing changes; the frame only repaints.
    Idle,
    /// Scrolled back into history, moving by a few pixels per frame.
    SmoothScroll,
}

const PHASES: [Phase; 5] = [Phase::Full, Phase::Stream, Phase::Typing, Phase::Idle, Phase::SmoothScroll];

#[derive(Default, Clone)]
struct Samples {
    feed: Vec<f64>,
    prepare: Vec<f64>,
    paint: Vec<f64>,
    interval: Vec<f64>,
    glyphs: u64,
    quads: u64,
    rows_shaped: u64,
    frames: u64,
}

pub struct PerfView {
    cfg: Config,
    out: Option<PathBuf>,
    pane: Option<Pane>,
    metrics: Option<Metrics>,
    phase: usize,
    frame: usize,
    seed: u64,
    last: Option<Instant>,
    results: Vec<(Phase, Samples)>,
    current: Samples,
    done: bool,
}

impl PerfView {
    pub fn new(cfg: Config, out: Option<PathBuf>, _window: &mut Window, _cx: &mut Context<Self>) -> Self {
        PerfView { cfg, out, pane: None, metrics: None, phase: 0, frame: 0, seed: 0x2545F4914F6CDD1D, last: None, results: Vec::new(), current: Samples::default(), done: false }
    }

    fn rand(&mut self) -> u64 {
        // xorshift64*
        self.seed ^= self.seed >> 12;
        self.seed ^= self.seed << 25;
        self.seed ^= self.seed >> 27;
        self.seed.wrapping_mul(0x2545F4914F6CDD1D)
    }

    /// One dense row of styled text, exactly COLS cells wide.
    fn line(&mut self, out: &mut Vec<u8>) {
        const WORDS: [&str; 12] = ["fn", "let", "match", "tuios", "render", "ghostty", "->", "!=", "=>", "0x1f", "pane", "async"];
        let mut col = 0usize;
        while col < COLS as usize {
            let r = self.rand();
            let fg = 16 + (r % 216) as u8;
            if r % 7 == 0 {
                let bg = 232 + ((r >> 8) % 24) as u8;
                out.extend_from_slice(format!("\x1b[38;5;{fg};48;5;{bg}m").as_bytes());
            } else if r % 5 == 0 {
                out.extend_from_slice(format!("\x1b[1;38;2;{};{};{}m", (r >> 16) as u8, (r >> 24) as u8, (r >> 32) as u8).as_bytes());
            } else {
                out.extend_from_slice(format!("\x1b[38;5;{fg}m").as_bytes());
            }
            let word: &str = if r % 23 == 0 { "漢字" } else if r % 29 == 0 { "│─┼" } else { WORDS[((r >> 40) % WORDS.len() as u64) as usize] };
            let w = unicode_cols(word);
            if col + w + 1 > COLS as usize {
                break;
            }
            out.extend_from_slice(word.as_bytes());
            out.extend_from_slice(b"\x1b[0m ");
            col += w + 1;
        }
        out.extend_from_slice(b"\x1b[K");
    }

    fn input_for(&mut self, phase: Phase) -> Vec<u8> {
        let mut out = Vec::with_capacity(32 << 10);
        match phase {
            Phase::Full => {
                out.extend_from_slice(b"\x1b[H");
                for y in 0..ROWS {
                    self.line(&mut out);
                    if y + 1 < ROWS {
                        out.extend_from_slice(b"\r\n");
                    }
                }
            }
            Phase::Stream => {
                for _ in 0..5 {
                    out.extend_from_slice(b"\r\n");
                    self.line(&mut out);
                }
            }
            Phase::Typing => {
                let c = b'a' + (self.frame % 26) as u8;
                out.push(c);
            }
            Phase::Idle | Phase::SmoothScroll => {}
        }
        out
    }

    fn step(&mut self, bounds: Bounds<Pixels>, window: &mut Window, cx: &mut App) {
        if self.done {
            return;
        }
        let now = Instant::now();
        if self.metrics.is_none() {
            self.metrics = Some(Metrics::new(&self.cfg.font_family, self.cfg.font_size, self.cfg.line_height, window, 1));
        }
        let m = self.metrics.clone().expect("metrics");
        let t = theme::NIGHT;
        let pane = self.pane.get_or_insert_with(|| {
            let mut p = Pane::new(COLS, ROWS, &t);
            // History for the scroll phase.
            p.write(b"\x1b[2J\x1b[H");
            p
        });
        let _ = pane;
        let phase = PHASES[self.phase];
        if self.frame == 0 && phase == Phase::SmoothScroll {
            let p = self.pane.as_mut().expect("pane");
            p.term.scroll_delta(-(ROWS as isize) * 2);
        }

        let input = self.input_for(phase);
        let p = self.pane.as_mut().expect("pane");
        let t0 = Instant::now();
        p.write(&input);
        if phase == Phase::SmoothScroll {
            p.scroll_pixels(3.0, f32::from(m.cell_h));
        }
        let feed = t0.elapsed();

        let t1 = Instant::now();
        let bg = {
            let Pane { term, painter, .. } = p;
            let s = term.snapshot();
            painter.prepare(s, &m, &t, window);
            s.bg
        };
        if p.scroll_px > 0. {
            let above = p.term.row_above().cloned();
            p.painter.prepare_above(above.as_ref(), bg, &m, &t, window);
        }
        let prepare = t1.elapsed();

        let before = p.painter.stats;
        let t2 = Instant::now();
        let origin = bounds.origin;
        let y_off = p.scroll_px;
        let Pane { term, painter, .. } = p;
        let rect = Bounds::new(origin, size(m.cell_w * COLS as f32, m.cell_h * ROWS as f32));
        window.with_content_mask(Some(ContentMask { bounds: rect }), |window| {
            painter.paint(term.screen(), origin, &m, CursorPaint { visible: true, focused: true, color: Rgb::from_u32(t.cursor) }, y_off, window, cx);
        });
        let paint = t2.elapsed();
        let after = p.painter.stats;

        if self.frame >= WARMUP {
            let s = &mut self.current;
            s.feed.push(feed.as_secs_f64() * 1e3);
            s.prepare.push(prepare.as_secs_f64() * 1e3);
            s.paint.push(paint.as_secs_f64() * 1e3);
            if let Some(last) = self.last {
                s.interval.push(now.duration_since(last).as_secs_f64() * 1e3);
            }
            s.glyphs += after.glyphs - before.glyphs;
            s.quads += after.quads - before.quads;
            s.rows_shaped += after.rows_shaped.saturating_sub(before.rows_shaped);
            s.frames += 1;
        }
        // rows_shaped is counted in prepare, which ran before `before` was
        // read; account for it from the painter's running total instead.
        self.last = Some(now);
        self.frame += 1;
        if self.frame >= FRAMES + WARMUP {
            let mut done = std::mem::take(&mut self.current);
            done.rows_shaped = 0;
            self.results.push((phase, done));
            self.phase += 1;
            self.frame = 0;
            self.last = None;
            if self.phase >= PHASES.len() {
                self.finish(&m, window, cx);
                return;
            }
        }
        window.request_animation_frame();
    }

    fn finish(&mut self, m: &Metrics, window: &mut Window, cx: &mut App) {
        self.done = true;
        let mut json = String::from("{\n");
        json.push_str(&format!(
            "  \"grid\": \"{COLS}x{ROWS}\",\n  \"font\": \"{}\",\n  \"font_size\": {},\n  \"cell\": [{:.2}, {:.2}],\n  \"scale_factor\": {},\n  \"frames_per_phase\": {FRAMES},\n  \"phases\": {{\n",
            self.cfg.font_family,
            self.cfg.font_size,
            f32::from(m.cell_w),
            f32::from(m.cell_h),
            window.scale_factor()
        ));
        println!("phase           feed p50/p95    prepare p50/p95   paint p50/p95    interval p50/p95   glyphs/f  quads/f");
        let n = self.results.len();
        for (i, (phase, s)) in self.results.iter().enumerate() {
            let pc = |v: &Vec<f64>| (percentile(v, 50.).unwrap_or(0.), percentile(v, 95.).unwrap_or(0.));
            let (f50, f95) = pc(&s.feed);
            let (r50, r95) = pc(&s.prepare);
            let (p50, p95) = pc(&s.paint);
            let (i50, i95) = pc(&s.interval);
            let frames = s.frames.max(1);
            println!(
                "{:<14} {:>6.3} {:>6.3}   {:>7.3} {:>7.3}   {:>6.3} {:>6.3}   {:>7.2} {:>7.2}   {:>8} {:>8}",
                format!("{phase:?}"),
                f50,
                f95,
                r50,
                r95,
                p50,
                p95,
                i50,
                i95,
                s.glyphs / frames,
                s.quads / frames
            );
            json.push_str(&format!(
                "    \"{phase:?}\": {{\"feed_ms\": [{f50:.3}, {f95:.3}], \"prepare_ms\": [{r50:.3}, {r95:.3}], \"paint_ms\": [{p50:.3}, {p95:.3}], \"interval_ms\": [{i50:.2}, {i95:.2}], \"glyphs_per_frame\": {}, \"quads_per_frame\": {}}}{}\n",
                s.glyphs / frames,
                s.quads / frames,
                if i + 1 < n { "," } else { "" }
            ));
        }
        json.push_str("  }\n}\n");
        if let Some(path) = &self.out {
            let _ = std::fs::write(path, &json);
        }
        cx.quit();
    }
}

fn unicode_cols(s: &str) -> usize {
    s.chars().map(|c| if (c as u32) >= 0x1100 && !('\u{2500}'..='\u{257f}').contains(&c) { 2 } else { 1 }).sum()
}

impl Render for PerfView {
    fn render(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let entity = cx.entity();
        div().size_full().bg(rgb(theme::NIGHT.bg)).child(
            canvas(|_, _, _| {}, move |bounds, _, window, cx| {
                entity.update(cx, |this, cx| this.step(bounds, window, cx));
            })
            .size_full(),
        )
    }
}
