//! The main window: sidebar, workspace strip, the pane grid and the palette.

use crate::control;
use crate::keys;
use crate::painter::{CursorPaint, Metrics};
use crate::palette::{self, Act, Entry};
use crate::pane::{Pane, apply_theme};
use crate::stats::FrameStats;
use crate::theme::{self, Theme};
use ghostty_vt::{KeyAction, MouseAction, MouseGeometry, MouseTracking, Rgb};
use gpui::prelude::FluentBuilder;
use gpui::*;
use std::collections::HashMap;
use std::path::PathBuf;
use std::time::{Duration, Instant};
use tuios_proto::{Bridge, Command, Launch, Message, Sender, SessionSummary, State};

const SIDEBAR_W: f32 = 240.;
const STRIP_H: f32 = 32.;
const STATUS_H: f32 = 24.;
const GRID_PAD: f32 = 6.;

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
    /// Show frame timings in the status bar.
    pub show_fps: bool,
    /// A control socket for tests (see control.rs).
    pub control: Option<PathBuf>,
}

#[derive(Clone, Copy, Debug, Default, PartialEq)]
struct Grid {
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
    connected: bool,
    status: SharedString,
    drag: Option<Drag>,
    /// The focused window as this client last asked for it, ahead of the
    /// state that confirms it.
    focus_wanted: Option<(String, Instant)>,
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
            connected: false,
            status: "Starting".into(),
            drag: None,
            focus_wanted: None,
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
        };
        this.connect(this.cfg.session.clone(), window, cx);
        this.poll_sessions(cx);
        this.blink(cx);
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
            _ => return Err(format!("unknown command {cmd}")),
        })
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

    fn poll_sessions(&mut self, cx: &mut Context<Self>) {
        let tuios = self.cfg.tuios.clone();
        let env = self.cfg.env.clone();
        cx.spawn(async move |this, cx| {
            loop {
                let (t, e) = (tuios.clone(), env.clone());
                let list = cx.background_executor().spawn(async move { tuios_proto::list_sessions(&t, &e) }).await;
                if let Ok(list) = list {
                    let alive = this
                        .update(cx, |this, cx| {
                            if this.sessions != list {
                                this.sessions = list;
                                cx.notify();
                            }
                        })
                        .is_ok();
                    if !alive {
                        break;
                    }
                }
                cx.background_executor().timer(Duration::from_secs(2)).await;
            }
        })
        .detach();
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
        self.state = Some(st);
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
            Act::NewSession => {
                let n = (1..).map(|i| format!("gui-{i}")).find(|n| !self.sessions.iter().any(|s| &s.name == n)).unwrap_or_default();
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

    fn open_palette(&mut self, cx: &mut Context<Self>) {
        let names: Vec<String> = self.sessions.iter().map(|s| s.name.clone()).collect();
        let current = self.state.as_ref().map(|s| s.session.clone()).unwrap_or_default();
        let mut entries = palette::entries(&names, &current);
        for t in &self.theme_names {
            if *t != self.theme.name {
                entries.push(Entry { title: format!("Theme: {t}"), hint: "", act: Act::Theme(t.clone()) });
            }
        }
        self.palette = Some(PaletteUi { query: String::new(), selected: 0, entries });
        cx.notify();
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
        let Some((id, pty, _, _)) = self.hit(ev.position) else { return };
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

    fn paint_grid(&mut self, bounds: Bounds<Pixels>, window: &mut Window, cx: &mut Context<Self>) {
        let started = Instant::now();
        let m = self.ensure_metrics(window);
        let (cw, ch) = (f32::from(m.cell_w), f32::from(m.cell_h));
        let cols = ((f32::from(bounds.size.width) - 2. * GRID_PAD) / cw).floor().max(1.) as u16;
        let rows = ((f32::from(bounds.size.height) - 2. * GRID_PAD) / ch).floor().max(1.) as u16;
        let origin = point(bounds.origin.x + px(GRID_PAD), bounds.origin.y + px(GRID_PAD));
        self.grid = Grid { origin, size: bounds.size, cols, rows };
        if self.connected {
            self.schedule_resize(cols, rows, cx);
        }

        let theme = self.theme.clone();
        window.paint_quad(fill(bounds, theme::hsla(Rgb::from_u32(theme.bg))));
        let Some(st) = self.state.clone() else { return };
        let focused = self.focused_id();
        let vis = st.visible();
        let multi = vis.len() > 1;
        let mut more_frames = false;

        for w in &vis {
            let (x, y, c, r) = w.content();
            let rect = Bounds::new(
                point(origin.x + px(x as f32 * cw), origin.y + px(y as f32 * ch)),
                size(px(c as f32 * cw), px(r as f32 * ch)),
            );
            let is_focused = focused.as_deref() == Some(w.id.as_str());
            let Some(pane) = self.panes.get_mut(&w.pty) else {
                window.paint_quad(fill(rect, theme::hsla(Rgb::from_u32(theme.bg))));
                continue;
            };
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
            let screen_bg = {
                let s = pane.term.snapshot();
                s.bg
            };
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
            window.with_content_mask(Some(ContentMask { bounds: rect }), |window| {
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
                // Panes without focus sit slightly back.
                window.paint_quad(fill(rect, Theme::alpha(theme.bg, 0x38)));
            }
            if !term.at_bottom() || y_off > 0. {
                paint_scrollbar(term, rect, &theme, window);
            }
            if let Some(state) = w.agent_state() {
                paint_agent_tag(state, rect, &theme, &self.cfg.ui_font, window, cx);
            }
        }

        // Pane separators and the focus ring, in the gaps the layout leaves.
        if multi {
            for w in &vis {
                let (x, y, c, r) = w.content();
                let rect = Bounds::new(
                    point(origin.x + px(x as f32 * cw), origin.y + px(y as f32 * ch)),
                    size(px(c as f32 * cw), px(r as f32 * ch)),
                );
                let is_focused = focused.as_deref() == Some(w.id.as_str());
                // tuios's own frame colours: the focused pane in the terminal-mode
                // border colour, the others in the unfocused one, kept quiet.
                let color = if is_focused { Theme::alpha(theme.border_focused, 0xff) } else { Theme::alpha(theme.border_unfocused, 0x70) };
                window.paint_quad(outline(rect.dilate(px(1.)), color, BorderStyle::Solid));
            }
        }

        let title = vis
            .iter()
            .find(|w| focused.as_deref() == Some(w.id.as_str()))
            .map(|w| format!("{} - tuios", w.label()))
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

    // ---- chrome ------------------------------------------------------------
    //
    // Sizes follow what well-made GPUI apps settled on (docs/RESEARCH.md):
    // 32 px strip, 24 px status bar, 28 px rows with 4 px corners, 13 px text,
    // 12 px secondary text, two weights. Colours come from tuios's rail
    // palette (GroundUI) for the frame and its dialog palette (UI) for the
    // command palette, so a theme reads as the same app in both clients.

    fn render_sidebar(&mut self, cx: &mut Context<Self>) -> impl IntoElement + use<> {
        let t = self.theme.clone();
        let st = self.state.clone().unwrap_or_default();
        let current = st.session.clone();
        let focused = self.focused_id();
        let section = |label: &str| {
            div()
                .px(px(14.))
                .pt(px(16.))
                .pb(px(6.))
                .text_size(px(11.))
                .font_weight(FontWeight::MEDIUM)
                .text_color(rgb(t.rail_mute))
                .child(SharedString::from(label.to_uppercase()))
        };
        let mut col = div()
            .id("sidebar")
            .flex()
            .flex_col()
            .w(px(SIDEBAR_W))
            .h_full()
            .flex_none()
            .bg(rgb(t.rail))
            .border_r_1()
            .border_color(rgb(t.rail_rule))
            .overflow_y_scroll()
            .child(
                div()
                    .flex()
                    .items_center()
                    .gap(px(8.))
                    .px(px(14.))
                    .h(px(STRIP_H))
                    .flex_none()
                    .border_b_1()
                    .border_color(rgb(t.rail_rule))
                    .child(div().size(px(7.)).rounded_full().bg(rgb(if self.connected { t.done } else { t.errored })))
                    .child(div().text_size(px(13.)).font_weight(FontWeight::MEDIUM).text_color(rgb(t.rail_fg)).child("tuios"))
                    .child(div().flex_1())
                    .child(div().text_size(px(11.)).text_color(rgb(t.rail_mute)).child(SharedString::from(if t.name.is_empty() { "default".to_string() } else { t.name.clone() }))),
            );

        col = col.child(section("Sessions"));
        let mut names: Vec<SessionSummary> = self.sessions.clone();
        if !current.is_empty() && !names.iter().any(|s| s.name == current) {
            names.insert(0, SessionSummary { name: current.clone(), window_count: 0, attached: true, current_workspace: 0, dir: String::new() });
        }
        for s in names {
            let active = s.name == current;
            let name = s.name.clone();
            col = col.child(
                row_item(&t, active)
                    .id(SharedString::from(format!("session-{}", s.name)))
                    .on_click(cx.listener(move |this, _, window, cx| {
                        if this.state.as_ref().map(|s| s.session.as_str()) != Some(name.as_str()) {
                            this.connect(Some(name.clone()), window, cx);
                        }
                    }))
                    .child(div().w(px(14.)).text_color(rgb(if active { t.accent_bright } else { t.rail_mute })).child("\u{f120}"))
                    .child(div().flex_1().truncate().child(SharedString::from(s.name.clone())))
                    .child(div().text_size(px(11.)).text_color(rgb(t.rail_mute)).child(SharedString::from(format!("{}", s.window_count)))),
            );
        }
        col = col.child(
            row_item(&t, false)
                .id("new-session")
                .text_color(rgb(t.rail_mute))
                .on_click(cx.listener(|this, _, window, cx| this.run(Act::NewSession, window, cx)))
                .child(div().w(px(14.)).child("\u{f067}"))
                .child("New session"),
        );

        col = col.child(section("Panes"));
        for w in st.windows.iter().filter(|w| w.workspace == st.workspace) {
            let id = w.id.clone();
            let active = focused.as_deref() == Some(w.id.as_str());
            let agent = w.agent_state().map(|s| s.to_string());
            col = col.child(
                row_item(&t, active)
                    .id(SharedString::from(format!("pane-{}", w.id)))
                    .on_click(cx.listener(move |this, _, _, cx| this.focus_window(&id, cx)))
                    .child(agent_dot(&t, agent.as_deref(), &w.id))
                    .child(div().flex_1().truncate().child(SharedString::from(w.label().to_string())))
                    .when_some(agent, |el, a| el.child(agent_badge(&t, &a))),
            );
        }
        let others: Vec<_> = st.windows.iter().filter(|w| w.workspace != st.workspace).collect();
        if !others.is_empty() {
            col = col.child(section("Other workspaces"));
            for w in others {
                let id = w.id.clone();
                let ws = w.workspace;
                let agent = w.agent_state().map(|s| s.to_string());
                col = col.child(
                    row_item(&t, false)
                        .id(SharedString::from(format!("other-{}", w.id)))
                        .on_click(cx.listener(move |this, _, _, cx| {
                            this.send(Command::workspace(ws));
                            this.focus_window(&id, cx);
                        }))
                        .child(div().w(px(14.)).text_size(px(11.)).text_color(rgb(t.rail_mute)).child(SharedString::from(ws.to_string())))
                        .child(div().flex_1().truncate().child(SharedString::from(w.label().to_string())))
                        .when_some(agent, |el, a| el.child(agent_dot(&t, Some(&a), &w.id))),
                );
            }
        }
        col
    }

    fn render_strip(&mut self, cx: &mut Context<Self>) -> impl IntoElement + use<> {
        let t = self.theme.clone();
        let st = self.state.clone().unwrap_or_default();
        let mut tabs = div().flex().items_center().gap(px(2.)).px(px(6.)).h_full().flex_none();
        let mut shown: Vec<u32> = st.occupied.clone();
        if st.workspace > 0 && !shown.contains(&st.workspace) {
            shown.push(st.workspace);
        }
        shown.sort();
        for ws in shown {
            let active = ws == st.workspace;
            let busy = st
                .windows
                .iter()
                .filter(|w| w.workspace == ws)
                .filter_map(|w| w.agent_state())
                .max_by_key(|s| match *s {
                    "needs_input" => 3,
                    "errored" => 2,
                    "working" => 1,
                    _ => 0,
                })
                .map(|s| t.agent_color(s));
            let name = st.workspace_name(ws).map(|s| s.to_string());
            tabs = tabs.child(
                div()
                    .id(SharedString::from(format!("ws-{ws}")))
                    .relative()
                    .flex()
                    .items_center()
                    .gap(px(6.))
                    .h(px(24.))
                    .px(px(10.))
                    .rounded(px(6.))
                    .text_size(px(13.))
                    .cursor_pointer()
                    .text_color(rgb(if active { t.rail_fg } else { t.rail_mute }))
                    .when(active, |el| el.bg(rgb(t.rail_row)).font_weight(FontWeight::MEDIUM))
                    .hover(|s| s.bg(rgb(t.rail_hover)).text_color(rgb(t.rail_fg)))
                    .on_click(cx.listener(move |this, _, _, cx| {
                        this.send(Command::workspace(ws));
                        cx.notify();
                    }))
                    .child(div().text_color(rgb(if active { t.accent_bright } else { t.rail_mute })).child(SharedString::from(ws.to_string())))
                    .when_some(name, |el, n| el.child(SharedString::from(n)))
                    .when_some(busy, |el, c| el.child(div().size(px(6.)).rounded_full().bg(rgb(c)))),
            );
        }
        let button = |id: &'static str, icon: &'static str, label: &'static str| {
            div()
                .id(id)
                .h(px(24.))
                .px(px(8.))
                .flex()
                .items_center()
                .gap(px(6.))
                .rounded(px(6.))
                .text_size(px(12.))
                .text_color(rgb(t.rail_dim))
                .cursor_pointer()
                .hover(|s| s.bg(rgb(t.rail_hover)).text_color(rgb(t.rail_fg)))
                .child(div().text_size(px(13.)).child(icon))
                .child(label)
        };
        div()
            .flex()
            .items_center()
            .justify_between()
            .h(px(STRIP_H))
            .flex_none()
            .border_b_1()
            .border_color(rgb(t.rail_rule))
            .bg(rgb(t.rail))
            .child(tabs)
            .child(
                div()
                    .flex()
                    .items_center()
                    .gap(px(2.))
                    .px(px(6.))
                    .child(button("split-r", "\u{eb56}", "Split").on_click(cx.listener(|this, _, w, cx| this.run(Act::Tape("Split", &["vertical"]), w, cx))))
                    .child(button("split-d", "\u{eb57}", "Stack").on_click(cx.listener(|this, _, w, cx| this.run(Act::Tape("Split", &["horizontal"]), w, cx))))
                    .child(
                        button("palette", "\u{f002}", "Commands")
                            .child(div().text_size(px(11.)).text_color(rgb(t.rail_mute)).child("ctrl+shift+p"))
                            .on_click(cx.listener(|this, _, _, cx| this.open_palette(cx))),
                    ),
            )
    }

    fn render_status(&mut self) -> impl IntoElement + use<> {
        let t = self.theme.clone();
        let st = self.state.clone().unwrap_or_default();
        let focused = self.focused_id().and_then(|id| st.window(&id).cloned());
        let sep = || div().w(px(1.)).h(px(12.)).bg(rgb(t.rail_rule));
        let mut left = div().flex().items_center().gap(px(8.)).min_w_0();
        match &focused {
            Some(w) => {
                left = left
                    .child(div().text_color(rgb(t.rail_dim)).child(SharedString::from(st.session.clone())))
                    .child(sep())
                    .child(SharedString::from(format!("workspace {}", st.workspace)))
                    .child(sep())
                    .child(div().truncate().child(SharedString::from(w.label().to_string())));
                if let Some(a) = w.agent_state() {
                    left = left.child(sep()).child(div().text_color(rgb(t.agent_color(a))).child(SharedString::from(a.replace('_', " "))));
                }
            }
            None => left = left.child(SharedString::from(self.status.to_string())),
        }
        let mut right = format!("{} x {}", self.grid.cols, self.grid.rows);
        if self.cfg.show_fps {
            if let Some((p50, p95)) = self.stats.paint_percentiles() {
                right = format!("paint p50 {:.2} ms  p95 {:.2} ms   {right}", p50, p95);
            }
        }
        div()
            .flex()
            .items_center()
            .justify_between()
            .h(px(STATUS_H))
            .flex_none()
            .px(px(10.))
            .text_size(px(12.))
            .text_color(rgb(t.rail_mute))
            .bg(rgb(t.rail))
            .border_t_1()
            .border_color(rgb(t.rail_rule))
            .child(left)
            .child(SharedString::from(right))
    }

    fn render_palette(&mut self, window: &mut Window, cx: &mut Context<Self>) -> Option<AnyElement> {
        let p = self.palette.as_ref()?;
        let t = self.theme.clone();
        let list = palette::filter(&p.entries, &p.query);
        let selected = p.selected.min(list.len().saturating_sub(1));
        let start = selected.saturating_sub(11);
        let mut items = div().flex().flex_col().p(px(6.));
        for (i, e) in list.iter().enumerate().skip(start).take(12) {
            let act = e.act.clone();
            let is_sel = i == selected;
            let swatch = match &e.act {
                Act::Theme(_) => true,
                _ => false,
            };
            items = items.child(
                div()
                    .id(("pal", i))
                    .relative()
                    .flex()
                    .justify_between()
                    .items_center()
                    .px(px(10.))
                    .h(px(30.))
                    .rounded(px(6.))
                    .text_size(px(13.))
                    .cursor_pointer()
                    .text_color(rgb(if is_sel { t.dlg_fg } else { t.dlg_dim }))
                    .when(is_sel, |el| el.bg(rgb(t.dlg_row)))
                    .hover(|s| s.bg(rgb(t.dlg_row)).text_color(rgb(t.dlg_fg)))
                    .on_click(cx.listener(move |this, _, window, cx| {
                        this.palette = None;
                        this.run(act.clone(), window, cx);
                    }))
                    .when(is_sel, |el| el.child(div().absolute().left_0().top(px(7.)).w(px(2.)).h(px(16.)).rounded(px(1.)).bg(rgb(t.accent))))
                    .child(
                        div()
                            .flex()
                            .items_center()
                            .gap(px(8.))
                            .when(swatch, |el| el.child(div().text_color(rgb(t.accent_bright)).child("\u{f53f}")))
                            .child(SharedString::from(e.title.clone())),
                    )
                    .child(div().text_size(px(11.)).text_color(rgb(t.dlg_mute)).child(e.hint)),
            );
        }
        if list.is_empty() {
            items = items.child(div().px(px(12.)).py(px(8.)).text_size(px(13.)).text_color(rgb(t.dlg_mute)).child("No command matches."));
        }
        let query = if p.query.is_empty() { SharedString::from("Run a command, switch a session or pick a theme") } else { SharedString::from(p.query.clone()) };
        let shadow = |y: f32, blur: f32, a: f32| BoxShadow {
            color: hsla(0., 0., 0., a),
            offset: point(px(0.), px(y)),
            blur_radius: px(blur),
            spread_radius: px(0.),
            inset: false,
        };
        let panel = div()
            .w(px(600.))
            .bg(rgb(t.dlg_surface))
            .border_1()
            .border_color(rgb(t.dlg_edge))
            .rounded(px(12.))
            .shadow(vec![shadow(2., 3., 0.12), shadow(3., 6., 0.10), shadow(6., 12., 0.08), shadow(16., 32., 0.18)])
            .overflow_hidden()
            .child(
                div()
                    .flex()
                    .items_center()
                    .gap(px(10.))
                    .px(px(16.))
                    .h(px(48.))
                    .border_b_1()
                    .border_color(rgb(t.dlg_edge))
                    .text_size(px(14.))
                    .child(div().text_color(rgb(t.dlg_mute)).child("\u{f002}"))
                    .child(div().text_color(rgb(if p.query.is_empty() { t.dlg_mute } else { t.dlg_fg })).child(query))
                    .child(div().w(px(1.5)).h(px(18.)).bg(rgb(t.accent)).with_animation(
                        "caret",
                        Animation::new(Duration::from_millis(1060)).repeat(),
                        |el, d| el.opacity(if d < 0.5 { 1. } else { 0. }),
                    )),
            )
            .child(items)
            .child(
                div()
                    .flex()
                    .justify_between()
                    .px(px(16.))
                    .h(px(28.))
                    .items_center()
                    .border_t_1()
                    .border_color(rgb(t.dlg_edge))
                    .text_size(px(11.))
                    .text_color(rgb(t.dlg_mute))
                    .child(SharedString::from(format!("{} of {}", list.len(), p.entries.len())))
                    .child("enter run   esc close"),
            );
        let top = (f32::from(window.viewport_size().height) / 10.).max(48.);
        Some(
            div()
                .id("palette-scrim")
                .absolute()
                .inset_0()
                .flex()
                .justify_center()
                .bg(Theme::alpha(0x000000, if t.light { 0x0d } else { 0x33 }))
                .on_click(cx.listener(|this, _, _, cx| {
                    this.palette = None;
                    cx.notify();
                }))
                .child(
                    div().pt(px(top)).child(panel).with_animation(
                        "palette-in",
                        Animation::new(Duration::from_millis(150)).with_easing(ease_out_quint()),
                        |el, d| el.opacity(d).mt(px(-8. * (1. - d))),
                    ),
                )
                .into_any_element(),
        )
    }
}

impl Render for TuiosApp {
    fn render(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let t = self.theme.clone();
        let entity = cx.entity();
        let grid = canvas(
            |_, _, _| {},
            move |bounds, _, window, cx| {
                entity.update(cx, |this, cx| this.paint_grid(bounds, window, cx));
            },
        )
        .size_full();
        let sidebar = self.sidebar.then(|| self.render_sidebar(cx));
        let strip = self.render_strip(cx);
        let status = self.render_status();
        let palette = self.render_palette(_window, cx);
        div()
            .id("root")
            .size_full()
            .flex()
            .flex_row()
            .relative()
            .bg(rgb(t.bg))
            .text_color(rgb(t.rail_fg))
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
                    .child(strip)
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
                    )
                    .child(status),
            )
            .children(palette)
    }
}

fn row_item(t: &Theme, active: bool) -> Div {
    div()
        .relative()
        .flex()
        .items_center()
        .gap(px(8.))
        .mx(px(6.))
        .px(px(8.))
        .h(px(28.))
        .flex_none()
        .rounded(px(4.))
        .text_size(px(13.))
        .cursor_pointer()
        .text_color(rgb(if active { t.rail_fg } else { t.rail_dim }))
        .when(active, |el| {
            el.bg(rgb(t.rail_row))
                .font_weight(FontWeight::MEDIUM)
                .child(div().absolute().left_0().top(px(6.)).w(px(2.)).h(px(16.)).rounded(px(1.)).bg(rgb(t.accent)))
        })
        .hover(|s| s.bg(rgb(t.rail_hover)).text_color(rgb(t.rail_fg)))
}

/// An agent's state as a dot; a working agent's dot breathes.
fn agent_dot(t: &Theme, state: Option<&str>, id: &str) -> AnyElement {
    let color = state.map(|a| t.agent_color(a)).unwrap_or(t.rail_rule);
    let dot = div().size(px(8.)).rounded_full().flex_none().bg(rgb(color));
    if state == Some("working") {
        dot.with_animation(
            SharedString::from(format!("breathe-{id}")),
            Animation::new(Duration::from_millis(1600)).repeat().with_easing(pulsating_between(0.35, 1.)),
            |el, d| el.opacity(d),
        )
        .into_any_element()
    } else {
        dot.into_any_element()
    }
}

fn agent_badge(t: &Theme, state: &str) -> Div {
    let c = t.agent_color(state);
    div()
        .text_size(px(11.))
        .px(px(6.))
        .h(px(18.))
        .flex()
        .items_center()
        .rounded(px(4.))
        .bg(Theme::alpha(c, 0x24))
        .text_color(rgb(c))
        .child(SharedString::from(state.replace('_', " ")))
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
            "q" => Some(Act::Quit),
            "enter" => Some(Act::Tape("NewWindow", &[])),
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
        fill(Bounds::new(point(x, rect.origin.y + px(top)), size(px(4.), px(thumb))), Theme::alpha(t.rail_mute, 0xb0)).corner_radii(px(2.)),
    );
}

