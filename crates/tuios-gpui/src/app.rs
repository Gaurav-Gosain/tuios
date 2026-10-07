//! The main window: the title band, the sidebar of every session and agent,
//! the stage with the panes and their headers, and the command palette. The
//! design is docs/design/FINAL.md.
//!
//! `TuiosApp` holds the model and renders the root. Each part of the window
//! is a view of its own (views.rs), cached by GPUI and redrawn only when it
//! is told to: pane output redraws the stage, an agent state change redraws
//! the sidebar, a key in the palette redraws the palette. A notify on
//! `TuiosApp` itself redraws every part.

mod chrome;
mod grid;
mod views;

use crate::control;
use crate::fleet::{self, PaneInfo, Status};
use crate::keys;
use crate::painter::Metrics;
use crate::palette::{self, Act, Entry};
use crate::pane::{Pane, apply_theme};
use crate::stats::FrameStats;
use crate::theme::Theme;
use ghostty_vt::{KeyAction, MouseAction, MouseGeometry, MouseTracking};
use gpui::*;
use std::cell::RefCell;
use std::collections::{HashMap, HashSet};
use std::path::PathBuf;
use std::rc::Rc;
use std::time::{Duration, Instant};
use tuios_proto::{Bridge, Command, Launch, Message, Sender, SessionSummary, State};
pub use views::SpinSlots;

/// The title band across the top of the window.
pub const BAND_H: f32 = 40.;
/// The sidebar's default width, and its limits when dragged.
pub const SIDEBAR_W: f32 = 256.;
pub const SIDEBAR_MIN: f32 = 220.;
pub const SIDEBAR_MAX: f32 = 360.;
/// The rail that replaces the sidebar in a narrow window.
pub const RAIL_W: f32 = 52.;
/// Window widths below which the sidebar becomes a rail, and the rail hides.
const RAIL_BELOW: f32 = 1000.;
const HIDE_RAIL_BELOW: f32 = 720.;
/// The stage's margin at the right and bottom, and at the left without a
/// sidebar.
const STAGE_MARGIN: f32 = 8.;
/// The pane header, and the room the bridge keeps around each pane's text
/// (docs/design/FINAL.md section 11): from the top of the pane's slot to
/// its text, and from each other slot edge to the text.
const HEADER_H: f32 = 28.;
const PANE_TOP: f32 = 32.;
const PANE_PAD: f32 = 12.;
/// How long "Needs you" takes to arrive.
const ARRIVE: Duration = Duration::from_millis(400);
/// The resize badge shows this long after the last change, then fades.
const BADGE: Duration = Duration::from_millis(750);
const BADGE_FADE: Duration = Duration::from_millis(150);
/// The scrollbar shows this long after the last scroll, then fades.
const SCROLLBAR: Duration = Duration::from_millis(600);
const SCROLLBAR_FADE: Duration = Duration::from_millis(150);
/// Nothing shows for a connection faster than this.
const CONNECT_QUIET: Duration = Duration::from_millis(400);
/// A pane without focus drops its row caches after this long unchanged.
const IDLE_CACHE: Duration = Duration::from_secs(30);
/// Cursor blink, on and off.
const BLINK: Duration = Duration::from_millis(600);

#[derive(Clone, Debug)]
pub struct Config {
    pub tuios: PathBuf,
    pub session: Option<String>,
    pub env: Vec<(String, String)>,
    pub font_family: String,
    pub font_size: f32,
    pub line_height: f32,
    pub ligatures: bool,
    /// A theme to use instead of the one the bridge's tuios config names.
    pub theme: Option<String>,
    pub ui_font: String,
    /// Lines of history per pane.
    pub scrollback: usize,
    /// No animations: every duration is zero and the working icon is still.
    pub reduce_motion: bool,
    /// Show frame timings over the grid.
    pub show_fps: bool,
    /// A control socket for tests (see control.rs).
    pub control: Option<PathBuf>,
}

/// Where the sidebar is.
#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub enum Side {
    /// The full sidebar, in the window's flow.
    #[default]
    Full,
    /// A 52 px rail of state icons.
    Rail,
    /// Nothing.
    Hidden,
}

/// The window's geometry, worked out once per frame from its size.
#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub struct Layout {
    pub width: f32,
    pub height: f32,
    pub side: Side,
    /// The width the sidebar or rail takes in the flow.
    pub side_w: f32,
    /// The full sidebar over the stage, in a narrow window.
    pub overlay: bool,
    pub stage: Bounds<Pixels>,
    /// The grid's top left corner: the top of the header row of the top
    /// panes.
    pub grid_origin: Point<Pixels>,
}

#[derive(Clone, Copy, Debug, Default, PartialEq)]
struct Grid {
    /// The top left of cell (0, 0). One cell row above it holds the headers
    /// of the panes at the top.
    origin: Point<Pixels>,
    cols: u16,
    rows: u16,
}

/// A mouse drag in progress.
#[derive(Clone, Debug)]
enum Drag {
    /// Selecting text in a pane, from an anchor cell.
    Select { pty: String, anchor: (u16, u16) },
    /// Reporting to the program in a pane, which asked for mouse events.
    Report { pty: String, button: u8 },
    /// Moving the sidebar's edge.
    Sidebar,
}

/// What a control command turns into.
enum Plan {
    Events(Vec<PlatformInput>),
    /// Keystrokes, dispatched the way the platform does, text input included.
    Keys(Vec<Keystroke>),
    Reply(String),
}

pub struct PaletteUi {
    query: String,
    selected: usize,
    entries: Vec<Entry>,
    /// When the palette began to close; it fades out, then goes.
    closing: Option<Instant>,
}

/// A pane to show once its session is attached.
#[derive(Clone, Debug)]
struct Jump {
    session: String,
    window: String,
    workspace: u32,
}

/// How the connection to the daemon stands, for the empty states.
#[derive(Clone, Debug, PartialEq)]
enum Link {
    Connecting(Instant),
    Attached,
    /// The bridge could not start or attach.
    Failed(String),
    /// The session the bridge was attached to went away.
    Ended,
}

pub struct TuiosApp {
    cfg: Config,
    focus: FocusHandle,
    views: Option<views::Views>,
    bridge: Option<Bridge>,
    sender: Option<Sender>,
    generation: u64,
    state: Option<State>,
    /// The attached session's panes, worked out when its state changes.
    attached: Vec<PaneInfo>,
    panes: HashMap<String, Pane>,
    metrics: Option<Metrics>,
    epoch: u64,
    theme: Rc<Theme>,
    layout: Layout,
    grid: Grid,
    sent_size: (u16, u16),
    resize_task: Option<Task<()>>,
    /// The full sidebar in a wide window.
    sidebar: bool,
    /// The full sidebar over the stage in a narrow window.
    sidebar_overlay: bool,
    sidebar_w: f32,
    /// Session groups the person folded or opened, over the default.
    folded: HashMap<String, bool>,
    palette: Option<PaletteUi>,
    sessions: Vec<SessionSummary>,
    /// Panes of every session, from the bridge's fleet events.
    fleet: Vec<PaneInfo>,
    /// The bridge sends fleet events; no need to poll.
    fleet_pushed: bool,
    /// Windows whose finished turn nobody has looked at.
    unread: HashSet<String>,
    link: Link,
    status: SharedString,
    drag: Option<Drag>,
    /// The focused window as this client last asked for it, ahead of the
    /// state that confirms it.
    focus_wanted: Option<(String, Instant)>,
    jump: Option<Jump>,
    asked_first_window: bool,
    asked_tiling: bool,
    pub stats: FrameStats,
    last_title: String,
    /// Animations ask for frames while running.
    animating: bool,
    window_active: bool,
    /// Typing restarts the cursor blink.
    last_input: Instant,
    blink_task: Option<Task<()>>,
    spin_task: Option<Task<()>>,
    poll_task: Option<Task<()>>,
    wake_task: Option<(Instant, Task<()>)>,
    any_working: bool,
    /// Text an input method is composing, drawn at the cursor until committed.
    marked: Option<String>,
    /// Themes tuios knows, for the palette, and the one last picked there.
    theme_names: Vec<String>,
    theme_wanted: Option<String>,
    /// When each window started to need you, for its arrival.
    need_since: HashMap<String, Instant>,
    /// When the grid size last changed, for the resize badge.
    resized_at: Option<Instant>,
    /// Shaped header text per window, rebuilt when what it shows changes.
    headers: HashMap<String, grid::HeaderLines>,
    spin_slots: Rc<RefCell<SpinSlots>>,
    /// One view per pane, by PTY.
    pane_views: HashMap<String, Entity<views::PaneView>>,
    /// Paint time of the stage frame in progress, for the stats.
    frame_ms: f64,
    /// The session to attach once the first frame knows the grid's size.
    first_connect: Option<Option<String>>,
    /// Frames drawn (root renders), for the `stats` control command.
    frames: u64,
}

