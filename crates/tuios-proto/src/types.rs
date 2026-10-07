//! JSON shapes shared with the bridge.

use serde::{Deserialize, Deserializer, Serialize};

/// Go encodes an empty map or slice as null; read null as the default.
fn null_default<'de, D: Deserializer<'de>, T: Default + Deserialize<'de>>(d: D) -> Result<T, D::Error> {
    Ok(Option::<T>::deserialize(d)?.unwrap_or_default())
}
use std::collections::BTreeMap;

#[derive(Debug, Clone, Deserialize, PartialEq)]
pub struct Event {
    #[serde(rename = "type")]
    pub kind: String,
    #[serde(default)]
    pub message: Option<String>,
    #[serde(default)]
    pub state: Option<State>,
    #[serde(default)]
    pub theme: Option<ThemeExport>,
    /// Set on "fleet" events: every session and every pane's agent state on
    /// the daemon, sent when they change.
    #[serde(default)]
    pub fleet: Option<Fleet>,
}

/// Every session on the daemon and the rows of `tuios list-agents --all
/// --all-sessions --json`, as the bridge reads them from its own daemon
/// connection.
#[derive(Debug, Clone, Default, Deserialize, PartialEq)]
pub struct Fleet {
    #[serde(default, deserialize_with = "null_default")]
    pub sessions: Vec<SessionSummary>,
    #[serde(default)]
    pub agents: serde_json::Value,
}

/// A theme as tuios works it out (internal/guibridge/theme.go). Colours are
/// "#rrggbb"; an empty string is no colour.
#[derive(Debug, Clone, Default, Deserialize, PartialEq)]
pub struct ThemeExport {
    #[serde(default)]
    pub name: String,
    #[serde(default, deserialize_with = "null_default")]
    pub names: Vec<String>,
    #[serde(default)]
    pub light: bool,
    #[serde(default)]
    pub terminal: TerminalColors,
    #[serde(default, deserialize_with = "null_default")]
    pub ui: BTreeMap<String, String>,
    #[serde(default, deserialize_with = "null_default")]
    pub ground: BTreeMap<String, String>,
    #[serde(default)]
    pub rail_ground: String,
    #[serde(default)]
    pub rail_rule: String,
    #[serde(default)]
    pub border_focused: String,
    #[serde(default)]
    pub border_focused_terminal: String,
    #[serde(default)]
    pub border_unfocused: String,
    #[serde(default, deserialize_with = "null_default")]
    pub agent: BTreeMap<String, String>,
}

#[derive(Debug, Clone, Default, Deserialize, PartialEq)]
pub struct TerminalColors {
    #[serde(default)]
    pub fg: String,
    #[serde(default)]
    pub bg: String,
    #[serde(default)]
    pub cursor: String,
    #[serde(default)]
    pub ansi: Vec<String>,
}

/// Parses "#rrggbb" into 0xRRGGBB.
pub fn parse_hex(s: &str) -> Option<u32> {
    let h = s.strip_prefix('#')?;
    if h.len() != 6 {
        return None;
    }
    u32::from_str_radix(h, 16).ok()
}

/// The session layout. Positions and sizes are cells.
#[derive(Debug, Clone, Default, Deserialize, PartialEq)]
pub struct State {
    pub session: String,
    pub cols: u16,
    pub rows: u16,
    pub workspace: u32,
    pub num_workspaces: u32,
    #[serde(default, deserialize_with = "null_default")]
    pub workspace_names: BTreeMap<String, String>,
    #[serde(default, deserialize_with = "null_default")]
    pub occupied: Vec<u32>,
    #[serde(default, deserialize_with = "null_default")]
    pub focused: String,
    #[serde(default)]
    pub tiling: bool,
    #[serde(default, deserialize_with = "null_default")]
    pub windows: Vec<Window>,
}

impl State {
    /// The windows of the current workspace that are on screen, bottom first.
    pub fn visible(&self) -> Vec<&Window> {
        let mut v: Vec<&Window> = self
            .windows
            .iter()
            .filter(|w| w.workspace == self.workspace && !w.minimized && w.w > 0 && w.h > 0)
            .collect();
        v.sort_by_key(|w| w.z);
        v
    }

    pub fn workspace_name(&self, ws: u32) -> Option<&str> {
        self.workspace_names.get(&ws.to_string()).map(|s| s.as_str()).filter(|s| !s.is_empty())
    }

