//! The chrome around the panes: the sidebar, the top bar and the command
//! palette. Sizes and colours follow docs/DESIGN-RESEARCH.md, "Design".

use super::*;
use crate::palette::{Icon, Section};
use gpui::prelude::FluentBuilder;

pub fn status_color(t: &Theme, s: Status) -> u32 {
    match s {
        Status::NeedsYou => t.needs_input,
        Status::Errored => t.errored,
        Status::Working => t.working,
        Status::Done => t.done,
        Status::Idle | Status::Terminal => t.text3,
    }
}

pub fn status_icon(s: Status) -> &'static str {
    match s {
        Status::NeedsYou => "icons/state-needs.svg",
        Status::Errored => "icons/state-error.svg",
        Status::Working => "icons/state-working.svg",
        Status::Done => "icons/state-done.svg",
        Status::Idle => "icons/state-idle.svg",
        Status::Terminal => "icons/state-terminal.svg",
    }
}

/// The 16 px state slot.
fn glyph(t: &Theme, s: Status, angle: f32) -> AnyElement {
    let color = status_color(t, s);
    let slot = div().size(px(16.)).flex_none().relative().flex().items_center().justify_center();
    if s == Status::Working {
        slot.child(svg().path("icons/state-ring.svg").absolute().size(px(14.)).text_color(Theme::alpha(color, 0x40)))
            .child(
                svg()
                    .path("icons/state-working.svg")
                    .absolute()
                    .size(px(14.))
                    .text_color(rgb(color))
                    .with_transformation(Transformation::rotate(radians(angle))),
            )
            .into_any_element()
    } else {
        slot.child(svg().path(status_icon(s)).size(px(14.)).text_color(rgb(color))).into_any_element()
    }
}

fn icon(path: &'static str, color: u32, side: f32) -> Svg {
    svg().path(path).size(px(side)).flex_none().text_color(rgb(color))
}

/// Keys as small caps: "Ctrl" "Shift" "P".
fn keycaps(t: &Theme, hint: &str) -> Div {
    keycaps_on(t, hint, t.hover)
}

/// Keycaps on a ground of their own colour, for use inside a field.
fn keycaps_on(t: &Theme, hint: &str, ground: u32) -> Div {
    let mut row = div().flex().flex_none().items_center().gap(px(3.));
    for k in palette::keycaps(hint) {
        row = row.child(
            div()
                .h(px(18.))
                .min_w(px(18.))
                .px(px(5.))
                .flex()
                .items_center()
                .justify_center()
                .rounded(px(4.))
                .bg(rgb(ground))
                .text_size(px(10.5))
                .font_weight(FontWeight::MEDIUM)
                .text_color(rgb(t.text2))
                .child(SharedString::from(k)),
        );
    }
    row
}

fn icon_button(t: &Theme, id: &'static str, path: &'static str) -> Stateful<Div> {
    div()
        .id(id)
        .size(px(28.))
        .flex()
        .flex_none()
        .items_center()
        .justify_center()
        .rounded(px(6.))
        .cursor_pointer()
        .hover(|s| s.bg(rgb(t.hover)))
        .child(icon(path, t.text2, 15.))
}