impl TuiosApp {
    pub fn new(cfg: Config, window: &mut Window, cx: &mut Context<Self>) -> Self {
        let focus = cx.focus_handle();
        window.focus(&focus, cx);
        let theme = Rc::new(Theme::fallback());
        let mut cfg = cfg;
        let installed = window.text_system().all_font_names();
        let mut wanted: Vec<&str> = vec![cfg.font_family.as_str()];
        wanted.extend(crate::config::TERMINAL_FONTS);
        cfg.font_family = crate::config::pick_font(&wanted, &installed, true);
        let mut wanted: Vec<&str> = vec![cfg.ui_font.as_str()];
        wanted.extend(crate::config::UI_FONTS);
        cfg.ui_font = crate::config::pick_font(&wanted, &installed, false);
        let spin_slots = Rc::new(RefCell::new(SpinSlots::new(&theme, cfg.reduce_motion)));
        let me = cx.entity();
        let views = views::Views::new(&me, spin_slots.clone(), cx);
        let mut this = TuiosApp {
            cfg,
            focus,
            views: Some(views),
            bridge: None,
            sender: None,
            generation: 0,
            state: None,
            attached: Vec::new(),
            panes: HashMap::new(),
            metrics: None,
            epoch: 1,
            theme,
            layout: Layout::default(),
            grid: Grid::default(),
            sent_size: (0, 0),
            resize_task: None,
            sidebar: true,
            sidebar_overlay: false,
            sidebar_w: SIDEBAR_W,
            folded: HashMap::new(),
            palette: None,
            sessions: Vec::new(),
            fleet: Vec::new(),
            fleet_pushed: false,
            unread: HashSet::new(),
            link: Link::Connecting(Instant::now()),
            status: "Starting".into(),
            drag: None,
            focus_wanted: None,
            jump: None,
            asked_first_window: false,
            asked_tiling: false,
            stats: FrameStats::default(),
            last_title: String::new(),
            animating: false,
            window_active: window.is_window_active(),
            last_input: Instant::now(),
            blink_task: None,
            spin_task: None,
            poll_task: None,
            wake_task: None,
            any_working: false,
            marked: None,
            theme_names: Vec::new(),
            theme_wanted: None,
            need_since: HashMap::new(),
            resized_at: None,
            headers: HashMap::new(),
            spin_slots,
            pane_views: HashMap::new(),
            frame_ms: 0.,
            first_connect: None,
            frames: 0,
        };
        // The bridge starts at the grid's size, which the first frame works
        // out; a later resize can lose a race with the bridge's own start.
        this.first_connect = Some(this.cfg.session.clone());
        this.tick_ages(cx);
        cx.observe_window_activation(window, |this, window, cx| {
            this.window_active = window.is_window_active();
            this.update_timers(cx);
            this.refresh_stage(cx);
            this.refresh_spin(cx);
        })
        .detach();
        if let Some(path) = this.cfg.control.clone() {
            this.serve_control(path, window, cx);
        }
        this
    }

    // ---- redraws -----------------------------------------------------------

    fn views(&self) -> &views::Views {
        self.views.as_ref().expect("views")
    }

    /// Redraws the stage around the panes: the stage, headers, splits.
    fn refresh_grid(&self, cx: &mut Context<Self>) {
        self.views().grid.update(cx, |_, cx| cx.notify());
    }

    /// Redraws the stage and every pane on it.
    fn refresh_stage(&self, cx: &mut Context<Self>) {
        self.refresh_grid(cx);
        for v in self.pane_views.values() {
            v.update(cx, |_, cx| cx.notify());
        }
    }

    /// Redraws one pane's content.
    fn refresh_pane(&self, pty: &str, cx: &mut Context<Self>) {
        match self.pane_views.get(pty) {
            Some(v) => v.update(cx, |_, cx| cx.notify()),
            None => self.refresh_grid(cx),
        }
    }

    /// Redraws the focused pane, for its cursor.
    fn refresh_focused(&self, cx: &mut Context<Self>) {
        match self.focused_pty() {
            Some(p) => self.refresh_pane(&p, cx),
            None => self.refresh_grid(cx),
        }
    }

    /// Redraws the sidebar and the title band.
    fn refresh_chrome(&self, cx: &mut Context<Self>) {
        self.views().sidebar.update(cx, |_, cx| cx.notify());
        self.views().band.update(cx, |_, cx| cx.notify());
    }

    fn refresh_sidebar(&self, cx: &mut Context<Self>) {
        self.views().sidebar.update(cx, |_, cx| cx.notify());
    }

    fn refresh_palette(&self, cx: &mut Context<Self>) {
        self.views().palette.update(cx, |_, cx| cx.notify());
    }

    /// Redraws the working icons, and nothing else.
    fn refresh_spin(&self, cx: &mut Context<Self>) {
        self.views().spin.update(cx, |_, cx| cx.notify());
    }

    /// Redraws the stage at `at`, for something that changes then: a fade
    /// that starts, a delayed state. One timer serves the earliest wish.
    fn wake_grid_at(&mut self, at: Instant, cx: &mut Context<Self>) {
        if self.wake_task.as_ref().is_some_and(|(t, _)| *t <= at && *t > Instant::now()) {
            return;
        }
        let wait = at.saturating_duration_since(Instant::now());
        let task = cx.spawn(async move |this, cx| {
            cx.background_executor().timer(wait).await;
            let _ = this.update(cx, |this, cx| {
                this.wake_task = None;
                this.refresh_stage(cx);
            });
        });
        self.wake_task = Some((at, task));
    }

    /// Starts or stops the timers that animate: the working icon, the
    /// cursor blink, and the fallback fleet poll.
    fn update_timers(&mut self, cx: &mut Context<Self>) {
        let spin = self.any_working && self.window_active && !self.cfg.reduce_motion;
        if spin && self.spin_task.is_none() {
            self.spin_task = Some(cx.spawn(async move |this, cx| {
                loop {
                    // 10 frames a second, the working icon's own rate.
                    cx.background_executor().timer(Duration::from_millis(100)).await;
                    let go = this.update(cx, |this, cx| {
                        this.refresh_spin(cx);
                        this.any_working && this.window_active && !this.cfg.reduce_motion
                    });
                    if !matches!(go, Ok(true)) {
                        let _ = this.update(cx, |this, cx| {
                            this.spin_task = None;
                            this.refresh_spin(cx);
                        });
                        break;
                    }
                }
            }));
        }
        let blink = self.cursor_blinks();
        if blink && self.blink_task.is_none() {
            self.blink_task = Some(cx.spawn(async move |this, cx| {
                loop {
                    let wait = this
                        .update(cx, |this, _| {
                            let ms = this.last_input.elapsed().as_millis() as u64 % BLINK.as_millis() as u64;
                            Duration::from_millis(BLINK.as_millis() as u64 - ms)
                        })
                        .unwrap_or(BLINK);
                    cx.background_executor().timer(wait).await;
                    let go = this.update(cx, |this, cx| {
                        this.refresh_focused(cx);
                        this.cursor_blinks()
                    });
                    if !matches!(go, Ok(true)) {
                        let _ = this.update(cx, |this, _| this.blink_task = None);
                        break;
                    }
                }
            }));
        }
        let poll = !self.fleet_pushed && self.window_active && matches!(self.link, Link::Attached);
        if poll && self.poll_task.is_none() {
            self.poll_fleet(cx);
        } else if !poll {
            self.poll_task = None;
        }
    }

    /// Whether the focused pane's cursor blinks now.
    fn cursor_blinks(&self) -> bool {
        self.window_active
            && self.palette.is_none()
            && self.focused_pty().and_then(|p| self.panes.get(&p)).is_some_and(|p| {
                let c = p.term.screen().cursor;
                c.blinking && c.visible
            })
    }

    /// Whether the cursor shows in this frame of its blink.
    fn blink_on(&self) -> bool {
        !self.cursor_blinks() || (self.last_input.elapsed().as_millis() / BLINK.as_millis()) % 2 == 0
    }

    /// Redraws the sidebar once a minute, when an age label may change, and
    /// drops the row caches of panes that are not focused and have not
    /// changed for `IDLE_CACHE`. They keep their history, and a later paint
    /// builds the rows again.
    fn tick_ages(&mut self, cx: &mut Context<Self>) {
        cx.spawn(async move |this, cx| {
            loop {
                let wait = 60_000 - (fleet::now_ms() % 60_000) as u64 + 50;
                cx.background_executor().timer(Duration::from_millis(wait)).await;
                let alive = this.update(cx, |this, cx| {
                    if this.attached.iter().chain(this.fleet.iter()).any(|p| p.status.is_agent() && p.since_ms > 0) {
                        this.refresh_sidebar(cx);
                    }
                    let focused = this.focused_pty();
                    for (pty, p) in this.panes.iter_mut() {
                        if focused.as_deref() != Some(pty.as_str()) && p.changed_at.elapsed() >= IDLE_CACHE {
                            p.painter.clear();
                        }
                    }
                });
                if alive.is_err() {
                    break;
                }
            }
        })
        .detach();
    }

    // ---- control socket ----------------------------------------------------

    fn serve_control(&mut self, path: PathBuf, window: &mut Window, cx: &mut Context<Self>) {
        let (tx, rx) = async_channel::unbounded::<control::Request>();
        control::serve(path, tx);
        let handle = window.window_handle();
        cx.spawn(async move |this, cx| {
            while let Ok(req) = rx.recv().await {
                let answer = cx
                    .update_window(handle, |_, window, cx| {
                        let plan = this.update(cx, |this, cx| this.control_plan(&req.line, window, cx));
                        match plan {
                            Ok(Ok(Plan::Events(evs))) => {
                                for e in evs {
                                    window.dispatch_event(e, cx);
                                }
                                "ok".to_string()
                            }
                            Ok(Ok(Plan::Keys(keys))) => {
                                for k in keys {
                                    window.dispatch_keystroke(k, cx);
                                }
                                "ok".to_string()
                            }
                            Ok(Ok(Plan::Reply(s))) => s,
                            Ok(Err(e)) => format!("err {e}"),
                            Err(e) => format!("err {e}"),
                        }
                    })
                    .unwrap_or_else(|e| format!("err {e}"));
                let _ = req.reply.send(answer);
            }
        })
        .detach();
    }

