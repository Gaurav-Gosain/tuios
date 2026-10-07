//! The stage: the rounded surface the panes sit on, each pane's header row,
//! the splits between panes, the needs-you ring, and the empty states
//! (docs/design/FINAL.md sections 5.1, 5.4 and 5.7).

use super::chrome;
use super::*;
use crate::painter::CursorPaint;
use crate::theme::{hsla, with_alpha};
use ghostty_vt::Rgb;

/// A pane's header text, shaped for one width and one look.
pub struct HeaderLines {
    key: (String, String, bool, u32, Status, u64),
    title: Option<ShapedLine>,
    detail: Option<ShapedLine>,
    pill: Option<ShapedLine>,
}

/// One visible pane this frame.
struct PaneRect {
    id: String,
    pty: String,
    /// Content rectangle and the header row above it.
    content: Bounds<Pixels>,
    header: Bounds<Pixels>,
    /// Which stage edges the pane touches: left, top, right, bottom.
    edges: [bool; 4],
    x: i32,
    y: i32,
}

/// `cubic-bezier(0.2, 0, 0, 1)`, the curve things arrive on.
pub fn decelerate(t: f32) -> f32 {
    bezier(0.2, 0., 0., 1., t)
}

/// `cubic-bezier(0.4, 0, 1, 1)`, the curve things leave on.
pub fn exit(t: f32) -> f32 {
    bezier(0.4, 0., 1., 1., t)
}

fn bezier(x1: f32, y1: f32, x2: f32, y2: f32, t: f32) -> f32 {
    let t = t.clamp(0., 1.);
    let c = |a: f32, b: f32, s: f32| 3. * a * s * (1. - s).powi(2) + 3. * b * s * s * (1. - s) + s.powi(3);
    // Solve x(s) = t by bisection; 20 steps is far below a pixel.
    let (mut lo, mut hi) = (0f32, 1f32);
    for _ in 0..20 {
        let mid = (lo + hi) / 2.;
        if c(x1, x2, mid) < t {
            lo = mid;
        } else {
            hi = mid;
        }
    }
    c(y1, y2, (lo + hi) / 2.)
}

