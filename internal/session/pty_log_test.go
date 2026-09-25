package session

import (
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
