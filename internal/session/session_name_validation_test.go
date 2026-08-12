package session

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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

// TestOrdinarySessionNamesStillCreate is the no-op guard: validation must not
// start rejecting names people already use.
func TestOrdinarySessionNamesStillCreate(t *testing.T) {
	tmpDir := t.TempDir()
	defer useResurrectionDir(tmpDir)()

	m := NewManager()
	defer m.Shutdown()

	for _, name := range []string{"work", "session-0", "my.project", "a b", "dash-and_underscore", "café", ""} {
		if _, err := m.CreateSession(name, &SessionConfig{}, 80, 24); err != nil {
			t.Errorf("CreateSession(%q) was rejected: %v", name, err)
		}
	}
}

// TestFinalSaveErrorIsSurfacedRatherThanDiscarded drives a session's last save
// against a state directory it cannot write. That save used to be
// `_ = SaveSessionForResurrection(...)`, so a session that failed to persist
// looked exactly like one that had, right up until it did not come back.
func TestFinalSaveErrorIsSurfacedRatherThanDiscarded(t *testing.T) {
	// A regular file where the state directory should be: MkdirAll under it
	// fails, so every save fails.
	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0600); err != nil {
		t.Fatalf("seeding the unwritable state dir: %v", err)
	}
	defer useResurrectionDir(blocked)()

	if err := SaveSessionForResurrection(&SessionState{Name: "doomed"}); err == nil {
		t.Fatal("precondition: the save into an unwritable directory was expected to fail")
	}

	var buf bytes.Buffer
	SetDebugOutput(&buf)
	SetDebugLevel(DebugErrors)
	t.Cleanup(func() { SetDebugLevel(DebugOff) })

	sess, err := NewSession("doomed", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sess.Stop()

	if !strings.Contains(buf.String(), "doomed") {
		t.Errorf("a session that could not be saved said nothing about it; log was:\n%s", buf.String())
	}
}