impl TuiosApp {
    pub(super) fn render_grid(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement + use<> {
        let entity = cx.entity();
        let canvas = canvas(
            |_, _, _| {},
            move |bounds, _, window, cx| {
                entity.update(cx, |this, cx| this.paint_grid(bounds, window, cx));
            },
        )
        .size_full();
        let empty = self.empty_state(cx);
        let lay = self.layout;
        let x0 = if lay.overlay || lay.side_w == 0. { 0. } else { lay.side_w };
        let stage = Bounds::new(point(lay.stage.origin.x - px(x0), lay.stage.origin.y - px(BAND_H)), lay.stage.size);
        div()
            .id("grid")
            .size_full()
            .relative()
            .cursor(CursorStyle::IBeam)
            .on_mouse_down(MouseButton::Left, cx.listener(Self::on_mouse_down))
            .on_mouse_down(MouseButton::Middle, cx.listener(Self::on_mouse_down))
            .on_mouse_down(MouseButton::Right, cx.listener(Self::on_mouse_down))
            .on_mouse_move(cx.listener(Self::on_mouse_move))
            .on_mouse_up(MouseButton::Left, cx.listener(Self::on_mouse_up))
            .on_mouse_up(MouseButton::Middle, cx.listener(Self::on_mouse_up))
            .on_mouse_up(MouseButton::Right, cx.listener(Self::on_mouse_up))
            .on_scroll_wheel(cx.listener(Self::on_scroll))
            .child(canvas)
            .children(empty.map(|e| {
                div()
                    .absolute()
                    .left(stage.origin.x)
                    .top(stage.origin.y)
                    .w(stage.size.width)
                    .h(stage.size.height)
                    .flex()
                    .items_center()
                    .justify_center()
                    .cursor(CursorStyle::Arrow)
                    .child(e)
            }))
    }

    /// The empty or waiting state for the stage, when there is one.
    fn empty_state(&mut self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let t = self.theme.clone();
        let column = || div().w(px(320.)).flex().flex_col().items_center().text_center();
        let title = |s: &str| chrome::text(s.to_string(), chrome::TITLE, FontWeight::SEMIBOLD, t.text);
        let line = |s: String| chrome::text(s, chrome::BODY, FontWeight::NORMAL, t.text2).mt(px(12.));
        Some(match &self.link {
            Link::Connecting(since) => {
                if since.elapsed() < CONNECT_QUIET {
                    return None;
                }
                column().child(chrome::text("Connecting to tuios".into(), chrome::BODY, FontWeight::NORMAL, t.text2)).into_any_element()
            }
            Link::Failed(why) => column()
                .child(title("Cannot reach tuios"))
                .child(line("Start it with tuios daemon, then try again.".into()))
                .child(
                    div()
                        .mt(px(12.))
                        .text_size(px(12.))
                        .line_height(px(16.))
                        .font_family(SharedString::from(self.cfg.font_family.clone()))
                        .text_color(rgb(t.text3))
                        .child(SharedString::from(why.clone())),
                )
                .child(
                    div().mt(px(16.)).child(chrome::button(&t, "retry", "Try again", "").on_click(cx.listener(|this, _, window, cx| {
                        let s = this.cfg.session.clone();
                        this.connect(s, window, cx);
                    }))),
                )
                .into_any_element(),
            Link::Ended => column()
                .child(title("The session ended"))
                .child(line("Pick another session or start a new one.".into()))
                .child(div().mt(px(16.)).child(chrome::button(&t, "new-session", "New session", "").on_click(cx.listener(|this, _, window, cx| this.run(Act::NewSession, window, cx)))))
                .into_any_element(),
            Link::Attached => {
                let st = self.state.as_ref()?;
                if !st.visible().is_empty() || st.windows.is_empty() {
                    return None;
                }
                column()
                    .child(title("No panes here"))
                    .child(line("Open a terminal to start.".into()))
                    .child(
                        div()
                            .mt(px(16.))
                            .flex()
                            .gap(px(8.))
                            .child(chrome::button(&t, "new-pane", "New pane", "ctrl+shift+t").on_click(cx.listener(|this, _, window, cx| this.run(Act::Tape("NewWindow", &[]), window, cx))))
                            .child(chrome::text_button(&t, "open-palette", "Open palette").on_click(cx.listener(|this, _, _, cx| this.open_palette(cx)))),
                    )
                    .into_any_element()
            }
        })
    }

    /// The visible panes with their rectangles, in stacking order.
    fn pane_rects(&self, m: &Metrics) -> Vec<PaneRect> {
        let Some(st) = self.state.as_ref() else { return Vec::new() };
        let (cw, ch) = (f32::from(m.cell_w), f32::from(m.cell_h));
        let o = self.grid.origin;
        let (gc, gr) = (self.grid.cols as i32, self.grid.rows as i32);
        st.visible()
            .into_iter()
            .map(|w| {
                let (x, y, c, r) = w.content();
                let content = Bounds::new(point(o.x + px(x as f32 * cw), o.y + px(y as f32 * ch)), size(px(c as f32 * cw), px(r as f32 * ch)));
                let header = Bounds::new(point(content.origin.x, content.origin.y - px(ch)), size(content.size.width, px(ch)));
                PaneRect { id: w.id.clone(), pty: w.pty.clone(), content, header, edges: [x <= 0, y <= 0, x + c >= gc, y + r >= gr], x, y }
            })
            .collect()
    }

    /// Shapes `text` in the UI font, cut with an ellipsis to fit `max`.
    pub(super) fn ui_line(&self, text: &str, size: f32, weight: FontWeight, color: impl Into<Hsla>, max: f32, window: &mut Window) -> Option<ShapedLine> {
        let color: Hsla = color.into();
        if text.is_empty() || max < 12. {
            return None;
        }
        let mut f = gpui::font(SharedString::from(self.cfg.ui_font.clone()));
        f.weight = weight;
        let shape = |s: String, window: &mut Window| {
            let run = TextRun { len: s.len(), font: f.clone(), color, background_color: None, underline: None, strikethrough: None };
            window.text_system().shape_line(SharedString::from(s), px(size), &[run], None)
        };
        let line = shape(text.to_string(), window);
        if f32::from(line.width) <= max {
            return Some(line);
        }
        // Measure once, cut at the character that crosses the limit, and
        // step back only if the ellipsis still does not fit.
        let ell = f32::from(shape("…".into(), window).width);
        let mut cut = line.closest_index_for_x(px((max - ell).max(0.)));
        while cut > 0 {
            while cut > 0 && !text.is_char_boundary(cut) {
                cut -= 1;
            }
            let l = shape(format!("{}…", text[..cut].trim_end()), window);
            if f32::from(l.width) <= max {
                return Some(l);
            }
            cut -= 1;
        }
        None
    }

    /// Draws a state icon centred on `center`: the 16 px slot of section 6.
    /// A working icon's arc is drawn by the spin view, from `slots`.
    fn paint_state(&self, status: Status, center: Point<Pixels>, mask: ContentMask<Pixels>, window: &mut Window, cx: &mut App) {
        let t = &self.theme;
        let b = Bounds::new(point(center.x - px(8.), center.y - px(8.)), size(px(16.), px(16.)));
        let unit = TransformationMatrix::unit();
        let mut svg = |path: &'static str, color: Rgba| {
            let _ = window.paint_svg(b, path.into(), None, unit, color.into(), cx);
        };
        match status {
            Status::NeedsYou => svg("icons/state-needs.svg", rgb(t.need)),
            Status::Errored => {
                svg("icons/state-disc.svg", rgb(t.err));
                svg("icons/mark-x.svg", rgb(t.on_state));
            }
            Status::Working => {
                svg("icons/state-ring.svg", with_alpha(t.text3, 0.5));
                self.spin_slots.borrow_mut().grid.push(views::Slot { bounds: b, mask });
            }
            Status::Done => {
                svg("icons/state-disc.svg", rgb(t.done));
                svg("icons/mark-check.svg", rgb(t.on_state));
            }
            Status::Idle => svg("icons/state-ring.svg", rgb(t.text3)),
            Status::Terminal => svg("icons/state-dot.svg", rgb(t.text3)),
        }
    }

