package app

import (
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Two orderings a client used to get wrong, each pinned by making it happen on
// purpose. Both are deterministic race regressions: the interleaving is built
// step by step against a real daemon, not waited for.
//
// The first is a push crossing a broadcast. A client pushes its state while a
// peer's broadcast is already on its way to it, and applies the broadcast
// afterwards. The daemon holds the client's push and sends it to every other
// client, but never back to the client that made it, so that client alone is
// left on the peer's older view for good. The convergence harness met it as a
// pane zoomed on two clients and not on the third, about one sequence in two
// hundred under load, once it stopped losing broadcasts of its own.
//
// The second is two copies of the session state arriving in the opposite order
// to the one they were taken in. The daemon sends from more than one goroutine,
// so it can happen, and a client that applied the older copy last dropped a
// window the daemon had just opened.
//
// How these could pass without the fix, written down first:
//   - The broadcast could be applied before the push instead of after it, which
//     is the order that was always right. The test holds the broadcast in the
//     exchange until the daemon is seen to hold the push.
//   - The daemon could have merged the two pushes into one state both sides
//     already agree with. The two pushes change the same pane in opposite
//     directions, so the last one has to win on every client.
//   - The client could be compared with a peer that is wrong the same way. It
//     is compared with the daemon's own newest state as well.
//   - The two copies could be the same state, so the order would not matter.
//     The older one lacks the window the newer one has.

// newestDaemonState records the newest state the daemon has broadcast to the
// rig's control connection.
type newestDaemonState struct {
	mu sync.Mutex
	st *session.SessionState
}

func trackDaemon(r *rig) *newestDaemonState {
	d := &newestDaemonState{}
	r.ctl.OnStateSync(func(st *session.SessionState, _, _ string) {
		d.mu.Lock()
		if d.st == nil || st.SnapshotSeq > d.st.SnapshotSeq {
			d.st = st
		}
		d.mu.Unlock()
	})
	return d
}

func (d *newestDaemonState) get() *session.SessionState {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.st
}

func (d *newestDaemonState) waitFor(t *testing.T, what string, cond func(*session.SessionState) bool) {
	t.Helper()
	deadline := time.Now().Add(rigWait)
	for {
		if st := d.get(); st != nil && cond(st) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func daemonZoomed(st *session.SessionState, ptyID string) (zoomed, found bool) {
	for _, ws := range st.Windows {
		if ws.PTYID == ptyID {
			return ws.Zoomed, true
		}
	}
	return false, false
}

// TestAPushThatCrossesABroadcastIsNotUndoneByIt: B unzooms a pane while A,
// which has not heard yet, changes focus and pushes a state that still has the
// pane zoomed. A's push reaches the daemon last, so the session keeps the zoom.
// A must not then apply B's broadcast and take the zoom off on its own screen.
//
// NEGATIVE CONTROL: measured. With the AcceptState check taken out of
// ApplyStateSyncFrom, A ends with the pane unzoomed while B and the daemon have
// it zoomed.
func TestAPushThatCrossesABroadcastIsNotUndoneByIt(t *testing.T) {
	r, p, ex := twoClientsOnOneTiledSession(t)
	daemon := trackDaemon(r)

	// A zooms the first pane and both clients take it.
	r.m.FocusedWindow = 0
	zoomPTY := r.m.Windows[0].PTYID
	r.m.ToggleZoom()
	r.m.SyncStateToDaemon()
	ex.settle(60, 300*time.Millisecond)
	if w := winByPTY(p.m, zoomPTY); w == nil || !w.Zoomed {
		t.Fatalf("B never took A's zoom: %s", rectOf(w))
	}

	// B unzooms it. Its broadcast to A is received and held, not applied.
	if f := p.m.GetFocusedWindow(); f == nil || f.PTYID != zoomPTY {
		t.Fatalf("B is not focused on the zoomed pane")
	}
	p.m.ToggleZoom()
	p.m.SyncStateToDaemon()
	daemon.waitFor(t, "the daemon to hold B's unzoom", func(st *session.SessionState) bool {
		z, ok := daemonZoomed(st, zoomPTY)
		return ok && !z
	})
	deadline := time.Now().Add(rigWait)
	for !ex.queued() {
		if time.Now().After(deadline) {
			t.Fatal("B's broadcast never reached A")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// A has not heard. It renames the other pane and pushes the state it is
	// showing, zoom and all, which is what any change of A's does. The rename
	// is what makes it a push at all: a client does not resend a state it
	// already sent.
	r.m.Windows[1].CustomName = "RENAMED"
	r.m.SyncStateToDaemon()
	daemon.waitFor(t, "the daemon to hold A's push", func(st *session.SessionState) bool {
		z, ok := daemonZoomed(st, zoomPTY)
		return ok && z
	})

	// Now A hears B, and everything else still in flight lands.
	ex.settle(60, 300*time.Millisecond)

	a, b := winByPTY(r.m, zoomPTY), winByPTY(p.m, zoomPTY)
	z, _ := daemonZoomed(daemon.get(), zoomPTY)
	t.Logf("A %s", rectOf(a))
	t.Logf("B %s", rectOf(b))
	t.Logf("daemon zoomed=%t", z)
	if !z {
		t.Fatalf("the daemon lost the push that reached it last")
	}
	if a.Zoomed != z || b.Zoomed != z {
		t.Fatalf("the clients disagree with the daemon (zoomed=%t):\n A %s\n B %s", z, rectOf(a), rectOf(b))
	}
}

// TestAnOlderStateArrivingLastIsNotApplied: A renames a pane twice, and the
// copy of the session the daemon took after the first rename reaches another
// client after the copy it took after the second. The second name must stay.
//
// A rename is a client's push, and a push does not move the daemon's Version,
// so the two copies carry the same Version and nothing but their order can
// tell them apart. The client they are handed to has attached and never
// pushed, so its own pushes cannot decide it either.
//
// NEGATIVE CONTROL: measured. With the SnapshotSeq comparison taken out of
// AcceptState, the client ends on the first name while the daemon holds the
// second.
func TestAnOlderStateArrivingLastIsNotApplied(t *testing.T) {
	r, _, _ := twoClientsOnOneTiledSession(t)
	daemon := trackDaemon(r)
	q := joinPeerOS(t, r, holderCols, holderRows)
	ptyID := r.m.Windows[0].PTYID

	named := func(name string) func(*session.SessionState) bool {
		return func(st *session.SessionState) bool {
			for _, ws := range st.Windows {
				if ws.PTYID == ptyID {
					return ws.CustomName == name
				}
			}
			return false
		}
	}
	rename := func(name string) *session.SessionState {
		r.m.Windows[0].CustomName = name
		r.m.SyncStateToDaemon()
		daemon.waitFor(t, "the name "+name+" to reach the daemon", named(name))
		return daemon.get()
	}
	older := rename("FIRST")
	newer := rename("SECOND")
	if older.Version != newer.Version {
		t.Fatalf("the two copies differ in Version (%d, %d), so the test would not be about their order", older.Version, newer.Version)
	}

	// The two copies in the wrong order, as the client can be handed them.
	if err := q.m.ApplyStateSyncFrom(newer, "peer"); err != nil {
		t.Fatalf("apply the newer state: %v", err)
	}
	if err := q.m.ApplyStateSyncFrom(older, "peer"); err != nil {
		t.Fatalf("apply the older state: %v", err)
	}
	if w := winByPTY(q.m, ptyID); w == nil || w.CustomName != "SECOND" {
		got := "<missing>"
		if w != nil {
			got = w.CustomName
		}
		t.Fatalf("the client shows the pane as %q after the older copy landed last; the daemon has SECOND", got)
	}
}
