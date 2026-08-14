package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDiagnoseCountsSavedSessions pins that the diagnosis knows what is on disk.
// Without the count the message has to guess, and it guessed wrong in the way
// that matters most: it told a user whose sessions were all saved that none
// existed.
func TestDiagnoseCountsSavedSessions(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	stateDir := t.TempDir()
	t.Cleanup(useResurrectionDir(stateDir))

	d := DiagnoseDaemon()
	if d.Running() {
		t.Fatal("a daemon is running in an isolated runtime dir")
	}
	if d.Restorable != 0 {
		t.Fatalf("Restorable = %d with an empty state dir, want 0", d.Restorable)
	}
	if !strings.Contains(d.Explain(), "tuios new") {
		t.Errorf("with nothing saved the fix should be to create a session:\n%s", d.Explain())
	}

	for _, name := range []string{"work", "notes"} {
		path := filepath.Join(stateDir, name+".json")
		if err := os.WriteFile(path, []byte(`{"name":"`+name+`"}`), 0600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	d = DiagnoseDaemon()
	if d.Restorable != 2 {
		t.Fatalf("Restorable = %d with two saved sessions, want 2", d.Restorable)
	}
	msg := d.Explain()
	if !strings.Contains(msg, "2 saved sessions") || !strings.Contains(msg, "tuios attach") {
		t.Errorf("message does not name the saved sessions or the command that reopens them:\n%s", msg)
	}
}
