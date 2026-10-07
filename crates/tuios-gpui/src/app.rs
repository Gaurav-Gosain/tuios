//! The main window: the sidebar of every session and agent, the top bar, the
//! pane grid with its headers, and the command palette. The design is in
//! docs/DESIGN-RESEARCH.md.

mod chrome;

use crate::control;
use crate::fleet::{self, PaneInfo, Status};
use crate::keys;
use crate::painter::{CursorPaint, Metrics};
use crate::palette::{self, Act, Entry};
use crate::pane::{Pane, apply_theme};
use crate::stats::FrameStats;
use crate::theme::{self, Theme};
use ghostty_vt::{KeyAction, MouseAction, MouseGeometry, MouseTracking, Rgb};
use gpui::*;
use std::collections::{HashMap, HashSet};
use std::path::PathBuf;
use std::time::{Duration, Instant};
use tuios_proto::{Bridge, Command, Launch, Message, Sender, SessionSummary, State};

pub const SIDEBAR_W: f32 = 264.;
pub const TOPBAR_H: f32 = 38.;
/// Space at the left and right of the grid, and below it.
const PAD_X: f32 = 10.;
const PAD_B: f32 = 6.;
const PAD_T: f32 = 4.;
/// How long a pane that starts to need you pulses.
const FLASH: Duration = Duration::from_millis(900);
/// How long the grid size shows after a resize.
const OVERLAY: Duration = Duration::from_millis(750);

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
    /// Show frame timings over the grid.
    pub show_fps: bool,
    /// A control socket for tests (see control.rs).
    pub control: Option<PathBuf>,
}

#[derive(Clone, Copy, Debug, Default, PartialEq)]
struct Grid {
    /// The top left of cell (0, 0). One cell row above it holds the headers
    /// of the panes at the top.
    origin: Point<Pixels>,
    size: Size<Pixels>,
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
}

/// What a control command turns into.
enum Plan {
    Events(Vec<PlatformInput>),
    /// Keystrokes, dispatched the way the platform does, text input included.
    Keys(Vec<Keystroke>),
    Reply(String),
}

struct PaletteUi {
    query: String,
    selected: usize,
    entries: Vec<Entry>,
}

/// A pane to show once its session is attached.
#[derive(Clone, Debug)]
struct Jump {
    session: String,
    window: String,
    workspace: u32,
}

pub struct TuiosApp {
    cfg: Config,
    focus: FocusHandle,
    bridge: Option<Bridge>,
    sender: Option<Sender>,
    generation: u64,
    state: Option<State>,
    panes: HashMap<String, Pane>,
    metrics: Option<Metrics>,
    epoch: u64,
    theme: Theme,
    grid: Grid,
    sent_size: (u16, u16),
    resize_task: Option<Task<()>>,
    sidebar: bool,
    palette: Option<PaletteUi>,
    sessions: Vec<SessionSummary>,
    /// Panes of every session, from `tuios list-agents`.
    fleet: Vec<PaneInfo>,
    /// Windows whose finished turn nobody has looked at.
    unread: HashSet<String>,
    connected: bool,
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
    /// Smooth scrolling and other animations ask for frames while running.
    animating: bool,
    /// Blink phase of the focused pane's cursor, when its program asked for
    /// a blinking cursor. Typing restarts the phase.
    blink_on: bool,
    last_input: Instant,
    /// Text an input method is composing, drawn at the cursor until committed.
    marked: Option<String>,
    /// Themes tuios knows, for the palette, and the one last picked there.
    theme_names: Vec<String>,
    theme_wanted: Option<String>,
    /// When each window started to need you, for the one pulse it gets.
    need_since: HashMap<String, Instant>,
    /// The grid size shows until then.
    overlay_until: Option<Instant>,
    /// The step the working glyphs are at.
    spin: u32,
    /// Frames drawn (root renders), for the `stats` control command.
    frames: u64,
}

impl TuiosApp {
    pub fn new(cfg: Config, window: &mut Window, cx: &mut Context<Self>) -> Self {
        let focus = cx.focus_handle();
        window.focus(&focus, cx);
        let theme = Theme::fallback();
        let mut cfg = cfg;
        let installed = window.text_system().all_font_names();
        let mut wanted: Vec<&str> = vec![cfg.font_family.as_str()];
        wanted.extend(crate::config::TERMINAL_FONTS);
        cfg.font_family = crate::config::pick_font(&wanted, &installed, true);
        let mut wanted: Vec<&str> = vec![cfg.ui_font.as_str()];
        wanted.extend(crate::config::UI_FONTS);
        cfg.ui_font = crate::config::pick_font(&wanted, &installed, false);
        let mut this = TuiosApp {
            cfg,
            focus,
            bridge: None,
            sender: None,
            generation: 0,
            state: None,
            panes: HashMap::new(),
            metrics: None,
            epoch: 1,
            theme,
            grid: Grid::default(),
            sent_size: (0, 0),
            resize_task: None,
            sidebar: true,
            palette: None,
            sessions: Vec::new(),
            fleet: Vec::new(),
            unread: HashSet::new(),
            connected: false,
            status: "Starting".into(),
            drag: None,
            focus_wanted: None,
            jump: None,
            asked_first_window: false,
            asked_tiling: false,
            stats: FrameStats::default(),
            last_title: String::new(),
            animating: false,
            blink_on: true,
            last_input: Instant::now(),
            marked: None,
            theme_names: Vec::new(),
            theme_wanted: None,
            need_since: HashMap::new(),
            overlay_until: None,
            spin: 0,
            frames: 0,
        };
        this.connect(this.cfg.session.clone(), window, cx);
        this.poll_fleet(cx);
        this.blink(cx);
        this.spinner(cx);
        if let Some(path) = this.cfg.control.clone() {
            this.serve_control(path, window, cx);
        }
        this
    }

