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

const SIDEBAR_W: f32 = 232.;
const STRIP_H: f32 = 34.;
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
    pub dark: bool,
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
}

impl TuiosApp {
    pub fn new(cfg: Config, window: &mut Window, cx: &mut Context<Self>) -> Self {
        let focus = cx.focus_handle();
        window.focus(&focus, cx);
        let theme = if cfg.dark { theme::NIGHT } else { theme::DAY };
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
        };
        this.connect(this.cfg.session.clone(), window, cx);
        this.poll_sessions(cx);
        if let Some(path) = this.cfg.control.clone() {
            this.serve_control(path, window, cx);
        }
        this
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
                Plan::Events(vec![control::key(Keystroke::parse(k).map_err(|e| e.to_string())?)])
            }
            "type" => {
                let text = line.get(5..).unwrap_or("");
                Plan::Events(
                    text.chars()
                        .map(|c| {
                            let shift = c.is_uppercase();
                            let key = if c == ' ' { "space".to_string() } else { c.to_lowercase().to_string() };
                            control::key(Keystroke {
                                modifiers: Modifiers { shift, ..Default::default() },
                                key,
                                key_char: Some(c.to_string()),
                            })
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
            let (text, sel, scroll, bottom, size) = match self.panes.get_mut(&w.pty) {
                Some(p) => {
                    let sel = p.term.selection_text().unwrap_or_default();
                    let bottom = p.term.at_bottom();
                    let size = (p.term.cols(), p.term.rows());
                    (p.term.snapshot().plain_text(), sel, p.scroll_px, bottom, size)
                }
                None => (String::new(), String::new(), 0., true, (0, 0)),
            };
            out.push_str(&format!(
                "{{\"id\":{},\"title\":{},\"cells\":[{x},{y},{c},{r}],\"term\":[{},{}],\"agent\":{},\"scroll_px\":{scroll},\"at_bottom\":{bottom},\"selection\":{},\"text\":{}}}",
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
            self.metrics = Some(Metrics::new(&self.cfg.font_family, self.cfg.font_size, self.cfg.line_height, window, self.epoch));
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
            Act::ToggleTheme => {
                self.theme = if self.theme.name == theme::NIGHT.name { theme::DAY } else { theme::NIGHT };
                let t = self.theme.clone();
                for p in self.panes.values_mut() {
                    apply_theme(&mut p.term, &t);
                }
                self.epoch += 1;
                self.metrics = None;
            }
            Act::Quit => cx.quit(),
        }
        cx.notify();
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
        cx.stop_propagation();
        let k = &ev.keystroke;
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
        self.palette = Some(PaletteUi { query: String::new(), selected: 0, entries: palette::entries(&names, &current) });
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
                    CursorPaint { visible: y_off == 0. && term.at_bottom(), focused: is_focused, color: Rgb::from_u32(theme.cursor) },
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
                paint_agent_tag(state, rect, &theme, window, cx);
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
                let color = if is_focused { Theme::alpha(theme.accent, 0xd0) } else { Theme::alpha(theme.border, 0xff) };
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

        if more_frames || self.animating {
            self.animating = more_frames;
            window.request_animation_frame();
        }
        self.stats.record_paint(started.elapsed());
    }

    // ---- chrome ------------------------------------------------------------

    fn render_sidebar(&mut self, cx: &mut Context<Self>) -> impl IntoElement + use<> {
        let t = self.theme.clone();
        let st = self.state.clone().unwrap_or_default();
        let current = st.session.clone();
        let focused = self.focused_id();
        let section = |label: &str| {
            div().px_3().pt_4().pb_1().text_xs().text_color(rgb(t.muted)).child(SharedString::from(label.to_uppercase()))
        };
        let mut col = div()
            .id("sidebar")
            .flex()
            .flex_col()
            .w(px(SIDEBAR_W))
            .h_full()
            .flex_none()
            .bg(rgb(t.sidebar))
            .border_r_1()
            .border_color(rgb(t.border))
            .overflow_y_scroll()
            .child(
                div()
                    .flex()
                    .items_center()
                    .gap_2()
                    .px_3()
                    .h(px(STRIP_H))
                    .child(div().size(px(8.)).rounded_full().bg(rgb(if self.connected { t.done } else { t.errored })))
                    .child(div().text_sm().font_weight(FontWeight::SEMIBOLD).text_color(rgb(t.text)).child("tuios")),
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
                    .child(div().flex_1().truncate().child(SharedString::from(s.name.clone())))
                    .child(div().text_xs().text_color(rgb(t.muted)).child(SharedString::from(format!("{}", s.window_count)))),
            );
        }
        col = col.child(
            row_item(&t, false)
                .id("new-session")
                .text_color(rgb(t.muted))
                .on_click(cx.listener(|this, _, window, cx| this.run(Act::NewSession, window, cx)))
                .child("+ New session"),
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
                    .child(
                        div()
                            .size(px(7.))
                            .rounded_full()
                            .flex_none()
                            .bg(rgb(agent.as_deref().map(|a| t.agent_color(a)).unwrap_or(t.border))),
                    )
                    .child(div().flex_1().truncate().child(SharedString::from(w.label().to_string())))
                    .when_some(agent, |el, a| {
                        el.child(
                            div()
                                .text_xs()
                                .px_1p5()
                                .rounded_sm()
                                .bg(Theme::alpha(t.agent_color(&a), 0x26))
                                .text_color(rgb(t.agent_color(&a)))
                                .child(SharedString::from(a.replace('_', " "))),
                        )
                    }),
            );
        }
        let others: Vec<_> = st.windows.iter().filter(|w| w.workspace != st.workspace).collect();
        if !others.is_empty() {
            col = col.child(section("Elsewhere"));
            for w in others {
                let id = w.id.clone();
                let ws = w.workspace;
                let agent = w.agent_state().map(|s| s.to_string());
                col = col.child(
                    row_item(&t, false)
                        .id(SharedString::from(format!("other-{}", w.id)))
                        .text_color(rgb(t.muted))
                        .on_click(cx.listener(move |this, _, _, cx| {
                            this.send(Command::workspace(ws));
                            this.focus_window(&id, cx);
                        }))
                        .child(div().text_xs().w(px(14.)).child(SharedString::from(ws.to_string())))
                        .child(div().flex_1().truncate().child(SharedString::from(w.label().to_string())))
                        .when_some(agent, |el, a| el.child(div().size(px(7.)).rounded_full().bg(rgb(t.agent_color(&a))))),
                );
            }
        }
        col
    }

    fn render_strip(&mut self, cx: &mut Context<Self>) -> impl IntoElement + use<> {
        let t = self.theme.clone();
        let st = self.state.clone().unwrap_or_default();
        let mut tabs = div().flex().items_center().gap_1().px_2().h(px(STRIP_H)).flex_none();
        let mut shown: Vec<u32> = st.occupied.clone();
        if st.workspace > 0 && !shown.contains(&st.workspace) {
            shown.push(st.workspace);
        }
        shown.sort();
        for ws in shown {
            let active = ws == st.workspace;
            let label = match st.workspace_name(ws) {
                Some(n) => format!("{ws}  {n}"),
                None => format!("{ws}"),
            };
            let busy = st.windows.iter().filter(|w| w.workspace == ws).filter_map(|w| w.agent_state()).next().map(|s| t.agent_color(s));
            tabs = tabs.child(
                div()
                    .id(SharedString::from(format!("ws-{ws}")))
                    .flex()
                    .items_center()
                    .gap_1p5()
                    .h(px(24.))
                    .px_2p5()
                    .rounded_md()
                    .text_sm()
                    .cursor_pointer()
                    .text_color(rgb(if active { t.text } else { t.muted }))
                    .when(active, |el| el.bg(rgb(t.surface)))
                    .hover(|s| s.bg(rgb(t.surface)))
                    .on_click(cx.listener(move |this, _, _, cx| {
                        this.send(Command::workspace(ws));
                        cx.notify();
                    }))
                    .child(SharedString::from(label))
                    .when_some(busy, |el, c| el.child(div().size(px(6.)).rounded_full().bg(rgb(c)))),
            );
        }
        let button = |id: &'static str, label: &'static str| {
            div()
                .id(id)
                .h(px(24.))
                .px_2()
                .flex()
                .items_center()
                .rounded_md()
                .text_sm()
                .text_color(rgb(t.muted))
                .cursor_pointer()
                .hover(|s| s.bg(rgb(t.surface)).text_color(rgb(t.text)))
                .child(label)
        };
        div()
            .flex()
            .items_center()
            .justify_between()
            .h(px(STRIP_H))
            .flex_none()
            .border_b_1()
            .border_color(rgb(t.border))
            .bg(rgb(t.sidebar))
            .child(tabs)
            .child(
                div()
                    .flex()
                    .gap_1()
                    .px_2()
                    .child(button("split-r", "Split right").on_click(cx.listener(|this, _, w, cx| this.run(Act::Tape("Split", &["vertical"]), w, cx))))
                    .child(button("split-d", "Split down").on_click(cx.listener(|this, _, w, cx| this.run(Act::Tape("Split", &["horizontal"]), w, cx))))
                    .child(button("palette", "Commands").on_click(cx.listener(|this, _, _, cx| this.open_palette(cx)))),
            )
    }

    fn render_status(&mut self) -> impl IntoElement + use<> {
        let t = self.theme.clone();
        let st = self.state.clone().unwrap_or_default();
        let focused = self.focused_id().and_then(|id| st.window(&id).cloned());
        let left = match &focused {
            Some(w) => format!("{}   workspace {}   {}", st.session, st.workspace, w.label()),
            None => format!("{}", self.status),
        };
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
            .px_3()
            .text_xs()
            .text_color(rgb(t.muted))
            .bg(rgb(t.sidebar))
            .border_t_1()
            .border_color(rgb(t.border))
            .child(div().truncate().child(SharedString::from(left)))
            .child(SharedString::from(right))
    }

    fn render_palette(&mut self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let p = self.palette.as_ref()?;
        let t = self.theme.clone();
        let list = palette::filter(&p.entries, &p.query);
        let selected = p.selected.min(list.len().saturating_sub(1));
        let start = selected.saturating_sub(9);
        let mut items = div().flex().flex_col().py_1();
        for (i, e) in list.iter().enumerate().skip(start).take(10) {
            let act = e.act.clone();
            items = items.child(
                div()
                    .id(("pal", i))
                    .flex()
                    .justify_between()
                    .items_center()
                    .mx_1()
                    .px_3()
                    .h(px(30.))
                    .rounded_md()
                    .text_sm()
                    .cursor_pointer()
                    .text_color(rgb(t.text))
                    .when(i == selected, |el| el.bg(Theme::alpha(t.accent, 0x30)))
                    .hover(|s| s.bg(Theme::alpha(t.accent, 0x18)))
                    .on_click(cx.listener(move |this, _, window, cx| {
                        this.palette = None;
                        this.run(act.clone(), window, cx);
                    }))
                    .child(SharedString::from(e.title.clone()))
                    .child(div().text_xs().text_color(rgb(t.muted)).child(e.hint)),
            );
        }
        if list.is_empty() {
            items = items.child(div().px_4().py_2().text_sm().text_color(rgb(t.muted)).child("No command matches."));
        }
        let query = if p.query.is_empty() { SharedString::from("Type a command") } else { SharedString::from(p.query.clone()) };
        let panel = div()
            .w(px(520.))
            .bg(rgb(t.surface))
            .border_1()
            .border_color(rgb(t.border))
            .rounded_lg()
            .shadow_lg()
            .overflow_hidden()
            .child(
                div()
                    .flex()
                    .items_center()
                    .px_4()
                    .h(px(44.))
                    .border_b_1()
                    .border_color(rgb(t.border))
                    .text_color(rgb(if p.query.is_empty() { t.muted } else { t.text }))
                    .child(query)
                    .child(div().w(px(1.5)).h(px(18.)).ml_0p5().bg(rgb(t.accent))),
            )
            .child(items);
        Some(
            div()
                .id("palette-scrim")
                .absolute()
                .inset_0()
                .flex()
                .justify_center()
                .bg(Theme::alpha(0x000000, 0x40))
                .on_click(cx.listener(|this, _, _, cx| {
                    this.palette = None;
                    cx.notify();
                }))
                .child(
                    div().pt(px(72.)).child(panel).with_animation(
                        "palette-in",
                        Animation::new(Duration::from_millis(150)).with_easing(ease_out_quint()),
                        |el, t| el.opacity(t).mt(px(-10. * (1. - t))),
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
        let palette = self.render_palette(cx);
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
        .flex()
        .items_center()
        .gap_2()
        .mx_1p5()
        .px_2()
        .h(px(28.))
        .rounded_md()
        .text_sm()
        .cursor_pointer()
        .text_color(rgb(if active { t.text } else { t.muted }))
        .when(active, |el| el.bg(rgb(t.surface)))
        .hover(|s| s.bg(rgb(t.surface)).text_color(rgb(t.text)))
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
        fill(Bounds::new(point(x, rect.origin.y + px(top)), size(px(4.), px(thumb))), Theme::alpha(t.muted, 0xb0)).corner_radii(px(2.)),
    );
}

fn paint_agent_tag(state: &str, rect: Bounds<Pixels>, t: &Theme, window: &mut Window, cx: &mut App) {
    let label: SharedString = state.replace('_', " ").into();
    let color = t.agent_color(state);
    let run = TextRun {
        len: label.len(),
        font: gpui::font("Adwaita Sans"),
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
    window.paint_quad(fill(Bounds::new(origin, size(w, h)), Theme::alpha(t.surface, 0xe8)).corner_radii(px(9.)));
    window.paint_quad(fill(Bounds::new(point(origin.x + px(6.), origin.y + px(7.)), size(px(4.), px(4.))), rgb(color)).corner_radii(px(2.)));
    let _ = line.paint(point(origin.x + px(12.), origin.y + px(2.)), h - px(4.), TextAlign::Left, None, window, cx);
}
