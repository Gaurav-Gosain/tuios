package session

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// dialTreeOpsClient attaches a raw client whose hello says whether it sends
// tree ops, and returns it with the state the attach handed it.
func dialTreeOpsClient(t *testing.T, socketPath, session string, treeOps bool) (*boundsClient, *SessionState) {
	t.Helper()
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	c := &boundsClient{conn: conn}
	c.send(t, MsgHello, &HelloPayload{Version: "test", PreferredCodec: "gob", Protocol: ProtocolVersion, LayoutTreeOps: treeOps})
	c.await(t, MsgWelcome)
	c.send(t, MsgAttach, &AttachPayload{SessionName: session, CreateNew: true, Width: 120, Height: 40})
	var attached AttachedPayload
	if err := c.await(t, MsgAttached).ParsePayload(&attached); err != nil {
		t.Fatalf("parse attach reply: %v", err)
	}
	return c, attached.State
}

// stateReader reads one client's state broadcasts and fails the test when one
// carries a Version below a state the client already read: a client adopts
// the state it reads last, so a late older state moves it backwards.
type stateReader struct {
	c    *boundsClient
	seen int
}

// next reads the next state broadcast.
func (r *stateReader) next(t *testing.T) *SessionState {
	t.Helper()
	var sync StateSyncPayload
	if err := r.c.await(t, MsgStateSync).ParsePayload(&sync); err != nil {
		t.Fatalf("parse state sync: %v", err)
	}
	if sync.State == nil {
		t.Fatal("a state sync carried no state")
	}
	if sync.State.Version < r.seen {
		t.Fatalf("a state at Version %d (tree ops on = %v) arrived after one at Version %d",
			sync.State.Version, sync.State.LayoutTreeOps, r.seen)
	}
	r.seen = sync.State.Version
	return sync.State
}

// awaitTreeOps reads state broadcasts until one says the tree ops are on or
// off as wanted, and returns its Version.
func (r *stateReader) awaitTreeOps(t *testing.T, want bool) int {
	t.Helper()
	for {
		if st := r.next(t); st.LayoutTreeOps == want {
			return st.Version
		}
	}
}

// TestTreeOpsOffWhileAnOlderClientIsAttached: the session's tree ops are on
// while every attached client sends them. A client too old for them attaching
// mid-session turns them off for everyone, in one broadcast at one Version,
// and its leaving turns them on again the same way.
//
// The older client attaches inside the current client's attach repair, after
// the repair took its snapshot. The current client always misses a state on
// its attach: its hello offers no scratch workspaces, so the refresh in that
// attach turns them off and broadcasts while the client is not yet in the
// broadcast set. The repair used to queue its snapshot with no order against
// the broadcasts, so the older client's "off" reached the current client
// first and the older snapshot, still "on", followed it. On CI this failed
// now and then as "tree ops came back on at Version 2, not after they went
// off at 3".
//
// Negative control: with refreshTreeOps a no-op, the older client's attach
// reply says the ops are on and the current client never hears them go off.
// With handleAttach queueing a session.GetState() taken before the hook
// instead of calling resendState, the repair arrives at Version 2 after the
// "off" at Version 3.
func TestTreeOpsOffWhileAnOlderClientIsAttached(t *testing.T) {
	_, socketPath := startTestDaemon(t)

	// The hook runs on the daemon's connection goroutine. It hands the
	// attach to the test goroutine and waits for it, so t is used there only.
	repairing := make(chan struct{})
	olderAttached := make(chan struct{})
	var once sync.Once
	hook := func() {
		once.Do(func() {
			close(repairing)
			select {
			case <-olderAttached:
			case <-time.After(10 * time.Second * testDeadlineScale):
			}
		})
	}
	stateResendSnapshotTaken.Store(&hook)
	t.Cleanup(func() { stateResendSnapshotTaken.Store(nil) })

	currentConn, st := dialTreeOpsClient(t, socketPath, "gate", true)
	current := &stateReader{c: currentConn, seen: st.Version}
	if !st.LayoutTreeOps {
		t.Fatal("a session with only a current client attached has tree ops off")
	}
	select {
	case <-repairing:
	case <-time.After(10 * time.Second * testDeadlineScale):
		t.Fatal("the current client's attach sent no repair, so the older attach cannot land inside one")
	}

	older, st := dialTreeOpsClient(t, socketPath, "gate", false)
	close(olderAttached)
	if st.LayoutTreeOps {
		t.Fatal("the older client's attach reply says tree ops are on")
	}
	off := current.awaitTreeOps(t, false)
	// The repair is the next state. Nothing else changes the session until
	// the detach below, and the repair is queued before that detach can be.
	if repair := current.next(t); repair.LayoutTreeOps {
		t.Fatalf("the attach repair at Version %d says tree ops are on beside the older client", repair.Version)
	}

	older.send(t, MsgDetach, struct{}{})
	on := current.awaitTreeOps(t, true)
	if on <= off {
		t.Fatalf("tree ops came back on at Version %d, not after they went off at %d", on, off)
	}

	// Again, with the older client leaving by dropping the connection.
	older2, _ := dialTreeOpsClient(t, socketPath, "gate", false)
	current.awaitTreeOps(t, false)
	_ = older2.conn.Close()
	current.awaitTreeOps(t, true)
}

