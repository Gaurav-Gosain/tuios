package app

import (
	"image/color"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// sessionColorOS is a rail attached to "main" beside two sessions that carry
// panes of their own, which is the only shape the colours exist for: more than
// one session on screen at once.
func sessionColorOS(t *testing.T, w, h int) (*OS, sessiontree.Tree) {
	t.Helper()
	m := newNarrowOS(t, w, h)
	m.CurrentWorkspace = 1
	m.SessionName = "main"
	m.Windows = []*terminal.Window{
		{ID: "aaaaaaaa1111", CustomName: "nvim", Width: 40, Height: 20, Workspace: 1},
		{ID: "bbbbbbbb2222", CustomName: "refactor", Width: 40, Height: 20, Workspace: 1, AgentState: "working"},
	}
	m.FocusedWindow = 0
	m.DaemonClient = &session.TUIClient{}
	m.IsDaemonSession = true
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	m.Settings = config.Global
	m.SidebarOrder = nil
	return m, sessionColorTree()
}

func sessionColorTree() sessiontree.Tree {
	return sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "nvim", Focused: true, Workspace: 1},
			{ID: "bbbbbbbb2222", Title: "refactor", AgentState: "working", Workspace: 1},
		}},
		{Name: "api", CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
			{ID: "dddddddd4444", Title: "server", AgentState: "working", Workspace: 1},
		}},
		{Name: "docs", CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
			{ID: "ffffffff6666", Title: "site", AgentState: "idle", Workspace: 1},
		}},
	})
}

// withSessionColors pins the config key for one test and puts it back.
func withSessionColors(t *testing.T, on bool) {
	t.Helper()
	prev := config.Global.SessionColors
	config.Global.SessionColors = on
	t.Cleanup(func() { config.Global.SessionColors = prev })
}

// TestSessionAccentVocabulary pins what a session accent may be written as. The
// daemon records the string verbatim and has never read it, so anything already
// on disk has to keep meaning what it meant, and anything unreadable has to read
// as unset rather than as a colour nobody chose.
func TestSessionAccentVocabulary(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Accent
		ok   bool
	}{
		{"cyan", SlotAccent(13), true},
		{"CYAN", SlotAccent(13), true},
		{"bright cyan", SlotAccent(6), true},
		{"bright-cyan", SlotAccent(6), true},
		{"Bright_Cyan", SlotAccent(6), true},
		{"magenta", SlotAccent(12), true},
		{"purple", SlotAccent(12), true},
		{"#89b4fa", RGBAccent(color.RGBA{R: 0x89, G: 0xb4, B: 0xfa, A: 0xff}), true},
		{"#f0a", RGBAccent(color.RGBA{R: 0xff, G: 0x00, B: 0xaa, A: 0xff}), true},
		{"", Accent{}, false},
		{"   ", Accent{}, false},
		{"chartreuse", Accent{}, false},
		{"#12345", Accent{}, false},
	} {
		got, ok := ParseAccent(tc.in)
		if ok != tc.ok || (tc.ok && got != tc.want) {
			t.Errorf("ParseAccent(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
