package session

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// A session's agent title.
//
// Claude Code, and other agents, set the terminal title to a summary of the
// task ("Fix login redirect"). A session that still wears the name tuios made
// up for it is shown by that title, in the rail, the switcher and tuios ls.
// It is a label only: the session's name does not change, so every selector,
// script and attach by name keeps working, and nothing is lost when the title
// moves on.
//
// The rules, kept in this one place so every surface agrees:
//   - The pane is the session's first agent pane, in creation order (the
//     order of the session's window list). A later agent's title never takes
//     over, so the label does not jump between panes.
//   - The title is what harness.TitleLabel keeps: spinner and state marks off,
//     and nothing for a title that is only the program's name, a path, a
//     shell's own title or the pane's default title. A "user@host:dir" prompt
//     title is dropped too.
//   - While the agent runs the label follows its title. When the agent exits
//     the pane loses its harness and the label drops back to the directory.
//   - Control characters are removed and the length is bounded here. Each
//     surface cuts the title to its own width with its own helper.

// maxAgentTitleBytes bounds the title a session is labelled with. A task
// summary is a few words; the bound is for a program that writes a paragraph.
const maxAgentTitleBytes = 120

// PaneAgentTitle is the label one pane's title gives its session: harnessID
// is the agent detected in the pane (empty for none), program its foreground
// command, title its terminal title. Empty when the title says nothing a
// directory would not.
func PaneAgentTitle(harnessID, program, title string) string {
	if harnessID == "" {
		return ""
	}
	t := printableClaim(harness.TitleLabel(harnessID, program, title), maxAgentTitleBytes)
	if t == "" || isPromptTitle(t) {
		return ""
	}
	return t
}

// SessionAgentTitle is PaneAgentTitle for the first agent pane among a
// session's window summaries, in the order the daemon lists them, which is
// creation order. Scratch and rail panes do not count.
func SessionAgentTitle(windows []WindowSummary) string {
	for i := range windows {
		w := &windows[i]
		if w.Scratch || w.AgentHarness == "" {
			continue
		}
		return PaneAgentTitle(w.AgentHarness, w.ForegroundCmd, w.Title)
	}
	return ""
}

// isPromptTitle reports a bash-style "user@host:dir" title: one word with an @
// before a colon.
func isPromptTitle(t string) bool {
	if strings.ContainsAny(t, " \t") {
		return false
	}
	at := strings.IndexByte(t, '@')
	return at > 0 && strings.IndexByte(t[at:], ':') > 1
}