impl TuiosApp {
    /// One pane as a sidebar row: state slot, name, age, and one muted line.
    fn pane_row(&self, p: &PaneInfo, selected: bool, suffix: Option<&str>, cx: &mut Context<Self>) -> Stateful<Div> {
        let t = &self.theme;
        let now = fleet::now_ms();
        let bright = selected || matches!(p.status, Status::NeedsYou | Status::Errored | Status::Working | Status::Done);
        let (session, window, workspace) = (p.session.clone(), p.window.clone(), p.workspace);
        let current = self.state.as_ref().map(|s| (s.session.clone(), s.workspace));
        let other_ws = current.as_ref().is_some_and(|(s, w)| *s == p.session && *w != p.workspace);
        let age = if p.status.is_agent() { fleet::age(p.since_ms, now) } else { String::new() };
        let detail = p.detail.clone();
        div()
            .id(SharedString::from(format!("row-{}", p.window)))
            .mx(px(6.))
            .px(px(10.))
            .h(px(46.))
            .flex()
            .flex_none()
            .gap(px(10.))
            .pt(px(7.))
            .rounded(px(6.))
            .cursor_pointer()
            .when(selected, |el| el.bg(rgb(t.selected)))
            .when(!selected, |el| el.hover(|s| s.bg(rgb(t.hover))))
            .on_click(cx.listener(move |this, _, window_, cx| {
                this.jump_to(session.clone(), window.clone(), workspace, window_, cx);
            }))
            .child(div().pt(px(1.)).child(glyph(t, p.status, self.spin_angle())))
            .child(
                div()
                    .flex()
                    .flex_col()
                    .flex_1()
                    .min_w_0()
                    .gap(px(2.))
                    .child(
                        div()
                            .flex()
                            .items_center()
                            .gap(px(6.))
                            .h(px(18.))
                            .child(
                                div()
                                    .truncate()
                                    .min_w_0()
                                    .text_size(px(13.))
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(rgb(if bright { t.text } else { t.text2 }))
                                    .child(SharedString::from(p.name.clone())),
                            )
                            .when_some(suffix.map(str::to_string), |el, s| {
                                el.child(div().flex_none().text_size(px(12.)).text_color(rgb(t.text3)).child(SharedString::from(s)))
                            })
                            .child(div().flex_1())
                            .when(other_ws, |el| {
                                el.child(
                                    div()
                                        .flex_none()
                                        .h(px(15.))
                                        .min_w(px(15.))
                                        .px(px(3.))
                                        .flex()
                                        .items_center()
                                        .justify_center()
                                        .rounded(px(3.))
                                        .bg(rgb(t.hover))
                                        .text_size(px(10.))
                                        .font_weight(FontWeight::SEMIBOLD)
                                        .text_color(rgb(t.text3))
                                        .child(SharedString::from(workspace.to_string())),
                                )
                            })
                            .when(!age.is_empty(), |el| {
                                el.child(div().flex_none().text_size(px(11.)).font_weight(FontWeight::MEDIUM).text_color(rgb(t.text3)).child(SharedString::from(age)))
                            }),
                    )
                    .child(div().truncate().h(px(16.)).text_size(px(12.)).text_color(rgb(t.text3)).child(SharedString::from(detail))),
            )
    }

    fn section_label(&self, label: String, right: Option<(String, u32)>) -> Div {
        let t = &self.theme;
        div()
            .flex()
            .flex_none()
            .items_center()
            .justify_between()
            .px(px(16.))
            .pt(px(14.))
            .pb(px(4.))
            .text_size(px(11.5))
            .font_weight(FontWeight::MEDIUM)
            .text_color(rgb(t.text3))
            .child(div().truncate().child(SharedString::from(label)))
            .when_some(right, |el, (s, c)| el.child(div().flex_none().text_size(px(11.)).text_color(rgb(c)).child(SharedString::from(s))))
    }

