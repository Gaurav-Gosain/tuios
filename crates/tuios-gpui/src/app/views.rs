//! The window's parts as views of their own, so GPUI can cache each one and
//! redraw only the part that changed (docs/design/FINAL.md section 10).
//!
//! A [`Part`] renders one part of [`TuiosApp`] through the app entity, so its
//! listeners act on the app. Each part observes the app: a notify on the app
//! redraws every part. The hot paths (pane output, fleet events, palette
//! keys) notify only their own part.
//!
//! [`Spin`] draws the arcs of the working icons over everything but the
//! palette. The sidebar and the pane headers draw the static ring and
//! record where each arc goes; the spinner timer redraws only this view, so
//! a working agent never redraws the sidebar or the stage.

use super::*;

#[derive(Clone, Copy)]
pub enum PartKind {
    Band,
    Sidebar,
    Grid,
    Palette,
}

pub struct Part {
    app: WeakEntity<TuiosApp>,
    kind: PartKind,
}

impl Render for Part {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let kind = self.kind;
        self.app
            .update(cx, |app, cx| match kind {
                PartKind::Band => app.render_band(window, cx).into_any_element(),
                PartKind::Sidebar => app.render_sidebar(window, cx).into_any_element(),
                PartKind::Grid => app.render_grid(window, cx).into_any_element(),
                PartKind::Palette => app.render_palette(window, cx).unwrap_or_else(|| div().into_any_element()),
            })
            .unwrap_or_else(|_| div().into_any_element())
    }
}

/// Where a working icon's arc goes, and the clip it is drawn in.
#[derive(Clone, Copy, Debug)]
pub struct Slot {
    pub bounds: Bounds<Pixels>,
    pub mask: ContentMask<Pixels>,
}

/// The arcs to draw, recorded by their owners when they paint.
pub struct SpinSlots {
    pub sidebar: Vec<Slot>,
    pub grid: Vec<Slot>,
    accent: u32,
    /// The palette is open: its scrim covers the arcs.
    pub hidden: bool,
    reduce_motion: bool,
    start: Instant,
}

impl SpinSlots {
    pub fn new(theme: &Theme, reduce_motion: bool) -> Self {
        SpinSlots { sidebar: Vec::new(), grid: Vec::new(), accent: theme.accent, hidden: false, reduce_motion, start: Instant::now() }
    }

    pub fn set_theme(&mut self, theme: &Theme) {
        self.accent = theme.accent;
    }
}

/// The 2.4 s pulse of the working arc: 100 % to 45 % and back, a sine.
pub fn pulse(elapsed: Duration) -> f32 {
    let phase = (elapsed.as_secs_f32() / 2.4) * std::f32::consts::TAU;
    0.725 + 0.275 * phase.cos()
}

pub struct Spin {
    slots: Rc<RefCell<SpinSlots>>,
    app: WeakEntity<TuiosApp>,
}

impl Render for Spin {
    fn render(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let slots = self.slots.clone();
        // The arcs pulse only while the spinner timer runs.
        let moving = self.app.upgrade().is_some_and(|a| a.read(cx).spin_task.is_some());
        canvas(
            |_, _, _| {},
            move |_, _, window, cx| {
                let s = slots.borrow();
                if s.hidden {
                    return;
                }
                let alpha = if moving && !s.reduce_motion { pulse(s.start.elapsed()) } else { 1. };
                let color = crate::theme::with_alpha(s.accent, alpha);
                for slot in s.sidebar.iter().chain(s.grid.iter()) {
                    window.with_content_mask(Some(slot.mask), |window| {
                        let _ = window.paint_svg(slot.bounds, "icons/state-arc.svg".into(), None, TransformationMatrix::unit(), color.into(), cx);
                    });
                }
            },
        )
        .absolute()
        .size_full()
    }
}

#[derive(Clone)]
pub struct Views {
    pub band: Entity<Part>,
    pub sidebar: Entity<Part>,
    pub grid: Entity<Part>,
    pub palette: Entity<Part>,
    pub spin: Entity<Spin>,
}

impl Views {
    pub fn new(app: &Entity<TuiosApp>, slots: Rc<RefCell<SpinSlots>>, cx: &mut Context<TuiosApp>) -> Self {
        let mut part = |kind: PartKind| {
            let app = app.clone();
            cx.new(move |cx: &mut Context<Part>| {
                cx.observe(&app, |_, _, cx| cx.notify()).detach();
                Part { app: app.downgrade(), kind }
            })
        };
        let band = part(PartKind::Band);
        let sidebar = part(PartKind::Sidebar);
        let grid = part(PartKind::Grid);
        let palette = part(PartKind::Palette);
        let weak = app.downgrade();
        let spin = cx.new(move |_| Spin { slots, app: weak });
        Views { band, sidebar, grid, palette, spin }
    }
}