    /// The window position of the centre of a cell of the `n`-th visible pane.
    fn cell_pos(&self, n: usize, col: f32, row: f32) -> Result<Point<Pixels>, String> {
        let m = self.metrics.as_ref().ok_or("no metrics yet")?;
        let st = self.state.as_ref().ok_or("not attached")?;
        let mut vis = st.visible();
        // Reading order, so an index names the same pane whatever the stacking.
        vis.sort_by_key(|w| (w.y, w.x));
        let w = vis.get(n).ok_or_else(|| format!("no pane {n}"))?;
        let o = self.pane_rect(m, w).content.origin;
        let (cw, ch) = (f32::from(m.cell_w), f32::from(m.cell_h));
        Ok(point(o.x + px((col + 0.5) * cw), o.y + px((row + 0.5) * ch)))
    }

    fn control_plan(&mut self, line: &str, _window: &mut Window, cx: &mut Context<Self>) -> Result<Plan, String> {
        let mut it = line.split_whitespace();
        let cmd = it.next().ok_or("empty command")?;
        let mut num = |name: &str| -> Result<f32, String> { it.next().ok_or(format!("missing {name}"))?.parse::<f32>().map_err(|e| e.to_string()) };
        Ok(match cmd {
            "key" => {
                let k = line[3..].trim();
                Plan::Keys(vec![Keystroke::parse(k).map_err(|e| e.to_string())?])
            }
            "type" => {
                let text = line.get(5..).unwrap_or("");
                Plan::Keys(
                    text.chars()
                        .map(|c| {
                            let shift = c.is_uppercase();
                            let key = if c == ' ' { "space".to_string() } else { c.to_lowercase().to_string() };
                            Keystroke { modifiers: Modifiers { shift, ..Default::default() }, key, key_char: Some(c.to_string()) }
                        })
                        .collect(),
                )
            }
            "click" => {
                let (x, y) = (num("x")?, num("y")?);
                let mut rest = line.split_whitespace().skip(3);
                let b = control::parse_button(rest.next());
                let count = rest.next().and_then(|c| c.parse().ok()).unwrap_or(1);
                let p = point(px(x), px(y));
                Plan::Events(vec![control::down(p, b, count), control::up(p, b, count)])
            }
            "move" => {
                let (x, y) = (num("x")?, num("y")?);
                Plan::Events(vec![control::moved(point(px(x), px(y)), None)])
            }
            "cellclick" => {
                let (n, c, r) = (num("pane")? as usize, num("col")?, num("row")?);
                let count = line.split_whitespace().nth(4).and_then(|c| c.parse().ok()).unwrap_or(1);
                let p = self.cell_pos(n, c, r)?;
                Plan::Events(vec![control::down(p, MouseButton::Left, count), control::up(p, MouseButton::Left, count)])
            }
            "drag" => {
                let (n, c1, r1, c2, r2) = (num("pane")? as usize, num("c1")?, num("r1")?, num("c2")?, num("r2")?);
                let a = self.cell_pos(n, c1, r1)?;
                let b = self.cell_pos(n, c2, r2)?;
                let mid = point(px((f32::from(a.x) + f32::from(b.x)) / 2.), px((f32::from(a.y) + f32::from(b.y)) / 2.));
                Plan::Events(vec![
                    control::down(a, MouseButton::Left, 1),
                    control::moved(mid, Some(MouseButton::Left)),
                    control::moved(b, Some(MouseButton::Left)),
                    control::up(b, MouseButton::Left, 1),
                ])
            }
            "wheel" | "lines" => {
                let (n, amount) = (num("pane")? as usize, num("amount")?);
                let p = self.cell_pos(n, 2., 2.)?;
                let delta = if cmd == "wheel" { ScrollDelta::Pixels(point(px(0.), px(amount))) } else { ScrollDelta::Lines(point(0., amount)) };
                Plan::Events(vec![control::wheel(p, delta)])
            }
            "palette" => {
                // Opens the palette with a query, for screenshots.
                let q = line.get(8..).unwrap_or("").to_string();
                self.open_palette(cx);
                if let Some(p) = self.palette.as_mut() {
                    p.query = q;
                }
                cx.notify();
                Plan::Reply("ok".into())
            }
            "dump" => Plan::Reply(self.dump()),
            "stats" => Plan::Reply(self.stats_json()),
            "resetstats" => {
                self.stats = FrameStats::default();
                self.frames = 0;
                Plan::Reply("ok".into())
            }
            _ => return Err(format!("unknown command {cmd}")),
        })
    }

    /// Frame and memory counters for the `stats` control command.
    fn stats_json(&self) -> String {
        let (p50, p95) = self.stats.paint_percentiles().unwrap_or((0., 0.));
        let (rss, anon) = crate::stats::memory_kb();
        format!(
            "{{\"pid\":{},\"frames\":{},\"grid_paints\":{},\"paint_p50_ms\":{p50:.3},\"paint_p95_ms\":{p95:.3},\"rss_kb\":{rss},\"anon_kb\":{anon}}}",
            std::process::id(),
            self.frames,
            self.stats.count()
        )
    }

    fn dump(&mut self) -> String {
        let (cw, ch) = self.metrics.as_ref().map(|m| (f32::from(m.cell_w), f32::from(m.cell_h))).unwrap_or((0., 0.));
        let scale = self.metrics.as_ref().map(|m| m.scale).unwrap_or(1.);
        let mut out = format!(
            "{{\"cols\":{},\"rows\":{},\"cell\":[{cw},{ch}],\"scale\":{scale},\"origin\":[{},{}],\"connected\":{},\"palette\":{},",
            self.grid.cols,
            self.grid.rows,
            f32::from(self.grid.origin.x),
            f32::from(self.grid.origin.y),
            self.link == Link::Attached,
            self.palette.is_some()
        );
        let focused = self.focused_id().unwrap_or_default();
        let st = self.state.clone().unwrap_or_default();
        out.push_str(&format!("\"session\":{},\"workspace\":{},\"focused\":{},\"panes\":[", control::json_str(&st.session), st.workspace, control::json_str(&focused)));
        let mut vis = st.visible();
        vis.sort_by_key(|w| (w.y, w.x));
        for (i, w) in vis.iter().enumerate() {
            if i > 0 {
                out.push(',');
            }
            let (x, y, c, r) = w.content();
            let (text, sel, scroll, bottom, size, modes) = match self.panes.get_mut(&w.pty) {
                Some(p) => {
                    let sel = p.term.selection_text().unwrap_or_default();
                    let bottom = p.term.at_bottom();
                    let size = (p.term.cols(), p.term.rows());
                    let modes = format!(
                        "{{\"kitty_flags\":{},\"mouse\":\"{:?}\",\"bracketed_paste\":{},\"alt_screen\":{}}}",
                        p.term.kitty_keyboard_flags(),
                        p.term.mouse_tracking(),
                        p.term.mode(ghostty_vt::MODE_BRACKETED_PASTE),
                        p.term.alt_screen()
                    );
                    (p.term.snapshot().plain_text(), sel, p.scroll_px, bottom, size, modes)
                }
                None => (String::new(), String::new(), 0., true, (0, 0), "{}".to_string()),
            };
            out.push_str(&format!(
                "{{\"id\":{},\"title\":{},\"cells\":[{x},{y},{c},{r}],\"term\":[{},{}],\"agent\":{},\"scroll_px\":{scroll},\"at_bottom\":{bottom},\"modes\":{modes},\"selection\":{},\"text\":{}}}",
                control::json_str(&w.id),
                control::json_str(w.label()),
                size.0,
                size.1,
                control::json_str(w.agent_state().unwrap_or("")),
                control::json_str(&sel),
                control::json_str(&text)
            ));
        }
        out.push_str("]}");
        out
    }

    // ---- geometry ----------------------------------------------------------

    /// The font metrics for the window's current scale. A new scale (the
    /// window moved to another screen) builds new metrics and starts every
    /// row cache over.
    fn ensure_metrics(&mut self, window: &mut Window) -> Metrics {
        let scale = window.scale_factor();
        if self.metrics.as_ref().is_some_and(|m| m.scale != scale) {
            self.epoch += 1;
            self.metrics = None;
            self.headers.clear();
        }
        if self.metrics.is_none() {
            self.metrics = Some(Metrics::new(&self.cfg.font_family, self.cfg.font_size, self.cfg.line_height, self.cfg.ligatures, window, self.epoch));
            for p in self.panes.values_mut() {
                p.painter.clear();
            }
        }
        self.metrics.clone().expect("metrics")
    }