    pub(super) fn render_sidebar(&mut self, cx: &mut Context<Self>) -> impl IntoElement + use<> {
        let t = self.theme.clone();
        let current = self.current_session();
        let all = self.all_panes();
        let (need, working, done) = fleet::counts(&all);

        let search = div()
            .id("sidebar-search")
            .mx(px(10.))
            .mt(px(10.))
            .h(px(30.))
            .px(px(10.))
            .flex()
            .flex_none()
            .items_center()
            .gap(px(8.))
            .rounded(px(7.))
            .bg(rgb(t.hover))
            .cursor_pointer()
            .on_click(cx.listener(|this, _, _, cx| this.open_palette(cx)))
            .child(icon("icons/search.svg", t.text3, 14.))
            .child(div().flex_1().text_size(px(12.5)).text_color(rgb(t.text3)).child("Search"))
            .child(keycaps_on(&t, "ctrl+shift+p", t.selected));

        let mut summary = div().flex().flex_none().items_center().gap(px(5.)).px(px(16.)).pt(px(14.)).text_size(px(12.)).text_color(rgb(t.text2));
        let mut parts = 0;
        for (n, word, color) in [(need, "needs you", t.needs_input), (working, "working", t.working), (done, "done", t.done)] {
            if n == 0 {
                continue;
            }
            if parts > 0 {
                summary = summary.child(div().text_color(rgb(t.text3)).child("·"));
            }
            summary = summary.child(div().font_weight(FontWeight::SEMIBOLD).text_color(rgb(color)).child(SharedString::from(n.to_string()))).child(word);
            parts += 1;
        }

        let mut list = div().id("sidebar-list").flex().flex_col().flex_1().min_h_0().overflow_y_scroll().pb(px(8.));
        let focused = self.focused_id();

        // Every pane that waits on the person, from any session, oldest first.
        let mut waiting: Vec<&PaneInfo> = all.iter().filter(|p| matches!(p.status, Status::NeedsYou | Status::Errored)).collect();
        waiting.sort_by(|a, b| fleet::order(a, b));
        if !waiting.is_empty() {
            list = list.child(self.section_label("Needs you".into(), None));
            for p in waiting {
                let selected = p.session == current && focused.as_deref() == Some(p.window.as_str());
                let suffix = (p.session != current).then_some(p.session.as_str());
                list = list.child(self.pane_row(p, selected, suffix, cx));
            }
        }

        // One group per session, the attached one first.
        let mut names = self.session_names();
        names.sort_by_key(|n| (*n != current, n.clone()));
        for name in names {
            let mut ps: Vec<&PaneInfo> = all.iter().filter(|p| p.session == name && !matches!(p.status, Status::NeedsYou | Status::Errored)).collect();
            ps.sort_by(|a, b| fleet::order(a, b));
            let attached = name == current;
            let waiting = all.iter().filter(|p| p.session == name && matches!(p.status, Status::NeedsYou | Status::Errored)).count();
            let right = (waiting > 0).then(|| (format!("{waiting} needs you"), t.needs_input));
            let n2 = name.clone();
            list = list.child(
                div()
                    .id(SharedString::from(format!("session-{name}")))
                    .cursor_pointer()
                    .on_click(cx.listener(move |this, _, window, cx| {
                        if this.current_session() != n2 {
                            this.connect(Some(n2.clone()), window, cx);
                        }
                    }))
                    .child(self.section_label(name.clone(), right).when(attached, |el| el.text_color(rgb(t.text2)))),
            );
            let terminals = ps.iter().filter(|p| p.status == Status::Terminal).count();
            for p in ps.iter().filter(|p| attached || p.status != Status::Terminal) {
                let selected = attached && focused.as_deref() == Some(p.window.as_str());
                list = list.child(self.pane_row(p, selected, None, cx));
            }
            if !attached && terminals > 0 {
                let word = if terminals == 1 { "terminal" } else { "terminals" };
                list = list.child(div().px(px(42.)).h(px(24.)).flex().items_center().text_size(px(12.)).text_color(rgb(t.text3)).child(SharedString::from(format!("{terminals} {word}"))));
            }
        }

        let footer = div()
            .flex()
            .flex_none()
            .items_center()
            .justify_between()
            .h(px(40.))
            .px(px(8.))
            .border_t_1()
            .border_color(rgb(t.hairline))
            .child(
                div()
                    .id("new-session")
                    .flex()
                    .items_center()
                    .gap(px(8.))
                    .h(px(28.))
                    .px(px(8.))
                    .rounded(px(6.))
                    .cursor_pointer()
                    .text_size(px(12.5))
                    .text_color(rgb(t.text2))
                    .hover(|s| s.bg(rgb(t.hover)).text_color(rgb(t.text)))
                    .on_click(cx.listener(|this, _, window, cx| this.run(Act::NewSession, window, cx)))
                    .child(icon("icons/plus.svg", t.text2, 14.))
                    .child("New session"),
            )
            .when(need > 0, |el| {
                el.child(
                    div()
                        .id("next-needs-you")
                        .flex()
                        .items_center()
                        .gap(px(6.))
                        .px(px(6.))
                        .h(px(28.))
                        .rounded(px(6.))
                        .cursor_pointer()
                        .hover(|s| s.bg(rgb(t.hover)))
                        .text_size(px(11.5))
                        .text_color(rgb(t.text3))
                        .on_click(cx.listener(|this, _, window, cx| this.run(Act::NextNeedsYou, window, cx)))
                        .child("Next")
                        .child(keycaps(&t, "ctrl+shift+j")),
                )
            });

        div()
            .id("sidebar")
            .flex()
            .flex_col()
            .w(px(SIDEBAR_W))
            .h_full()
            .flex_none()
            .bg(rgb(t.sidebar))
            .border_r_1()
            .border_color(rgb(t.hairline))
            .child(search)
            .when(parts > 0, |el| el.child(summary))
            .child(list)
            .child(footer)
    }

