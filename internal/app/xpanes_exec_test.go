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
	m.CurrentWorkspace = 1
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
	if err == nil || !strings.Contains(err.Error(), ErrNotHereYet) {
		t.Fatalf("an unknown window: %v", err)
	}
	if len(m.MultifocusSet) != 2 {
		t.Fatalf("a failed call changed the set: %v", m.MultifocusSet)
	}
	if err := m.SetMultifocusExec(nil); err != nil || m.MultifocusSet != nil {
		t.Fatalf("clear: %v, %v", err, m.MultifocusSet)
	}
}

// SetMultifocus refuses a window on another workspace or minimized: keys do
// not go there, and the person cannot see the window.
func TestSetMultifocusRefusesWindowsOffScreen(t *testing.T) {
	m := scratchOS(t, false)
	m.CurrentWorkspace = 1
	m.Windows = []*terminal.Window{
		{ID: "w-a", Workspace: 1},
		{ID: "w-far", Workspace: 2},
		{ID: "w-min", Workspace: 1, Minimized: true},
	}
	for _, id := range []string{"w-far", "w-min"} {
		if err := m.SetMultifocusExec([]string{"w-a", id}); err == nil {
			t.Errorf("%s joined the multifocus set", id)
		}
		if m.MultifocusSet != nil {
			t.Fatalf("a refused call changed the set: %v", m.MultifocusSet)
		}
	}
}

// ArrangePanes names its workspace. When that workspace is not showing, the
// layout of the workspace that is showing stays as it is.
func TestArrangePanesOnlyOnTheNamedWorkspace(t *testing.T) {
	m := scratchOS(t, false)
	m.AutoTiling = true
	m.UseBSPLayout = true
	m.CurrentWorkspace = 1
	m.Windows = []*terminal.Window{{ID: "w-a", Workspace: 1}, {ID: "w-b", Workspace: 1}, {ID: "w-c", Workspace: 1}}
	tree := m.GetOrCreateBSPTree()
	before := tree.Root
	err := m.ArrangePanesExec("even-vertical", []string{"2", "w-a"})
	if err == nil || !strings.Contains(err.Error(), ErrNotHereYet) {
		t.Fatalf("err = %v, want a retry on a workspace that is not showing", err)
	}
	if tree.Root != before {
		t.Fatal("ArrangePanes for workspace 2 changed the layout of workspace 1")
	}
	if err := m.ArrangePanesExec("even-vertical", []string{"1", "w-c"}); err != nil {
		t.Fatal(err)
	}
	if tree.WindowCount() != 3 {
		t.Fatalf("the tree holds %d windows, want 3", tree.WindowCount())
	}
}