    /// Works out the window's geometry: the sidebar, the stage and the grid,
    /// every edge on a whole device pixel (docs/design/FINAL.md section 5).
    fn compute_layout(&mut self, window: &mut Window, m: &Metrics) -> Layout {
        let vp = window.viewport_size();
        let (w, h) = (f32::from(vp.width), f32::from(vp.height));
        let s = m.scale;
        let snap = |v: f32| (v * s).round() / s;
        let side = if w >= RAIL_BELOW {
            Side::Full
        } else if w >= HIDE_RAIL_BELOW {
            Side::Rail
        } else {
            Side::Hidden
        };
        let (side_w, overlay) = match side {
            Side::Full if self.sidebar => (snap(self.sidebar_w), false),
            Side::Full => (0., false),
            Side::Rail => (RAIL_W, self.sidebar_overlay),
            Side::Hidden => (0., self.sidebar_overlay),
        };
        let x = if side_w > 0. { side_w } else { STAGE_MARGIN };
        let stage = Bounds::new(point(px(x), px(BAND_H)), size(px(snap(w - x - STAGE_MARGIN).max(40.)), px(snap(h - BAND_H - STAGE_MARGIN).max(40.))));
        // The grid in device pixels. Each pane's slot is its cells plus the
        // gap after it: half a gap column on each side and the gap row
        // above. The slots of the edge panes reach the stage edge, give or
        // take the leftover, which is split evenly; the bridge keeps the
        // padding inside each slot.
        let (cwd, chd) = (m.cell_w_dev as f32, m.cell_h_dev as f32);
        let sw = (f32::from(stage.size.width) * s).round();
        let sh = (f32::from(stage.size.height) * s).round();
        let cols = ((sw / cwd).floor() - 1.).max(1.);
        let rows = ((sh / chd).floor() - 1.).max(1.);
        let half = (cwd / 2.).floor();
        let gx = (x * s).round() + ((sw - (cols + 1.) * cwd) / 2.).floor() + half;
        let gy = (BAND_H * s).round() + ((sh - (rows + 1.) * chd) / 2.).floor() + chd;
        // Where the text of a pane at the top left starts.
        let grid_origin = point(px((gx - half + (PANE_PAD * s).round()) / s), px((gy - chd + (PANE_TOP * s).round()) / s));
        self.grid = Grid { origin: point(px(gx / s), px(gy / s)), cols: cols as u16, rows: rows as u16 };
        Layout { width: w, height: h, side, side_w, overlay, stage, grid_origin }
    }

    /// The room around each pane's text, in device pixels, as the bridge
    /// gets it: top, left, right, bottom.
    fn insets_dev(&self) -> [u32; 4] {
        let s = self.metrics.as_ref().map(|m| m.scale).unwrap_or(1.);
        let d = |v: f32| (v * s).round() as u32;
        [d(PANE_TOP), d(PANE_PAD), d(PANE_PAD), d(PANE_PAD)]
    }

    // ---- connection --------------------------------------------------------

    fn connect(&mut self, session: Option<String>, window: &mut Window, cx: &mut Context<Self>) {
        self.generation += 1;
        let generation = self.generation;
        self.bridge = None;
        self.sender = None;
        self.state = None;
        self.attached.clear();
        self.panes.clear();
        self.headers.clear();
        self.asked_first_window = false;
        self.asked_tiling = false;
        self.link = Link::Connecting(Instant::now());
        self.sent_size = (0, 0);
        let m = self.ensure_metrics(window);
        let (cols, rows) = if self.grid.cols > 0 { (self.grid.cols, self.grid.rows) } else { (120, 40) };
        let launch = Launch {
            tuios: self.cfg.tuios.clone(),
            session: session.clone(),
            cols,
            rows,
            cell_width: m.cell_w_dev,
            cell_height: m.cell_h_dev,
            insets: self.insets_dev().to_vec(),
            theme: self.theme_wanted.clone().or_else(|| self.cfg.theme.clone()),
            env: self.cfg.env.clone(),
        };
        let (tx, rx) = async_channel::unbounded::<Message>();
        match Bridge::spawn(&launch, move |msg| {
            let _ = tx.send_blocking(msg);
        }) {
            Ok((bridge, sender)) => {
                self.status = format!("Connecting to {}", session.as_deref().unwrap_or("tuios")).into();
                self.bridge = Some(bridge);
                self.sender = Some(sender);
                self.sent_size = (cols, rows);
            }
            Err(e) => {
                self.link = Link::Failed(e.to_string());
                cx.notify();
                return;
            }
        }
        cx.spawn(async move |this, cx| {
            while let Ok(first) = rx.recv().await {
                let mut batch = vec![first];
                while batch.len() < 8192 {
                    match rx.try_recv() {
                        Ok(m) => batch.push(m),
                        Err(_) => break,
                    }
                }
                if this.update(cx, |this, cx| this.apply(generation, batch, cx)).is_err() {
                    break;
                }
            }
        })
        .detach();
        // "Connecting" shows only for a slow connection.
        self.wake_grid_at(Instant::now() + CONNECT_QUIET, cx);
        cx.notify();
    }

    // ---- the fleet ---------------------------------------------------------

    /// Reads the daemon's sessions and every pane in them, every 5 s, for a
    /// bridge too old to send fleet events. Stops when one arrives, and while
    /// the window is not active.
    fn poll_fleet(&mut self, cx: &mut Context<Self>) {
        let tuios = self.cfg.tuios.clone();
        let env = self.cfg.env.clone();
        self.poll_task = Some(cx.spawn(async move |this, cx| {
            // Give the bridge a moment to send its first fleet event.
            cx.background_executor().timer(Duration::from_secs(3)).await;
            loop {
                let still = this.update(cx, |this, _| !this.fleet_pushed).unwrap_or(false);
                if !still {
                    break;
                }
                let (t, e) = (tuios.clone(), env.clone());
                let read = cx
                    .background_executor()
                    .spawn(async move {
                        let sessions = tuios_proto::list_sessions(&t, &e).ok();
                        let agents = fleet::fetch(&t, &e);
                        (sessions, agents)
                    })
                    .await;
                let alive = this.update(cx, |this, cx| {
                    if this.fleet_pushed {
                        return;
                    }
                    let (sessions, agents) = read;
                    this.on_fleet(sessions, agents, cx);
                });
                if alive.is_err() {
                    break;
                }
                cx.background_executor().timer(Duration::from_secs(5)).await;
            }
        }));
    }

    /// New sessions and fleet rows: redraws the sidebar only when they
    /// changed.
    fn on_fleet(&mut self, sessions: Option<Vec<SessionSummary>>, agents: Option<(Vec<PaneInfo>, HashSet<String>)>, cx: &mut Context<Self>) {
        let mut changed = false;
        if let Some(list) = sessions {
            if self.sessions != list {
                self.sessions = list;
                changed = true;
            }
        }
        if let Some((panes, unread)) = agents {
            if self.fleet != panes || self.unread != unread {
                self.fleet = panes;
                if self.unread != unread {
                    self.unread = unread;
                    self.rebuild_attached();
                }
                changed = true;
            }
        }
        if changed {
            self.recount_working(cx);
            self.refresh_chrome(cx);
            if self.palette.is_some() {
                self.refresh_palette(cx);
            }
        }
    }

    fn recount_working(&mut self, cx: &mut Context<Self>) {
        let current = self.current_session();
        let working = self.attached.iter().chain(self.fleet.iter().filter(|p| p.session != current)).any(|p| p.status == Status::Working);
        if working != self.any_working {
            self.any_working = working;
            self.update_timers(cx);
        }
    }

    fn current_session(&self) -> String {
        self.state.as_ref().map(|s| s.session.clone()).unwrap_or_default()
    }

    fn rebuild_attached(&mut self) {
        self.attached = match &self.state {
            Some(st) => {
                let focused = self.focused_id();
                fleet::from_state(st, focused.as_deref(), |id| self.unread.contains(id))
            }
            None => Vec::new(),
        };
    }

    /// Every pane in every session: the attached one live, the rest from the
    /// fleet.
    fn all_panes(&self) -> Vec<PaneInfo> {
        let current = self.current_session();
        let mut v = self.attached.clone();
        v.extend(self.fleet.iter().filter(|p| p.session != current).cloned());
        v
    }

    fn session_names(&self) -> Vec<String> {
        let mut names: Vec<String> = self.sessions.iter().map(|s| s.name.clone()).collect();
        let current = self.current_session();
        if !current.is_empty() && !names.contains(&current) {
            names.push(current);
        }
        names.sort();
        names
    }

    /// Shows a pane: attaches its session if needed, goes to its workspace and
    /// focuses it.
    fn jump_to(&mut self, session: String, window_id: String, workspace: u32, window: &mut Window, cx: &mut Context<Self>) {
        if session == self.current_session() {
            if self.state.as_ref().is_some_and(|s| s.workspace != workspace) {
                self.send(Command::workspace(workspace));
            }
            self.focus_window(&window_id, cx);
        } else {
            self.jump = Some(Jump { session: session.clone(), window: window_id, workspace });
            self.connect(Some(session), window, cx);
        }
    }

    /// The next pane that needs you, oldest first, after the focused one.
    fn next_needs_you(&self) -> Option<PaneInfo> {
        let mut waiting: Vec<PaneInfo> = self.all_panes().into_iter().filter(|p| matches!(p.status, Status::NeedsYou | Status::Errored)).collect();
        waiting.sort_by(fleet::order);
        let focused = self.focused_id();
        let at = waiting.iter().position(|p| Some(&p.window) == focused.as_ref());
        match at {
            Some(i) => waiting.get(i + 1).or_else(|| waiting.first()).filter(|p| Some(&p.window) != focused.as_ref()).cloned(),
            None => waiting.first().cloned(),
        }
    }

    fn step_session(&mut self, by: i32, window: &mut Window, cx: &mut Context<Self>) {
        let names = self.session_names();
        if names.len() < 2 {
            return;
        }
        let current = self.current_session();
        let i = names.iter().position(|n| *n == current).unwrap_or(0) as i32;
        let next = names[(i + by).rem_euclid(names.len() as i32) as usize].clone();
        self.connect(Some(next), window, cx);
    }

