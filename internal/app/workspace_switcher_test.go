package app

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// wsSwitcherOS builds an OS with panes spread over workspaces 1, 2 and 4.
func wsSwitcherOS() *OS {
	m := &OS{
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		Windows: []*terminal.Window{
			{ID: "a", Workspace: 1},
			{ID: "b", Workspace: 2},
			{ID: "c", Workspace: 2},
			{ID: "d", Workspace: 4},
		},
	}
	return m
}

// TestWorkspaceSwitcherListsOccupiedNamedAndCurrent checks which workspaces earn
// a row: the ones holding a pane, the one you are on, and any you have named.
func TestWorkspaceSwitcherListsOccupiedNamedAndCurrent(t *testing.T) {
	m := wsSwitcherOS()

	got := m.buildWorkspaceItems()
	var nums []int
	for _, w := range got {
		nums = append(nums, w.Number)
	}
	if len(nums) != 3 || nums[0] != 1 || nums[1] != 2 || nums[2] != 4 {
		t.Fatalf("workspaces = %v, want [1 2 4]", nums)
	}
	if got[1].Panes != 2 {
		t.Errorf("workspace 2 pane count = %d, want 2", got[1].Panes)
	}
	if !got[0].IsCurrent || got[1].IsCurrent {
		t.Errorf("current flag is on the wrong row: %+v", got)
	}

	// Naming an empty workspace is what says it is wanted, so it earns a row.
	m.adoptSessionLabels(&session.SessionState{WorkspaceNames: map[int]string{7: "review"}})
	nums = nil
	for _, w := range m.buildWorkspaceItems() {
		nums = append(nums, w.Number)
	}
	if len(nums) != 4 || nums[3] != 7 {
		t.Errorf("workspaces after naming an empty one = %v, want [1 2 4 7]", nums)
	}
}

// TestUnnamedWorkspacePresentsAsItsNumber pins the no-op case: a workspace
// nobody has named must show its number and nothing else, which is exactly what
// it showed before names existed.
func TestUnnamedWorkspacePresentsAsItsNumber(t *testing.T) {
	m := wsSwitcherOS()
	m.OpenWorkspaceSwitcher()

	for _, w := range m.WorkspaceSwitcherItems {
		if got := w.Label(); got != strconv.Itoa(w.Number) {
			t.Errorf("unnamed workspace %d labelled %q, want %q", w.Number, got, strconv.Itoa(w.Number))
		}
	}

	out, _, _ := m.renderWorkspaceSwitcher()
	t.Logf("\n%s", out)
	for _, n := range []string{"1", "2", "4"} {
		if !strings.Contains(out, n) {
			t.Errorf("frame is missing workspace %s", n)
		}
	}
	if !strings.Contains(out, "2 panes") || !strings.Contains(out, "1 pane") {
		t.Errorf("frame is missing pane counts:\n%s", out)
	}
}

// TestWorkspaceSwitcherOpensOnTheCurrentWorkspace checks Enter is a no-op right
// after opening, so the arrows move away from a known place.
func TestWorkspaceSwitcherOpensOnTheCurrentWorkspace(t *testing.T) {
	m := wsSwitcherOS()
	m.CurrentWorkspace = 4
	m.OpenWorkspaceSwitcher()

	target, ok := m.WorkspaceSwitcherTarget(m.WorkspaceSwitcherSelected)
	if !ok || target.Number != 4 {
		t.Errorf("selection opened on %+v, want workspace 4", target)
	}
}

// TestWorkspaceSwitcherFilterTargetsTheFilteredRow is the off-by-one guard:
// with a query typed, row 0 is the first row on screen, not the first workspace.
func TestWorkspaceSwitcherFilterTargetsTheFilteredRow(t *testing.T) {
	m := wsSwitcherOS()
	m.adoptSessionLabels(&session.SessionState{WorkspaceNames: map[int]string{4: "review"}})
	m.OpenWorkspaceSwitcher()

	m.WorkspaceSwitcherQuery = "rev"
	m.WorkspaceSwitcherSelected = 0

	filtered := FilterWorkspaceItems(m.WorkspaceSwitcherItems, m.WorkspaceSwitcherQuery)
	if len(filtered) != 1 {
		t.Fatalf("filtered count = %d, want 1", len(filtered))
	}
	target, ok := m.WorkspaceSwitcherTarget(0)
	if !ok || target.Number != 4 {
		t.Errorf("target = %+v, want workspace 4: the row was resolved against the unfiltered list", target)
	}

	// The number stays the way to reach a workspace whatever it is called.
	m.WorkspaceSwitcherQuery = "4"
	byNumber, ok := m.WorkspaceSwitcherTarget(0)
	if !ok || byNumber.Number != 4 {
		t.Errorf("searching by number found %+v, want workspace 4", byNumber)
	}

	m.WorkspaceSwitcherQuery = "zzz"
	if _, ok := m.WorkspaceSwitcherTarget(0); ok {
		t.Error("an empty filtered list still resolved a target")
	}
}

// TestWorkspaceSwitcherHitRectsMatchDrawnRows checks the recorded rects against
// the frame they were recorded from, at three widths.
func TestWorkspaceSwitcherHitRectsMatchDrawnRows(t *testing.T) {
	for _, w := range []int{48, 90, 160} {
		t.Run(strconv.Itoa(w), func(t *testing.T) {
			m := wsSwitcherOS()
			m.adoptSessionLabels(&session.SessionState{WorkspaceNames: map[int]string{2: "review"}})
			m.OpenWorkspaceSwitcher()
			m.Width, m.Height = w, 30

			out, _, rows := m.renderWorkspaceSwitcher()
			if len(rows) != 3 {
				t.Fatalf("rows = %d, want 3", len(rows))
			}
			lines := strings.Split(out, "\n")
			want := []string{"1", "review", "4"}

			for _, row := range rows {
				if row.Rect.Y0 < 0 || row.Rect.Y0 >= len(lines) {
					t.Fatalf("row %d rect Y0=%d is outside the %d-line frame", row.Idx, row.Rect.Y0, len(lines))
				}
				if line := lines[row.Rect.Y0]; !strings.Contains(line, want[row.Idx]) {
					t.Errorf("width %d: rect for row %d points at line %d %q, want the row holding %q",
						w, row.Idx, row.Rect.Y0, line, want[row.Idx])
				}
			}
		})
	}
}
