//! The command palette: tuios actions and app actions, filtered by a fuzzy
//! subsequence match.

/// What a palette entry does.
#[derive(Clone, Debug, PartialEq)]
pub enum Act {
    /// A tuios tape command with its arguments, run by the bridge.
    Tape(&'static str, &'static [&'static str]),
    Workspace(u32),
    Session(String),
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

#[derive(Clone, Debug)]
pub struct Entry {
    pub title: String,
    pub hint: &'static str,
    pub act: Act,
}

pub fn entries(sessions: &[String], current: &str) -> Vec<Entry> {
    let e = |title: &str, hint: &'static str, act: Act| Entry { title: title.to_string(), hint, act };
    let mut v = vec![
        e("New pane", "ctrl+shift+enter", Act::Tape("NewWindow", &[])),
        e("Split right", "ctrl+shift+d", Act::Tape("Split", &["vertical"])),
        e("Split down", "ctrl+shift+e", Act::Tape("Split", &["horizontal"])),
        e("Close pane", "ctrl+shift+w", Act::Tape("CloseWindow", &[])),
        e("Next pane", "ctrl+tab", Act::Tape("NextWindow", &[])),
        e("Previous pane", "ctrl+shift+tab", Act::Tape("PrevWindow", &[])),
        e("Focus left", "alt+h", Act::Tape("FocusDirection", &["left"])),
        e("Focus right", "alt+l", Act::Tape("FocusDirection", &["right"])),
        e("Focus up", "alt+k", Act::Tape("FocusDirection", &["up"])),
        e("Focus down", "alt+j", Act::Tape("FocusDirection", &["down"])),
        e("Zoom pane", "ctrl+shift+z", Act::Tape("ToggleZoom", &[])),
        e("Rotate split", "", Act::Tape("RotateSplit", &[])),
        e("Equalize splits", "", Act::Tape("EqualizeSplits", &[])),
        e("Toggle tiling", "", Act::Tape("ToggleTiling", &[])),
        e("Minimize pane", "", Act::Tape("MinimizeWindow", &[])),
        e("Swap with master", "", Act::Tape("SwapWithMaster", &[])),
        e("Copy selection", "ctrl+shift+c", Act::Copy),
        e("Paste", "ctrl+shift+v", Act::Paste),
        e("Bigger text", "ctrl+=", Act::FontBigger),
        e("Smaller text", "ctrl+-", Act::FontSmaller),
        e("Reset text size", "ctrl+0", Act::FontReset),
        e("Toggle sidebar", "ctrl+shift+b", Act::ToggleSidebar),
        e("New session", "", Act::NewSession),
        e("Quit", "ctrl+shift+q", Act::Quit),
    ];
    for n in 1..=9 {
        v.push(Entry { title: format!("Go to workspace {n}"), hint: "alt+N", act: Act::Workspace(n) });
    }
    for n in 1..=9u32 {
        let args: &'static [&'static str] = WS_ARGS[n as usize - 1];
        v.push(Entry { title: format!("Move pane to workspace {n}"), hint: "", act: Act::Tape("MoveToWorkspace", args) });
    }
    for s in sessions {
        if s != current {
            v.push(Entry { title: format!("Switch to session {s}"), hint: "", act: Act::Session(s.clone()) });
        }
    }
    v
}

const WS_ARGS: [&[&str]; 9] = [&["1"], &["2"], &["3"], &["4"], &["5"], &["6"], &["7"], &["8"], &["9"]];

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
    for qc in query.chars().flat_map(char::to_lowercase) {
        if qc == ' ' {
            continue;
        }
        let pos = t[ti..].iter().position(|&c| c == qc)? + ti;
        let word_start = pos == 0 || !t[pos - 1].is_alphanumeric();
        s += 1;
        if word_start {
            s += 8;
        }
        if prev.is_some_and(|p| p + 1 == pos) {
            s += 8;
        }
        if pos == 0 {
            s += 10;
        }
        prev = Some(pos);
        ti = pos + 1;
    }
    Some(s * 100 - t.len() as i32)
}

/// Hundreds of themes would bury the commands, so a theme ranks below a
/// command that matches as well.
fn bias(e: &Entry) -> i32 {
    if matches!(e.act, Act::Theme(_)) { -1000 } else { 0 }
}

/// The entries matching `query`, best first. Order is stable for equal scores.
pub fn filter<'a>(entries: &'a [Entry], query: &str) -> Vec<&'a Entry> {
    let mut v: Vec<(i32, usize, &Entry)> =
        entries.iter().enumerate().filter_map(|(i, e)| score(query, &e.title).map(|s| (s + bias(e), i, e))).collect();
    if !query.is_empty() {
        v.sort_by(|a, b| b.0.cmp(&a.0).then(a.1.cmp(&b.1)));
    }
    v.into_iter().map(|(_, _, e)| e).collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn subsequence_and_word_starts() {
        assert!(score("sr", "Split right").is_some());
        assert!(score("xyz", "Split right").is_none());
        assert!(score("sr", "Split right") > score("sr", "Show the readme"), "shorter wins ties");
        assert!(score("spl", "Split down").unwrap() > score("spl", "Swap pane later").unwrap(), "runs beat scattered hits");
    }

    #[test]
    fn filter_ranks_best_first() {
        let mut e = entries(&["work".into(), "play".into()], "work");
        e.push(Entry { title: "Theme: seafoam_pastel".into(), hint: "", act: Act::Theme("seafoam_pastel".into()) });
        assert!(filter(&e, "sp")[0].title.starts_with("Split"), "commands before themes");
        assert_eq!(filter(&e, "theme seafoam")[0].act, Act::Theme("seafoam_pastel".into()));
        let r = filter(&e, "split");
        assert!(r[0].title.starts_with("Split"));
        assert!(filter(&e, "session play").iter().any(|e| e.act == Act::Session("play".into())));
        assert!(!filter(&e, "").iter().any(|e| e.act == Act::Session("work".into())), "the current session is not offered");
    }
}