    /// A pane's header: state icon, title, detail, and at the right the
    /// "Needs you" pill. No fill, no age (section 5.4).
    fn paint_header(&mut self, info: &PaneInfo, rect: Bounds<Pixels>, focused: bool, arrive: f32, mask: ContentMask<Pixels>, window: &mut Window, cx: &mut App) {
        let t = self.theme.clone();
        let m = self.metrics.clone().expect("metrics");
        let snap = |v: f32| m.snap(v);
        let ch = f32::from(rect.size.height);
        let x0 = f32::from(rect.origin.x);
        let top = f32::from(rect.origin.y);
        let right = x0 + f32::from(rect.size.width);
        self.paint_state(info.status, point(px(x0 + 8.), px(snap(top + ch / 2.))), mask, window, cx);
        let baseline = snap(top + (ch * 0.7).round());
        let needs = info.status == Status::NeedsYou;
        let width = f32::from(rect.size.width).round() as u32;
        let detail_text = info.detail();
        let key = (info.name.clone(), detail_text.clone(), focused, width, info.status, self.epoch);
        if self.headers.get(&info.window).is_none_or(|h| h.key != key) {
            let pill = needs.then(|| self.ui_line("Needs you", chrome::LABEL.0, FontWeight::SEMIBOLD, rgb(t.need_ink), 200., window)).flatten();
            let pill_w = pill.as_ref().map(|p| f32::from(p.width) + 12.).unwrap_or(0.);
            let limit = right - x0 - 22. - if pill_w > 0. { pill_w + 8. } else { 4. };
            let (tc, tw) = if focused { (t.text, FontWeight::MEDIUM) } else { (t.text2, FontWeight::NORMAL) };
            // The detail goes first when space runs out, then the title.
            let full = self.ui_line(&info.name, chrome::SMALL.0, tw, rgb(tc), f32::MAX, window);
            let title_w = full.as_ref().map(|l| f32::from(l.width)).unwrap_or(0.);
            let title = if title_w <= limit { full } else { self.ui_line(&info.name, chrome::SMALL.0, tw, rgb(tc), limit, window) };
            let used = title.as_ref().map(|l| f32::from(l.width)).unwrap_or(0.);
            let room = limit - used - 10.;
            let detail = if room >= 40. { self.ui_line(&detail_text, chrome::SMALL.0, FontWeight::NORMAL, rgb(t.text3), room, window) } else { None };
            self.headers.insert(info.window.clone(), HeaderLines { key, title, detail, pill });
        }
        let h = &self.headers[&info.window];
        let paint_line = |l: &ShapedLine, x: f32, window: &mut Window, cx: &mut App| {
            let _ = l.paint(point(px(snap(x)), px(baseline) - l.ascent), l.ascent + l.descent, TextAlign::Left, None, window, cx);
        };
        let mut x = x0 + 22.;
        if let Some(l) = &h.title {
            paint_line(l, x, window, cx);
            x += f32::from(l.width) + 10.;
        }
        if let Some(l) = &h.detail {
            paint_line(l, x, window, cx);
        }
        if let Some(l) = h.pill.clone() {
            let w = (f32::from(l.width) + 12.).round();
            let pill = Bounds::new(point(px(snap(right - w)), px(snap(top + (ch - 16.) / 2.))), size(px(w), px(16.)));
            // While it arrives, the pill fades in: its text is shaped again
            // at the frame's alpha, for those few frames only.
            let l = if arrive < 1. { self.ui_line("Needs you", chrome::LABEL.0, FontWeight::SEMIBOLD, with_alpha(t.need_ink, arrive), 200., window).unwrap_or(l) } else { l };
            window.paint_quad(fill(pill, with_alpha(t.need_fill, arrive)).corner_radii(px(8.)));
            let base = f32::from(pill.origin.y) + 12.;
            let _ = l.paint(point(pill.origin.x + px(6.), px(base) - l.ascent), l.ascent + l.descent, TextAlign::Left, None, window, cx);
        }
    }