    pub(super) fn render_topbar(&mut self, cx: &mut Context<Self>) -> impl IntoElement + use<> {
        let t = self.theme.clone();
        let st = self.state.clone().unwrap_or_default();
        let panes = self.attached_panes();
        let mut tabs = div().flex().items_center().gap(px(2.)).min_w_0();
        let mut shown: Vec<u32> = st.occupied.clone();
        if st.workspace > 0 && !shown.contains(&st.workspace) {
            shown.push(st.workspace);
        }
        shown.sort();
        for ws in shown {
            let active = ws == st.workspace;
            // The most urgent state on the workspace, if it is worth a dot.
            let urgent = panes.iter().filter(|p| p.workspace == ws).map(|p| p.status).min().filter(|s| matches!(s, Status::NeedsYou | Status::Errored | Status::Working));
            let name = st.workspace_name(ws).map(|s| s.to_string());
            tabs = tabs.child(
                div()
                    .id(SharedString::from(format!("ws-{ws}")))
                    .flex()
                    .flex_none()
                    .items_center()
                    .gap(px(7.))
                    .h(px(26.))
                    .px(px(9.))
                    .rounded(px(6.))
                    .cursor_pointer()
                    .when(active, |el| el.bg(rgb(t.selected)))
                    .when(!active, |el| el.hover(|s| s.bg(rgb(t.hover))))
                    .on_click(cx.listener(move |this, _, _, cx| {
                        this.send(Command::workspace(ws));
                        cx.notify();
                    }))
                    .child(div().text_size(px(12.)).font_weight(FontWeight::SEMIBOLD).text_color(rgb(if active { t.text2 } else { t.text3 })).child(SharedString::from(ws.to_string())))
                    .when_some(name, |el, n| {
                        el.child(
                            div()
                                .text_size(px(12.5))
                                .font_weight(if active { FontWeight::MEDIUM } else { FontWeight::NORMAL })
                                .text_color(rgb(if active { t.text } else { t.text2 }))
                                .child(SharedString::from(n)),
                        )
                    })
                    .when_some(urgent, |el, s| el.child(div().size(px(6.)).rounded_full().bg(rgb(status_color(&t, s))))),
            );
        }
        let session = if st.session.is_empty() { self.status.to_string() } else { st.session.clone() };
        div()
            .flex()
            .flex_none()
            .items_center()
            .gap(px(6.))
            .h(px(TOPBAR_H))
            .px(px(10.))
            .bg(rgb(t.bg))
            .border_b_1()
            .border_color(rgb(t.hairline))
            .when(!self.sidebar, |el| {
                el.child(icon_button(&t, "show-sidebar", "icons/panel-left.svg").on_click(cx.listener(|this, _, w, cx| this.run(Act::ToggleSidebar, w, cx))))
            })
            .child(div().pl(px(6.)).flex_none().text_size(px(13.)).font_weight(FontWeight::SEMIBOLD).text_color(rgb(t.text)).child(SharedString::from(session)))
            .child(div().flex_none().text_size(px(13.)).text_color(rgb(t.text3)).child("/"))
            .child(tabs)
            .child(div().flex_1())
            .child(icon_button(&t, "split-right", "icons/columns-2.svg").on_click(cx.listener(|this, _, w, cx| this.run(Act::Tape("Split", &["vertical"]), w, cx))))
            .child(icon_button(&t, "split-down", "icons/rows-2.svg").on_click(cx.listener(|this, _, w, cx| this.run(Act::Tape("Split", &["horizontal"]), w, cx))))
            .child(icon_button(&t, "zoom", "icons/maximize-2.svg").on_click(cx.listener(|this, _, w, cx| this.run(Act::Tape("ToggleZoom", &[]), w, cx))))
    }

