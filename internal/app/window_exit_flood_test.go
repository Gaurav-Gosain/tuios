package app

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestWindowExitFloodKeepsTheReadLoopAlive closes more panes at once than the
// window-exit channel can hold while the consumer is busy, which is what killing
// a full session or a daemon shutdown fan-out looks like.
//
// The exit notification arrives on the client's daemon read loop, the same
// goroutine that carries pane output and every round-trip response, so a send
// that blocks there does not merely delay a window teardown: it stops the whole
// client.
func TestWindowExitFloodKeepsTheReadLoopAlive(t *testing.T) {
	const panes = 12
	r := newRig(t, panes, keepExits)

	survivor := r.m.Windows[0]
	if survivor.PTYID == "" {
		t.Fatalf("survivor pane has no PTY")
	}

	doomed := make([]string, 0, panes-1)
	for _, w := range r.m.Windows[1:] {
		doomed = append(doomed, w.ID)
		if err := r.client.ClosePTY(w.PTYID); err != nil {
			t.Fatalf("close pty: %v", err)
		}
	}

	// The precondition this test exists for: more exits in flight than the
	// channel holds. Without it a pass would only prove the flood never
	// happened.
	rigWaitUntil(t, "the window-exit channel to fill", func() bool {
		return len(r.m.WindowExitChan) == cap(r.m.WindowExitChan)
	})
	if len(doomed) <= cap(r.m.WindowExitChan) {
		t.Fatalf("vacuous: %d exits fit in a channel of %d", len(doomed), cap(r.m.WindowExitChan))
	}

	// A round trip is the honest liveness probe: it is answered by the read
	// loop, so it comes back only if the read loop is still reading.
	type reply struct {
		state *session.TerminalState
		err   error
	}
	got := make(chan reply, 1)
	go func() {
		st, err := r.client.GetTerminalState(survivor.PTYID, 0, 0)
		got <- reply{st, err}
	}()
	select {
	case res := <-got:
		if res.err != nil {
			t.Fatalf("round trip after exit flood: %v", res.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("client read loop wedged: %d pane exits with a %d-slot channel blocked it, so no output and no round trip reaches the client",
			len(doomed), cap(r.m.WindowExitChan))
	}

	// Nothing may be dropped either: an unreported exit is a pane that stays on
	// screen forever, because a daemon-backed window has no ProcessExited
	// backstop for the maintenance tick to find.
	seen := make(map[string]bool, len(doomed))
	deadline := time.After(10 * time.Second)
	for len(seen) < len(doomed) {
		select {
		case id := <-r.m.WindowExitChan:
			seen[id] = true
		case <-deadline:
			t.Fatalf("only %d of %d window exits were delivered", len(seen), len(doomed))
		}
	}
	for _, id := range doomed {
		if !seen[id] {
			t.Fatalf("window %s exited but was never reported", id)
		}
	}
}
