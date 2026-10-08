package session

import (
	"maps"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// pingClient sends a ping and reads until the pong, keeping the workspace of
// every state sync read on the way. Frames on a connection are handled in
// order, so the pong says the daemon has handled every frame this client sent
// before it.
func pingClient(t *testing.T, c *boundsClient) []int {
	t.Helper()
	c.send(t, MsgPing, struct{}{})
	var seen []int
	deadline := time.Now().Add(10 * time.Second * testDeadlineScale)
	for {
		_ = c.conn.SetReadDeadline(deadline)
		msg, err := ReadMessage(c.conn)
		if err != nil {
			t.Fatalf("waiting for the pong: %v", err)
		}
		switch msg.Type {
		case MsgPong:
			return seen
		case MsgStateSync:
			var sync StateSyncPayload
			if err := msg.ParsePayload(&sync); err != nil || sync.State == nil {
				t.Fatalf("parse state sync: %v", err)
			}
			seen = append(seen, sync.State.CurrentWorkspace)
		}
	}
}

// pushWorkspace sends a client push of the session's current state with the
// workspace changed, as a client that has seen every change would send it.
func pushWorkspace(t *testing.T, c *boundsClient, sess *Session, ws int) {
	t.Helper()
	st := sess.GetState()
	st.CurrentWorkspace = ws
	st.BaseVersion = st.Version
	st.PushOrigin, st.PushSeq, st.PushSeen, st.SnapshotSeq = "", 0, nil, 0
	c.send(t, MsgUpdateState, st)
}

// publishMarker makes a daemon-side change that reaches every client behind
// whatever was already queued for it.
func publishMarker(t *testing.T, sess *Session) {
	t.Helper()
	err := sess.mutateState(func(st *SessionState) error {
		opts := maps.Clone(st.Options)
		if opts == nil {
			opts = map[string]string{}
		}
		opts["order-marker"] = "1"
		st.Options = opts
		return nil
	})
	if err != nil {
		t.Fatalf("publish the marker: %v", err)
	}
}

// statesUntilMarker reads one client's state syncs until the marker, and
// returns the workspace of each, the marker's last.
func statesUntilMarker(t *testing.T, c *boundsClient, before []int) []int {
	t.Helper()
	seen := append([]int(nil), before...)
	for {
		var sync StateSyncPayload
		if err := c.await(t, MsgStateSync).ParsePayload(&sync); err != nil || sync.State == nil {
			t.Fatalf("parse state sync: %v", err)
		}
		seen = append(seen, sync.State.CurrentWorkspace)
		if sync.State.Options["order-marker"] == "1" {
			return seen
		}
	}
}

// checkNoStepBack fails when a state with another workspace arrived after one
// with want: the client adopts the state it reads last, so it went back.
func checkNoStepBack(t *testing.T, who string, seen []int, want int) {
	t.Helper()
	got := false
	for i, ws := range seen {
		if ws == want {
			got = true
			continue
		}
		if got {
			t.Fatalf("%s read workspace %d after workspace %d (states in order: %v, at %d)", who, ws, want, seen, i)
		}
	}
	if !got {
		t.Fatalf("%s never read workspace %d (states in order: %v)", who, want, seen)
	}
}

// TestAttachRepairDoesNotFollowAPeerPush: a client that missed a state while
// it attached is sent the current state after its reply. A peer push that
// lands between the repair's snapshot and its delivery is forwarded to the
// client first. The repair must not then send the older snapshot behind it.
//
// The repair compared Version to tell the two apart, and a client push keeps
// Version the same. The older snapshot passed as the same state and went out
// last, and the client ended on the workspace it had left.
//
// Negative control: with resendState and forwardPush comparing Version
// instead of the change count, B reads workspace 1 after workspace 7.
func TestAttachRepairDoesNotFollowAPeerPush(t *testing.T) {
	d, socketPath := startTestDaemon(t)

	a, _ := dialTreeOpsClient(t, socketPath, "order", true)
	// A's own attach repair, if it has one, is over before the hook is armed.
	pingClient(t, a)
	sess := d.manager.GetSession("order")
	if sess == nil {
		t.Fatal("the session is not there")
	}
	const want = 7
	if ws := sess.GetState().CurrentWorkspace; ws == want {
		t.Fatalf("the session starts on workspace %d, so the push changes nothing", ws)
	}

	// The hook runs on the daemon's connection goroutine for B. It hands the
	// push to the test goroutine and waits for it, so t is used there only.
	var armed atomic.Bool
	armed.Store(true)
	repairing := make(chan struct{})
	pushed := make(chan struct{})
	hook := func() {
		if !armed.CompareAndSwap(true, false) {
			return
		}
		close(repairing)
		select {
		case <-pushed:
		case <-time.After(10 * time.Second * testDeadlineScale):
		}
	}
	stateResendSnapshotTaken.Store(&hook)
	t.Cleanup(func() { stateResendSnapshotTaken.Store(nil) })

	// B has tree ops off, so its attach turns them off for the session while
	// B is not yet in the broadcast set. B misses that state and is repaired.
	b, _ := dialTreeOpsClient(t, socketPath, "order", false)
	select {
	case <-repairing:
	case <-time.After(10 * time.Second * testDeadlineScale):
		t.Fatal("B's attach sent no repair, so the push cannot land inside one")
	}

	pushWorkspace(t, a, sess, want)
	// The pong says A's push, and its forward to B, have been handled.
	pingClient(t, a)
	close(pushed)

	// B's repair is queued before the pong to B, and the marker after it.
	seen := pingClient(t, b)
	publishMarker(t, sess)
	seen = statesUntilMarker(t, b, seen)
	checkNoStepBack(t, "B", seen, want)
	if ws := sess.GetState().CurrentWorkspace; ws != want {
		t.Fatalf("the session is on workspace %d, want %d", ws, want)
	}
}

// TestConcurrentPushesForwardInOrder: two clients push at once. The push
// that lands second is the session's state, and every peer must end on it.
//
// Each push's merged state was forwarded outside pushMu, with no check. A
// forward delayed past a later push's forward reached the peers last, and a
// peer adopted the older push.
//
// Negative control: with forwardPush sending to the peers without the change
// count check, C reads workspace 2 after workspace 3, and B reads A's older
// push after its own.
func TestConcurrentPushesForwardInOrder(t *testing.T) {
	d, socketPath := startTestDaemon(t)

	a, _ := dialTreeOpsClient(t, socketPath, "order", true)
	b, _ := dialTreeOpsClient(t, socketPath, "order", true)
	c, _ := dialTreeOpsClient(t, socketPath, "order", true)
	for _, cl := range []*boundsClient{a, b, c} {
		pingClient(t, cl)
	}
	sess := d.manager.GetSession("order")
	if sess == nil {
		t.Fatal("the session is not there")
	}
	const older, newer = 2, 3
	if ws := sess.GetState().CurrentWorkspace; ws == older || ws == newer {
		t.Fatalf("the session starts on workspace %d, which the pushes use", ws)
	}

	// Holds A's forward after its snapshot until B's push is forwarded.
	var armed atomic.Bool
	armed.Store(true)
	holding := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	hook := func() {
		if !armed.CompareAndSwap(true, false) {
			return
		}
		close(holding)
		select {
		case <-release:
		case <-time.After(10 * time.Second * testDeadlineScale):
		}
	}
	statePushSnapshotTaken.Store(&hook)
	t.Cleanup(func() { statePushSnapshotTaken.Store(nil) })

	pushWorkspace(t, a, sess, older)
	select {
	case <-holding:
	case <-time.After(10 * time.Second * testDeadlineScale):
		t.Fatal("A's push was not forwarded, so B's cannot land inside it")
	}
	pushWorkspace(t, b, sess, newer)
	seenB := pingClient(t, b)
	once.Do(func() { close(release) })
	pingClient(t, a)

	publishMarker(t, sess)
	seenC := statesUntilMarker(t, c, nil)
	checkNoStepBack(t, "C", seenC, newer)
	seenB = statesUntilMarker(t, b, seenB)
	for _, ws := range seenB {
		if ws == older {
			t.Fatalf("B read A's older push after its own (states in order: %v)", seenB)
		}
	}
	if ws := sess.GetState().CurrentWorkspace; ws != newer {
		t.Fatalf("the session is on workspace %d, want %d", ws, newer)
	}
}