    pub fn window(&self, id: &str) -> Option<&Window> {
        self.windows.iter().find(|w| w.id == id)
    }
}

#[derive(Debug, Clone, Default, Deserialize, PartialEq)]
pub struct Window {
    pub id: String,
    pub pty: String,
    #[serde(default, deserialize_with = "null_default")]
    pub title: String,
    #[serde(default)]
    pub name: Option<String>,
    pub workspace: u32,
    pub x: i32,
    pub y: i32,
    pub w: i32,
    pub h: i32,
    #[serde(default)]
    pub z: i32,
    #[serde(default)]
    pub border: i32,
    #[serde(default)]
    pub minimized: bool,
    #[serde(default)]
    pub floating: bool,
    #[serde(default)]
    pub zoomed: bool,
    #[serde(default)]
    pub agent: Option<String>,
    #[serde(default)]
    pub agent_message: Option<String>,
    #[serde(default)]
    pub agent_kind: Option<String>,
    #[serde(default)]
    pub cwd: String,
    /// What the pane runs; empty at a shell prompt.
    #[serde(default)]
    pub foreground: String,
    /// The harness that reported the agent state.
    #[serde(default)]
    pub harness: String,
    /// When the pane entered its agent state, Unix milliseconds.
    #[serde(default)]
    pub agent_at: i64,
    #[serde(default)]
    pub repo: String,
    #[serde(default)]
    pub branch: String,
}

impl Window {
    /// The content rectangle in cells: (x, y, cols, rows).
    pub fn content(&self) -> (i32, i32, i32, i32) {
        let b = self.border.max(0);
        (self.x + b, self.y + b, (self.w - 2 * b).max(1), (self.h - 2 * b).max(1))
    }

    pub fn label(&self) -> &str {
        match &self.name {
            Some(n) if !n.is_empty() => n,
            _ if !self.title.is_empty() => &self.title,
            _ => "shell",
        }
    }

    pub fn agent_state(&self) -> Option<&str> {
        self.agent.as_deref().filter(|s| !s.is_empty() && *s != "none")
    }
}

/// What the GUI asks the bridge to do.
#[derive(Debug, Clone, Default, Serialize, PartialEq)]
pub struct Command {
    pub cmd: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub cols: u16,
    #[serde(skip_serializing_if = "is_zero")]
    pub rows: u16,
    #[serde(skip_serializing_if = "is_zero32")]
    pub cell_width: u32,
    #[serde(skip_serializing_if = "is_zero32")]
    pub cell_height: u32,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub window: String,
    #[serde(skip_serializing_if = "is_zero32")]
    pub n: u32,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub theme: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub command: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub args: Vec<String>,
}

fn is_zero(v: &u16) -> bool {
    *v == 0
}
fn is_zero32(v: &u32) -> bool {
    *v == 0
}

impl Command {
    pub fn resize(cols: u16, rows: u16, cell_width: u32, cell_height: u32) -> Self {
        Command { cmd: "resize".into(), cols, rows, cell_width, cell_height, ..Default::default() }
    }
    pub fn focus(window: &str) -> Self {
        Command { cmd: "focus".into(), window: window.into(), ..Default::default() }
    }
    pub fn theme(name: &str) -> Self {
        Command { cmd: "theme".into(), theme: name.into(), ..Default::default() }
    }
    pub fn workspace(n: u32) -> Self {
        Command { cmd: "workspace".into(), n, ..Default::default() }
    }
    /// Any tape command, run the way `tuios run-command` runs it.
    pub fn tape(command: &str, args: &[&str]) -> Self {
        Command {
            cmd: "tape".into(),
            command: command.into(),
            args: args.iter().map(|s| s.to_string()).collect(),
            ..Default::default()
        }
    }
}

/// A row of `tuios ls --json`.
#[derive(Debug, Clone, Deserialize, PartialEq)]
pub struct SessionSummary {
    pub name: String,
    #[serde(default)]
    pub window_count: u32,
    #[serde(default)]
    pub attached: bool,
    #[serde(default)]
    pub current_workspace: u32,
    #[serde(default)]
    pub dir: String,
}

pub fn parse_sessions(json: &[u8]) -> anyhow::Result<Vec<SessionSummary>> {
    let text = std::str::from_utf8(json)?.trim();
    if text.is_empty() || text == "null" {
        return Ok(Vec::new());
    }
    Ok(serde_json::from_str(text)?)
}
