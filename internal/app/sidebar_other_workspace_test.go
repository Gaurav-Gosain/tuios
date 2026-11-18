package app

import (
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// fgSeq is the escape sequence a foreground color renders as, so a row can be
// checked for the color it was actually drawn in rather than for a color name.
func fgSeq(c color.Color) string {
	rendered := lipgloss.NewStyle().Foreground(c).Render("x")
	return rendered[:strings.Index(rendered, "x")]
}

// TestUnknownWorkspaceTagsNothing: a pane whose workspace is not known, and a
// session whose current workspace is not known, must both go untagged. An older
// daemon sends neither field, and a rail that tagged every row on that listing
// would be confidently wrong about all of them.
func TestUnknownWorkspaceTagsNothing(t *testing.T) {
	m, _ := sectionsTestOS(t, 120, 30)
	tree := sessiontree.Build([]sessiontree.SessionInput{
		// No CurrentWorkspace and no per-pane workspace: an older daemon's reply.
		{Name: "api", Windows: []sessiontree.WindowInput{
			{ID: "dddddddd4444", Title: "server"},
			{ID: "eeeeeeee5555", Title: "worker"},
		}},
	})
	for _, e := range m.sidebarTerminals(tree.Sessions, "api") {
		if e.Tag != "" {
			t.Errorf("pane %q was tagged %q off a listing that says nothing about workspaces", e.WindowID, e.Tag)
		}
	}
}

func TestZeroBasedWorkspaceLabels(t *testing.T) {
	m, _ := sectionsTestOS(t, 120, 30)
	m.Settings.WorkspaceIndexOffset = 1
	tree := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "api", CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "worker", Workspace: 2},
		}},
	})
	if tag := m.sidebarTerminals(tree.Sessions, "api")[0].Tag; tag != "w1" {
		t.Errorf("other session's workspace tag = %q, want w1", tag)
	}
	m.BeginRenameWorkspace(1)
	if title := m.RenameDialogTitle(); title != "rename workspace 0" {
		t.Errorf("rename dialog title = %q, want rename workspace 0", title)
	}
	for _, item := range m.sidebarSessionSettingItems() {
		if item.Label != "Left workspaces" {
			continue
		}
		item.adjust(m, 1)
		if got := item.value(m); got != "0" || item.Options[1] != "0" {
			t.Errorf("first workspace displays as %q (option %q), want 0", got, item.Options[1])
		}
		if slot := m.sidebarSessionConfig(sidebarLeft).SessionWorkspace; slot != 1 {
			t.Errorf("saved workspace slot = %d, want 1", slot)
		}
		return
	}
	t.Fatal("missing left workspace setting")
}

// TestTerminalsFollowStripOrder: in the tiled scrolling layout the terminals
// section lists a workspace's panes in the order the strip draws them, not in
// the order they were created. Moving a column changes the strip and nothing
// about creation order, so a rail that ignored the strip looked shuffled.
func TestTerminalsFollowStripOrder(t *testing.T) {
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
	tree := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "api", IsCurrent: true, CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "a", Workspace: 1},
			{ID: "bbbbbbbb2222", Title: "b", Workspace: 1},
			{ID: "cccccccc3333", Title: "c", Workspace: 1},
			{ID: "dddddddd4444", Title: "d", Workspace: 1},
		}},
	})
	var got []string
	for _, e := range m.sidebarTerminals(tree.Sessions, "api") {
		got = append(got, e.WindowID[:1])
	}
	if want := "cadb"; strings.Join(got, "") != want {
		t.Errorf("terminals order = %v, want %q", got, want)
	}

	// Moving a column in the strip or disabling scrolling layout moves the rail
	// signature so cached frames do not serve stale pane ordering.
	sigScrolling := m.sidebarSignature()
	m.WorkspaceScrollingLayouts[1] = &layout.ScrollingLayout{
		Columns: []layout.ScrollColumn{{WindowIDs: []int{1}}, {WindowIDs: []int{3}}, {WindowIDs: []int{4, 2}}},
	}
	if sigRearranged := m.sidebarSignature(); sigRearranged == sigScrolling {
		t.Error("sidebarSignature did not move when strip columns rearranged")
	}

	// Outside the scrolling layout the creation order stands.
	m.UseScrollingLayout = false
	if sigDisabled := m.sidebarSignature(); sigDisabled == sigScrolling {
		t.Error("sidebarSignature did not move when scrolling layout disabled")
	}
	got = got[:0]
	for _, e := range m.sidebarTerminals(tree.Sessions, "api") {
		got = append(got, e.WindowID[:1])
	}
	if want := "abcd"; strings.Join(got, "") != want {
		t.Errorf("terminals order outside scrolling = %v, want %q", got, want)
	}
}
