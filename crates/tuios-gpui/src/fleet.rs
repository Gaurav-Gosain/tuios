//! The fleet: every pane in every session on the daemon, with what the
//! sidebar and the palette say about it.
//!
//! The attached session comes from the bridge's live state. Every other
//! session comes from `tuios list-agents --all --all-sessions --json`, polled,
//! so an agent in a session nobody is looking at still reaches the person.

use serde::Deserialize;
use std::cmp::Ordering;
use tuios_proto::State;

/// What a pane is doing, in the order the person should look at it.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum Status {
    NeedsYou,
    Errored,
    Working,
    /// Finished a turn the person has not looked at.
    Done,
    /// An agent at rest, or finished and seen.
    Idle,
    /// No agent: a shell or a program.
    Terminal,
}

impl Status {
    pub fn from_agent(state: &str, unseen: bool) -> Status {
        match state {
            "needs_input" => Status::NeedsYou,
            "errored" => Status::Errored,
            "working" => Status::Working,
            // A finished turn shows as done until the pane is looked at; tuios
            // clears the state then. `unseen` only brightens the row.
            "done" => {
                let _ = unseen;
                Status::Done
            }
            // "unknown" is an agent whose state went stale: still an agent.
            "idle" | "unknown" | "stalled" => Status::Idle,
            _ => Status::Terminal,
        }
    }

    pub fn is_agent(self) -> bool {
        self != Status::Terminal
    }

    /// The one word the app uses for each state.
    pub fn word(self) -> &'static str {
        match self {
            Status::NeedsYou => "Needs you",
            Status::Errored => "Error",
            Status::Working => "Working",
            Status::Done => "Done",
            Status::Idle => "Idle",
            Status::Terminal => "Terminal",
        }
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct PaneInfo {
    pub session: String,
    pub window: String,
    pub workspace: u32,
    pub status: Status,
    /// The name shown on line one.
    pub name: String,
    /// The second line: the agent's message, or program, folder and branch.
    pub detail: String,
    /// When the pane entered its state, Unix milliseconds; 0 when unknown.
    pub since_ms: i64,
    pub focused: bool,
}

/// A row of `tuios list-agents --json`.
#[derive(Debug, Clone, Default, Deserialize)]
struct AgentRow {
    #[serde(default)]
    session: String,
    #[serde(default)]
    window_id: String,
    #[serde(default)]
    name: String,
    #[serde(default)]
    state: String,
    #[serde(default)]
    message: String,
    #[serde(default)]
    harness_id: String,
    #[serde(default)]
    foreground: String,
    #[serde(default)]
    cwd: String,
    #[serde(default)]
    workspace: u32,
    #[serde(default)]
    focused: bool,
    #[serde(default)]
    finished_unread: bool,
    #[serde(default)]
    agent_state_at: i64,
}

#[derive(Debug, Default, Deserialize)]
struct AgentList {
    #[serde(default)]
    agents: Vec<AgentRow>,
}

/// The short name of a harness id.
pub fn harness_name(id: &str) -> &str {
    match id {
        "claude-code" | "claude" => "claude",
        "codex" | "codex-cli" => "codex",
        "gemini-cli" | "gemini" => "gemini",
        "opencode" => "opencode",
        "" => "",
        other => other,
    }
}

/// A title worth showing: not tuios's placeholder, not the shell's
/// user@host:path, and without the spinner glyphs agents prefix.
pub fn clean_title(title: &str) -> Option<String> {
    let t = title.trim_start_matches(|c: char| !c.is_alphanumeric() && !"~/.(".contains(c)).trim();
    if t.is_empty() {
        return None;
    }
    if let Some(rest) = t.strip_prefix("Terminal ") {
        if rest.len() >= 6 && rest.chars().all(|c| c.is_ascii_hexdigit()) {
            return None;
        }
    }
    if t.contains('@') && t.contains(':') && !t.contains(' ') {
        return None;
    }
    Some(t.to_string())
}

/// `/home/me/dev/x` as `~/dev/x`, given `home`.
pub fn tilde(path: &str, home: &str) -> String {
    if !home.is_empty() {
        if path == home {
            return "~".into();
        }
        if let Some(rest) = path.strip_prefix(home).filter(|r| r.starts_with('/')) {
            return format!("~{rest}");
        }
    }
    path.to_string()
}

fn home() -> String {
    std::env::var("HOME").unwrap_or_default()
}

struct Raw<'a> {
    custom: Option<&'a str>,
    title: &'a str,
    harness: &'a str,
    foreground: &'a str,
    cwd: &'a str,
    branch: &'a str,
    message: &'a str,
}

