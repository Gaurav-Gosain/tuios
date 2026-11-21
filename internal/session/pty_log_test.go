package session

import (
	"testing"
)

// TestTheRawLogIsOffUnlessAskedFor. It is a debugging aid, and a pane that
// wrote every byte it produced to disk by default would be a worse thing than
// the bug it is for.
func TestTheRawLogIsOffUnlessAskedFor(t *testing.T) {
	t.Setenv("TUIOS_PTY_LOG", "")
	if l := newPTYLogger("pane-1"); l != nil {
		t.Error("a log was opened with nothing asking for one")
	}
}