fn paint_agent_tag(state: &str, rect: Bounds<Pixels>, t: &Theme, font: &str, window: &mut Window, cx: &mut App) {
    let label: SharedString = state.replace('_', " ").into();
    let color = t.agent_color(state);
    let run = TextRun {
        len: label.len(),
        font: gpui::font(SharedString::from(font.to_string())),
        color: rgb(color).into(),
        background_color: None,
        underline: None,
        strikethrough: None,
    };
    let fs = px(11.);
    let line = window.text_system().shape_line(label, fs, &[run], None);
    let w = line.width + px(16.);
    let h = px(18.);
    let origin = point(rect.origin.x + rect.size.width - w - px(10.), rect.origin.y + px(8.));
    window.paint_quad(fill(Bounds::new(origin, size(w, h)), Theme::alpha(t.rail_row, 0xe8)).corner_radii(px(9.)));
    window.paint_quad(fill(Bounds::new(point(origin.x + px(6.), origin.y + px(7.)), size(px(4.), px(4.))), rgb(color)).corner_radii(px(2.)));
    let _ = line.paint(point(origin.x + px(12.), origin.y + px(2.)), h - px(4.), TextAlign::Left, None, window, cx);
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
        let run = TextRun { len: text.len(), font: m.fonts[0].clone(), color: rgb(t.rail_fg).into(), background_color: None, underline: None, strikethrough: None };
        let line = window.text_system().shape_line(text.clone().into(), m.font_size, &[run], None);
        let w = line.width.max(m.cell_w);
        window.paint_quad(fill(Bounds::new(b.origin, size(w, m.cell_h)), rgb(t.rail_row)));
        window.paint_quad(fill(Bounds::new(point(b.origin.x, b.origin.y + m.cell_h - px(2.)), size(w, px(1.5))), rgb(t.accent)));
        let _ = line.paint(b.origin, m.cell_h, TextAlign::Left, None, window, cx);
    }
}
