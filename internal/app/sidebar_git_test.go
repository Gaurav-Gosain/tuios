package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
)

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