    /// What the sidebar and band show of the attached session. A change in
    /// a window's title alone (agents animate theirs) changes nothing here.
    fn chrome_key(&self) -> (Vec<PaneInfo>, u32, Vec<u32>, Vec<(String, String)>) {
        let st = self.state.as_ref();
        (
            self.attached.clone(),
            st.map(|s| s.workspace).unwrap_or(0),
            st.map(|s| s.occupied.clone()).unwrap_or_default(),
            st.map(|s| s.workspace_names.iter().map(|(a, b)| (a.clone(), b.clone())).collect()).unwrap_or_default(),
        )
    }

    fn on_state(&mut self, st: State, cx: &mut Context<Self>) {
        let before = self.chrome_key();
        // Panes whose window is gone are dropped with it.
        self.panes.retain(|pty, _| st.windows.iter().any(|w| &w.pty == pty));
        self.headers.retain(|id, _| st.windows.iter().any(|w| &w.id == id));
        if st.windows.is_empty() && !self.asked_first_window {
            self.asked_first_window = true;
            self.send(Command::tape("NewWindow", &[]));
        }
        if !st.tiling && !self.asked_tiling {
            self.asked_tiling = true;
            self.send(Command::tape("EnableTiling", &[]));
        }
        if let Some((want, at)) = &self.focus_wanted {
            if *want == st.focused || at.elapsed() > Duration::from_millis(800) {
                self.focus_wanted = None;
            }
        }
        // A pane that starts to need you gets its arrival.
        let was: HashSet<String> = self
            .state
            .as_ref()
            .map(|s| s.windows.iter().filter(|w| w.agent.as_deref() == Some("needs_input")).map(|w| w.id.clone()).collect())
            .unwrap_or_default();
        for w in &st.windows {
            if w.agent.as_deref() == Some("needs_input") && !was.contains(&w.id) && self.state.is_some() && !self.cfg.reduce_motion {
                self.need_since.insert(w.id.clone(), Instant::now());
                self.animating = true;
            }
        }
        self.need_since.retain(|_, t| t.elapsed() < ARRIVE);
        if let Some(j) = self.jump.clone().filter(|j| j.session == st.session) {
            if st.workspace != j.workspace {
                self.send(Command::workspace(j.workspace));
            }
            if st.focused != j.window {
                self.focus_wanted = Some((j.window.clone(), Instant::now()));
                self.send(Command::focus(&j.window));
            }
            self.jump = None;
        }
        let first = self.state.is_none();
        self.state = Some(st);
        self.rebuild_attached();
        if first || self.chrome_key() != before {
            self.recount_working(cx);
            self.refresh_chrome(cx);
        }
        self.update_timers(cx);
    }

    fn send(&self, cmd: Command) {
        if let Some(s) = &self.sender {
            s.command(&cmd);
        }
    }

    fn input(&self, pty: &str, bytes: &[u8]) {
        if let Some(s) = &self.sender {
            s.input(pty, bytes);
        }
    }

    fn apply(&mut self, generation: u64, batch: Vec<Message>, cx: &mut Context<Self>) {
        if generation != self.generation {
            return;
        }
        let cell = self.cell_px();
        let debug = std::env::var_os("TUIOS_GPUI_DEBUG").is_some();
        let mut all = false;
        // Output redraws its own pane; output to a pane on another
        // workspace redraws nothing. A state change redraws the stage.
        let mut stage = false;
        let mut touched: HashSet<String> = HashSet::new();
        for msg in batch {
            if debug {
                match &msg {
                    Message::Event(e) => eprintln!("[gpui] event {}", e.kind),
                    Message::Snapshot { pty, cols, rows, bytes } => eprintln!("[gpui] snapshot {pty} {cols}x{rows} {} bytes", bytes.len()),
                    Message::Output { pty, bytes } => eprintln!("[gpui] output {pty} {} bytes", bytes.len()),
                    Message::Resized { pty, cols, rows } => eprintln!("[gpui] resized {pty} {cols}x{rows}"),
                    Message::Closed(w) => eprintln!("[gpui] closed: {w}"),
                }
            }
            match msg {
                Message::Event(ev) => match ev.kind.as_str() {
                    "attached" => {
                        self.link = Link::Attached;
                        self.status = format!("Attached to {}", ev.message.unwrap_or_default()).into();
                        all = true;
                        // Send the size once more after the bridge settles, in
                        // case it applied its start size after an earlier one.
                        cx.spawn(async move |this, cx| {
                            cx.background_executor().timer(Duration::from_millis(300)).await;
                            let _ = this.update(cx, |this, cx| {
                                this.sent_size = (0, 0);
                                let (c, r) = (this.grid.cols, this.grid.rows);
                                this.schedule_resize(c, r, cx);
                            });
                        })
                        .detach();
                    }
                    "state" => {
                        if let Some(st) = ev.state {
                            self.on_state(st, cx);
                            stage = true;
                        }
                    }
                    "theme" => {
                        if let Some(t) = ev.theme {
                            all |= self.set_theme(Theme::from_export(&t), t.names);
                        }
                    }
                    "fleet" => {
                        if let Some(f) = ev.fleet {
                            self.fleet_pushed = true;
                            self.poll_task = None;
                            let agents = fleet::from_rows(&f.agents);
                            self.on_fleet(Some(f.sessions), Some(agents), cx);
                        }
                    }
                    _ => {}
                },
                Message::Snapshot { pty, cols, rows, bytes } => {
                    touched.insert(pty.clone());
                    let theme = self.theme.clone();
                    let sb = self.cfg.scrollback;
                    let pane = self.panes.entry(pty).or_insert_with(|| Pane::new(cols.max(1), rows.max(1), &theme, sb));
                    pane.restore(cols, rows, &bytes, &theme, cell);
                }
                Message::Output { pty, bytes } => {
                    touched.insert(pty.clone());
                    let (c, r) = self.layout_size(&pty);
                    let theme = self.theme.clone();
                    let sb = self.cfg.scrollback;
                    self.panes.entry(pty).or_insert_with(|| Pane::new(c, r, &theme, sb)).write(&bytes);
                }
                Message::Resized { pty, cols, rows } => {
                    touched.insert(pty.clone());
                    if let Some(p) = self.panes.get_mut(&pty) {
                        p.term.resize(cols, rows, cell.0, cell.1);
                    }
                }
                Message::Closed(why) => {
                    self.link = if self.link == Link::Attached { Link::Ended } else { Link::Failed(why.clone()) };
                    self.status = why.into();
                    self.sender = None;
                    all = true;
                }
            }
        }
        if all {
            cx.notify();
        } else if stage {
            self.refresh_stage(cx);
        } else {
            let visible: HashSet<String> = self.state.as_ref().map(|s| s.visible().iter().map(|w| w.pty.clone()).collect()).unwrap_or_default();
            for pty in touched.iter().filter(|p| visible.contains(*p)) {
                self.refresh_pane(pty, cx);
            }
        }
    }

    fn layout_size(&self, pty: &str) -> (u16, u16) {
        self.state
            .as_ref()
            .and_then(|s| s.windows.iter().find(|w| w.pty == pty))
            .map(|w| {
                let (_, _, c, r) = w.content();
                let (cw, ch) = self.cell_px();
                let [t, l, rt, b] = self.insets_dev();
                (tuios_proto::Window::inset_cells(c, cw, l, rt) as u16, tuios_proto::Window::inset_cells(r, ch, t, b) as u16)
            })
            .unwrap_or((80, 24))
    }

    /// The cell in device pixels, for programs that ask for it.
    fn cell_px(&self) -> (u32, u32) {
        self.metrics.as_ref().map(|m| (m.cell_w_dev, m.cell_h_dev)).unwrap_or((9, 20))
    }

    fn focused_id(&self) -> Option<String> {
        if let Some((w, _)) = &self.focus_wanted {
            return Some(w.clone());
        }
        self.state.as_ref().map(|s| s.focused.clone()).filter(|s| !s.is_empty())
    }

    fn focused_pty(&self) -> Option<String> {
        let id = self.focused_id()?;
        let st = self.state.as_ref()?;
        st.window(&id).map(|w| w.pty.clone())
    }

    fn focus_window(&mut self, id: &str, cx: &mut Context<Self>) {
        if self.focused_id().as_deref() != Some(id) {
            self.focus_wanted = Some((id.to_string(), Instant::now()));
            self.send(Command::focus(id));
            self.rebuild_attached();
            self.refresh_stage(cx);
            self.refresh_chrome(cx);
        }
    }

    fn schedule_resize(&mut self, cols: u16, rows: u16, cx: &mut Context<Self>) {
        if (cols, rows) == self.sent_size || cols < 2 || rows < 2 {
            return;
        }
        let (cw, ch) = self.cell_px();
        let insets = self.insets_dev();
        self.resize_task = Some(cx.spawn(async move |this, cx| {
            cx.background_executor().timer(Duration::from_millis(40)).await;
            let _ = this.update(cx, |this, _| {
                this.sent_size = (cols, rows);
                this.send(Command::resize(cols, rows, cw, ch, insets));
            });
        }));
    }

    // ---- actions -----------------------------------------------------------