// TestTreeOpsSwitchDoesNotMakeAPushStale: turning the ops off is a mutation,
// and a push built before it would read as stale, and the reconcile would put
// back the minimise the push carries. The switch changes nothing a push
// carries, so the push stands.
//
// Negative control: with the noteTreeOpLocked call cut from SetLayoutTreeOps,
// the push is reconciled and the minimise is lost.
func TestTreeOpsSwitchDoesNotMakeAPushStale(t *testing.T) {
	sess, a, _ := treeSession(t)
	push := clientSnapshot(sess)
	sess.SetLayoutTreeOps(false)
	windowByID(t, push, a).Minimized = true
	if !sess.UpdateState(push) {
		t.Error("a push built before the tree ops switched off was read as stale")
	}
	if w := windowByID(t, sess.GetState(), a); w == nil || !w.Minimized {
		t.Fatalf("the minimise was lost to the switch: %+v", w)
	}
}

// sessionTreeOps reads whether the named session has its tree ops on.
func sessionTreeOps(t *testing.T, d *Daemon, name string) bool {
	t.Helper()
	s := d.manager.GetSession(name)
	if s == nil {
		t.Fatalf("no session %q", name)
	}
	return s.GetState().LayoutTreeOps
}

// waitTreeOps waits until the named session has its tree ops on or off.
func waitTreeOps(t *testing.T, d *Daemon, name string, want bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second * testDeadlineScale)
	for sessionTreeOps(t, d, name) != want {
		if time.Now().After(deadline) {
			t.Fatalf("%s: tree ops on = %v, want %v", what, !want, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestTreeOpsRefreshesApplyInOrder: an older client leaves while another
// older client attaches. The leave counts "no older client here" and applies
// it late; the attach counts "an older client here" and applies it first. The
// late answer must not win, or the ops stay on beside a client that cannot
// send them.
//
// Negative control: with treeOpsMu not taken in refreshTreeOps, the ops end
// on with the second older client attached.
func TestTreeOpsRefreshesApplyInOrder(t *testing.T) {
	d, socketPath := startTestDaemon(t)
	var slow atomic.Bool
	// Only the first refresh after arming is slowed: the leave's.
	hook := func() {
		if slow.CompareAndSwap(true, false) {
			time.Sleep(20 * time.Millisecond)
		}
	}
	treeOpsCounted.Store(&hook)
	t.Cleanup(func() { treeOpsCounted.Store(nil) })

	dialTreeOpsClient(t, socketPath, "order", true)
	for round := range 10 {
		leaving, _ := dialTreeOpsClient(t, socketPath, "order", false)
		waitTreeOps(t, d, "order", false, "after the first older client attached")
		slow.Store(true)
		leaving.send(t, MsgDetach, struct{}{})
		time.Sleep(2 * time.Millisecond)
		staying, _ := dialTreeOpsClient(t, socketPath, "order", false)
		time.Sleep(60 * time.Millisecond)
		if sessionTreeOps(t, d, "order") {
			t.Fatalf("round %d: tree ops are on with an older client attached", round)
		}
		_ = staying.conn.Close()
		_ = leaving.conn.Close()
		waitTreeOps(t, d, "order", true, "after both older clients left")
	}
}

// TestTreeOpsFollowAClientThatMovesWithoutADetach: an older client attaches
// to one session and then to another on the same connection, without a
// detach. The session it left runs ops again.
//
// Negative control: with the previousSession refresh cut from handleAttach,
// the first session keeps its ops off.
func TestTreeOpsFollowAClientThatMovesWithoutADetach(t *testing.T) {
	d, socketPath := startTestDaemon(t)
	older, _ := dialTreeOpsClient(t, socketPath, "first", false)
	waitTreeOps(t, d, "first", false, "with the older client attached")
	older.send(t, MsgAttach, &AttachPayload{SessionName: "second", CreateNew: true, Width: 120, Height: 40})
	older.await(t, MsgAttached)
	waitTreeOps(t, d, "first", true, "after the older client moved away")
	if sessionTreeOps(t, d, "second") {
		t.Fatal("the session the older client moved to has tree ops on")
	}
}

// TestTreeOpsFollowASecondHello: an attached client says hello again, this
// time offering tree ops. The session runs ops again.
//
// Negative control: with the refresh cut from handleHello, the session keeps
// its ops off.
func TestTreeOpsFollowASecondHello(t *testing.T) {
	d, socketPath := startTestDaemon(t)
	c, _ := dialTreeOpsClient(t, socketPath, "hello", false)
	waitTreeOps(t, d, "hello", false, "with the client too old for ops")
	c.send(t, MsgHello, &HelloPayload{Version: "test", PreferredCodec: "gob", Protocol: ProtocolVersion, LayoutTreeOps: true})
	c.await(t, MsgWelcome)
	waitTreeOps(t, d, "hello", true, "after the second hello")
}
