package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// TestDispatchRecordsTheActionForACrashReport pins the one line that gives a
// crash report its closest honest answer to "what were you doing".
//
// Dispatch is the single choke point every keybinding, prefix chord and context
// menu row passes through, which is why the recording lives there rather than
// in each of the three. Without it the report says "none recorded" for a
// session the user spent an hour in.
func TestDispatchRecordsTheActionForACrashReport(t *testing.T) {
	d := NewActionDispatcher()
	d.Register("split_vertical", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) { return o, nil })
	d.Register("focus_next", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) { return o, nil })

	o := &app.OS{}
	d.Dispatch("split_vertical", tea.KeyPressMsg{}, o)
	d.Dispatch("focus_next", tea.KeyPressMsg{}, o)
	// An action nobody registered runs nothing, so it records nothing.
	d.Dispatch("no_such_action", tea.KeyPressMsg{}, o)

	got := o.RecentActions()
	if len(got) != 2 || got[0] != "split_vertical" || got[1] != "focus_next" {
		t.Fatalf("recent actions = %v, want the two dispatched ones in order", got)
	}
}
