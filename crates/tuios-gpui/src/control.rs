//! A control socket for driving the app in tests: synthetic keys, clicks,
//! drags and wheel events go through GPUI's own event dispatch, the same path
//! real input takes, and `dump` reports what every pane shows.
//!
//! Start the app with `--control PATH`, then send one command per line:
//!
//!   key ctrl-shift-p             a keystroke, in GPUI's notation
//!   type some text               printable text, one keystroke per char
//!   click X Y [left|middle|right] [COUNT]   window coordinates in pixels
//!   cellclick PANE COL ROW [COUNT]  a click on a cell of the PANE-th visible pane
//!   drag PANE C1 R1 C2 R2        a left drag between two cells of a pane
//!   wheel PANE PIXELS            a pixel scroll over a pane (positive goes back)
//!   lines PANE LINES             a line scroll over a pane
//!   dump                         JSON: grid, panes, screens, selection
//!
//! Each command is answered with one line: `ok`, `err ...` or the JSON.

use gpui::*;
use std::io::{BufRead, BufReader, Write};
use std::os::unix::net::UnixListener;
use std::path::PathBuf;

pub struct Request {
    pub line: String,
    pub reply: std::sync::mpsc::Sender<String>,
}

/// Listens on `path` and forwards each line to `tx`, blocking for the reply.
pub fn serve(path: PathBuf, tx: async_channel::Sender<Request>) {
    let _ = std::fs::remove_file(&path);
    let listener = match UnixListener::bind(&path) {
        Ok(l) => l,
        Err(e) => {
            eprintln!("control socket {}: {e}", path.display());
            return;
        }
    };
    std::thread::Builder::new()
        .name("control".into())
        .spawn(move || {
            for conn in listener.incoming().flatten() {
                let tx = tx.clone();
                std::thread::spawn(move || {
                    let mut w = match conn.try_clone() {
                        Ok(w) => w,
                        Err(_) => return,
                    };
                    for line in BufReader::new(conn).lines().map_while(Result::ok) {
                        let (rtx, rrx) = std::sync::mpsc::channel();
                        if tx.send_blocking(Request { line, reply: rtx }).is_err() {
                            return;
                        }
                        let answer = rrx.recv().unwrap_or_else(|_| "err app closed".into());
                        if writeln!(w, "{answer}").is_err() {
                            return;
                        }
                    }
                });
            }
        })
        .expect("control thread");
}

pub fn parse_button(s: Option<&str>) -> MouseButton {
    match s {
        Some("right") => MouseButton::Right,
        Some("middle") => MouseButton::Middle,
        _ => MouseButton::Left,
    }
}

pub fn down(pos: Point<Pixels>, button: MouseButton, count: usize) -> PlatformInput {
    PlatformInput::MouseDown(MouseDownEvent { button, position: pos, modifiers: Modifiers::default(), click_count: count, first_mouse: false })
}

pub fn up(pos: Point<Pixels>, button: MouseButton, count: usize) -> PlatformInput {
    PlatformInput::MouseUp(MouseUpEvent { button, position: pos, modifiers: Modifiers::default(), click_count: count })
}

pub fn moved(pos: Point<Pixels>, pressed: Option<MouseButton>) -> PlatformInput {
    PlatformInput::MouseMove(MouseMoveEvent { position: pos, pressed_button: pressed, modifiers: Modifiers::default() })
}

pub fn wheel(pos: Point<Pixels>, delta: ScrollDelta) -> PlatformInput {
    PlatformInput::ScrollWheel(ScrollWheelEvent { position: pos, delta, modifiers: Modifiers::default(), touch_phase: TouchPhase::Moved })
}

pub fn json_str(s: &str) -> String {
    let mut o = String::with_capacity(s.len() + 2);
    o.push('"');
    for c in s.chars() {
        match c {
            '"' => o.push_str("\\\""),
            '\\' => o.push_str("\\\\"),
            '\n' => o.push_str("\\n"),
            c if (c as u32) < 0x20 => o.push_str(&format!("\\u{:04x}", c as u32)),
            c => o.push(c),
        }
    }
    o.push('"');
    o
}
