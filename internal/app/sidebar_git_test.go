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
