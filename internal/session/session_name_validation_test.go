package session

import (
	"testing"
)

// A session's name is also the name of its state file. A name carrying a path
// separator used to be accepted, run perfectly, and never persist: the write
// went to a directory that does not exist and the error was discarded on both
// save paths. These pin both halves of that.

// TestSessionNameWithAPathSeparatorIsRejectedAtCreation is the important one:
// the failure has to surface where the user chose the name, not silently at a
// save nobody is watching.
func TestSessionNameWithAPathSeparatorIsRejectedAtCreation(t *testing.T) {
	tmpDir := t.TempDir()
	defer useResurrectionDir(tmpDir)()

	m := NewManager()
	defer m.Shutdown()

	for _, name := range []string{"a/b", "../escape", "..", ".", "sub/dir/sess", " leading", "trailing ", "nul\x00name", "bell\x07"} {
		t.Run(name, func(t *testing.T) {
			sess, err := m.CreateSession(name, &SessionConfig{}, 80, 24)
			if err == nil {
				t.Fatalf("CreateSession(%q) succeeded; it would run and never save", name)
			}
			if sess != nil {
				t.Errorf("CreateSession(%q) returned a session alongside its error", name)
			}
			if m.GetSession(name) != nil {
				t.Errorf("the rejected session %q was registered anyway", name)
			}
		})
	}
}
