package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// TestABranchWithNoUpstreamCarriesNoFigure. Zero and zero is what a branch in
// step reports, so a branch that follows nothing has to be distinguishable from
// one that is level. Otherwise the rail says "in step" about a branch that has
// nothing to be in step with.
func TestABranchWithNoUpstreamCarriesNoFigure(t *testing.T) {
	m := &OS{Settings: config.Global}
	m.gitView = gitView{
		Dir: "/src/tuios", Found: true,
		State: gitstate.State{Repo: "tuios", Branch: "scratch"},
	}
	rows := m.gitRows()
	if len(rows) != 2 {
		t.Fatalf("the section drew %d rows, want 2", len(rows))
	}
	if rows[1].Right != "" {
		t.Errorf("a branch with no upstream carries %q", rows[1].Right)
	}
}

// TestAReadingForAnAbandonedDirectoryIsDropped. A reading is taken on a
// goroutine, so it can land after the focus has moved to a pane somewhere else.
// Applying it would put another directory's branch under this pane's name.
func TestAReadingForAnAbandonedDirectoryIsDropped(t *testing.T) {
	m := &OS{Settings: config.Global}
	m.gitView.asked = "/src/tuios"

	m.ApplyGitState(GitStateMsg{
		Dir:   "/somewhere/else",
		Found: true,
		State: gitstate.State{Repo: "other", Branch: "main"},
	})
	if m.gitView.Found {
		t.Errorf("a reading for a directory the focus had left was applied: %+v", m.gitView.State)
	}

	// The positive half, so the guard is not just refusing everything.
	m.ApplyGitState(GitStateMsg{
		Dir:   "/src/tuios",
		Found: true,
		State: gitstate.State{Repo: "tuios", Branch: "main"},
	})
	if !m.gitView.Found || m.gitView.State.Repo != "tuios" {
		t.Errorf("the reading for the directory in hand was not applied: %+v", m.gitView)
	}
}

// TestTheBranchRowGivesWayBeforeTheFigure is the truncation rule. A branch cut
// to "feature/long-nam…" still says which branch. A figure cut from "↑12" to
// "↑1" says something false, so it is never the thing that shrinks.
func TestTheBranchRowGivesWayBeforeTheFigure(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	m.gitView = gitView{
		Dir: "/src/tuios", Found: true,
		State: gitstate.State{
			Repo:     "tuios",
			Branch:   "feature/a-really-quite-long-branch-name",
			Upstream: "refs/remotes/origin/main", Ahead: 12, Behind: 34,
		},
	}
	rows := m.gitRows()
	if len(rows) != 2 {
		t.Fatalf("the section drew %d rows, want 2", len(rows))
	}

	const cw = 28
	out := stripANSIForTrace(m.sidebarGitRow(rows[1], cw, theme.UI(), sidebarRowState{}))
	if !strings.Contains(out, "↑12 ↓34") {
		t.Errorf("the figure did not survive a narrow rail: %q", out)
	}
	if !strings.Contains(out, "…") {
		t.Errorf("the branch was not the thing that gave way: %q", out)
	}
}
