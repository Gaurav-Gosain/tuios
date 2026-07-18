package session

import (
	"os"
	"testing"
)

// TestMain redirects resurrection state for the whole test binary.
//
// The resurrection state directory is derived from xdg.StateHome, which is
// resolved at package init, so a per-test t.Setenv cannot redirect it. Without
// this, every test that creates a session persists a real state file into the
// developer's ~/.local/state/tuios/sessions, which both pollutes their state
// directory and leaves phantom sessions for the next real daemon start to
// resurrect. Tests that need to inspect state files still set
// resurrectionDirOverride themselves; this only provides a safe default.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "tuios-session-test-state")
	if err != nil {
		panic(err)
	}
	resurrectionDirOverride = tmp

	code := m.Run()

	_ = os.RemoveAll(tmp)
	os.Exit(code)
}

// useResurrectionDir points resurrection state at dir and returns a function
// that restores the previous value. Restoring the previous value rather than
// clearing it keeps the TestMain default in place, so a later test cannot fall
// back to the developer's real state directory.
func useResurrectionDir(dir string) func() {
	prev := resurrectionDirOverride
	resurrectionDirOverride = dir
	return func() { resurrectionDirOverride = prev }
}
