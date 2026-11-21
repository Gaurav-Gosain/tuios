package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TestNavigatorFollowsStripOrder: in the tiled scrolling layout the pane
// navigator lists a workspace's panes in the order the strip draws them, not in
// the order they were created in. Moving a column changes the strip and nothing
// about creation order, so a navigator that ignored the strip showed panes out
// of order with the screen.
func TestNavigatorFollowsStripOrder(t *testing.T) {
	m, _ := sectionsTestOS(t, 120, 30)
	m.UseScrollingLayout, m.AutoTiling = true, true
	m.SessionName = "api"

	// Created a, b, c, d; the strip holds them c, a, (d over b stacked).
	ids := map[string]int{"aaaaaaaa1111": 1, "bbbbbbbb2222": 2, "cccccccc3333": 3, "dddddddd4444": 4}
	m.BSPIDToWindowID = map[int]string{}
	for w, n := range ids {
		m.BSPIDToWindowID[n] = w
	}
	m.WorkspaceScrollingLayouts = map[int]*layout.ScrollingLayout{
		1: {Columns: []layout.ScrollColumn{{WindowIDs: []int{3}}, {WindowIDs: []int{1}}, {WindowIDs: []int{4, 2}}}},
	}
	m.Windows = []*terminal.Window{
		{ID: "aaaaaaaa1111", CustomName: "a", Workspace: 1},
		{ID: "bbbbbbbb2222", CustomName: "b", Workspace: 1},
		{ID: "cccccccc3333", CustomName: "c", Workspace: 1},
		{ID: "dddddddd4444", CustomName: "d", Workspace: 1},
	}

	s := m.navigatorCurrentSession()
	var got []string
	for _, p := range s.Panes {
		got = append(got, p.ID[:1])
	}
	if want := "cadb"; strings.Join(got, "") != want {
		t.Errorf("navigator current session pane order = %v, want %q", got, want)
	}

	// Outside scrolling layout the creation order stands.
	m.UseScrollingLayout = false
	s = m.navigatorCurrentSession()
	got = got[:0]
	for _, p := range s.Panes {
		got = append(got, p.ID[:1])
	}
	if want := "abcd"; strings.Join(got, "") != want {
		t.Errorf("navigator current session pane order outside scrolling = %v, want %q", got, want)
	}
}