    pub(super) fn paint_grid(&mut self, bounds: Bounds<Pixels>, window: &mut Window, cx: &mut Context<Self>) {
        let started = Instant::now();
        let Some(m) = self.metrics.clone() else { return };
        let t = self.theme.clone();
        let lay = self.layout;
        let s = m.scale;
        let one = px(1. / s);
        let _ = bounds;
        // The stage: one bordered quad.
        window.paint_quad(quad(lay.stage, px(10.), rgb(t.stage), one, t.border, BorderStyle::Solid));
        let stage_mask = ContentMask { bounds: lay.stage };
        self.spin_slots.borrow_mut().grid.clear();
        if self.state.is_none() {
            self.stats.record_paint(started.elapsed());
            return;
        }
        let cw = f32::from(m.cell_w);
        let rects = self.pane_rects(&m);
        let multi = rects.len() > 1;
        let focused = self.focused_id();
        let mut more_frames = false;
        let now = Instant::now();
        let blink_on = self.blink_on();
        let infos: HashMap<String, PaneInfo> = self.attached.iter().map(|p| (p.window.clone(), p.clone())).collect();
        let inner = lay.stage.dilate(-one);

        for r in &rects {
            let is_focused = focused.as_deref() == Some(r.id.as_str());
            let arrive = self.need_since.get(&r.id).map(|t0| decelerate(t0.elapsed().as_secs_f32() / ARRIVE.as_secs_f32())).unwrap_or(1.);
            if arrive < 1. {
                more_frames = true;
            }
            if let Some(info) = infos.get(&r.id) {
                self.paint_header(info, r.header, is_focused || !multi, arrive, stage_mask, window, cx);
            }
            let Some(pane) = self.panes.get_mut(&r.pty) else { continue };
            pane.shown = true;
            let y_off = pane.scroll_px;
            let screen_bg = pane.term.snapshot().bg;
            // A program that set its own background fills its content grown
            // into the gaps, and out to the stage edge where it touches it.
            if screen_bg.to_u32() != t.stage {
                let grow = 4.;
                let c = r.content;
                let x0 = if r.edges[0] { f32::from(inner.origin.x) } else { f32::from(c.origin.x) - grow };
                let x1 = if r.edges[2] { f32::from(inner.right()) } else { f32::from(c.right()) + grow };
                let y1 = if r.edges[3] { f32::from(inner.bottom()) } else { f32::from(c.bottom()) + grow };
                let y0 = f32::from(c.origin.y);
                let rad = px(9.);
                let corners = Corners {
                    top_left: px(0.),
                    top_right: px(0.),
                    bottom_left: if r.edges[0] && r.edges[3] { rad } else { px(0.) },
                    bottom_right: if r.edges[2] && r.edges[3] { rad } else { px(0.) },
                };
                window.paint_quad(fill(Bounds::new(point(px(x0), px(y0)), size(px(x1 - x0), px(y1 - y0))), hsla(screen_bg)).corner_radii(corners));
            }
            {
                let Pane { term, painter, .. } = pane;
                painter.prepare(term.snapshot(), &m, &t, window);
            }
            if y_off > 0. {
                let above = pane.term.row_above().cloned();
                pane.painter.prepare_above(above.as_ref(), screen_bg, &m, &t, window);
            }
            let Pane { term, painter, scrolled_at, .. } = pane;
            let cursor = CursorPaint {
                visible: y_off == 0. && term.at_bottom() && (blink_on || !is_focused),
                focused: is_focused && self.window_active,
                color: Rgb::from_u32(t.cursor),
            };
            window.with_content_mask(Some(ContentMask { bounds: r.content }), |window| {
                painter.paint(term.screen(), r.content.origin, &m, cursor, y_off, window, cx);
            });
            if multi && !is_focused {
                // Panes without focus sit back; the header keeps its marks.
                window.paint_quad(fill(r.content, with_alpha(t.stage, t.dim)));
            }
            // The scrollbar shows while scrolling, then fades.
            if let Some(at) = *scrolled_at {
                let age = now.saturating_duration_since(at);
                if age < SCROLLBAR + SCROLLBAR_FADE {
                    let fade = if age > SCROLLBAR { 1. - exit((age - SCROLLBAR).as_secs_f32() / SCROLLBAR_FADE.as_secs_f32()) } else { 1. };
                    if age > SCROLLBAR {
                        more_frames = true;
                    }
                    paint_scrollbar(term, r.content, &t, fade, s, window);
                } else {
                    *scrolled_at = None;
                }
            }
        }
        // Panes out of sight drop their row caches; they keep their history.
        let shown: HashSet<&str> = rects.iter().map(|r| r.pty.as_str()).collect();
        for (pty, p) in self.panes.iter_mut() {
            if p.shown && !shown.contains(pty.as_str()) {
                p.shown = false;
                p.painter.clear();
            }
        }

        // Splits: 1 px hairlines on the gap centre lines.
        let line = t.hairline;
        for r in &rects {
            let left = f32::from(r.content.origin.x);
            let top = f32::from(r.header.origin.y);
            let bottom = f32::from(r.content.bottom());
            if r.x > 0 {
                let gx = m.snap(left - cw / 2.);
                window.paint_quad(fill(Bounds::new(point(px(gx), px(top)), size(one, px(bottom - top))), line));
            }
            if r.y > 1 {
                let x0 = if r.x > 0 { left - cw / 2. } else { f32::from(inner.origin.x) };
                let x1 = if r.edges[2] { f32::from(inner.right()) } else { f32::from(r.content.right()) + cw / 2. };
                let (x0, x1) = (m.snap(x0), m.snap(x1));
                window.paint_quad(fill(Bounds::new(point(px(x0), px(m.snap(top))), size(px(x1 - x0), one)), line));
            }
        }

        // The one coloured frame: around a pane that needs you.
        for r in &rects {
            if infos.get(&r.id).is_none_or(|i| i.status != Status::NeedsYou) {
                continue;
            }
            let c = &r.content;
            let x0 = if r.edges[0] { f32::from(lay.stage.origin.x) + 4. } else { f32::from(c.origin.x) - cw / 2. };
            let x1 = if r.edges[2] { f32::from(lay.stage.right()) - 4. } else { f32::from(c.right()) + cw / 2. };
            let y0 = f32::from(r.header.origin.y) - 3.;
            let y1 = if r.edges[3] { f32::from(lay.stage.bottom()) - 4. } else { f32::from(c.bottom()) + 3. };
            let ring = Bounds::new(point(px(m.snap(x0)), px(m.snap(y0))), size(px(m.snap(x1 - x0)), px(m.snap(y1 - y0))));
            let arrive = self.need_since.get(&r.id).map(|t0| (t0.elapsed().as_secs_f32() / ARRIVE.as_secs_f32()).min(1.)).unwrap_or(1.);
            // The glow peaks at 40 % on the way in, then rests at 16 %.
            let glow = if arrive < 1. { if arrive < 0.5 { 0.4 * arrive * 2. } else { 0.4 - 0.24 * (arrive - 0.5) * 2. } } else { 0.16 };
            window.paint_quad(quad(ring.dilate(px(3.)), px(9.), transparent_black(), px(3.), with_alpha(t.need, glow), BorderStyle::Solid));
            window.paint_quad(quad(ring, px(6.), transparent_black(), one, with_alpha(t.need, decelerate(arrive)), BorderStyle::Solid));
        }

        if let Some(at) = self.resized_at {
            let age = now.saturating_duration_since(at);
            if age < BADGE + BADGE_FADE {
                let a = if age > BADGE { 1. - exit((age - BADGE).as_secs_f32() / BADGE_FADE.as_secs_f32()) } else { 1. };
                if age > BADGE {
                    more_frames = true;
                }
                self.paint_size_badge(lay.stage, a, window, cx);
            } else {
                self.resized_at = None;
            }
        }
        if self.cfg.show_fps {
            if let Some((p50, p95)) = self.stats.paint_percentiles() {
                let s = format!("paint p50 {p50:.2} ms  p95 {p95:.2} ms");
                if let Some(l) = self.ui_line(&s, 11., FontWeight::MEDIUM, rgb(t.text3), 400., window) {
                    let x = lay.stage.right() - l.width - px(16.);
                    let _ = l.paint(point(x, lay.stage.bottom() - px(18.)), px(14.), TextAlign::Left, None, window, cx);
                }
            }
        }

        let title = rects
            .iter()
            .find(|r| focused.as_deref() == Some(r.id.as_str()))
            .and_then(|r| infos.get(&r.id))
            .map(|i| format!("{} - {} - tuios", i.name, self.current_session()))
            .unwrap_or_else(|| "tuios".into());
        if title != self.last_title {
            window.set_window_title(&title);
            self.last_title = title;
        }

        self.paint_preedit(window, cx);
        window.handle_input(&self.focus, ElementInputHandler::new(lay.stage, cx.entity()), cx);

        if more_frames || self.animating {
            self.animating = more_frames;
            window.request_animation_frame();
        }
        self.stats.record_paint(started.elapsed());
    }

