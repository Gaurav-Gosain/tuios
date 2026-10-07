//! Client side of `tuios gui-bridge`.
//!
//! The bridge is a tuios client with no screen. It attaches a session and
//! sends the session layout as JSON and each pane's byte stream as binary
//! frames; this crate starts it, decodes what it sends and encodes what the
//! GUI sends back. The format is documented in the tuios tree at
//! `internal/guibridge/bridge.go`.

pub mod frame;
pub mod types;

pub use frame::{Decoded, FrameReader, encode_command, encode_input};
pub use types::{Command, Event, Fleet, SessionSummary, State, ThemeExport, Window, parse_hex};

use std::io::{BufReader, Write};
use std::path::PathBuf;
use std::process::{Child, ChildStdin, Command as Process, Stdio};
use std::sync::{Arc, Mutex};

/// Something the bridge sent, decoded.
#[derive(Debug)]
pub enum Message {
    /// A JSON event (attached, state, error).
    Event(Event),
    /// Bytes a pane produced.
    Output { pty: String, bytes: Vec<u8> },
    /// Reset the pane's emulator to `cols` x `rows` and write `bytes`.
    /// A zero size keeps the current size.
    Snapshot { pty: String, cols: u16, rows: u16, bytes: Vec<u8> },
    /// The pane's PTY changed size, in band with its output.
    Resized { pty: String, cols: u16, rows: u16 },
    /// The bridge exited; the string says why.
    Closed(String),
}

/// How to start the bridge.
#[derive(Debug, Clone)]
pub struct Launch {
    /// The tuios binary.
    pub tuios: PathBuf,
    pub session: Option<String>,
    pub cols: u16,
    pub rows: u16,
    pub cell_width: u32,
    pub cell_height: u32,
    /// The room around each pane's text, in device pixels: top, left,
    /// right, bottom. Empty keeps the panes at their full cell size.
    pub insets: Vec<u32>,
    /// A theme for the bridge to use instead of the one its config names.
    pub theme: Option<String>,
    /// Extra environment for the bridge (and the daemon it may start).
    pub env: Vec<(String, String)>,
}

/// A running bridge. Dropping it closes the bridge's input, which ends it.
pub struct Bridge {
    child: Child,
    stdin: Arc<Mutex<Option<ChildStdin>>>,
}

/// The sending half, cheap to clone and usable from any thread.
#[derive(Clone)]
pub struct Sender {
    stdin: Arc<Mutex<Option<ChildStdin>>>,
}

impl Sender {
    pub fn input(&self, pty: &str, bytes: &[u8]) {
        if bytes.is_empty() {
            return;
        }
        self.write(&encode_input(pty, bytes));
    }

    pub fn command(&self, cmd: &Command) {
        self.write(&encode_command(cmd));
    }

    fn write(&self, frame: &[u8]) {
        if let Ok(mut g) = self.stdin.lock() {
            if let Some(w) = g.as_mut() {
                if w.write_all(frame).and_then(|_| w.flush()).is_err() {
                    *g = None;
                }
            }
        }
    }
}

impl Bridge {
    /// Starts the bridge. `on_message` runs on a reader thread for every
    /// message, ending with [`Message::Closed`].
    pub fn spawn(launch: &Launch, on_message: impl Fn(Message) + Send + 'static) -> anyhow::Result<(Bridge, Sender)> {
        let mut cmd = Process::new(&launch.tuios);
        cmd.arg("gui-bridge")
            .arg("--cols")
            .arg(launch.cols.to_string())
            .arg("--rows")
            .arg(launch.rows.to_string())
            .arg("--cell-width")
            .arg(launch.cell_width.to_string())
            .arg("--cell-height")
            .arg(launch.cell_height.to_string());
        if launch.insets.len() == 4 {
            cmd.arg("--insets").arg(launch.insets.iter().map(|v| v.to_string()).collect::<Vec<_>>().join(","));
        }
        if let Some(s) = &launch.session {
            cmd.arg("--session").arg(s);
        }
        if let Some(t) = launch.theme.as_ref().filter(|t| !t.is_empty()) {
            cmd.arg("--theme").arg(t);
        }
        for (k, v) in &launch.env {
            if v.is_empty() {
                cmd.env_remove(k);
            } else {
                cmd.env(k, v);
            }
        }
        cmd.stdin(Stdio::piped()).stdout(Stdio::piped()).stderr(Stdio::inherit());
        let mut child = cmd.spawn().map_err(|e| anyhow::anyhow!("failed to start {}: {e}", launch.tuios.display()))?;
        let stdout = child.stdout.take().ok_or_else(|| anyhow::anyhow!("no stdout"))?;
        let stdin = Arc::new(Mutex::new(child.stdin.take()));
        std::thread::Builder::new().name("tuios-bridge-read".into()).spawn(move || {
            let mut r = FrameReader::new(BufReader::with_capacity(256 << 10, stdout));
            loop {
                match r.next() {
                    Ok(Some(d)) => {
                        if let Some(m) = d.into_message() {
                            on_message(m);
                        }
                    }
                    Ok(None) => {
                        on_message(Message::Closed("the bridge closed its output".into()));
                        return;
                    }
                    Err(e) => {
                        on_message(Message::Closed(format!("bad frame from the bridge: {e}")));
                        return;
                    }
                }
            }
        })?;
        Ok((Bridge { child, stdin: stdin.clone() }, Sender { stdin }))
    }

    pub fn pid(&self) -> u32 {
        self.child.id()
    }
}

impl Drop for Bridge {
    fn drop(&mut self) {
        if let Ok(mut g) = self.stdin.lock() {
            *g = None;
        }
        // The bridge quits when its input closes; give it a moment, then stop it.
        for _ in 0..50 {
            if let Ok(Some(_)) = self.child.try_wait() {
                return;
            }
            std::thread::sleep(std::time::Duration::from_millis(10));
        }
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}

/// Lists the daemon's sessions with `tuios ls --json`.
pub fn list_sessions(tuios: &std::path::Path, env: &[(String, String)]) -> anyhow::Result<Vec<SessionSummary>> {
    let mut cmd = Process::new(tuios);
    cmd.arg("ls").arg("--json");
    for (k, v) in env {
        if v.is_empty() {
            cmd.env_remove(k);
        } else {
            cmd.env(k, v);
        }
    }
    let out = cmd.stderr(Stdio::null()).output()?;
    types::parse_sessions(&out.stdout)
}