    pub(super) fn render_palette(&mut self, window: &mut Window, cx: &mut Context<Self>) -> Option<AnyElement> {
        let p = self.palette.as_ref()?;
        let t = self.theme.clone();
        let list = palette::filter(&p.entries, &p.query);
        let selected = p.selected.min(list.len().saturating_sub(1));
        const SHOWN: usize = 10;
        let start = selected.saturating_sub(SHOWN - 1);
        let mut items = div().flex().flex_col().px(px(8.)).pb(px(8.));
        let mut last: Option<Section> = if start > 0 { list.get(start - 1).map(|e| e.section) } else { None };
        let angle = self.spin_angle();
        for (i, e) in list.iter().enumerate().skip(start).take(SHOWN) {
            if last != Some(e.section) {
                items = items.child(div().px(px(10.)).pt(px(10.)).pb(px(4.)).text_size(px(11.)).font_weight(FontWeight::MEDIUM).text_color(rgb(t.text3)).child(e.section.label()));
                last = Some(e.section);
            }
            let act = e.act.clone();
            let is_sel = i == selected;
            let lead: AnyElement = match e.icon {
                Icon::State(s) => glyph(&t, s, angle),
                Icon::Session => div().size(px(16.)).flex().items_center().justify_center().child(icon("icons/layers.svg", t.text3, 14.)).into_any_element(),
                Icon::Command => div().size(px(16.)).flex().items_center().justify_center().child(icon("icons/chevron-right.svg", t.text3, 14.)).into_any_element(),
                Icon::Theme => div().size(px(16.)).flex().items_center().justify_center().child(icon("icons/palette.svg", t.text3, 14.)).into_any_element(),
            };
            items = items.child(
                div()
                    .id(("pal", i))
                    .relative()
                    .flex()
                    .items_center()
                    .gap(px(10.))
                    .px(px(10.))
                    .h(px(36.))
                    .flex_none()
                    .rounded(px(7.))
                    .cursor_pointer()
                    .when(is_sel, |el| el.bg(rgb(t.raised_sel)))
                    .when(!is_sel, |el| el.hover(|s| s.bg(rgb(t.raised_sel))))
                    .on_click(cx.listener(move |this, _, window, cx| {
                        this.palette = None;
                        this.run(act.clone(), window, cx);
                    }))
                    .when(is_sel, |el| el.child(div().absolute().left_0().top(px(10.)).w(px(2.)).h(px(16.)).rounded(px(1.)).bg(rgb(t.accent))))
                    .child(lead)
                    .child(
                        div()
                            .flex_none()
                            .max_w(px(300.))
                            .truncate()
                            .text_size(px(13.))
                            .font_weight(if is_sel { FontWeight::MEDIUM } else { FontWeight::NORMAL })
                            .text_color(rgb(t.text))
                            .child(SharedString::from(e.title.clone())),
                    )
                    .child(div().flex_1().min_w_0().truncate().text_size(px(12.)).text_color(rgb(t.text3)).child(SharedString::from(e.subtitle.clone())))
                    .child(keycaps(&t, e.hint)),
            );
        }
        if list.is_empty() {
            items = items.child(div().px(px(10.)).py(px(14.)).text_size(px(13.)).text_color(rgb(t.text3)).child("Nothing matches."));
        }
        let query_el = if p.query.is_empty() {
            div().text_color(rgb(t.text3)).child("Search panes, sessions and commands")
        } else {
            div().text_color(rgb(t.text)).child(SharedString::from(p.query.clone()))
        };
        let shadow = |y: f32, blur: f32, a: f32| BoxShadow { color: hsla(0., 0., 0., a), offset: point(px(0.), px(y)), blur_radius: px(blur), spread_radius: px(0.), inset: false };
        let caret = div().w(px(1.5)).h(px(18.)).bg(rgb(t.accent)).with_animation("caret", Animation::new(Duration::from_millis(1060)).repeat(), |el, d| el.opacity(if d < 0.5 { 1. } else { 0. }));
        let run_label = list.get(selected).map(|e| if matches!(e.act, Act::Jump { .. } | Act::Session(_)) { "Open" } else { "Run" }).unwrap_or("Run");
        let footer_key = |label: &'static str, key: &'static str| {
            div()
                .flex()
                .items_center()
                .gap(px(6.))
                .child(div().text_color(rgb(t.text2)).child(label))
                .child(div().h(px(18.)).px(px(5.)).flex().items_center().rounded(px(4.)).bg(rgb(t.hover)).text_size(px(10.5)).text_color(rgb(t.text2)).child(key))
        };
        let panel = div()
            .w(px(640.))
            .bg(rgb(t.raised))
            .border_1()
            .border_color(rgb(t.hairline))
            .rounded(px(12.))
            .shadow(vec![shadow(2., 4., 0.14), shadow(18., 40., if t.light { 0.16 } else { 0.4 })])
            .overflow_hidden()
            .child(
                div()
                    .flex()
                    .items_center()
                    .gap(px(10.))
                    .px(px(18.))
                    .h(px(52.))
                    .border_b_1()
                    .border_color(rgb(t.hairline))
                    .text_size(px(15.))
                    .child(icon("icons/search.svg", t.text3, 16.))
                    .child(div().flex().items_center().gap(px(1.)).child(query_el).child(caret)),
            )
            .child(items)
            .child(
                div()
                    .flex()
                    .justify_between()
                    .items_center()
                    .px(px(18.))
                    .h(px(36.))
                    .border_t_1()
                    .border_color(rgb(t.hairline))
                    .text_size(px(11.5))
                    .text_color(rgb(t.text3))
                    .child(SharedString::from(format!("{} {}", list.len(), if list.len() == 1 { "result" } else { "results" })))
                    .child(div().flex().items_center().gap(px(14.)).child(footer_key(run_label, "↵")).child(footer_key("Close", "esc"))),
            );
        let top = (f32::from(window.viewport_size().height) * 0.12).max(48.);
        Some(
            div()
                .id("palette-scrim")
                .absolute()
                .inset_0()
                .flex()
                .justify_center()
                .bg(Theme::alpha(0x000000, if t.light { 0x14 } else { 0x33 }))
                .on_click(cx.listener(|this, _, _, cx| {
                    this.palette = None;
                    cx.notify();
                }))
                .child(
                    div()
                        .pt(px(top))
                        .child(div().id("palette-panel").on_click(|_, _, cx| cx.stop_propagation()).child(panel))
                        .with_animation("palette-in", Animation::new(Duration::from_millis(140)).with_easing(ease_out_quint()), |el, d| el.opacity(d).mt(px(-6. * (1. - d)))),
                )
                .into_any_element(),
        )
    }
}
