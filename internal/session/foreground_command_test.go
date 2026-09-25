package session

import "testing"

// TestForegroundCommandSurvivesClientSync guards the field against the merge
// that wipes anything a client omits: no client ever sets it, so the daemon's
// value has to carry over, and has to clear when the command exits.
func TestForegroundCommandSurvivesClientSync(t *testing.T) {
	canonical := &SessionState{Windows: []WindowState{{ID: "w1", ForegroundCmd: "nvim"}}}
	incoming := &SessionState{Windows: []WindowState{{ID: "w1"}}}

	retainDaemonExclusive(incoming, canonical)
	if got := incoming.Windows[0].ForegroundCmd; got != "nvim" {
		t.Errorf("a client sync wiped the command: got %q", got)
	}

	canonical.Windows[0].ForegroundCmd = "" // nvim exited
	incoming = &SessionState{Windows: []WindowState{{ID: "w1"}}}
	retainDaemonExclusive(incoming, canonical)
	if got := incoming.Windows[0].ForegroundCmd; got != "" {
		t.Errorf("an exited command stuck at %q", got)
	}
}
