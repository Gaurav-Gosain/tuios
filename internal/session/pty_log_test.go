package session

import (
	"os"
	"path/filepath"
	"testing"
)

// The raw output log exists to make a live rendering fault reproducible. A
// capture taken afterwards shows the grid as it is now, which says nothing
// about the moment it went wrong, and the grid is downstream of the bytes
// anyway. These pin the two things a person relying on it needs: that it is
// off unless asked for, and that what lands in the file is what was written.

// TestTheRawLogIsOffUnlessAskedFor. It is a debugging aid, and a pane that
// wrote every byte it produced to disk by default would be a worse thing than
// the bug it is for.
func TestTheRawLogIsOffUnlessAskedFor(t *testing.T) {
	t.Setenv("TUIOS_PTY_LOG", "")
	if l := newPTYLogger("pane-1"); l != nil {
		t.Error("a log was opened with nothing asking for one")
	}
}

// TestTheRawLogKeepsTheBytesAsWritten, escape sequences included. Anything
// filtered or decoded on the way in is something the replay cannot see.
//
// Negative control: writing the bytes through a text path that drops control
// characters fails here.
func TestTheRawLogKeepsTheBytesAsWritten(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TUIOS_PTY_LOG", dir)

	l := newPTYLogger("pane-1")
	if l == nil {
		t.Fatal("no log was opened with a directory named")
	}
	// A cursor move, an erase and some text, which is the shape of the stream
	// this was added to chase.
	want := "\x1b[2;5H\x1b[Khello\x1b[0m"
	l.Write([]byte(want))
	l.Close()

	got, err := os.ReadFile(filepath.Join(dir, "pane-1.raw"))
	if err != nil {
		t.Fatalf("the log was not written: %v", err)
	}
	if string(got) != want {
		t.Errorf("the log holds %q, want %q", got, want)
	}
}

// TestTheRawLogAppends, so a pane that is written to twice keeps both, and a
// reconnect does not lose what came before it.
func TestTheRawLogAppends(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TUIOS_PTY_LOG", dir)

	first := newPTYLogger("pane-2")
	first.Write([]byte("one"))
	first.Close()

	second := newPTYLogger("pane-2")
	second.Write([]byte("two"))
	second.Close()

	got, _ := os.ReadFile(filepath.Join(dir, "pane-2.raw"))
	if string(got) != "onetwo" {
		t.Errorf("the log holds %q, want both writes", got)
	}
}
