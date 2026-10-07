//! The chrome around the stage: the title band, the sidebar (or its rail in
//! a narrow window) and the command palette, and the small parts they share:
//! state icons, keycap chips, buttons. Sizes, type and colours follow
//! docs/design/FINAL.md sections 2 to 7.

use super::grid::{decelerate, exit};
use super::views::Slot;
use super::*;
use crate::palette::{Icon, Section};
use crate::theme::with_alpha;
use gpui::prelude::FluentBuilder;

/// The type scale: size and line height in pixels (section 2.2).
pub const LABEL: (f32, f32) = (11., 16.);
pub const SMALL: (f32, f32) = (12., 16.);
pub const BODY: (f32, f32) = (13., 18.);
pub const ITEM: (f32, f32) = (14., 20.);
pub const QUERY: (f32, f32) = (16., 24.);
pub const TITLE: (f32, f32) = (15., 20.);

/// Tabular figures, for ages, counts and workspace numbers.
fn tnum() -> FontFeatures {
    FontFeatures(std::sync::Arc::new(vec![("tnum".into(), 1)]))
}

/// A line of chrome text in one style of the type scale.
pub fn text(s: String, style: (f32, f32), weight: FontWeight, color: u32) -> Div {
    div().text_size(px(style.0)).line_height(px(style.1)).font_weight(weight).text_color(rgb(color)).child(SharedString::from(s))
}

/// A number in tabular figures.
fn num(s: String, style: (f32, f32), weight: FontWeight, color: u32) -> Div {
    text(s, style, weight, color).font_features(tnum()).flex_none()
}

fn icon(path: &'static str, color: impl Into<Hsla>, side: f32) -> Svg {
    svg().path(path).size(px(side)).flex_none().text_color(color.into())
}

/// One chip per shortcut: "Ctrl+Shift+P", no fill, a 1 px border.
pub fn chip(t: &Theme, hint: &str) -> Option<Div> {
    if hint.is_empty() {
        return None;
    }
    Some(
        div()
            .flex_none()
            .h(px(18.))
            .px(px(6.))
            .flex()
            .items_center()
            .rounded(px(4.))
            .border_1()
            .border_color(t.border)
            .child(num(palette::chip(hint), (LABEL.0, LABEL.0 + 2.), FontWeight::MEDIUM, t.text3)),
    )
}

/// A 32 px button with a label and its shortcut.
pub fn button(t: &Theme, id: &'static str, label: &'static str, hint: &str) -> Stateful<Div> {
    div()
        .id(id)
        .h(px(32.))
        .px(px(12.))
        .flex()
        .items_center()
        .gap(px(8.))
        .rounded(px(6.))
        .bg(rgb(t.raised))
        .border_1()
        .border_color(t.border)
        .cursor_pointer()
        .hover(|s| s.bg(rgb(t.raised_sel)))
        .active(|s| s.bg(rgb(t.selected)))
        .child(text(label.into(), BODY, FontWeight::MEDIUM, t.text))
        .children(chip(t, hint))
}

/// A button with no fill and no border until the pointer is over it.
pub fn text_button(t: &Theme, id: &'static str, label: &'static str) -> Stateful<Div> {
    div()
        .id(id)
        .h(px(32.))
        .px(px(12.))
        .flex()
        .items_center()
        .rounded(px(6.))
        .cursor_pointer()
        .hover(|s| s.bg(rgb(t.hover)))
        .active(|s| s.bg(rgb(t.selected)))
        .child(text(label.into(), BODY, FontWeight::MEDIUM, t.text2))
}

/// A 28 px square button with a 16 px outline icon.
fn icon_button(t: &Theme, id: &'static str, path: &'static str, on: bool) -> Stateful<Div> {
    let group: SharedString = format!("{id}-g").into();
    div()
        .id(id)
        .group(group.clone())
        .size(px(28.))
        .flex()
        .flex_none()
        .items_center()
        .justify_center()
        .rounded(px(6.))
        .cursor_pointer()
        .hover(|s| s.bg(rgb(t.hover)))
        .active(|s| s.bg(rgb(t.selected)))
        .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
        .child(icon(path, rgb(if on { t.text } else { t.text3 }), 16.).group_hover(group, |s| s.text_color(rgb(t.text2))))
}