    fn blink(&mut self, cx: &mut Context<Self>) {
        cx.spawn(async move |this, cx| {
            loop {
                cx.background_executor().timer(Duration::from_millis(530)).await;
                let alive = this.update(cx, |this, cx| {
                    let blinking = this
                        .focused_pty()
                        .and_then(|p| this.panes.get(&p))
                        .is_some_and(|p| p.term.screen().cursor.blinking && p.term.screen().cursor.visible);
                    let next = if this.last_input.elapsed() < Duration::from_millis(600) { true } else { !this.blink_on };
                    if blinking && next != this.blink_on {
                        this.blink_on = next;
                        cx.notify();
                    } else if !blinking && !this.blink_on {
                        this.blink_on = true;
                        cx.notify();
                    }
                });
                if alive.is_err() {
                    break;
                }
            }
        })
        .detach();
    }

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
        let (x, y, _, _) = w.content();
        let (cw, ch) = (f32::from(m.cell_w), f32::from(m.cell_h));
        Ok(point(
            self.grid.origin.x + px((x as f32 + col + 0.5) * cw),
            self.grid.origin.y + px((y as f32 + row + 0.5) * ch),
        ))
    }

    fn control_plan(&mut self, line: &str, _window: &mut Window, _cx: &mut Context<Self>) -> Result<Plan, String> {
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
        let mut out = format!(
            "{{\"cols\":{},\"rows\":{},\"cell\":[{cw},{ch}],\"origin\":[{},{}],\"connected\":{},\"palette\":{},",
            self.grid.cols,
            self.grid.rows,
            f32::from(self.grid.origin.x),
            f32::from(self.grid.origin.y),
            self.connected,
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

    // ---- connection -------------------------------------------------------

    fn ensure_metrics(&mut self, window: &mut Window) -> Metrics {
        if self.metrics.is_none() {
            self.metrics = Some(Metrics::new(&self.cfg.font_family, self.cfg.font_size, self.cfg.line_height, self.cfg.ligatures, window, self.epoch));
        }
        self.metrics.clone().expect("metrics")
    }

    fn connect(&mut self, session: Option<String>, window: &mut Window, cx: &mut Context<Self>) {
        self.generation += 1;
        let generation = self.generation;
        self.bridge = None;
        self.sender = None;
        self.state = None;
        self.panes.clear();
        self.asked_first_window = false;
        self.asked_tiling = false;
        self.connected = false;
        self.sent_size = (0, 0);
        let m = self.ensure_metrics(window);
        let (cols, rows) = if self.grid.cols > 0 { (self.grid.cols, self.grid.rows) } else { (120, 40) };
        let launch = Launch {
            tuios: self.cfg.tuios.clone(),
            session: session.clone(),
            cols,
            rows,
            cell_width: f32::from(m.cell_w).round() as u32,
            cell_height: f32::from(m.cell_h).round() as u32,
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
                self.status = format!("Cannot start tuios: {e}").into();
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
        cx.notify();
    }

    // ---- the fleet ---------------------------------------------------------

    /// Reads the daemon's sessions and every pane in them, every 1.5 s.
    fn poll_fleet(&mut self, cx: &mut Context<Self>) {
        let tuios = self.cfg.tuios.clone();
        let env = self.cfg.env.clone();
        cx.spawn(async move |this, cx| {
            loop {
                let (t, e) = (tuios.clone(), env.clone());
                let read = cx
                    .background_executor()
                    .spawn(async move {
                        let sessions = tuios_proto::list_sessions(&t, &e).ok();
                        let agents = fleet::fetch(&t, &e);
                        (sessions, agents)
                    })
                    .await;
                let alive = this
                    .update(cx, |this, cx| {
                        let (sessions, agents) = read;
                        let mut changed = false;
                        if let Some(list) = sessions {
                            if this.sessions != list {
                                this.sessions = list;
                                changed = true;
                            }
                        }
                        if let Some((panes, unread)) = agents {
                            if this.fleet != panes || this.unread != unread {
                                this.fleet = panes;
                                this.unread = unread;
                                changed = true;
                            }
                        }
                        // Ages tick, so the sidebar redraws at least this often.
                        if changed || this.fleet.iter().any(|p| p.status.is_agent()) {
                            cx.notify();
                        }
                    })
                    .is_ok();
                if !alive {
                    break;
                }
                cx.background_executor().timer(Duration::from_millis(1500)).await;
            }
        })
        .detach();
    }

    fn current_session(&self) -> String {
        self.state.as_ref().map(|s| s.session.clone()).unwrap_or_default()
    }

    /// Panes of the attached session, live from the bridge.
    fn attached_panes(&self) -> Vec<PaneInfo> {
        let Some(st) = &self.state else { return Vec::new() };
        let focused = self.focused_id();
        fleet::from_state(st, focused.as_deref(), |id| self.unread.contains(id))
    }

    /// Every pane in every session: the attached one live, the rest polled.
    fn all_panes(&self) -> Vec<PaneInfo> {
        let current = self.current_session();
        let mut v = self.attached_panes();
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

    fn on_state(&mut self, st: State) {
        // Panes whose window is gone are dropped with it.
        self.panes.retain(|pty, _| st.windows.iter().any(|w| &w.pty == pty));
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
        // A pane that starts to need you gets one pulse.
        let was: HashSet<String> = self
            .state
            .as_ref()
            .map(|s| s.windows.iter().filter(|w| w.agent.as_deref() == Some("needs_input")).map(|w| w.id.clone()).collect())
            .unwrap_or_default();
        for w in &st.windows {
            if w.agent.as_deref() == Some("needs_input") && !was.contains(&w.id) && self.state.is_some() {
                self.need_since.insert(w.id.clone(), Instant::now());
                self.animating = true;
            }
        }
        self.need_since.retain(|_, t| t.elapsed() < FLASH);
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
        self.state = Some(st);
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
        for msg in batch {
            if debug {
                match &msg {
                    Message::Event(e) => eprintln!("[gpui] event {} {:?}", e.kind, e.state.as_ref().map(|s| (s.workspace, s.tiling, s.windows.iter().map(|w| (w.x, w.y, w.w, w.h, w.border)).collect::<Vec<_>>()))),
                    Message::Snapshot { pty, cols, rows, bytes } => eprintln!("[gpui] snapshot {pty} {cols}x{rows} {} bytes", bytes.len()),
                    Message::Output { pty, bytes } => eprintln!("[gpui] output {pty} {} bytes", bytes.len()),
                    Message::Resized { pty, cols, rows } => eprintln!("[gpui] resized {pty} {cols}x{rows}"),
                    Message::Closed(w) => eprintln!("[gpui] closed: {w}"),
                }
            }
            match msg {
                Message::Event(ev) => match ev.kind.as_str() {
                    "attached" => {
                        self.connected = true;
                        self.status = format!("Attached to {}", ev.message.unwrap_or_default()).into();
                    }
                    "state" => {
                        if let Some(st) = ev.state {
                            self.on_state(st);
                        }
                    }
                    "theme" => {
                        if let Some(t) = ev.theme {
                            self.set_theme(Theme::from_export(&t), t.names);
                        }
                    }
                    _ => {}
                },
                Message::Snapshot { pty, cols, rows, bytes } => {
                    let theme = self.theme.clone();
                    let pane = self.panes.entry(pty).or_insert_with(|| Pane::new(cols.max(1), rows.max(1), &theme));
                    pane.restore(cols, rows, &bytes, &theme, cell);
                }
                Message::Output { pty, bytes } => {
                    let (c, r) = self.layout_size(&pty);
                    let theme = self.theme.clone();
                    self.panes.entry(pty).or_insert_with(|| Pane::new(c, r, &theme)).write(&bytes);
                }
                Message::Resized { pty, cols, rows } => {
                    if let Some(p) = self.panes.get_mut(&pty) {
                        p.term.resize(cols, rows, cell.0, cell.1);
                    }
                }
                Message::Closed(why) => {
                    self.connected = false;
                    self.status = why.into();
                    self.sender = None;
                }
            }
        }
        cx.notify();
    }

    fn layout_size(&self, pty: &str) -> (u16, u16) {
        self.state
            .as_ref()
            .and_then(|s| s.windows.iter().find(|w| w.pty == pty))
            .map(|w| {
                let (_, _, c, r) = w.content();
                (c as u16, r as u16)
            })
            .unwrap_or((80, 24))
    }

    fn cell_px(&self) -> (u32, u32) {
        self.metrics.as_ref().map(|m| (f32::from(m.cell_w).round() as u32, f32::from(m.cell_h).round() as u32)).unwrap_or((9, 18))
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
            cx.notify();
        }
    }

    fn schedule_resize(&mut self, cols: u16, rows: u16, cx: &mut Context<Self>) {
        if (cols, rows) == self.sent_size || cols < 2 || rows < 2 {
            return;
        }
        let (cw, ch) = self.cell_px();
        self.resize_task = Some(cx.spawn(async move |this, cx| {
            cx.background_executor().timer(Duration::from_millis(40)).await;
            let _ = this.update(cx, |this, _| {
                this.sent_size = (cols, rows);
                this.send(Command::resize(cols, rows, cw, ch));
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
            Act::FontBigger => self.set_font_size(self.cfg.font_size + 1., cx),
            Act::FontSmaller => self.set_font_size(self.cfg.font_size - 1., cx),
            Act::FontReset => self.set_font_size(14., cx),
            Act::ToggleSidebar => self.sidebar = !self.sidebar,
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
        self.palette = Some(PaletteUi { query: String::new(), selected: 0, entries });
        cx.notify();
    }

    fn set_theme(&mut self, theme: Theme, names: Vec<String>) {
        if !names.is_empty() {
            self.theme_names = names;
        }
        if theme == self.theme {
            return;
        }
        self.theme = theme;
        let t = self.theme.clone();
        for p in self.panes.values_mut() {
            apply_theme(&mut p.term, &t);
        }
        // Row caches hold resolved colours; new metrics start them over.
        self.epoch += 1;
        self.metrics = None;
    }

    fn set_font_size(&mut self, size: f32, cx: &mut Context<Self>) {
        self.cfg.font_size = size.clamp(7., 40.);
        self.epoch += 1;
        self.metrics = None;
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
        if self.palette.is_none() && keys::is_text(k) && !ev.is_held {
            let report_all = self.focused_pty().and_then(|p| self.panes.get(&p)).is_some_and(|p| p.term.kitty_keyboard_flags() & 8 != 0);
            if !report_all {
                return;
            }
        }
        cx.stop_propagation();
        if self.palette.is_some() {
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
                let ch = self.metrics.as_ref().map(|m| f32::from(m.cell_h)).unwrap_or(18.);
                if let Some(p) = self.panes.get_mut(&pty) {
                    let page = p.term.rows().saturating_sub(2) as f32 * ch;
                    p.scroll_pending += if k.key == "pageup" { page } else { -page };
                    self.animating = true;
                    cx.notify();
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
            self.blink_on = true;
            cx.notify();
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
            "escape" => self.palette = None,
            "enter" => {
                let act = palette::filter(&p.entries, &p.query).get(p.selected).map(|e| e.act.clone());
                self.palette = None;
                if let Some(act) = act {
                    self.run(act, window, cx);
                }
            }
            "up" => p.selected = p.selected.saturating_sub(1),
            "down" => p.selected = (p.selected + 1).min(n.saturating_sub(1)),
            "backspace" => {
                p.query.pop();
                p.selected = 0;
            }
            _ => {
                if k.modifiers.control && k.key == "p" && k.modifiers.shift {
                    self.palette = None;
                } else if let Some(c) = k.key_char.as_ref().filter(|_| !k.modifiers.control && !k.modifiers.alt) {
                    p.query.push_str(c);
                    p.selected = 0;
                }
            }
        }
        cx.notify();
    }

    // ---- mouse -------------------------------------------------------------

    /// The pane under a window position, topmost first, and the cell in it.
    fn hit(&self, pos: Point<Pixels>) -> Option<(String, String, i32, i32)> {
        let m = self.metrics.as_ref()?;
        let st = self.state.as_ref()?;
        let (cw, ch) = (f32::from(m.cell_w), f32::from(m.cell_h));
        let gx = (f32::from(pos.x) - f32::from(self.grid.origin.x)) / cw;
        let gy = (f32::from(pos.y) - f32::from(self.grid.origin.y)) / ch;
        let mut vis = st.visible();
        vis.reverse();
        for w in vis {
            let (x, y, c, r) = w.content();
            if gx >= x as f32 && gx < (x + c) as f32 && gy >= y as f32 && gy < (y + r) as f32 {
                return Some((w.id.clone(), w.pty.clone(), (gx - x as f32) as i32, (gy - y as f32) as i32));
            }
        }
        None
    }

    /// Pixel position of `pos` inside the pane's content, and the pane's geometry.
    fn pane_local(&self, pty: &str, pos: Point<Pixels>) -> Option<(f32, f32, MouseGeometry)> {
        let m = self.metrics.as_ref()?;
        let st = self.state.as_ref()?;
        let w = st.windows.iter().find(|w| w.pty == pty)?;
        let (x, y, c, r) = w.content();
        let (cw, ch) = (f32::from(m.cell_w), f32::from(m.cell_h));
        let ox = f32::from(self.grid.origin.x) + x as f32 * cw;
        let oy = f32::from(self.grid.origin.y) + y as f32 * ch;
        let geo = MouseGeometry { width: (c as f32 * cw) as u32, height: (r as f32 * ch) as u32, cell_width: cw as u32, cell_height: ch as u32 };
        Some((f32::from(pos.x) - ox, f32::from(pos.y) - oy, geo))
    }

    fn cell_in(&self, pty: &str, pos: Point<Pixels>) -> Option<(u16, u16)> {
        let (lx, ly, g) = self.pane_local(pty, pos)?;
        let p = self.panes.get(pty)?;
        let x = (lx / g.cell_width.max(1) as f32).floor().clamp(0., p.term.cols().saturating_sub(1) as f32) as u16;
        let y = (ly / g.cell_height.max(1) as f32).floor().clamp(0., p.term.rows().saturating_sub(1) as f32) as u16;
        Some((x, y))
    }

    fn on_mouse_down(&mut self, ev: &MouseDownEvent, _window: &mut Window, cx: &mut Context<Self>) {
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
                self.drag = Some(Drag::Select { pty, anchor: cell });
            }
            3 => self.paste(cx, true),
            _ => {}
        }
        cx.notify();
    }

    fn on_mouse_move(&mut self, ev: &MouseMoveEvent, _window: &mut Window, cx: &mut Context<Self>) {
        match self.drag.clone() {
            Some(Drag::Select { pty, anchor }) => {
                if ev.pressed_button != Some(MouseButton::Left) {
                    return;
                }
                if let Some(cell) = self.cell_in(&pty, ev.position) {
                    if let Some(p) = self.panes.get_mut(&pty) {
                        if cell != anchor {
                            p.term.select(anchor, cell, ev.modifiers.alt);
                            cx.notify();
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
            None => {}
        }
    }

    fn on_scroll(&mut self, ev: &ScrollWheelEvent, _window: &mut Window, cx: &mut Context<Self>) {
        let Some((_, pty, _, _)) = self.hit(ev.position) else { return };
        let ch = self.metrics.as_ref().map(|m| f32::from(m.cell_h)).unwrap_or(18.);
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
        if lines {
            p.scroll_pending += dy;
            self.animating = true;
        } else {
            p.scroll_pixels(dy, ch);
        }
        cx.notify();
    }

    // ---- painting ----------------------------------------------------------

    /// Each visible pane with its content rectangle and its header, the cell
    /// row above the content. tuios leaves one cell row between stacked
    /// panes and the grid reserves one above the top panes, so every pane has
    /// a header row without the layout giving up a row of content.
    fn pane_rects(&self) -> Vec<(tuios_proto::Window, Bounds<Pixels>, Bounds<Pixels>)> {
        let (Some(m), Some(st)) = (self.metrics.as_ref(), self.state.as_ref()) else { return Vec::new() };
        let (cw, ch) = (f32::from(m.cell_w), f32::from(m.cell_h));
        let o = self.grid.origin;
        st.visible()
            .into_iter()
            .map(|w| {
                let (x, y, c, r) = w.content();
                let content = Bounds::new(point(o.x + px(x as f32 * cw), o.y + px(y as f32 * ch)), size(px(c as f32 * cw), px(r as f32 * ch)));
                let header = Bounds::new(point(content.origin.x, content.origin.y - px(ch)), size(content.size.width, px(ch)));
                (w.clone(), content, header)
            })
            .collect()
    }

    fn header_hit(&self, pos: Point<Pixels>) -> Option<String> {
        self.pane_rects().into_iter().rev().find(|(_, _, h)| h.contains(&pos)).map(|(w, _, _)| w.id)
    }

    /// Shapes `text` in the UI font, cut with an ellipsis to fit `max`.
    fn ui_line(&self, text: &str, size: f32, weight: FontWeight, color: impl Into<Hsla>, max: f32, window: &mut Window) -> Option<ShapedLine> {
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
        let chars: Vec<char> = text.chars().collect();
        let mut keep = ((chars.len() as f32) * max / f32::from(line.width)) as usize;
        while keep > 0 {
            let s: String = chars[..keep].iter().collect::<String>().trim_end().to_string() + "…";
            let l = shape(s, window);
            if f32::from(l.width) <= max {
                return Some(l);
            }
            keep -= 1.max(keep / 8);
        }
        None
    }

    /// The angle of the working glyph, which turns in twelve steps.
    fn spin_angle(&self) -> f32 {
        (self.spin % 12) as f32 * std::f32::consts::TAU / 12.
    }

    fn paint_glyph(&self, status: Status, center: Point<Pixels>, side: f32, window: &mut Window, cx: &mut App) {
        let t = &self.theme;
        let b = Bounds::new(point(center.x - px(side / 2.), center.y - px(side / 2.)), size(px(side), px(side)));
        let color = chrome::status_color(t, status);
        let unit = TransformationMatrix::unit();
        if status == Status::Working {
            let _ = window.paint_svg(b, "icons/state-ring.svg".into(), None, unit, Theme::alpha(color, 0x40).into(), cx);
            let s = window.scale_factor();
            let c = point(ScaledPixels(f32::from(center.x) * s), ScaledPixels(f32::from(center.y) * s));
            let m = unit.translate(c).rotate(radians(self.spin_angle())).translate(point(ScaledPixels(-c.x.0), ScaledPixels(-c.y.0)));
            let _ = window.paint_svg(b, "icons/state-working.svg".into(), None, m, rgb(color).into(), cx);
        } else {
            let _ = window.paint_svg(b, chrome::status_icon(status).into(), None, unit, rgb(color).into(), cx);
        }
    }

    /// A pane's header: state glyph, name, folder and branch, and at the
    /// right either "Needs you" or how long the agent has been at it.
    fn paint_header(&self, info: &PaneInfo, rect: Bounds<Pixels>, focused: bool, window: &mut Window, cx: &mut App) {
        let t = self.theme.clone();
        let h = f32::from(rect.size.height);
        let x0 = f32::from(rect.origin.x);
        let mid = f32::from(rect.origin.y) + h / 2.;
        let right = x0 + f32::from(rect.size.width);
        self.paint_glyph(info.status, point(px(x0 + 7.), px(mid)), 13., window, cx);
        let size = 12.;
        let baseline_y = px(mid - (size * 1.25) / 2.);
        let lh = px(size * 1.25);
        // The right side first, so the name knows how much room it has.
        let mut limit = right - 4.;
        if matches!(info.status, Status::NeedsYou | Status::Errored) {
            let word = info.status.word();
            if let Some(l) = self.ui_line(word, 11., FontWeight::SEMIBOLD, rgb(chrome::status_color(&t, info.status)), 200., window) {
                let w = f32::from(l.width) + 14.;
                let ph = (h - 4.).min(17.);
                let pill = Bounds::new(point(px(right - w - 2.), px(mid - ph / 2.)), size_px(w, ph));
                window.paint_quad(fill(pill, Theme::alpha(chrome::status_color(&t, info.status), 0x26)).corner_radii(px(ph / 2.)));
                let _ = l.paint(point(px(right - w + 5.), px(mid - 11. * 1.25 / 2.)), px(11. * 1.25), TextAlign::Left, None, window, cx);
                limit = right - w - 10.;
            }
        } else if let Some(l) = self.ui_line(&fleet::age(info.since_ms, fleet::now_ms()), 11., FontWeight::MEDIUM, rgb(t.text3), 60., window) {
            if info.status.is_agent() {
                let w = f32::from(l.width);
                let _ = l.paint(point(px(right - w - 4.), px(mid - 11. * 1.25 / 2.)), px(11. * 1.25), TextAlign::Left, None, window, cx);
                limit = right - w - 14.;
            }
        }
        let name_x = x0 + 18.;
        let (name_color, weight) = if focused { (t.text, FontWeight::MEDIUM) } else { (t.text2, FontWeight::NORMAL) };
        let Some(name) = self.ui_line(&info.name, size, weight, rgb(name_color), limit - name_x, window) else { return };
        let nw = f32::from(name.width);
        let _ = name.paint(point(px(name_x), baseline_y), lh, TextAlign::Left, None, window, cx);
        let detail_x = name_x + nw + 10.;
        if let Some(d) = self.ui_line(&info.detail, size, FontWeight::NORMAL, rgb(t.text3), limit - detail_x, window) {
            let _ = d.paint(point(px(detail_x), baseline_y), lh, TextAlign::Left, None, window, cx);
        }
    }

    fn paint_grid(&mut self, bounds: Bounds<Pixels>, window: &mut Window, cx: &mut Context<Self>) {
        let started = Instant::now();
        let m = self.ensure_metrics(window);
        let (cw, ch) = (f32::from(m.cell_w), f32::from(m.cell_h));
        let width = f32::from(bounds.size.width);
        let cols = ((width - 2. * PAD_X) / cw).floor().max(1.) as u16;
        // One row above the grid holds the headers of the top panes.
        let rows = ((f32::from(bounds.size.height) - PAD_B - PAD_T) / ch - 1.).floor().max(1.) as u16;
        let side = ((width - cols as f32 * cw) / 2.).floor();
        let origin = point(bounds.origin.x + px(side), bounds.origin.y + px(PAD_T + ch));
        if (self.grid.cols, self.grid.rows) != (cols, rows) && self.grid.cols > 0 && self.connected {
            self.overlay_until = Some(Instant::now() + OVERLAY);
        }
        self.grid = Grid { origin, size: bounds.size, cols, rows };
        if self.connected {
            self.schedule_resize(cols, rows, cx);
        }

        let theme = self.theme.clone();
        window.paint_quad(fill(bounds, theme::hsla(Rgb::from_u32(theme.bg))));
        let Some(st) = self.state.clone() else {
            self.paint_center_note(bounds, &self.status.to_string(), window, cx);
            return;
        };
        let focused = self.focused_id();
        let infos: HashMap<String, PaneInfo> = self.attached_panes().into_iter().map(|p| (p.window.clone(), p)).collect();
        let rects = self.pane_rects();
        let multi = rects.len() > 1;
        let mut more_frames = false;

        for (w, rect, header) in &rects {
            let is_focused = focused.as_deref() == Some(w.id.as_str());
            if let Some(info) = infos.get(&w.id) {
                self.paint_header(info, *header, is_focused || !multi, window, cx);
            }
            let Some(pane) = self.panes.get_mut(&w.pty) else { continue };
            // Animated scrolling: move a share of what is left each frame.
            if pane.scroll_pending != 0. {
                let step = if pane.scroll_pending.abs() < 1. { pane.scroll_pending } else { pane.scroll_pending * 0.28 };
                pane.scroll_pending -= step;
                pane.scroll_pixels(step, ch);
                if pane.scroll_pending.abs() < 0.5 {
                    pane.scroll_pending = 0.;
                }
                more_frames |= pane.scroll_pending != 0.;
            }
            let y_off = pane.scroll_px;
            let screen_bg = pane.term.snapshot().bg;
            {
                let Pane { term, painter, .. } = pane;
                let s = term.snapshot();
                painter.prepare(s, &m, &theme, window);
            }
            if y_off > 0. {
                let above = pane.term.row_above().cloned();
                pane.painter.prepare_above(above.as_ref(), screen_bg, &m, &theme, window);
            }
            let Pane { term, painter, .. } = pane;
            let screen = term.screen();
            window.with_content_mask(Some(ContentMask { bounds: *rect }), |window| {
                painter.paint(
                    screen,
                    rect.origin,
                    &m,
                    CursorPaint { visible: y_off == 0. && term.at_bottom() && (self.blink_on || !is_focused), focused: is_focused, color: Rgb::from_u32(theme.cursor) },
                    y_off,
                    window,
                    cx,
                );
            });
            if multi && !is_focused {
                // Panes without focus sit back a little, as in Ghostty.
                window.paint_quad(fill(*rect, Theme::alpha(theme.bg, if theme.light { 0x30 } else { 0x40 })));
            }
            if !term.at_bottom() || y_off > 0. {
                paint_scrollbar(term, *rect, &theme, window);
            }
        }

        // Hairlines in the gaps tuios leaves between panes.
        let line = rgb(theme.hairline);
        for (w, rect, header) in &rects {
            let (x, y, _, _) = w.content();
            let left = f32::from(rect.origin.x);
            let top = f32::from(header.origin.y);
            let bottom = f32::from(rect.origin.y + rect.size.height);
            if x > 0 {
                let gx = (left - cw / 2.).round();
                window.paint_quad(fill(Bounds::new(point(px(gx), px(top)), size(px(1.), px(bottom - top))), line));
            }
            if y > 0 {
                let x0 = if x > 0 { left - cw / 2. } else { left - side + 1. };
                let x1 = f32::from(rect.origin.x + rect.size.width) + cw / 2.;
                window.paint_quad(fill(Bounds::new(point(px(x0.round()), px(top.round())), size(px((x1 - x0).round()), px(1.))), line));
            }
        }

        // The one coloured frame: around a pane that needs you.
        for (w, rect, header) in &rects {
            let Some(info) = infos.get(&w.id) else { continue };
            if info.status != Status::NeedsYou {
                continue;
            }
            let c = theme.needs_input;
            let ring = Bounds::new(
                point(rect.origin.x - px(4.), header.origin.y),
                size(rect.size.width + px(8.), rect.size.height + header.size.height + px(2.)),
            );
            if let Some(t0) = self.need_since.get(&w.id) {
                let p = t0.elapsed().as_secs_f32() / FLASH.as_secs_f32();
                if p < 1. {
                    let a = ((1. - p) * 0.5 * 255.) as u8;
                    let grow = px(3. * p);
                    window.paint_quad(quad(ring.dilate(grow), px(8.), transparent_black(), px(2.), Theme::alpha(c, a), BorderStyle::Solid));
                    more_frames = true;
                }
            }
            window.paint_quad(quad(ring, px(6.), transparent_black(), px(1.5), rgb(c), BorderStyle::Solid));
        }

        if let Some(until) = self.overlay_until {
            let now = Instant::now();
            if now < until {
                let left = (until - now).as_secs_f32();
                let a = (left / 0.25).min(1.);
                self.paint_size_badge(bounds, a, window, cx);
                more_frames = true;
            } else {
                self.overlay_until = None;
            }
        }
        if self.cfg.show_fps {
            if let Some((p50, p95)) = self.stats.paint_percentiles() {
                let s = format!("paint p50 {p50:.2} ms  p95 {p95:.2} ms");
                if let Some(l) = self.ui_line(&s, 11., FontWeight::MEDIUM, rgb(theme.text3), 400., window) {
                    let x = bounds.origin.x + bounds.size.width - l.width - px(12.);
                    let _ = l.paint(point(x, bounds.origin.y + bounds.size.height - px(18.)), px(14.), TextAlign::Left, None, window, cx);
                }
            }
        }

        let title = rects
            .iter()
            .find(|(w, _, _)| focused.as_deref() == Some(w.id.as_str()))
            .and_then(|(w, _, _)| infos.get(&w.id))
            .map(|i| format!("{} - {} - tuios", i.name, st.session))
            .unwrap_or_else(|| "tuios".into());
        if title != self.last_title {
            window.set_window_title(&title);
            self.last_title = title;
        }

        self.paint_preedit(window, cx);
        window.handle_input(&self.focus, ElementInputHandler::new(bounds, cx.entity()), cx);

        if more_frames || self.animating {
            self.animating = more_frames;
            window.request_animation_frame();
        }
        self.stats.record_paint(started.elapsed());
    }

    /// "120 × 40" in the middle of the grid while the window resizes.
    fn paint_size_badge(&self, bounds: Bounds<Pixels>, alpha: f32, window: &mut Window, cx: &mut App) {
        let t = &self.theme;
        let text = format!("{} × {}", self.grid.cols, self.grid.rows);
        let a = (alpha * 255.) as u8;
        let Some(l) = self.ui_line(&text, 13., FontWeight::MEDIUM, Theme::alpha(t.text, a), 300., window) else { return };
        let (w, h) = (f32::from(l.width) + 28., 32.);
        let c = bounds.center();
        let b = Bounds::new(point(c.x - px(w / 2.), c.y - px(h / 2.)), size_px(w, h));
        window.paint_quad(quad(b, px(8.), Theme::alpha(t.raised, a), px(1.), Theme::alpha(t.hairline, a), BorderStyle::Solid));
        let _ = l.paint(point(b.origin.x + px(14.), c.y - px(13. * 1.25 / 2.)), px(13. * 1.25), TextAlign::Left, None, window, cx);
    }

    /// A line of text in the middle of the grid, before anything is attached.
    fn paint_center_note(&self, bounds: Bounds<Pixels>, text: &str, window: &mut Window, cx: &mut App) {
        let t = &self.theme;
        if let Some(l) = self.ui_line(text, 13., FontWeight::NORMAL, rgb(t.text3), f32::from(bounds.size.width) - 40., window) {
            let c = bounds.center();
            let _ = l.paint(point(c.x - l.width / 2., c.y - px(10.)), px(16.), TextAlign::Left, None, window, cx);
        }
    }

    /// Turns the working glyphs in twelve steps a second and a bit, while any
    /// agent works. A timer, not an animation frame loop, so a busy fleet does
    /// not repaint at the display's rate.
    fn spinner(&mut self, cx: &mut Context<Self>) {
        cx.spawn(async move |this, cx| {
            loop {
                cx.background_executor().timer(Duration::from_millis(100)).await;
                let alive = this.update(cx, |this, cx| {
                    if this.all_panes().iter().any(|p| p.status == Status::Working) {
                        this.spin = this.spin.wrapping_add(1);
                        cx.notify();
                    }
                });
                if alive.is_err() {
                    break;
                }
            }
        })
        .detach();
    }
}

impl Render for TuiosApp {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        self.frames += 1;
        let t = self.theme.clone();
        let entity = cx.entity();
        let grid = canvas(
            |_, _, _| {},
            move |bounds, _, window, cx| {
                entity.update(cx, |this, cx| this.paint_grid(bounds, window, cx));
            },
        )
        .size_full();
        let sidebar = self.sidebar.then(|| self.render_sidebar(cx).into_any_element());
        let topbar = self.render_topbar(cx);
        let palette = self.render_palette(window, cx);
        div()
            .id("root")
            .size_full()
            .flex()
            .flex_row()
            .relative()
            .bg(rgb(t.bg))
            .text_color(rgb(t.text))
            .font_family(SharedString::from(self.cfg.ui_font.clone()))
            .track_focus(&self.focus)
            .on_key_down(cx.listener(Self::on_key_down))
            .on_key_up(cx.listener(Self::on_key_up))
            .children(sidebar)
            .child(
                div()
                    .flex()
                    .flex_col()
                    .flex_1()
                    .h_full()
                    .min_w_0()
                    .child(topbar)
                    .child(
                        div()
                            .id("grid")
                            .flex_1()
                            .min_h_0()
                            .relative()
                            .overflow_hidden()
                            .cursor(CursorStyle::IBeam)
                            .on_mouse_down(MouseButton::Left, cx.listener(Self::on_mouse_down))
                            .on_mouse_down(MouseButton::Middle, cx.listener(Self::on_mouse_down))
                            .on_mouse_down(MouseButton::Right, cx.listener(Self::on_mouse_down))
                            .on_mouse_move(cx.listener(Self::on_mouse_move))
                            .on_mouse_up(MouseButton::Left, cx.listener(Self::on_mouse_up))
                            .on_mouse_up(MouseButton::Middle, cx.listener(Self::on_mouse_up))
                            .on_mouse_up(MouseButton::Right, cx.listener(Self::on_mouse_up))
                            .on_scroll_wheel(cx.listener(Self::on_scroll))
                            .child(grid),
                    ),
            )
            .children(palette)
    }
}

fn size_px(w: f32, h: f32) -> Size<Pixels> {
    size(px(w), px(h))
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

fn paint_scrollbar(term: &ghostty_vt::Terminal, rect: Bounds<Pixels>, t: &Theme, window: &mut Window) {
    let (total, offset, len) = term.scrollbar();
    if total <= len || total == 0 {
        return;
    }
    let h = f32::from(rect.size.height);
    let thumb = (len as f32 / total as f32 * h).max(24.);
    let top = offset as f32 / (total - len) as f32 * (h - thumb);
    let x = rect.origin.x + rect.size.width - px(6.);
    window.paint_quad(
        fill(Bounds::new(point(x, rect.origin.y + px(top)), size(px(4.), px(thumb))), Theme::alpha(t.text3, 0x90)).corner_radii(px(2.)),
    );
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
        cx.notify();
    }

    fn replace_text_in_range(&mut self, _: Option<std::ops::Range<usize>>, text: &str, _: &mut Window, cx: &mut Context<Self>) {
        self.marked = None;
        if text.is_empty() {
            cx.notify();
            return;
        }
        if let Some(p) = self.palette.as_mut() {
            p.query.push_str(text);
            p.selected = 0;
            cx.notify();
            return;
        }
        let Some(pty) = self.focused_pty() else { return };
        if let Some(p) = self.panes.get_mut(&pty) {
            p.snap_to_bottom();
            p.term.clear_selection();
        }
        self.input(&pty, text.as_bytes());
        self.last_input = Instant::now();
        self.blink_on = true;
        cx.notify();
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
        cx.notify();
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
        let (x, y, _, _) = w.content();
        let (cw, ch) = (f32::from(m.cell_w), f32::from(m.cell_h));
        Some(Bounds::new(
            point(self.grid.origin.x + px((x as f32 + cur.x as f32) * cw), self.grid.origin.y + px((y as f32 + cur.y as f32) * ch)),
            size(px(cw), px(ch)),
        ))
    }

    /// Draws the text an input method is composing over the cursor.
    fn paint_preedit(&self, window: &mut Window, cx: &mut App) {
        let (Some(text), Some(b), Some(m)) = (self.marked.as_ref(), self.cursor_bounds(), self.metrics.as_ref()) else { return };
        let t = &self.theme;
        let run = TextRun { len: text.len(), font: m.fonts[0].clone(), color: rgb(t.text).into(), background_color: None, underline: None, strikethrough: None };
        let line = window.text_system().shape_line(text.clone().into(), m.font_size, &[run], None);
        let w = line.width.max(m.cell_w);
        window.paint_quad(fill(Bounds::new(b.origin, size(w, m.cell_h)), rgb(t.selected)));
        window.paint_quad(fill(Bounds::new(point(b.origin.x, b.origin.y + m.cell_h - px(2.)), size(w, px(1.5))), rgb(t.accent)));
        let _ = line.paint(b.origin, m.cell_h, TextAlign::Left, None, window, cx);
    }
}
