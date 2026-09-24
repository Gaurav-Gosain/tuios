package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestEveryStateMarkSurvivesTheTitleSanitiser: the palette runs its rows
// through printableTitle, which strips the geometric-shapes block except for
// our own marks. The list of our own marks was written by hand and left out
// unknown's "□", so a palette row for an agent in that state read
// "Window:  lint" with no mark.
func TestEveryStateMarkSurvivesTheTitleSanitiser(t *testing.T) {
	for _, state := range agentMarkStates {
		label := sessionPaletteLabel("Window: ", "lint", state, false)
		if got := printableTitle(label); got != label {
			t.Errorf("%s: the palette row %q is drawn as %q", state, label, got)
		}
	}
	// Foreign ornaments in the same block are still stripped.
	if got := printableTitle("◆ build ◇"); got != "build" {
		t.Errorf("printableTitle kept a foreign ornament: %q", got)
	}
}

// TestAskedPaneNeedsYou: a pane with an ask-human question open is waiting
// for the person, so it reads as needs_input on the rail, the title bar and
// the palette, where "@n" finds it. Asking moves no agent state, and the
// pane had no mark, no agents-section row and no "@n" match while the
// question sat in the Inbox.
func TestAskedPaneNeedsYou(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{askItem("1", "here", "w-1", "yes", "no")}})

	tree := m.BuildSessionTree()
	var asked, other *struct{ state, message string }
	for _, s := range tree.Sessions {
		for _, w := range s.Children {
			v := &struct{ state, message string }{w.AgentState, w.Message}
			switch w.ID {
			case "w-1":
				asked = v
			case "w-2":
				other = v
			}
		}
	}
	if asked == nil || asked.state != "needs_input" {
		t.Fatalf("the asking pane reads as %+v, want needs_input", asked)
	}
	if asked.message != "Deploy to staging?" {
		t.Errorf("the asking pane's rail note is %q, want the question", asked.message)
	}
	if other == nil || other.state != "" {
		t.Errorf("a pane that asked nothing reads as %+v", other)
	}

	if got := m.windowMarkState(m.Windows[0]); got != "needs_input" {
		t.Errorf("the asking pane's title bar draws %q's mark, want needs_input's", got)
	}

	var found bool
	for _, it := range FilterCommandPalette(getSessionPaletteItems(m), "@n") {
		if strings.Contains(it.Name, "w-1") {
			found = true
		}
		if strings.Contains(it.Name, "w-2") {
			t.Errorf("@n matched a pane that asked nothing: %q", it.Name)
		}
	}
	if !found {
		t.Error("@n does not find the pane with a question open")
	}

	// Answered or dismissed, the pane is itself again.
	m.applyInboxSnapshot(InboxSnapshotMsg{})
	if got := m.windowMarkState(m.Windows[0]); got != "" {
		t.Errorf("with the question gone the pane still reads as %q", got)
	}
}