    /// "128 × 41" in the middle of the stage while the window resizes.
    fn paint_size_badge(&self, stage: Bounds<Pixels>, alpha: f32, window: &mut Window, cx: &mut App) {
        let t = &self.theme;
        let text = format!("{} × {}", self.grid.cols, self.grid.rows);
        let Some(l) = self.ui_line(&text, chrome::BODY.0, FontWeight::MEDIUM, with_alpha(t.text, alpha), 300., window) else { return };
        let (w, h) = ((f32::from(l.width) + 24.).round(), 32.);
        let c = stage.center();
        let b = Bounds::new(point(px((f32::from(c.x) - w / 2.).round()), px((f32::from(c.y) - h / 2.).round())), size(px(w), px(h)));
        window.paint_quad(quad(b, px(8.), with_alpha(t.raised, alpha), px(1.), Theme::fade(t.border, alpha), BorderStyle::Solid));
        let base = f32::from(b.origin.y) + 20.;
        let _ = l.paint(point(b.origin.x + px(12.), px(base) - l.ascent), l.ascent + l.descent, TextAlign::Left, None, window, cx);
    }
}

/// The overlay scrollbar: a 4 px thumb, 2 px in from the pane's right edge.
fn paint_scrollbar(term: &ghostty_vt::Terminal, rect: Bounds<Pixels>, t: &Theme, fade: f32, scale: f32, window: &mut Window) {
    let (total, offset, len) = term.scrollbar();
    if total <= len || total == 0 {
        return;
    }
    let h = f32::from(rect.size.height);
    let thumb = (len as f32 / total as f32 * h).max(24.);
    let top = offset as f32 / (total - len) as f32 * (h - thumb);
    let snap = |v: f32| (v * scale).round() / scale;
    let x = snap(f32::from(rect.right()) - 6.);
    let y = snap(f32::from(rect.origin.y) + top);
    window.paint_quad(fill(Bounds::new(point(px(x), px(y)), size(px(4.), px(snap(thumb)))), with_alpha(t.text3, 0.5 * fade)).corner_radii(px(2.)));
}

#[cfg(test)]
mod tests {
    use super::{decelerate, exit};

    #[test]
    fn curves_start_and_end_in_place() {
        for f in [decelerate, exit] {
            assert!(f(0.).abs() < 1e-3);
            assert!((f(1.) - 1.).abs() < 1e-3);
        }
        assert!(decelerate(0.3) > 0.5, "decelerate is fast at first");
        assert!(exit(0.3) < 0.3, "exit is slow at first");
    }
}