    fn run(&mut self, act: Act, window: &mut Window, cx: &mut Context<Self>) {
        match act {
            Act::Tape(cmd, args) => self.send(Command::tape(cmd, args)),
            Act::Workspace(n) => self.send(Command::workspace(n)),
            Act::Session(name) => self.connect(Some(name), window, cx),
            Act::Jump { session, window: id, workspace } => self.jump_to(session, id, workspace, window, cx),
            Act::NextNeedsYou => {
                if let Some(p) = self.next_needs_you() {
                    self.jump_to(p.session, p.window, p.workspace, window, cx);
                }
            }
            Act::PrevSession => self.step_session(-1, window, cx),
            Act::NextSession => self.step_session(1, window, cx),
            Act::NewSession => {
                let n = (1..).map(|i| format!("session-{i}")).find(|n| !self.sessions.iter().any(|s| &s.name == n)).unwrap_or_default();
                self.connect(Some(n), window, cx);
            }
            Act::Copy => self.copy(cx),
            Act::Paste => self.paste(cx, false),
            Act::FontBigger => self.step_font(1, cx),
            Act::FontSmaller => self.step_font(-1, cx),
            Act::FontReset => self.set_font_size(15., cx),
            Act::ToggleSidebar => {
                if self.layout.width >= RAIL_BELOW {
                    self.sidebar = !self.sidebar;
                } else {
                    self.sidebar_overlay = !self.sidebar_overlay;
                }
                let mut slots = self.spin_slots.borrow_mut();
                slots.sidebar.clear();
                slots.cover = None;
            }
            Act::Theme(name) => {
                self.theme_wanted = Some(name.clone());
                self.send(Command::theme(&name));
            }
            Act::Quit => cx.quit(),
        }
        cx.notify();
    }

    fn open_palette(&mut self, cx: &mut Context<Self>) {
        let panes = self.all_panes();
        let sessions = self.session_names();
        let entries = palette::entries(&panes, &sessions, &self.current_session(), &self.theme_names, &self.theme.name);
        self.palette = Some(PaletteUi { query: String::new(), selected: 0, entries, closing: None });
        self.spin_slots.borrow_mut().hidden = true;
        self.update_timers(cx);
        cx.notify();
    }

    /// Fades the palette out, then removes it.
    fn close_palette(&mut self, cx: &mut Context<Self>) {
        let Some(p) = self.palette.as_mut() else { return };
        if p.closing.is_some() {
            return;
        }
        self.spin_slots.borrow_mut().hidden = false;
        if self.cfg.reduce_motion {
            self.palette = None;
            self.update_timers(cx);
            cx.notify();
            return;
        }
        p.closing = Some(Instant::now());
        self.refresh_palette(cx);
        self.refresh_spin(cx);
        cx.spawn(async move |this, cx| {
            cx.background_executor().timer(Duration::from_millis(80)).await;
            let _ = this.update(cx, |this, cx| {
                if this.palette.as_ref().is_some_and(|p| p.closing.is_some()) {
                    this.palette = None;
                    this.update_timers(cx);
                    cx.notify();
                }
            });
        })
        .detach();
    }

    /// The palette is open and takes keys.
    fn palette_open(&self) -> bool {
        self.palette.as_ref().is_some_and(|p| p.closing.is_none())
    }

    /// Returns whether everything must redraw.
    fn set_theme(&mut self, theme: Theme, names: Vec<String>) -> bool {
        if !names.is_empty() {
            self.theme_names = names;
        }
        if theme == *self.theme {
            return false;
        }
        self.theme = Rc::new(theme);
        let t = self.theme.clone();
        for p in self.panes.values_mut() {
            apply_theme(&mut p.term, &t);
        }
        self.spin_slots.borrow_mut().set_theme(&t);
        // Row caches hold resolved colours; new metrics start them over.
        self.epoch += 1;
        self.metrics = None;
        self.headers.clear();
        true
    }

    /// The text sizes the size keys step through.
    const SIZES: [f32; 7] = [12., 13., 14., 15., 16., 18., 20.];

    fn step_font(&mut self, by: i32, cx: &mut Context<Self>) {
        let cur = self.cfg.font_size;
        let next = if by > 0 { Self::SIZES.iter().find(|s| **s > cur + 0.01) } else { Self::SIZES.iter().rev().find(|s| **s < cur - 0.01) };
        if let Some(s) = next {
            self.set_font_size(*s, cx);
        }
    }

    fn set_font_size(&mut self, size: f32, cx: &mut Context<Self>) {
        self.cfg.font_size = size.round().clamp(7., 40.);
        self.epoch += 1;
        self.metrics = None;
        self.headers.clear();
        cx.notify();
    }

    fn copy(&mut self, cx: &mut Context<Self>) {
        if let Some(pty) = self.focused_pty() {
            if let Some(text) = self.panes.get(&pty).and_then(|p| p.term.selection_text()) {
                if !text.is_empty() {
                    cx.write_to_clipboard(ClipboardItem::new_string(text));
                }
            }
        }
    }

    fn paste(&mut self, cx: &mut Context<Self>, primary: bool) {
        let item = if primary { cx.read_from_primary() } else { cx.read_from_clipboard() };
        let Some(text) = item.and_then(|i| i.text()) else { return };
        let Some(pty) = self.focused_pty() else { return };
        if let Some(p) = self.panes.get_mut(&pty) {
            p.snap_to_bottom();
            let bytes = p.term.encode_paste(&text);
            self.input(&pty, &bytes);
        }
    }

    // ---- keyboard ----------------------------------------------------------

    fn on_key_down(&mut self, ev: &KeyDownEvent, window: &mut Window, cx: &mut Context<Self>) {
        let k = &ev.keystroke;
        // Plain text goes to the platform input handler, so input methods,
        // dead keys and compose sequences work; it arrives in
        // replace_text_in_range. Programs that asked the kitty protocol to
        // report every key as an escape code (flag 8) get key events instead.
        if !self.palette_open() && keys::is_text(k) && !ev.is_held {
            let report_all = self.focused_pty().and_then(|p| self.panes.get(&p)).is_some_and(|p| p.term.kitty_keyboard_flags() & 8 != 0);
            if !report_all {
                return;
            }
        }
        cx.stop_propagation();
        if self.palette_open() {
            self.palette_key(k, window, cx);
            return;
        }
        if let Some(act) = shortcut(k) {
            self.run(act, window, cx);
            return;
        }
        let m = &k.modifiers;
        if m.control && m.shift && k.key == "p" {
            self.open_palette(cx);
            return;
        }
        if m.shift && !m.control && !m.alt && (k.key == "pageup" || k.key == "pagedown") {
            if let Some(pty) = self.focused_pty() {
                let ch = self.metrics.as_ref().map(|m| f32::from(m.cell_h)).unwrap_or(20.);
                if let Some(p) = self.panes.get_mut(&pty) {
                    let page = p.term.rows().saturating_sub(2) as f32 * ch;
                    p.scroll_pixels(if k.key == "pageup" { page } else { -page }, ch);
                    p.scrolled_at = Some(Instant::now());
                    self.wake_grid_at(Instant::now() + SCROLLBAR, cx);
                    self.refresh_pane(&pty, cx);
                }
            }
            return;
        }
        let Some(pty) = self.focused_pty() else { return };
        let Some(p) = self.panes.get_mut(&pty) else { return };
        let text = keys::key_text(k);
        let action = if ev.is_held { KeyAction::Repeat } else { KeyAction::Press };
        let bytes = p.term.encode_key(&keys::key_input(k, &text, action));
        if !bytes.is_empty() {
            p.snap_to_bottom();
            p.term.clear_selection();
            self.input(&pty, &bytes);
            self.last_input = Instant::now();
            self.refresh_pane(&pty, cx);
        }
    }

    fn on_key_up(&mut self, ev: &KeyUpEvent, _window: &mut Window, cx: &mut Context<Self>) {
        // Key releases matter only to programs that asked for them through
        // the kitty keyboard protocol (flag 2, report event types).
        let Some(pty) = self.focused_pty() else { return };
        let Some(p) = self.panes.get_mut(&pty) else { return };
        if p.term.kitty_keyboard_flags() & 2 == 0 {
            return;
        }
        let text = keys::key_text(&ev.keystroke);
        let bytes = p.term.encode_key(&keys::key_input(&ev.keystroke, &text, KeyAction::Release));
        if !bytes.is_empty() {
            self.input(&pty, &bytes);
        }
        cx.stop_propagation();
    }

    fn palette_key(&mut self, k: &Keystroke, window: &mut Window, cx: &mut Context<Self>) {
        let Some(p) = self.palette.as_mut() else { return };
        let n = palette::filter(&p.entries, &p.query).len();
        match k.key.as_str() {
            "escape" => return self.close_palette(cx),
            "enter" => {
                let act = palette::filter(&p.entries, &p.query).get(p.selected).map(|e| e.act.clone());
                self.close_palette(cx);
                if let Some(act) = act {
                    self.run(act, window, cx);
                }
                return;
            }
            "up" => p.selected = p.selected.saturating_sub(1),
            "down" => p.selected = (p.selected + 1).min(n.saturating_sub(1)),
            "backspace" => {
                p.query.pop();
                p.selected = 0;
            }
            _ => {
                if k.modifiers.control && k.key == "p" && k.modifiers.shift {
                    return self.close_palette(cx);
                } else if let Some(c) = k.key_char.as_ref().filter(|_| !k.modifiers.control && !k.modifiers.alt) {
                    p.query.push_str(c);
                    p.selected = 0;
                }
            }
        }
        self.refresh_palette(cx);
    }

    // ---- mouse -------------------------------------------------------------

