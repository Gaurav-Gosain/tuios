//! The command palette: panes in every session, sessions, tuios actions and
//! themes, filtered by a fuzzy subsequence match.

use crate::fleet::{PaneInfo, Status};

/// What a palette entry does.
#[derive(Clone, Debug, PartialEq)]
pub enum Act {
    /// A tuios tape command with its arguments, run by the bridge.
    Tape(&'static str, &'static [&'static str]),
    Workspace(u32),
    Session(String),
    /// Go to a pane: attach its session if needed, show its workspace, focus it.
    Jump { session: String, window: String, workspace: u32 },
    NextNeedsYou,
    PrevSession,
    NextSession,
    NewSession,
    Copy,
    Paste,
    FontBigger,
    FontSmaller,
    FontReset,
    ToggleSidebar,
    /// Switch to a tuios theme, for this window only.
    Theme(String),
    Quit,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub enum Section {
    NeedsYou,
    Panes,
    Sessions,
    Commands,
    Themes,
}

impl Section {
    pub fn label(self) -> &'static str {
        match self {
            Section::NeedsYou => "Needs you",
            Section::Panes => "Panes",
            Section::Sessions => "Sessions",
            Section::Commands => "Commands",
            Section::Themes => "Themes",
        }
    }
}

/// The glyph at the left of a row.
#[derive(Clone, Copy, Debug, PartialEq)]
pub enum Icon {
    State(Status),
    Session,
    Command,
    Theme,
}

#[derive(Clone, Debug)]
pub struct Entry {
    pub section: Section,
    pub icon: Icon,
    pub title: String,
    pub subtitle: String,
    /// Keys, as "ctrl+shift+d".
    pub hint: &'static str,
    pub act: Act,
}

fn cmd(title: &str, hint: &'static str, act: Act) -> Entry {
    Entry { section: Section::Commands, icon: Icon::Command, title: title.into(), subtitle: String::new(), hint, act }
}

/// Every entry. `panes` is the fleet, `current` the attached session.
pub fn entries(panes: &[PaneInfo], sessions: &[String], current: &str, themes: &[String], theme: &str) -> Vec<Entry> {
    let mut v = Vec::new();
    let mut ps: Vec<&PaneInfo> = panes.iter().collect();
    ps.sort_by(|a, b| crate::fleet::order(a, b));
    let mut first_waiting = true;
    for p in ps {
        let waiting = matches!(p.status, Status::NeedsYou | Status::Errored);
        let section = if waiting { Section::NeedsYou } else { Section::Panes };
        // One fragment: the harness and the message, or where the pane is.
        let mut parts: Vec<&str> = Vec::new();
        if !p.harness.is_empty() {
            parts.push(&p.harness);
        }
        let fragment = p.fragment();
        if !fragment.is_empty() && (p.status.is_agent() && !p.message.is_empty() || p.harness.is_empty()) {
            parts.push(fragment);
        }
        if p.session != current && parts.len() < 2 {
            parts.push(&p.session);
        }
        v.push(Entry {
            section,
            icon: Icon::State(p.status),
            title: p.name.clone(),
            subtitle: parts.join(" · "),
            hint: if waiting && std::mem::take(&mut first_waiting) { "ctrl+shift+j" } else { "" },
            act: Act::Jump { session: p.session.clone(), window: p.window.clone(), workspace: p.workspace },
        });
    }
    for s in sessions.iter().filter(|s| *s != current) {
        v.push(Entry { section: Section::Sessions, icon: Icon::Session, title: s.clone(), subtitle: String::new(), hint: "", act: Act::Session(s.clone()) });
    }
    v.extend([
        cmd("Jump to the next pane that needs you", "ctrl+shift+j", Act::NextNeedsYou),
        cmd("New pane", "ctrl+shift+t", Act::Tape("NewWindow", &[])),
        cmd("Split right", "ctrl+shift+d", Act::Tape("Split", &["vertical"])),
        cmd("Split down", "ctrl+shift+e", Act::Tape("Split", &["horizontal"])),
        cmd("Close pane", "ctrl+shift+w", Act::Tape("CloseWindow", &[])),
        cmd("Zoom pane", "ctrl+shift+z", Act::Tape("ToggleZoom", &[])),
        cmd("Next pane", "ctrl+tab", Act::Tape("NextWindow", &[])),
        cmd("Previous pane", "ctrl+shift+tab", Act::Tape("PrevWindow", &[])),
        cmd("Focus left", "alt+left", Act::Tape("FocusDirection", &["left"])),
        cmd("Focus right", "alt+right", Act::Tape("FocusDirection", &["right"])),
        cmd("Focus up", "alt+up", Act::Tape("FocusDirection", &["up"])),
        cmd("Focus down", "alt+down", Act::Tape("FocusDirection", &["down"])),
        cmd("Rotate split", "", Act::Tape("RotateSplit", &[])),
        cmd("Equalize splits", "", Act::Tape("EqualizeSplits", &[])),
        cmd("Swap with master", "", Act::Tape("SwapWithMaster", &[])),
        cmd("Next session", "ctrl+shift+]", Act::NextSession),
        cmd("Previous session", "ctrl+shift+[", Act::PrevSession),
        cmd("New session", "", Act::NewSession),
        cmd("Copy selection", "ctrl+shift+c", Act::Copy),
        cmd("Paste", "ctrl+shift+v", Act::Paste),
        cmd("Bigger text", "ctrl+=", Act::FontBigger),
        cmd("Smaller text", "ctrl+-", Act::FontSmaller),
        cmd("Reset text size", "ctrl+0", Act::FontReset),
        cmd("Show or hide the sidebar", "ctrl+shift+b", Act::ToggleSidebar),
        cmd("Quit", "ctrl+shift+q", Act::Quit),
    ]);
    for n in 1..=9 {
        v.push(cmd(&format!("Go to workspace {n}"), WS_KEYS[n as usize - 1], Act::Workspace(n)));
    }
    for n in 1..=9u32 {
        v.push(cmd(&format!("Move pane to workspace {n}"), "", Act::Tape("MoveToWorkspace", WS_ARGS[n as usize - 1])));
    }
    for t in themes.iter().filter(|t| *t != theme) {
        v.push(Entry { section: Section::Themes, icon: Icon::Theme, title: t.replace('_', " "), subtitle: String::new(), hint: "", act: Act::Theme(t.clone()) });
    }
    v
}

const WS_ARGS: [&[&str]; 9] = [&["1"], &["2"], &["3"], &["4"], &["5"], &["6"], &["7"], &["8"], &["9"]];
const WS_KEYS: [&str; 9] = ["alt+1", "alt+2", "alt+3", "alt+4", "alt+5", "alt+6", "alt+7", "alt+8", "alt+9"];

/// A hint as the one chip shows it: "ctrl+shift+d" is "Ctrl+Shift+D".
pub fn chip(hint: &str) -> String {
    keycaps(hint).join("+")
}

/// The byte offsets in `title` of the characters `query` matches, first
/// match after the one before, as [`score`] reads them.
pub fn matches(query: &str, title: &str) -> Vec<usize> {
    let mut out = Vec::new();
    let lower: Vec<(usize, char)> = title.char_indices().map(|(i, c)| (i, c.to_lowercase().next().unwrap_or(c))).collect();
    let mut at = 0usize;
    for qc in query.chars().flat_map(char::to_lowercase).filter(|c| *c != ' ') {
        match lower[at..].iter().position(|(_, c)| *c == qc) {
            Some(p) => {
                out.push(lower[at + p].0);
                at += p + 1;
            }
            None => return Vec::new(),
        }
    }
    out
}

/// The keycaps of a hint: "ctrl+shift+d" is ["Ctrl", "Shift", "D"].
pub fn keycaps(hint: &str) -> Vec<String> {
    if hint.is_empty() {
        return Vec::new();
    }
    let mut parts: Vec<&str> = hint.split('+').collect();
    // "ctrl+=" and "ctrl++" keep their last character.
    if hint.ends_with("++") {
        parts.retain(|p| !p.is_empty());
        parts.push("+");
    }
    parts
        .into_iter()
        .filter(|p| !p.is_empty())
        .map(|p| match p {
            "ctrl" => "Ctrl".into(),
            "shift" => "Shift".into(),
            "alt" => "Alt".into(),
            "tab" => "Tab".into(),
            "enter" => "Enter".into(),
            "escape" | "esc" => "Esc".into(),
            "left" => "←".into(),
            "right" => "→".into(),
            "up" => "↑".into(),
            "down" => "↓".into(),
            k => k.to_uppercase(),
        })
        .collect()
}

/// Scores `title` against `query`: None when the query's characters do not
/// all appear in order. Higher is better: matches at word starts and runs of
/// consecutive characters score more, and shorter titles break ties.
pub fn score(query: &str, title: &str) -> Option<i32> {
    if query.is_empty() {
        return Some(0);
    }
    let t: Vec<char> = title.chars().flat_map(char::to_lowercase).collect();
    let mut s = 0i32;
    let mut ti = 0usize;
    let mut prev: Option<usize> = None;
    // Characters that land on a word start or right after the previous one.
    // A query whose letters are mostly scattered through the text is noise.
    let (mut n, mut good) = (0, 0);
    for qc in query.chars().flat_map(char::to_lowercase) {
        if qc == ' ' {
            continue;
        }
        let pos = t[ti..].iter().position(|&c| c == qc)? + ti;
        let word_start = pos == 0 || !t[pos - 1].is_alphanumeric();
        let run = prev.is_some_and(|p| p + 1 == pos);
        n += 1;
        if word_start || run {
            good += 1;
        }
        s += 1;
        if word_start {
            s += 8;
        }
        if run {
            s += 8;
        }
        if pos == 0 {
            s += 10;
        }
        prev = Some(pos);
        ti = pos + 1;
    }
    if n >= 3 && good * 4 < n * 3 {
        return None;
    }
    Some(s * 100 - t.len() as i32)
}

/// The text an entry is matched on.
fn haystack(e: &Entry) -> String {
    match e.section {
        Section::Themes => format!("theme {}", e.title),
        Section::Sessions => format!("session {}", e.title),
        // The name and the harness or folder; not the agent's message, whose
        // letters would match almost anything.
        Section::NeedsYou | Section::Panes => format!("{} {}", e.title, e.subtitle.split(" · ").next().unwrap_or("")),
        Section::Commands => e.title.clone(),
    }
}

/// The entries matching `query`, in section order and best first inside a
/// section. With no query, themes stay hidden: there are hundreds.
pub fn filter<'a>(entries: &'a [Entry], query: &str) -> Vec<&'a Entry> {
    let q = query.trim();
    let mut v: Vec<(Section, i32, usize, &Entry)> = entries
        .iter()
        .enumerate()
        .filter(|(_, e)| !(q.is_empty() && e.section == Section::Themes))
        .filter_map(|(i, e)| score(q, &haystack(e)).map(|s| (e.section, s, i, e)))
        .collect();
    if q.is_empty() {
        v.sort_by(|a, b| a.0.cmp(&b.0).then(a.2.cmp(&b.2)));
    } else {
        // The best match decides which section leads; the rest follow in order.
        let best = v.iter().max_by_key(|x| x.1).map(|x| x.0);
        v.sort_by(|a, b| {
            let ka = (Some(a.0) != best, a.0);
            let kb = (Some(b.0) != best, b.0);
            ka.cmp(&kb).then(b.1.cmp(&a.1)).then(a.2.cmp(&b.2))
        });
    }
    v.into_iter().map(|(_, _, _, e)| e).collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn sample() -> Vec<Entry> {
        let p = |name: &str, status| PaneInfo {
            session: "work".into(),
            window: name.into(),
            workspace: 1,
            status,
            name: name.into(),
            harness: String::new(),
            message: String::new(),
            place: String::new(),
            since_ms: 1,
            focused: false,
            seen: false,
        };
        entries(&[p("claude · api", Status::NeedsYou), p("nvim", Status::Terminal)], &["work".into(), "play".into()], "work", &["seafoam_pastel".into()], "dracula")
    }

