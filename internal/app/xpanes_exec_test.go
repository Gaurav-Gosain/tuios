package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// SetMultifocus makes the set exactly the named windows, and names a window
// the client does not know yet in a way xpanes can retry on.
func TestSetMultifocusExec(t *testing.T) {
	m := scratchOS(t, false)
	m.Windows = []*terminal.Window{
		{ID: "w-a", Workspace: 1, CustomName: "a"},
		{ID: "w-b", Workspace: 1, CustomName: "b"},
		{ID: "w-c", Workspace: 1, CustomName: "c"},
	}
	m.MultifocusSet = map[string]bool{"w-c": true}
	if err := m.SetMultifocusExec([]string{"w-a", "b"}); err != nil {
		t.Fatal(err)
	}
	if len(m.MultifocusSet) != 2 || !m.MultifocusSet["w-a"] || !m.MultifocusSet["w-b"] {
		t.Fatalf("set = %v, want w-a and w-b only", m.MultifocusSet)
	}
	err := m.SetMultifocusExec([]string{"w-a", "w-new"})
	if err == nil || !strings.Contains(err.Error(), ErrWindowNotHereYet) {
		t.Fatalf("an unknown window: %v", err)
	}
	if len(m.MultifocusSet) != 2 {
		t.Fatalf("a failed call changed the set: %v", m.MultifocusSet)
	}
	if err := m.SetMultifocusExec(nil); err != nil || m.MultifocusSet != nil {
		t.Fatalf("clear: %v, %v", err, m.MultifocusSet)
	}
}
