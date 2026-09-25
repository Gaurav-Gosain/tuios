package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// sidebarMultiSessionOS builds an OS attached to "main" with agent-flagged
// windows and the sidebar on, plus a synthetic three-session tree the way a
// daemon-backed client would see one. The tree order is the daemon's creation
// order: main, scratch, deploy.
func sidebarMultiSessionOS(t *testing.T, w, h int) (*OS, sessiontree.Tree) {
	t.Helper()
	m := newNarrowOS(t, w, h)
	m.CurrentWorkspace = 1
	m.SessionName = "main"
	m.Windows = []*terminal.Window{
		{ID: "aaaaaaaa1111", CustomName: "claude", Width: 40, Height: 20, Workspace: 1, AgentState: "working"},
		{ID: "bbbbbbbb2222", CustomName: "tests", Width: 40, Height: 20, Workspace: 1, AgentState: "needs_input"},
		{ID: "cccccccc3333", CustomName: "logs", Width: 40, Height: 20, Workspace: 1},
	}
	m.FocusedWindow = 0
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	m.Settings = config.Global
	m.SidebarOrder = nil

	tree := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "claude", AgentState: "working", Focused: true},
			{ID: "bbbbbbbb2222", Title: "tests", AgentState: "needs_input"},
			{ID: "cccccccc3333", Title: "logs"},
		}},
		{Name: "scratch", WindowCount: 2},
		{Name: "deploy", WindowCount: 1},
	})
	return m, tree
}

// TestSidebarSanitizesTitles checks a title carrying nerd-font private-use
// icons and control characters reaches the rail laundered.
func TestSidebarSanitizesTitles(t *testing.T) {
	if got := printableTitle(" nvim \x1b]0;x\x07"); got != "nvim ]0;x" {
		// The escape byte and the bell go; printable remnants of a title
		// sequence stay (they are the shell's bug to fix, not tofu).
		t.Errorf("printableTitle = %q", got)
	}
	overlay.SetASCII(true)
	t.Cleanup(func() { overlay.SetASCII(false) })
	if got := printableTitle("café ▲"); got != "caf" {
		t.Errorf("ASCII printableTitle = %q, want %q", got, "caf")
	}
}