    #[test]
    fn subsequence_and_word_starts() {
        assert!(score("sr", "Split right").is_some());
        assert!(score("xyz", "Split right").is_none());
        assert!(score("spl", "Split down") > score("spl", "Swap pane later"), "runs beat scattered hits");
    }

    #[test]
    fn empty_query_lists_by_section_and_hides_themes() {
        let e = sample();
        let r = filter(&e, "");
        assert_eq!(r[0].section, Section::NeedsYou);
        assert!(!r.iter().any(|e| e.section == Section::Themes));
        assert!(!r.iter().any(|e| e.act == Act::Session("work".into())), "the current session is not offered");
    }

    #[test]
    fn the_best_match_leads() {
        let e = sample();
        assert!(filter(&e, "split")[0].title.starts_with("Split"));
        assert_eq!(filter(&e, "theme seafoam")[0].act, Act::Theme("seafoam_pastel".into()));
        assert_eq!(filter(&e, "session play")[0].act, Act::Session("play".into()));
        assert_eq!(filter(&e, "nvim")[0].title, "nvim");
        assert!(!filter(&e, "split").iter().any(|e| e.section != Section::Commands), "scattered letters do not match");
    }

    #[test]
    fn matched_characters_follow_the_query() {
        assert_eq!(matches("re", "api retries"), vec![4, 5]);
        assert_eq!(matches("re", "Previous pane"), vec![1, 2]);
        assert!(matches("zz", "readme").is_empty());
        assert_eq!(chip("ctrl+shift+p"), "Ctrl+Shift+P");
    }

    #[test]
    fn keycaps_read_like_keys() {
        assert_eq!(keycaps("ctrl+shift+d"), vec!["Ctrl", "Shift", "D"]);
        assert_eq!(keycaps("ctrl+="), vec!["Ctrl", "="]);
        assert!(keycaps("").is_empty());
    }
}
