package app

import (
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

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

	r.m.FocusedWindow = 0
	zoomPTY := r.m.Windows[0].PTYID
	r.m.ToggleZoom()
	r.m.SyncStateToDaemon()
	ex.settle(60, 300*time.Millisecond)
	if w := winByPTY(p.m, zoomPTY); w == nil || !w.Zoomed {
		t.Fatalf("B never took A's zoom: %s", rectOf(w))
	}

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

	r.m.Windows[1].CustomName = "RENAMED"
	r.m.SyncStateToDaemon()
	daemon.waitFor(t, "the daemon to hold A's push", func(st *session.SessionState) bool {
		z, ok := daemonZoomed(st, zoomPTY)
		return ok && z
	})

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
