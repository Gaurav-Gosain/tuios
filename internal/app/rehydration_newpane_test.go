package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestRehydrationAdoptsAPaneCreatedElsewhere is the seventh route: a pane this
// client did not ask for, created while it was attached, arriving on a state
// push. It reaches the same prime-from-snapshot path a workspace switch uses,
// but on an emulator that has never held anything, and the pane already has
// output by the time this client hears about it.
//
// It is its own test rather than a row of the matrix because the pane under
// examination is one the matrix's shapes never arranged: it comes into
// existence part way through.
func TestRehydrationAdoptsAPaneCreatedElsewhere(t *testing.T) {
	r := newRig(t, 1)

	// The push the daemon sends every attached client is what materializes the
	// pane, exactly as cmd/tuios wires it: the handler hands the state to the
	// UI, which applies it.
	pushed := make(chan *session.SessionState, 8)
	r.client.OnStateSync(func(state *session.SessionState, _, _ string) {
		select {
		case pushed <- state:
		default:
		}
	})

	before := len(r.m.Windows)
	if err := r.ctl.SendIntent("NewWindow"); err != nil {
		t.Fatalf("ask for a window: %v", err)
	}

	var ptyID string
	rigWaitUntil(t, "the new pane to arrive and be adopted", func() bool {
		select {
		case state := <-pushed:
			if err := r.m.ApplyStateSync(state); err != nil {
				t.Fatalf("apply state sync: %v", err)
			}
		default:
			return false
		}
		if len(r.m.Windows) <= before {
			return false
		}
		for _, w := range r.m.Windows {
			if w.PTYID != "" && w.PTYID != r.win(0).PTYID {
				ptyID = w.PTYID
			}
		}
		return ptyID != ""
	})

	r.feedPTY(ptyID, `printf 'ADOPTED-PANE\n'`, "ADOPTED-PANE")
	r.settle()
	r.converge(ptyID)
	compareSides(t, r, ptyID)
}