/// The 16 px state slot of section 6. A working icon's arc is drawn by the
/// spin view when `slots` is given, so it can pulse without redrawing the
/// sidebar; otherwise it is drawn here, still.
pub fn state_icon(t: &Theme, s: Status, slots: Option<Rc<RefCell<SpinSlots>>>) -> AnyElement {
    let layer = |path: &'static str, color: Rgba| svg().path(path).absolute().top_0().left_0().size(px(16.)).text_color(color);
    let slot = div().size(px(16.)).flex_none().relative();
    match s {
        Status::NeedsYou => slot.child(layer("icons/state-needs.svg", rgb(t.need))),
        Status::Errored => slot.child(layer("icons/state-disc.svg", rgb(t.err))).child(layer("icons/mark-x.svg", rgb(t.on_state))),
        Status::Done => slot.child(layer("icons/state-disc.svg", rgb(t.done))).child(layer("icons/mark-check.svg", rgb(t.on_state))),
        Status::Idle => slot.child(layer("icons/state-ring.svg", rgb(t.text3))),
        Status::Terminal => slot.child(layer("icons/state-dot.svg", rgb(t.text3))),
        Status::Working => {
            let slot = slot.child(layer("icons/state-ring.svg", with_alpha(t.text3, 0.5)));
            match slots {
                Some(slots) => slot.child(
                    canvas(
                        move |bounds, window, _| slots.borrow_mut().sidebar.push(Slot { bounds, mask: window.content_mask() }),
                        |_, _, _, _| {},
                    )
                    .absolute()
                    .top_0()
                    .left_0()
                    .size(px(16.)),
                ),
                None => slot.child(layer("icons/state-arc.svg", rgb(t.accent))),
            }
        }
    }
    .into_any_element()
}

impl TuiosApp {
    // ---- title band --------------------------------------------------------