    /// The pane under a window position, topmost first, and the cell in it.
    fn hit(&self, pos: Point<Pixels>) -> Option<(String, String, i32, i32)> {
        let m = self.metrics.as_ref()?;
        let st = self.state.as_ref()?;
        let (cw, ch) = (f32::from(m.cell_w), f32::from(m.cell_h));
        let mut vis = st.visible();
        vis.reverse();
        // The padding around a pane's text belongs to the pane.
        for w in vis {
            let r = self.pane_rect(m, w);
            if r.body.contains(&pos) {
                let c = r.content;
                let gx = ((f32::from(pos.x) - f32::from(c.origin.x)) / cw).floor() as i32;
                let gy = ((f32::from(pos.y) - f32::from(c.origin.y)) / ch).floor() as i32;
                return Some((w.id.clone(), w.pty.clone(), gx, gy));
            }
        }
        None
    }

    /// Pixel position of `pos` inside the pane's content, and the pane's geometry.
    fn pane_local(&self, pty: &str, pos: Point<Pixels>) -> Option<(f32, f32, MouseGeometry)> {
        let m = self.metrics.as_ref()?;
        let st = self.state.as_ref()?;
        let w = st.windows.iter().find(|w| w.pty == pty)?;
        let c = self.pane_rect(m, w).content;
        let (cw, ch) = (f32::from(m.cell_w), f32::from(m.cell_h));
        let geo = MouseGeometry { width: f32::from(c.size.width) as u32, height: f32::from(c.size.height) as u32, cell_width: cw as u32, cell_height: ch as u32 };
        Some((f32::from(pos.x) - f32::from(c.origin.x), f32::from(pos.y) - f32::from(c.origin.y), geo))
    }

    fn cell_in(&self, pty: &str, pos: Point<Pixels>) -> Option<(u16, u16)> {
        let (lx, ly, g) = self.pane_local(pty, pos)?;
        let p = self.panes.get(pty)?;
        let x = (lx / g.cell_width.max(1) as f32).floor().clamp(0., p.term.cols().saturating_sub(1) as f32) as u16;
        let y = (ly / g.cell_height.max(1) as f32).floor().clamp(0., p.term.rows().saturating_sub(1) as f32) as u16;
        Some((x, y))
    }

    fn on_mouse_down(&mut self, ev: &MouseDownEvent, _window: &mut Window, cx: &mut Context<Self>) {
        // The stage's left edge moves the sidebar.
        if self.layout.side == Side::Full && self.sidebar && (f32::from(ev.position.x) - self.layout.side_w).abs() <= 4. && ev.button == MouseButton::Left {
            if ev.click_count >= 2 {
                self.sidebar_w = SIDEBAR_W;
                cx.notify();
            } else {
                self.drag = Some(Drag::Sidebar);
                cx.notify();
            }
            return;
        }
        let Some((id, pty, _, _)) = self.hit(ev.position) else {
            // A click on a pane's header focuses the pane.
            if let Some(id) = self.header_hit(ev.position) {
                self.focus_window(&id, cx);
            }
            return;
        };
        self.focus_window(&id, cx);
        let button = match ev.button {
            MouseButton::Left => 1u8,
            MouseButton::Middle => 3,
            MouseButton::Right => 2,
            _ => return,
        };
        let reporting = self.panes.get(&pty).is_some_and(|p| p.term.mouse_tracking() != MouseTracking::None) && !ev.modifiers.shift;
        if reporting {
            if let Some((lx, ly, geo)) = self.pane_local(&pty, ev.position) {
                let mods = mouse_mods(&ev.modifiers);
                if let Some(p) = self.panes.get_mut(&pty) {
                    let bytes = p.term.encode_mouse(MouseAction::Press, Some(button), mods, lx, ly, geo, true);
                    self.input(&pty, &bytes);
                }
                self.drag = Some(Drag::Report { pty, button });
            }
            return;
        }
        match button {
            1 => {
                let Some(cell) = self.cell_in(&pty, ev.position) else { return };
                if let Some(p) = self.panes.get_mut(&pty) {
                    match ev.click_count {
                        2 => p.term.select_word(cell.0, cell.1),
                        n if n >= 3 => p.term.select_line(cell.0, cell.1),
                        _ => p.term.clear_selection(),
                    }
                }
                self.refresh_pane(&pty, cx);
                self.drag = Some(Drag::Select { pty, anchor: cell });
            }
            3 => self.paste(cx, true),
            _ => {}
        }
    }

    fn on_mouse_move(&mut self, ev: &MouseMoveEvent, _window: &mut Window, cx: &mut Context<Self>) {
        match self.drag.clone() {
            Some(Drag::Sidebar) => {
                if ev.pressed_button != Some(MouseButton::Left) {
                    self.drag = None;
                    cx.notify();
                    return;
                }
                let w = f32::from(ev.position.x).round().clamp(SIDEBAR_MIN, SIDEBAR_MAX);
                if w != self.sidebar_w {
                    self.sidebar_w = w;
                    cx.notify();
                }
            }
            Some(Drag::Select { pty, anchor }) => {
                if ev.pressed_button != Some(MouseButton::Left) {
                    return;
                }
                if let Some(cell) = self.cell_in(&pty, ev.position) {
                    if let Some(p) = self.panes.get_mut(&pty) {
                        if cell != anchor {
                            p.term.select(anchor, cell, ev.modifiers.alt);
                            self.refresh_pane(&pty, cx);
                        }
                    }
                }
            }
            Some(Drag::Report { pty, button }) => {
                self.report_motion(&pty, Some(button), ev, true);
            }
            None => {
                // Programs in "any motion" mode want hover too.
                if let Some((_, pty, _, _)) = self.hit(ev.position) {
                    if self.panes.get(&pty).is_some_and(|p| p.term.mouse_tracking() == MouseTracking::Any) {
                        self.report_motion(&pty, None, ev, false);
                    }
                }
            }
        }
    }

    fn report_motion(&mut self, pty: &str, button: Option<u8>, ev: &MouseMoveEvent, pressed: bool) {
        let Some((lx, ly, geo)) = self.pane_local(pty, ev.position) else { return };
        let mods = mouse_mods(&ev.modifiers);
        if let Some(p) = self.panes.get_mut(pty) {
            let bytes = p.term.encode_mouse(MouseAction::Motion, button, mods, lx, ly, geo, pressed);
            self.input(pty, &bytes);
        }
    }

    fn on_mouse_up(&mut self, ev: &MouseUpEvent, _window: &mut Window, cx: &mut Context<Self>) {
        match self.drag.take() {
            Some(Drag::Report { pty, button }) => {
                if let Some((lx, ly, geo)) = self.pane_local(&pty, ev.position) {
                    let mods = mouse_mods(&ev.modifiers);
                    if let Some(p) = self.panes.get_mut(&pty) {
                        let bytes = p.term.encode_mouse(MouseAction::Release, Some(button), mods, lx, ly, geo, false);
                        self.input(&pty, &bytes);
                    }
                }
            }
            Some(Drag::Select { pty, .. }) => {
                // Selected text goes to the primary selection, as on any X11
                // or Wayland terminal.
                if let Some(text) = self.panes.get(&pty).and_then(|p| p.term.selection_text()) {
                    if !text.is_empty() {
                        cx.write_to_primary(ClipboardItem::new_string(text));
                    }
                }
            }
            Some(Drag::Sidebar) => cx.notify(),
            None => {}
        }
    }

    fn on_scroll(&mut self, ev: &ScrollWheelEvent, _window: &mut Window, cx: &mut Context<Self>) {
        let Some((_, pty, _, _)) = self.hit(ev.position) else { return };
        let ch = self.metrics.as_ref().map(|m| f32::from(m.cell_h)).unwrap_or(20.);
        let (dy, lines) = match ev.delta {
            ScrollDelta::Pixels(p) => (f32::from(p.y), false),
            ScrollDelta::Lines(l) => (l.y * ch * 3., true),
        };
        if dy == 0. {
            return;
        }
        let tracking = self.panes.get(&pty).map(|p| p.term.mouse_tracking()).unwrap_or(MouseTracking::None);
        if tracking != MouseTracking::None && !ev.modifiers.shift {
            let Some((lx, ly, geo)) = self.pane_local(&pty, ev.position) else { return };
            let n = ((dy.abs() / ch).round() as usize).clamp(1, 10);
            let button = if dy > 0. { 4 } else { 5 };
            let mods = mouse_mods(&ev.modifiers);
            if let Some(p) = self.panes.get_mut(&pty) {
                let mut out = Vec::new();
                for _ in 0..n {
                    out.extend(p.term.encode_mouse(MouseAction::Press, Some(button), mods, lx, ly, geo, false));
                }
                self.input(&pty, &out);
            }
            return;
        }
        let Some(p) = self.panes.get_mut(&pty) else { return };
        if p.term.alt_screen() {
            // A full-screen program without mouse reporting gets arrow keys,
            // as xterm's alternate scroll mode does.
            let n = ((dy.abs() / ch).round() as usize).clamp(1, 10);
            let key = if dy > 0. { "up" } else { "down" };
            let k = Keystroke { modifiers: Modifiers::default(), key: key.into(), key_char: None };
            let one = p.term.encode_key(&keys::key_input(&k, "", KeyAction::Press));
            let bytes = one.repeat(n);
            self.input(&pty, &bytes);
            return;
        }
        // A wheel step moves whole rows at once; a touchpad follows the
        // finger in whole device pixels.
        if lines {
            let rows = (dy / ch).round();
            p.scroll_pixels(rows * ch - p.scroll_px, ch);
        } else {
            p.scroll_pixels(dy, ch);
        }
        p.scrolled_at = Some(Instant::now());
        self.wake_grid_at(Instant::now() + SCROLLBAR, cx);
        self.refresh_pane(&pty, cx);
    }