fn describe(r: &Raw, status: Status, home: &str) -> (String, String) {
    let folder = tilde(r.cwd, home);
    let place = match (folder.is_empty(), r.branch.is_empty()) {
        (false, false) => format!("{folder} · {}", r.branch),
        (false, true) => folder.clone(),
        (true, false) => r.branch.to_string(),
        (true, true) => String::new(),
    };
    let title = r.custom.filter(|s| !s.is_empty()).map(str::to_string).or_else(|| clean_title(r.title));
    let harness = harness_name(r.harness);
    let name = match (harness.is_empty(), &title) {
        (false, Some(t)) if !t.eq_ignore_ascii_case(harness) => format!("{harness} · {t}"),
        (false, _) => harness.to_string(),
        (true, _) if !r.foreground.is_empty() && status == Status::Terminal => match &title {
            Some(t) if !t.starts_with(r.foreground) => format!("{} · {t}", r.foreground),
            _ => r.foreground.to_string(),
        },
        (true, Some(t)) => t.clone(),
        (true, None) if !folder.is_empty() => short_folder(&folder),
        (true, None) => "shell".into(),
    };
    let detail = if !r.message.is_empty() && status.is_agent() {
        r.message.lines().next().unwrap_or("").to_string()
    } else if status == Status::Working {
        if place.is_empty() { "Working".into() } else { place }
    } else {
        place
    };
    (name, detail)
}

/// The last two parts of a folder, which say where a shell is.
fn short_folder(f: &str) -> String {
    let parts: Vec<&str> = f.split('/').filter(|p| !p.is_empty()).collect();
    if parts.len() <= 2 { f.to_string() } else { parts[parts.len() - 2..].join("/") }
}

/// Panes from `list-agents --all --all-sessions --json`.
pub fn parse_list(json: &[u8]) -> Vec<PaneInfo> {
    let list: AgentList = serde_json::from_slice(json).unwrap_or_default();
    let home = home();
    list.agents
        .iter()
        .map(|a| {
            let status = Status::from_agent(&a.state, a.finished_unread);
            let raw = Raw { custom: None, title: &a.name, harness: &a.harness_id, foreground: &a.foreground, cwd: &a.cwd, branch: "", message: &a.message };
            let (name, detail) = describe(&raw, status, &home);
            PaneInfo {
                session: a.session.clone(),
                window: a.window_id.clone(),
                workspace: a.workspace,
                status,
                name,
                detail,
                since_ms: a.agent_state_at / 1_000_000,
                focused: a.focused,
            }
        })
        .collect()
}

/// Runs `tuios list-agents --all --all-sessions --json`: every pane, and the
/// windows whose finished turn nobody has looked at.
pub fn fetch(tuios: &std::path::Path, env: &[(String, String)]) -> Option<(Vec<PaneInfo>, std::collections::HashSet<String>)> {
    let mut cmd = std::process::Command::new(tuios);
    cmd.args(["list-agents", "--all", "--all-sessions", "--json"]);
    for (k, v) in env {
        if v.is_empty() {
            cmd.env_remove(k);
        } else {
            cmd.env(k, v);
        }
    }
    let out = cmd.stderr(std::process::Stdio::null()).output().ok()?;
    if !out.status.success() {
        return None;
    }
    let list: AgentList = serde_json::from_slice(&out.stdout).ok()?;
    let unread = list.agents.iter().filter(|a| a.finished_unread).map(|a| a.window_id.clone()).collect();
    Some((parse_list(&out.stdout), unread))
}

/// Panes of the attached session, from the bridge.
pub fn from_state(st: &State, focused: Option<&str>, unseen: impl Fn(&str) -> bool) -> Vec<PaneInfo> {
    let home = home();
    st.windows
        .iter()
        .map(|w| {
            let state = w.agent.as_deref().unwrap_or("");
            let status = Status::from_agent(state, unseen(&w.id));
            let raw = Raw {
                custom: w.name.as_deref(),
                title: &w.title,
                harness: &w.harness,
                foreground: &w.foreground,
                cwd: &w.cwd,
                branch: &w.branch,
                message: w.agent_message.as_deref().unwrap_or(""),
            };
            let (name, detail) = describe(&raw, status, &home);
            PaneInfo {
                session: st.session.clone(),
                window: w.id.clone(),
                workspace: w.workspace,
                status,
                name,
                detail,
                since_ms: w.agent_at,
                focused: focused == Some(w.id.as_str()),
            }
        })
        .collect()
}