    pub(super) fn render_band(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement + use<> {
        let t = self.theme.clone();
        let lay = self.layout;
        let st = self.state.clone().unwrap_or_default();
        let wide = lay.side == Side::Full;
        // The left end: the search field over the sidebar, a search button
        // over the rail, or the sidebar button with neither.
        let left: AnyElement = if wide && self.sidebar {
            let field_w = lay.side_w - 24.;
            div()
                .id("search")
                .absolute()
                .left(px(12.))
                .top(px(6.))
                .w(px(field_w))
                .h(px(28.))
                .pl(px(8.))
                .pr(px(6.))
                .flex()
                .items_center()
                .gap(px(6.))
                .rounded(px(6.))
                .bg(rgb(t.field))
                .border_1()
                .border_color(Theme::fade(t.border, 0.7))
                .hover(|s| s.border_color(t.border))
                .cursor_pointer()
                .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                .on_click(cx.listener(|this, _, _, cx| this.open_palette(cx)))
                .child(icon("icons/search.svg", rgb(t.text3), 14.))
                .child(text("Search".into(), BODY, FontWeight::NORMAL, t.text3).flex_1())
                .children(chip(&t, "ctrl+shift+p"))
                .into_any_element()
        } else if lay.side == Side::Rail && !lay.overlay {
            div()
                .absolute()
                .left(px(12.))
                .top(px(6.))
                .child(icon_button(&t, "search-button", "icons/search.svg", false).on_click(cx.listener(|this, _, _, cx| this.open_palette(cx))))
                .into_any_element()
        } else {
            div()
                .absolute()
                .left(px(8.))
                .top(px(6.))
                .child(icon_button(&t, "show-sidebar", "icons/panel-left.svg", lay.overlay).on_click(cx.listener(|this, _, w, cx| this.run(Act::ToggleSidebar, w, cx))))
                .into_any_element()
        };
        let start = f32::from(lay.grid_origin.x).max(if wide && self.sidebar || lay.side == Side::Rail { 0. } else { 48. });

        // The workspaces: occupied ones and the current one.
        let mut shown: Vec<u32> = st.occupied.clone();
        if st.workspace > 0 && !shown.contains(&st.workspace) {
            shown.push(st.workspace);
        }
        shown.sort();
        let mut seg = div().flex().flex_none().items_center().h(px(28.)).p(px(2.)).rounded(px(7.)).bg(rgb(t.field));
        for ws in shown {
            let active = ws == st.workspace;
            let needs = self.attached.iter().any(|p| p.workspace == ws && p.status == Status::NeedsYou);
            let name = if wide { st.workspace_name(ws).map(|s| s.to_string()) } else { None };
            let group: SharedString = format!("ws-{ws}-g").into();
            let mut s = div()
                .id(SharedString::from(format!("ws-{ws}")))
                .group(group.clone())
                .h(px(24.))
                .min_w(px(28.))
                .px(px(10.))
                .flex()
                .flex_none()
                .items_center()
                .justify_center()
                .gap(px(6.))
                .rounded(px(5.))
                .cursor_pointer()
                .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                .on_click(cx.listener(move |this, _, _, _| this.send(Command::workspace(ws))))
                .child(num(ws.to_string(), LABEL, FontWeight::MEDIUM, t.text3));
            if active {
                s = s.bg(rgb(if t.light { 0xffffff } else { t.stage })).border_1().border_color(t.border).shadow(vec![BoxShadow {
                    color: hsla(0., 0., 0., 0.25),
                    offset: point(px(0.), px(1.)),
                    blur_radius: px(0.),
                    spread_radius: px(0.),
                    inset: false,
                }]);
            }
            if let Some(n) = name {
                s = s.child(
                    text(n, BODY, FontWeight::MEDIUM, if active { t.text } else { t.text2 })
                        .when(!active, |el| el.group_hover(group.clone(), |s| s.text_color(rgb(t.text)))),
                );
            }
            if needs {
                s = s.child(div().size(px(6.)).flex_none().rounded_full().bg(rgb(t.need)));
            }
            seg = seg.child(s);
        }
        let session = if st.session.is_empty() { String::new() } else { st.session.clone() };
        let switcher = div()
            .id("session-switcher")
            .flex()
            .flex_none()
            .items_center()
            .gap(px(4.))
            .h(px(28.))
            .cursor_pointer()
            .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
            .on_click(cx.listener(|this, _, _, cx| {
                this.open_palette(cx);
                if let Some(p) = this.palette.as_mut() {
                    p.query = "session ".into();
                }
            }))
            .child(text(session, BODY, FontWeight::SEMIBOLD, t.text))
            .child(icon("icons/chevron-down.svg", rgb(t.text3), 12.));
        let zoomed = self.focused_id().and_then(|id| st.window(&id).map(|w| w.zoomed)).unwrap_or(false);
        let right_gap = (lay.width - f32::from(lay.stage.right())).max(0.);
        div()
            .id("band")
            .size_full()
            .relative()
            .bg(rgb(t.base))
            // The band is the window's title bar.
            .on_mouse_down(MouseButton::Left, |ev, window, _| {
                if ev.click_count >= 2 {
                    window.zoom_window();
                } else {
                    window.start_window_move();
                }
            })
            .child(left)
            .child(
                div()
                    .absolute()
                    .left(px(start))
                    .top_0()
                    .h(px(BAND_H))
                    .flex()
                    .items_center()
                    .gap(px(12.))
                    .when(self.state.is_some(), |el| el.child(switcher).child(seg)),
            )
            .child(
                div()
                    .absolute()
                    .right(px(right_gap))
                    .top(px(6.))
                    .flex()
                    .gap(px(4.))
                    .child(icon_button(&t, "split-right", "icons/columns-2.svg", false).on_click(cx.listener(|this, _, w, cx| this.run(Act::Tape("Split", &["vertical"]), w, cx))))
                    .child(icon_button(&t, "split-down", "icons/rows-2.svg", false).on_click(cx.listener(|this, _, w, cx| this.run(Act::Tape("Split", &["horizontal"]), w, cx))))
                    .child(icon_button(&t, "zoom", "icons/maximize-2.svg", zoomed).on_click(cx.listener(|this, _, w, cx| this.run(Act::Tape("ToggleZoom", &[]), w, cx)))),
            )
    }

    // ---- sidebar -----------------------------------------------------------

    /// One pane as a sidebar row: state icon, title, age, and one line below.
    fn pane_row(&self, p: &PaneInfo, selected: bool, right: Option<String>, cx: &mut Context<Self>) -> Stateful<Div> {
        let t = &self.theme;
        let now = fleet::now_ms();
        let (session, window, workspace) = (p.session.clone(), p.window.clone(), p.workspace);
        let age = if p.status.is_agent() { fleet::age(p.since_ms, now) } else { String::new() };
        let quiet = p.status == Status::Terminal || (p.status == Status::Done && p.seen);
        let loud = matches!(p.status, Status::NeedsYou | Status::Errored);
        let mut line2 = div().h(px(16.)).flex().items_center().overflow_hidden().text_size(px(SMALL.0)).line_height(px(SMALL.1)).whitespace_nowrap();
        if p.harness.is_empty() || p.message.is_empty() {
            line2 = line2.child(div().truncate().text_color(rgb(t.text3)).child(SharedString::from(p.detail())));
        } else {
            line2 = line2
                .child(div().flex_none().text_color(rgb(t.text3)).child(SharedString::from(format!("{} · ", p.harness))))
                .child(div().min_w_0().truncate().text_color(rgb(if loud { t.text2 } else { t.text3 })).child(SharedString::from(p.message.clone())));
        }
        div()
            .id(SharedString::from(format!("row-{}-{}", p.session, p.window)))
            .mx(px(8.))
            .h(px(44.))
            .mb(px(2.))
            .flex_none()
            .relative()
            .rounded(px(6.))
            .cursor_pointer()
            .when(selected, |el| el.bg(rgb(t.selected)))
            .when(!selected, |el| el.hover(|s| s.bg(rgb(t.hover))).active(|s| s.bg(rgb(t.selected))))
            .on_click(cx.listener(move |this, _, window_, cx| {
                this.jump_to(session.clone(), window.clone(), workspace, window_, cx);
            }))
            .child(div().absolute().left(px(12.)).top(px(7.)).child(state_icon(t, p.status, Some(self.spin_slots.clone()))))
            .child(
                div()
                    .absolute()
                    .left(px(36.))
                    .right(px(12.))
                    .top(px(6.))
                    .flex()
                    .flex_col()
                    .child(
                        div()
                            .h(px(18.))
                            .flex()
                            .items_center()
                            .gap(px(6.))
                            .child(text(p.name.clone(), BODY, FontWeight::MEDIUM, if quiet { t.text2 } else { t.text }).flex_1().min_w_0().truncate())
                            .children(right.map(|r| num(r, LABEL, FontWeight::MEDIUM, t.text3)))
                            .when(!age.is_empty(), |el| el.child(num(age, LABEL, FontWeight::MEDIUM, t.text3))),
                    )
                    .child(line2),
            )
    }

    /// A pane's place for the right of its row: its session when not the
    /// attached one, its workspace when not the shown one.
    fn row_place(&self, p: &PaneInfo) -> Option<String> {
        let st = self.state.as_ref()?;
        if p.session != st.session {
            Some(p.session.clone())
        } else if p.workspace != st.workspace {
            Some(p.workspace.to_string())
        } else {
            None
        }
    }

    pub(super) fn render_sidebar(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement + use<> {
        self.spin_slots.borrow_mut().sidebar.clear();
        if self.layout.side == Side::Rail && !self.layout.overlay {
            return self.render_rail(window, cx).into_any_element();
        }
        let t = self.theme.clone();
        let current = self.current_session();
        let all = self.all_panes();
        let focused = self.focused_id();
        let mut list = div().id("sidebar-list").flex().flex_col().flex_1().min_h_0().overflow_y_scroll().pt(px(8.)).pb(px(8.));

        // Every pane that waits on the person, from any session, oldest first.
        let mut waiting: Vec<&PaneInfo> = all.iter().filter(|p| matches!(p.status, Status::NeedsYou | Status::Errored)).collect();
        waiting.sort_by(|a, b| fleet::order(a, b));
        if !waiting.is_empty() {
            let need = waiting.iter().filter(|p| p.status == Status::NeedsYou).count();
            list = list.child(
                div()
                    .h(px(24.))
                    .flex()
                    .flex_none()
                    .items_center()
                    .gap(px(8.))
                    .pl(px(20.))
                    .mb(px(2.))
                    .child(text("Needs you".into(), SMALL, FontWeight::SEMIBOLD, t.text2))
                    .when(need > 0, |el| {
                        el.child(div().h(px(16.)).px(px(6.)).flex().items_center().rounded(px(8.)).bg(rgb(t.need_fill)).child(num(need.to_string(), LABEL, FontWeight::SEMIBOLD, t.need_ink)))
                    }),
            );
            for p in waiting {
                let selected = p.session == current && focused.as_deref() == Some(p.window.as_str());
                list = list.child(self.pane_row(p, selected, self.row_place(p), cx));
            }
        }

        // One group per session, the attached one first.
        let mut names = self.session_names();
        names.sort_by_key(|n| (*n != current, n.clone()));
        for name in names {
            let attached = name == current;
            let mut ps: Vec<&PaneInfo> = all.iter().filter(|p| p.session == name).collect();
            ps.sort_by(|a, b| a.status.cmp(&b.status).then(a.workspace.cmp(&b.workspace)).then_with(|| fleet::order(a, b)));
            let agents = ps.iter().any(|p| p.status.is_agent());
            let count = if attached { ps.len() } else { self.sessions.iter().find(|s| s.name == name).map(|s| s.window_count as usize).unwrap_or(ps.len()) };
            let word = match (agents, count) {
                (true, 1) => "1 pane".to_string(),
                (true, n) => format!("{n} panes"),
                (false, 1) => "1 terminal".to_string(),
                (false, n) => format!("{n} terminals"),
            };
            let open = !self.folded.get(&name).copied().unwrap_or(!agents);
            let (n1, n2) = (name.clone(), name.clone());
            let group: SharedString = format!("grp-{name}").into();
            list = list.child(
                div()
                    .id(SharedString::from(format!("session-{name}")))
                    .group(group.clone())
                    .mt(px(14.))
                    .h(px(28.))
                    .flex()
                    .flex_none()
                    .items_center()
                    .relative()
                    .cursor_pointer()
                    .on_click(cx.listener(move |this, _, window, cx| {
                        if this.current_session() != n1 {
                            this.connect(Some(n1.clone()), window, cx);
                        } else {
                            let open = !this.folded.get(&n1).copied().unwrap_or(false);
                            this.folded.insert(n1.clone(), open);
                            this.refresh_sidebar(cx);
                        }
                    }))
                    .child(
                        div()
                            .id(SharedString::from(format!("fold-{name}")))
                            .absolute()
                            .left(px(14.))
                            .top(px(6.))
                            .size(px(16.))
                            .flex()
                            .items_center()
                            .justify_center()
                            .on_click(cx.listener(move |this, _, _, cx| {
                                cx.stop_propagation();
                                let agents = this.all_panes().iter().any(|p| p.session == n2 && p.status.is_agent());
                                let folded = this.folded.get(&n2).copied().unwrap_or(!agents);
                                this.folded.insert(n2.clone(), !folded);
                                this.refresh_sidebar(cx);
                            }))
                            .child(icon(if open { "icons/chevron-down.svg" } else { "icons/chevron-right.svg" }, rgb(t.text3), 12.).group_hover(group.clone(), |s| s.text_color(rgb(t.text2)))),
                    )
                    .child(
                        div()
                            .absolute()
                            .left(px(32.))
                            .right(px(12.))
                            .top(px(6.))
                            .flex()
                            .items_center()
                            .gap(px(6.))
                            .child(text(name.clone(), SMALL, FontWeight::SEMIBOLD, t.text2).truncate())
                            .child(text(word, SMALL, FontWeight::NORMAL, t.text3).flex_none()),
                    ),
            );
            if !open {
                continue;
            }
            for p in ps.iter().filter(|p| attached || p.status.is_agent()) {
                let selected = attached && focused.as_deref() == Some(p.window.as_str());
                // Inside its own group a row needs no session name.
                let place = if attached { self.row_place(p) } else { None };
                list = list.child(self.pane_row(p, selected, place, cx));
            }
        }

        let footer = div()
            .flex_none()
            .h(px(44.))
            .pl(px(12.))
            .child(
                div()
                    .id("new-session")
                    .w(px(140.))
                    .h(px(32.))
                    .px(px(8.))
                    .flex()
                    .items_center()
                    .gap(px(8.))
                    .rounded(px(6.))
                    .cursor_pointer()
                    .hover(|s| s.bg(rgb(t.hover)))
                    .active(|s| s.bg(rgb(t.selected)))
                    .on_click(cx.listener(|this, _, window, cx| this.run(Act::NewSession, window, cx)))
                    .child(icon("icons/plus.svg", rgb(t.text2), 12.))
                    .child(text("New session".into(), BODY, FontWeight::MEDIUM, t.text2)),
            );

        let overlay = self.layout.overlay;
        div()
            .id("sidebar")
            .flex()
            .flex_col()
            .size_full()
            .bg(rgb(t.base))
            .when(overlay, |el| {
                el.border_r_1().border_color(t.border).shadow(vec![
                    BoxShadow { color: hsla(0., 0., 0., 0.18), offset: point(px(0.), px(8.)), blur_radius: px(16.), spread_radius: px(0.), inset: false },
                    BoxShadow { color: hsla(0., 0., 0., 0.22), offset: point(px(0.), px(24.)), blur_radius: px(48.), spread_radius: px(0.), inset: false },
                ])
            })
            .child(list)
            .child(footer)
            .into_any_element()
    }

    /// The narrow window's rail: one 36 px cell per pane.
    fn render_rail(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement + use<> {
        let t = self.theme.clone();
        let current = self.current_session();
        let all = self.all_panes();
        let focused = self.focused_id();
        let mut list = div().id("rail").flex().flex_col().items_center().size_full().overflow_y_scroll().pt(px(8.)).bg(rgb(t.base));
        let cell = |this: &Self, p: &PaneInfo, cx: &mut Context<Self>| {
            let selected = p.session == current && focused.as_deref() == Some(p.window.as_str());
            let (session, window, workspace) = (p.session.clone(), p.window.clone(), p.workspace);
            div()
                .id(SharedString::from(format!("rail-{}-{}", p.session, p.window)))
                .size(px(36.))
                .flex_none()
                .flex()
                .items_center()
                .justify_center()
                .rounded(px(6.))
                .cursor_pointer()
                .when(selected, |el| el.bg(rgb(t.selected)))
                .when(!selected, |el| el.hover(|s| s.bg(rgb(t.hover))))
                .on_click(cx.listener(move |this, _, window_, cx| this.jump_to(session.clone(), window.clone(), workspace, window_, cx)))
                .child(state_icon(&t, p.status, Some(this.spin_slots.clone())))
        };
        let mut waiting: Vec<&PaneInfo> = all.iter().filter(|p| matches!(p.status, Status::NeedsYou | Status::Errored)).collect();
        waiting.sort_by(|a, b| fleet::order(a, b));
        let any_waiting = !waiting.is_empty();
        for p in waiting {
            list = list.child(cell(self, p, cx));
        }
        let mut names = self.session_names();
        names.sort_by_key(|n| (*n != current, n.clone()));
        for (i, name) in names.iter().enumerate() {
            let mut ps: Vec<&PaneInfo> = all.iter().filter(|p| &p.session == name && (*name == current || p.status.is_agent())).collect();
            if ps.is_empty() {
                continue;
            }
            if i > 0 || any_waiting {
                list = list.child(div().my(px(8.)).w(px(36.)).h(px(1.)).flex_none().bg(t.hairline));
            }
            ps.sort_by(|a, b| a.status.cmp(&b.status).then(a.workspace.cmp(&b.workspace)));
            for p in ps {
                list = list.child(cell(self, p, cx));
            }
        }
        list
    }

    // ---- palette -----------------------------------------------------------

    pub(super) fn render_palette(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> Option<AnyElement> {
        let p = self.palette.as_ref()?;
        let t = self.theme.clone();
        let lay = self.layout;
        let list = palette::filter(&p.entries, &p.query);
        let selected = p.selected.min(list.len().saturating_sub(1));
        // As many rows as fit in 60 % of the window.
        let budget = (lay.height * 0.6 - 52. - 40. - 12.).max(80.);
        let shown = ((budget - 2. * 28.) / 40.).floor().clamp(3., 12.) as usize;
        let start = selected.saturating_sub(shown - 1);
        let q = p.query.trim().to_string();
        let mut items = div().flex().flex_col().px(px(6.)).py(px(6.));
        let mut last: Option<Section> = if start > 0 { list.get(start - 1).map(|e| e.section) } else { None };
        for (i, e) in list.iter().enumerate().skip(start).take(shown) {
            if last != Some(e.section) {
                items = items.child(div().h(px(28.)).pl(px(14.)).flex().items_center().child(text(e.section.label().into(), SMALL, FontWeight::SEMIBOLD, t.text3)));
                last = Some(e.section);
            }
            let act = e.act.clone();
            let is_sel = i == selected;
            let lead: AnyElement = match e.icon {
                Icon::State(s) => state_icon(&t, s, None),
                Icon::Session => icon("icons/layers.svg", rgb(t.text3), 14.).into_any_element(),
                Icon::Command => icon("icons/chevron-right.svg", rgb(t.text3), 14.).into_any_element(),
                Icon::Theme => icon("icons/palette.svg", rgb(t.text3), 14.).into_any_element(),
            };
            // Matched characters in 600, the rest in 400.
            let marks = palette::matches(&q, &e.title);
            let highlights: Vec<(std::ops::Range<usize>, HighlightStyle)> = marks
                .iter()
                .filter_map(|&b| {
                    let len = e.title[b..].chars().next()?.len_utf8();
                    Some((b..b + len, HighlightStyle { font_weight: Some(FontWeight::SEMIBOLD), ..Default::default() }))
                })
                .collect();
            let title = StyledText::new(SharedString::from(e.title.clone())).with_highlights(highlights);
            items = items.child(
                div()
                    .id(("pal", i))
                    .h(px(40.))
                    .flex()
                    .flex_none()
                    .items_center()
                    .pl(px(12.))
                    .pr(px(14.))
                    .rounded(px(8.))
                    .cursor_pointer()
                    .when(is_sel, |el| el.bg(rgb(t.raised_sel)))
                    .on_mouse_move(cx.listener(move |this, _, _, cx| {
                        if let Some(p) = this.palette.as_mut() {
                            if p.selected != i {
                                p.selected = i;
                                this.refresh_palette(cx);
                            }
                        }
                    }))
                    .on_click(cx.listener(move |this, _, window, cx| {
                        this.close_palette(cx);
                        this.run(act.clone(), window, cx);
                    }))
                    .child(div().size(px(16.)).flex().flex_none().items_center().justify_center().child(lead))
                    .child(
                        div()
                            .ml(px(10.))
                            .flex_none()
                            .max_w(px(320.))
                            .truncate()
                            .text_size(px(ITEM.0))
                            .line_height(px(ITEM.1))
                            .font_weight(FontWeight::NORMAL)
                            .text_color(rgb(t.text))
                            .child(title),
                    )
                    .child(text(e.subtitle.clone(), SMALL, FontWeight::NORMAL, if is_sel { t.text2 } else { t.text3 }).ml(px(12.)).flex_1().min_w_0().truncate())
                    .children(chip(&t, e.hint).map(|c| c.ml(px(12.)))),
            );
        }
        if list.is_empty() {
            items = items.child(div().h(px(80.)).flex().items_center().justify_center().child(text(format!("No results for “{q}”"), BODY, FontWeight::NORMAL, t.text2)));
        }
        let caret = div().w(px(2.)).h(px(20.)).flex_none().bg(rgb(t.accent));
        let query_el = if p.query.is_empty() {
            div().flex().items_center().child(caret).child(text("Search panes, sessions and commands".into(), QUERY, FontWeight::NORMAL, t.text3))
        } else {
            div().flex().items_center().child(text(p.query.clone(), QUERY, FontWeight::NORMAL, t.text)).child(caret)
        };
        let shadow = |y: f32, blur: f32, a: f32| BoxShadow { color: hsla(0., 0., 0., a), offset: point(px(0.), px(y)), blur_radius: px(blur), spread_radius: px(0.), inset: false };
        let open_label = list.get(selected).map(|e| if matches!(e.act, Act::Jump { .. } | Act::Session(_)) { "Open" } else { "Run" }).unwrap_or("Open");
        let footer_key = |label: &'static str, color: u32, key: &'static str| {
            div().flex().items_center().gap(px(8.)).child(text(label.into(), SMALL, FontWeight::MEDIUM, color)).children(chip(&t, key))
        };
        let count = list.len();
        let width = (lay.width - 64.).min(680.);
        let panel = div()
            .id("palette-panel")
            .w(px(width))
            .bg(rgb(t.raised))
            .border_1()
            .border_color(t.border)
            .rounded(px(12.))
            .shadow(if t.light {
                vec![shadow(2., 3., 0.06), shadow(8., 16., 0.08), shadow(24., 48., 0.10)]
            } else {
                vec![shadow(2., 3., 0.14), shadow(8., 16., 0.18), shadow(24., 48., 0.22)]
            })
            .overflow_hidden()
            .on_click(|_, _, cx| cx.stop_propagation())
            .child(
                div()
                    .relative()
                    .h(px(52.))
                    .flex()
                    .items_center()
                    .border_b_1()
                    .border_color(t.hairline)
                    .child(div().absolute().left(px(16.)).top(px(18.)).child(icon("icons/search.svg", rgb(t.text3), 16.)))
                    .child(div().absolute().left(px(44.)).top(px(14.)).right(px(64.)).child(query_el))
                    .child(div().absolute().right(px(16.)).top(px(17.)).children(chip(&t, "esc"))),
            )
            .child(items)
            .child(
                div()
                    .h(px(40.))
                    .flex()
                    .justify_between()
                    .items_center()
                    .pl(px(20.))
                    .pr(px(16.))
                    .border_t_1()
                    .border_color(t.hairline)
                    .child(num(format!("{count} {}", if count == 1 { "result" } else { "results" }), SMALL, FontWeight::NORMAL, t.text3))
                    .child(div().flex().items_center().gap(px(16.)).child(footer_key(open_label, t.text, "enter")).child(footer_key("Close", t.text2, "esc"))),
            );
        let top = (lay.height * 0.14).round();
        let left = ((lay.width - width) / 2.).round();
        let closing = p.closing;
        let reduce = self.cfg.reduce_motion;
        let holder = div().absolute().left(px(left)).top(px(top)).child(panel);
        // Opacity and a whole-pixel rise only: scaled text blurs.
        let holder = if closing.is_none() && !reduce {
            holder.with_animation("palette-rise", Animation::new(Duration::from_millis(120)), move |el, d| el.top(px(top + (4. * (1. - decelerate(d))).round()))).into_any_element()
        } else {
            holder.into_any_element()
        };
        let root = div()
            .id("palette-scrim")
            .size_full()
            .relative()
            .bg(t.scrim)
            .on_click(cx.listener(|this, _, _, cx| this.close_palette(cx)))
            .child(holder);
        Some(match (closing, reduce) {
            (_, true) => root.into_any_element(),
            (Some(_), false) => root.with_animation("palette-out", Animation::new(Duration::from_millis(80)), |el, d| el.opacity(1. - exit(d))).into_any_element(),
            (None, false) => root.with_animation("palette-in", Animation::new(Duration::from_millis(120)), |el, d| el.opacity(decelerate(d))).into_any_element(),
        })
    }
}
