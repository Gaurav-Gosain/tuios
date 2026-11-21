package session

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

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

func isPromptTitle(t string) bool {
	if strings.ContainsAny(t, " \t") {
		return false
	}
	at := strings.IndexByte(t, '@')
	return at > 0 && strings.IndexByte(t[at:], ':') > 1
}