/// Sidebar order inside a session: by state, then oldest first for states
/// that wait, newest first for the rest, then workspace.
pub fn order(a: &PaneInfo, b: &PaneInfo) -> Ordering {
    a.status.cmp(&b.status).then_with(|| match a.status {
        Status::NeedsYou | Status::Errored => a.since_ms.cmp(&b.since_ms),
        Status::Terminal => a.workspace.cmp(&b.workspace),
        _ => b.since_ms.cmp(&a.since_ms),
    })
}

/// How long ago, in the fewest characters: "now", "12s", "4m", "3h", "2d".
pub fn age(since_ms: i64, now_ms: i64) -> String {
    if since_ms <= 0 {
        return String::new();
    }
    let s = ((now_ms - since_ms) / 1000).max(0);
    match s {
        0..=9 => "now".into(),
        10..=59 => format!("{s}s"),
        60..=3599 => format!("{}m", s / 60),
        3600..=86_399 => format!("{}h", s / 3600),
        _ => format!("{}d", s / 86_400),
    }
}

pub fn now_ms() -> i64 {
    std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).map(|d| d.as_millis() as i64).unwrap_or(0)
}

/// Counts for the summary line: needs you, working, done.
pub fn counts(panes: &[PaneInfo]) -> (usize, usize, usize) {
    let n = |s: Status| panes.iter().filter(|p| p.status == s).count();
    (n(Status::NeedsYou) + n(Status::Errored), n(Status::Working), n(Status::Done))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn placeholder_titles_are_not_names() {
        assert_eq!(clean_title("Terminal 7c87b868"), None);
        assert_eq!(clean_title("gaurav@arch:~/dev"), None);
        assert_eq!(clean_title("✳ Fix the flaky test").as_deref(), Some("Fix the flaky test"));
        assert_eq!(clean_title("⠂ api retries").as_deref(), Some("api retries"));
    }

    #[test]
    fn names_follow_agent_then_program_then_folder() {
        let r = |title, harness, fg, cwd| Raw { custom: None, title, harness, foreground: fg, cwd, branch: "main", message: "" };
        let home = "/home/me";
        assert_eq!(describe(&r("✳ api retries", "claude-code", "", "/home/me/api"), Status::Working, home).0, "claude · api retries");
        assert_eq!(describe(&r("Terminal 7c87b868", "codex", "", ""), Status::Idle, home).0, "codex");
        assert_eq!(describe(&r("Terminal 7c87b868", "", "nvim", "/home/me"), Status::Terminal, home).0, "nvim");
        let (name, detail) = describe(&r("Terminal 7c87b868", "", "", "/home/me/dev/tuios"), Status::Terminal, home);
        assert_eq!(name, "dev/tuios");
        assert_eq!(detail, "~/dev/tuios · main");
    }

    #[test]
    fn needs_you_sorts_first_and_oldest_first() {
        let p = |status, since| PaneInfo { session: "s".into(), window: "w".into(), workspace: 1, status, name: String::new(), detail: String::new(), since_ms: since, focused: false };
        let mut v = vec![p(Status::Terminal, 0), p(Status::Working, 5), p(Status::NeedsYou, 9), p(Status::NeedsYou, 3), p(Status::Done, 1)];
        v.sort_by(order);
        let got: Vec<(Status, i64)> = v.iter().map(|p| (p.status, p.since_ms)).collect();
        assert_eq!(got, vec![(Status::NeedsYou, 3), (Status::NeedsYou, 9), (Status::Working, 5), (Status::Done, 1), (Status::Terminal, 0)]);
    }

    #[test]
    fn ages_are_short() {
        assert_eq!(age(0, 100), "");
        assert_eq!(age(1_000, 5_000), "now");
        assert_eq!(age(0 + 1, 1 + 125_000), "2m");
        assert_eq!(age(1, 1 + 7_200_000), "2h");
    }
}