    /// The pane whose header row is under `pos`.
    fn header_hit(&self, pos: Point<Pixels>) -> Option<String> {
        let (m, st) = (self.metrics.as_ref()?, self.state.as_ref()?);
        st.visible().into_iter().rev().find_map(|w| {
            let r = self.pane_rect(m, w);
            let band = Bounds::new(r.outer.origin, size(r.outer.size.width, r.body.origin.y - r.outer.origin.y));
            band.contains(&pos).then(|| w.id.clone())
        })
    }
}

impl Render for TuiosApp {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        self.frames += 1;
        let m = self.ensure_metrics(window);
        let before = (self.grid.cols, self.grid.rows);
        let lay = self.compute_layout(window, &m);
        self.layout = lay;
        if let Some(session) = self.first_connect.take() {
            self.connect(session, window, cx);
        }
        if before != (self.grid.cols, self.grid.rows) && before.0 > 0 && self.link == Link::Attached {
            self.resized_at = Some(Instant::now());
            self.wake_grid_at(Instant::now() + BADGE, cx);
        }
        if self.link == Link::Attached {
            let (c, r) = (self.grid.cols, self.grid.rows);
            self.schedule_resize(c, r, cx);
        }
        let t = self.theme.clone();
        let v = self.views().clone();
        let (w, h) = (lay.width, lay.height);
        let abs = |x: f32, y: f32, w: f32, h: f32| StyleRefinement::default().absolute().left(px(x)).top(px(y)).w(px(w)).h(px(h));
        let side = match (lay.side, lay.overlay) {
            (_, true) => Some(abs(0., BAND_H, self.sidebar_w.min(w - 40.), h - BAND_H)),
            (_, false) if lay.side_w > 0. => Some(abs(0., BAND_H, lay.side_w, h - BAND_H)),
            _ => None,
        };
        let grid_x = if lay.overlay || lay.side_w == 0. { 0. } else { lay.side_w };
        let mut root = div()
            .id("root")
            .size_full()
            .relative()
            .bg(rgb(t.base))
            .text_color(rgb(t.text))
            .font_family(SharedString::from(self.cfg.ui_font.clone()))
            .track_focus(&self.focus)
            .on_key_down(cx.listener(Self::on_key_down))
            .on_key_up(cx.listener(Self::on_key_up))
            .child(v.band.clone().cached(abs(0., 0., w, BAND_H)))
            .child(v.grid.clone().cached(abs(grid_x, BAND_H, w - grid_x, h - BAND_H)));
        if let Some(style) = side {
            root = root.child(v.sidebar.clone().cached(style));
        }
        root = root.child(v.spin.clone());
        if self.palette.is_some() {
            root = root.child(v.palette.clone().cached(abs(0., 0., w, h)));
        }
        if matches!(self.drag, Some(Drag::Sidebar)) {
            // Keeps the drag going when the pointer leaves the edge.
            root = root
                .on_mouse_move(cx.listener(Self::on_mouse_move))
                .on_mouse_up(MouseButton::Left, cx.listener(Self::on_mouse_up))
                .cursor(CursorStyle::ResizeLeftRight);
        }
        root
    }
}

/// App shortcuts. Everything else goes to the focused pane.
fn shortcut(k: &Keystroke) -> Option<Act> {
    let m = &k.modifiers;
    let key = k.key.as_str();
    if m.control && m.shift && !m.alt {
        return match key {
            "c" => Some(Act::Copy),
            "v" => Some(Act::Paste),
            "d" => Some(Act::Tape("Split", &["vertical"])),
            "e" => Some(Act::Tape("Split", &["horizontal"])),
            "w" => Some(Act::Tape("CloseWindow", &[])),
            "z" => Some(Act::Tape("ToggleZoom", &[])),
            "b" => Some(Act::ToggleSidebar),
            "j" => Some(Act::NextNeedsYou),
            "t" | "enter" => Some(Act::Tape("NewWindow", &[])),
            "q" => Some(Act::Quit),
            "[" | "{" => Some(Act::PrevSession),
            "]" | "}" => Some(Act::NextSession),
            "tab" => Some(Act::Tape("PrevWindow", &[])),
            _ => None,
        };
    }
    if m.control && !m.shift && !m.alt {
        return match key {
            "=" | "+" => Some(Act::FontBigger),
            "-" => Some(Act::FontSmaller),
            "0" => Some(Act::FontReset),
            "tab" => Some(Act::Tape("NextWindow", &[])),
            _ => None,
        };
    }
    if m.alt && !m.control && !m.shift {
        if let Some(d) = key.chars().next().filter(|c| key.len() == 1 && ('1'..='9').contains(c)) {
            return Some(Act::Workspace(d as u32 - '0' as u32));
        }
        return match key {
            "left" => Some(Act::Tape("FocusDirection", &["left"])),
            "right" => Some(Act::Tape("FocusDirection", &["right"])),
            "up" => Some(Act::Tape("FocusDirection", &["up"])),
            "down" => Some(Act::Tape("FocusDirection", &["down"])),
            _ => None,
        };
    }
    None
}

fn mouse_mods(m: &Modifiers) -> u16 {
    use ghostty_vt::ffi::*;
    let mut v = 0u32;
    if m.shift {
        v |= GHOSTTY_MODS_SHIFT;
    }
    if m.control {
        v |= GHOSTTY_MODS_CTRL;
    }
    if m.alt {
        v |= GHOSTTY_MODS_ALT;
    }
    v as u16
}

impl EntityInputHandler for TuiosApp {
    fn text_for_range(&mut self, _: std::ops::Range<usize>, _: &mut Option<std::ops::Range<usize>>, _: &mut Window, _: &mut Context<Self>) -> Option<String> {
        None
    }

    fn selected_text_range(&mut self, _: bool, _: &mut Window, _: &mut Context<Self>) -> Option<UTF16Selection> {
        let n = self.marked.as_ref().map(|m| m.encode_utf16().count()).unwrap_or(0);
        Some(UTF16Selection { range: n..n, reversed: false })
    }

    fn marked_text_range(&self, _: &mut Window, _: &mut Context<Self>) -> Option<std::ops::Range<usize>> {
        self.marked.as_ref().map(|m| 0..m.encode_utf16().count())
    }

    fn unmark_text(&mut self, _: &mut Window, cx: &mut Context<Self>) {
        self.marked = None;
        self.refresh_grid(cx);
    }

    fn replace_text_in_range(&mut self, _: Option<std::ops::Range<usize>>, text: &str, _: &mut Window, cx: &mut Context<Self>) {
        self.marked = None;
        if text.is_empty() {
            self.refresh_grid(cx);
            return;
        }
        if self.palette_open() {
            if let Some(p) = self.palette.as_mut() {
                p.query.push_str(text);
                p.selected = 0;
            }
            self.refresh_palette(cx);
            return;
        }
        let Some(pty) = self.focused_pty() else { return };
        if let Some(p) = self.panes.get_mut(&pty) {
            p.snap_to_bottom();
            p.term.clear_selection();
        }
        self.input(&pty, text.as_bytes());
        self.last_input = Instant::now();
        self.refresh_pane(&pty, cx);
    }

    fn replace_and_mark_text_in_range(
        &mut self,
        _: Option<std::ops::Range<usize>>,
        new_text: &str,
        _: Option<std::ops::Range<usize>>,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.marked = (!new_text.is_empty()).then(|| new_text.to_string());
        self.refresh_grid(cx);
    }

    fn bounds_for_range(&mut self, _: std::ops::Range<usize>, _: Bounds<Pixels>, _: &mut Window, _: &mut Context<Self>) -> Option<Bounds<Pixels>> {
        self.cursor_bounds()
    }

    fn character_index_for_point(&mut self, _: Point<Pixels>, _: &mut Window, _: &mut Context<Self>) -> Option<usize> {
        None
    }
}

impl TuiosApp {
    /// The focused pane's cursor cell in window coordinates.
    fn cursor_bounds(&self) -> Option<Bounds<Pixels>> {
        let m = self.metrics.as_ref()?;
        let st = self.state.as_ref()?;
        let id = self.focused_id()?;
        let w = st.window(&id)?;
        let p = self.panes.get(&w.pty)?;
        let cur = p.term.screen().cursor;
        let o = self.pane_rect(m, w).content.origin;
        let (cw, ch) = (f32::from(m.cell_w), f32::from(m.cell_h));
        Some(Bounds::new(point(o.x + px(cur.x as f32 * cw), o.y + px(cur.y as f32 * ch)), size(px(cw), px(ch))))
    }

    /// Draws the text an input method is composing over the cursor.
    fn paint_preedit(&self, window: &mut Window, cx: &mut App) {
        let (Some(text), Some(b), Some(m)) = (self.marked.as_ref(), self.cursor_bounds(), self.metrics.as_ref()) else { return };
        let t = &self.theme;
        let run = TextRun { len: text.len(), font: m.fonts[0].clone(), color: rgb(t.text).into(), background_color: None, underline: None, strikethrough: None };
        let line = window.text_system().shape_line(text.clone().into(), m.font_size, &[run], None);
        let w = line.width.max(m.cell_w);
        let two = px(2. / m.scale);
        window.paint_quad(fill(Bounds::new(b.origin, size(w, m.cell_h)), rgb(t.selected)));
        window.paint_quad(fill(Bounds::new(point(b.origin.x, b.origin.y + m.cell_h - two), size(w, two)), rgb(t.accent)));
        let _ = line.paint(point(b.origin.x, b.origin.y + m.baseline - line.ascent), line.ascent + line.descent, TextAlign::Left, None, window, cx);
    }
}
