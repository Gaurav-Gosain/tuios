package session

import (
	"maps"
	"strings"
	"testing"
	"time"
)

// TestLayoutTreeOpNamingNoneOfTheSessionsWindowsIsRefused is the layout half
// of the slip TestUpdateStateDropsAnotherSessionsWindows holds for the window
// list: a client showing another session's state sends that session's tree
// for a workspace, every leaf of it names a window this session does not hold,
// and taken as sent the op deletes the workspace's tree. The session keeps its
// panes and loses their split sizes. A real client sends the ops and a real
// daemon answers.
//
// Written down before the code, how this test could pass with the session
// still wrong:
//   - the op is never sent or never handled, so the tree stands for the wrong
//     reason: each op's own count must show in PushSeen;
//   - the op is answered as stale, not as another session's tree: the ops
//     carry the session's own version as their base, which the stale check
//     never refuses;
//   - the refusal is returned as an error, so the tree stands but the daemon
//     logs "Refused a layout tree from" the client: that line for the client
//     is counted around the two ops, read once both replies are in, since it
//     is written just before the reply;
//   - the sender is never told and keeps showing the foreign tree: each op is
//     answered with a "reconcile" state holding the session's tree;
//   - the refusal also takes legitimate ops: a nil tree, a tree with no
//     leaves, one held leaf beside an unknown one, a new shape of the held
//     windows, an op on a session with no window, and the held windows of
//     workspace 1 laid out on workspace 2, each handled as before and logging
//     nothing;
//   - the line counted is an earlier run's: every session has a name no other
//     in the process has, so -count=N in one process reads only its own;
//   - the line is written for every op of a slip, or never again after one:
//     the second group follows the one field both refusals share through each
//     event that sets, keeps or clears it.
func TestLayoutTreeOpNamingNoneOfTheSessionsWindowsIsRefused(t *testing.T) {
	d, _ := startTestDaemon(t)

	t.Run("the refusal and what it must not refuse", func(t *testing.T) {
		t.Run("a: two ops naming none of the session's windows leave its tree, are answered with it and logged once", func(t *testing.T) {
			sess, _, _ := tiledSession(t, d, "tree-foreign")
			c := attachTestClient(t, sess.Name())
			syncs := takeSyncs(c)
			before := sess.GetState()
			want := treeKeyAt(before, 1)
			from := layoutRefusalsFrom(t, c)
			for i := 1; i <= 2; i++ {
				after := sendForeign(t, sess, c, 1)
				if got := treeKeyAt(after, 1); got != want {
					t.Errorf("op %d: the session's tree is %q, want its own %q", i, got, want)
				}
				if after.Version != before.Version {
					t.Errorf("op %d: the session's version is %d, want %d", i, after.Version, before.Version)
				}
				if !maps.Equal(after.WindowToBSPID, before.WindowToBSPID) || after.NextBSPWindowID != before.NextBSPWindowID {
					t.Errorf("op %d: the numbering is %v next %d, want %v next %d", i, after.WindowToBSPID, after.NextBSPWindowID, before.WindowToBSPID, before.NextBSPWindowID)
				}
				reply := waitSync(t, syncs, "reconcile")
				if reply.state == nil {
					t.Fatalf("op %d: the reconcile reply carried no state", i)
				}
				if got := treeKeyAt(reply.state, 1); got != want {
					t.Errorf("op %d: the reconcile reply holds the tree %q, want the session's %q", i, got, want)
				}
			}
			if n := treeLines(sess.Name()); n != 1 {
				t.Errorf("the log holds %d lines %q after two foreign ops from one client, want 1", n, treeRefusalLine(sess.Name()))
			}
			if n := layoutRefusalsFrom(t, c); n != from {
				t.Errorf("the log holds %d lines \"Refused a layout tree from\" the client after the two ops, %d before them", n, from)
			}
		})

		t.Run("b1: a nil tree naming windows the session does not hold clears the tree", func(t *testing.T) {
			sess, _, _ := tiledSession(t, d, "tree-nil")
			c := attachTestClient(t, sess.Name())
			after := sendTree(t, sess, c, 1, nil, farLeaves(), sess.GetState().Version)
			if got := treeKeyAt(after, 1); got != "" {
				t.Errorf("the session's tree is %q after an op with no tree, want none", got)
			}
			noTreeLine(t, sess)
		})

		t.Run("b1b: a tree with no leaf names is handled as before", func(t *testing.T) {
			sess, _, _ := tiledSession(t, d, "tree-noleaves")
			c := attachTestClient(t, sess.Name())
			after := sendTree(t, sess, c, 1, bspTree(bspSplit(2, 0.6, bspLeaf(1), bspLeaf(2))), map[int]string{}, sess.GetState().Version)
			if got := treeKeyAt(after, 1); got != "" {
				t.Errorf("the session's tree is %q after an op whose leaves name no window, want none", got)
			}
			noTreeLine(t, sess)
		})

		t.Run("b2: one held leaf beside an unknown one is applied without the unknown", func(t *testing.T) {
			sess, a, _ := tiledSession(t, d, "tree-mixed")
			c := attachTestClient(t, sess.Name())
			after := sendTree(t, sess, c, 1, bspTree(bspSplit(2, 0.6, bspLeaf(1), bspLeaf(2))),
				map[int]string{1: a, 2: "far-window-one"}, sess.GetState().Version)
			want := TreeKey(bspTree(bspLeaf(1)), func(int) string { return a })
			if got := treeKeyAt(after, 1); got != want {
				t.Errorf("the session's tree is %q after a mixed op, want %q", got, want)
			}
			noTreeLine(t, sess)
		})

		t.Run("b3: a new shape of the held windows is applied", func(t *testing.T) {
			sess, a, b := tiledSession(t, d, "tree-held")
			c := attachTestClient(t, sess.Name())
			op := &LayoutTreePayload{Tree: bspTree(bspSplit(2, 0.6, bspLeaf(1), bspLeaf(2))), Leaves: map[int]string{1: a, 2: b}}
			if opKey(op) == sessionTreeKey(sess, 1) {
				t.Fatal("the op's tree is the session's, so it cannot be seen applied")
			}
			after := sendTree(t, sess, c, 1, op.Tree, op.Leaves, sess.GetState().Version)
			if got, want := treeKeyAt(after, 1), opKey(op); got != want {
				t.Errorf("the session's tree is %q, want the op's %q", got, want)
			}
			noTreeLine(t, sess)
		})

		t.Run("b4: an op on a session with no window is handled as before and logs nothing", func(t *testing.T) {
			name := foreignSessionName("tree-empty")
			sess, err := d.manager.CreateSession(name, &SessionConfig{}, 80, 24)
			if err != nil {
				t.Fatalf("CreateSession: %v", err)
			}
			c := attachTestClient(t, name)
			before := sess.GetState()
			if len(before.Windows) != 0 {
				t.Fatalf("the session holds %d windows, want none", len(before.Windows))
			}
			after := sendForeign(t, sess, c, 1)
			if after.Version != before.Version {
				t.Errorf("the session's version is %d after the op, want %d", after.Version, before.Version)
			}
			if got := treeKeyAt(after, 1); got != "" {
				t.Errorf("the session's tree is %q, want none", got)
			}
			noTreeLine(t, sess)
		})

		t.Run("b5: the held windows of workspace 1 laid out on workspace 2 are applied", func(t *testing.T) {
			sess, a, b := tiledSession(t, d, "tree-otherws")
			c := attachTestClient(t, sess.Name())
			for _, w := range sess.GetState().Windows {
				if w.Workspace != 1 {
					t.Fatalf("window %s is on workspace %d, want 1", w.ID, w.Workspace)
				}
			}
			op := &LayoutTreePayload{Tree: bspTree(bspSplit(1, 0.4, bspLeaf(1), bspLeaf(2))), Leaves: map[int]string{1: a, 2: b}}
			after := sendTree(t, sess, c, 2, op.Tree, op.Leaves, sess.GetState().Version)
			if got, want := treeKeyAt(after, 2), opKey(op); got != want {
				t.Errorf("workspace 2's tree is %q, want the op's %q", got, want)
			}
			noTreeLine(t, sess)
		})
	})

	// The tree refusal and the window list refusal log through one field, which
	// holds the origin of the last slip logged, so a slip is logged once
	// whichever refusal sees it first, and again once the client names the
	// session's windows.
	t.Run("one log line a slip", func(t *testing.T) {
		t.Run("c: a foreign op then a foreign push from the same client log one line in all", func(t *testing.T) {
			sess, _, _ := tiledSession(t, d, "tree-thenpush")
			c := attachTestClient(t, sess.Name())
			sendForeign(t, sess, c, 1)
			push := farPush(sess)
			push.BaseVersion = sess.GetState().Version + 40
			stateOnceCounted(t, sess, c, pushFrom(t, c, push))
			if n := slipLines(sess.Name()); n != 1 {
				t.Errorf("the log holds %d refusal lines for the session after a foreign op and a foreign push, want 1", n)
			}
		})

		t.Run("d: a stale push naming none of the session's windows does not end the slip", func(t *testing.T) {
			sess, _, _ := tiledSession(t, d, "tree-stalefar")
			c := attachTestClient(t, sess.Name())
			sendForeign(t, sess, c, 1)
			stalePush(t, sess, c, farPush(sess))
			sendForeign(t, sess, c, 1)
			if n := slipLines(sess.Name()); n != 1 {
				t.Errorf("the log holds %d refusal lines for the session, want 1", n)
			}
		})

		t.Run("d2: a stale push naming a held window ends the slip", func(t *testing.T) {
			sess, a, _ := tiledSession(t, d, "tree-staleheld")
			c := attachTestClient(t, sess.Name())
			sendForeign(t, sess, c, 1)
			push := sess.GetState()
			for _, w := range push.Windows {
				if w.ID == a {
					push.Windows = []WindowState{w}
					break
				}
			}
			stalePush(t, sess, c, push)
			sendForeign(t, sess, c, 1)
			if n := treeLines(sess.Name()); n != 2 {
				t.Errorf("the log holds %d lines %q, want 2", n, treeRefusalLine(sess.Name()))
			}
		})

		t.Run("e: an applied op naming held windows ends the slip", func(t *testing.T) {
			sess, a, b := tiledSession(t, d, "tree-applied")
			c := attachTestClient(t, sess.Name())
			sendForeign(t, sess, c, 1)
			v := sess.GetState().Version
			after := sendTree(t, sess, c, 1, bspTree(bspSplit(2, 0.6, bspLeaf(1), bspLeaf(2))), map[int]string{1: a, 2: b}, v)
			if after.Version == v {
				t.Fatal("the op naming the held windows was not applied")
			}
			sendForeign(t, sess, c, 1)
			if n := treeLines(sess.Name()); n != 2 {
				t.Errorf("the log holds %d lines %q, want 2", n, treeRefusalLine(sess.Name()))
			}
		})

		t.Run("e2: an op naming held windows that changes nothing ends the slip", func(t *testing.T) {
			sess, a, b := tiledSession(t, d, "tree-same")
			c := attachTestClient(t, sess.Name())
			sendForeign(t, sess, c, 1)
			v := sess.GetState().Version
			// The tree the session was given, numbered as another client would.
			after := sendTree(t, sess, c, 1, bspTree(bspSplit(1, 0.3, bspLeaf(5), bspLeaf(6))), map[int]string{5: a, 6: b}, v)
			if after.Version != v {
				t.Errorf("the session's version moved to %d on an op restating its tree, want %d", after.Version, v)
			}
			sendForeign(t, sess, c, 1)
			if n := treeLines(sess.Name()); n != 2 {
				t.Errorf("the log holds %d lines %q, want 2", n, treeRefusalLine(sess.Name()))
			}
		})

		// A stale op and a refused one both leave the tree and the version as
		// they were. A refused foreign op is logged while the field is empty and
		// a stale one is not, and an op naming held windows on a base after
		// the peer's would be applied: that is how each stale op below shows it
		// was stale.
		t.Run("f: a stale op neither logs nor ends the slip", func(t *testing.T) {
			sess, a, b := tiledSession(t, d, "tree-stale")
			x := attachTestClient(t, sess.Name())
			p := attachTestClient(t, sess.Name())
			early := sess.GetState().Version
			peer := &LayoutTreePayload{Tree: bspTree(bspSplit(2, 0.7, bspLeaf(1), bspLeaf(2))), Leaves: map[int]string{1: a, 2: b}}
			afterPeer := sendTree(t, sess, p, 1, peer.Tree, peer.Leaves, early)
			if afterPeer.Version == early || treeKeyAt(afterPeer, 1) != opKey(peer) {
				t.Fatalf("the peer's op was not applied: version %d, tree %q", afterPeer.Version, treeKeyAt(afterPeer, 1))
			}
			after := sendTree(t, sess, x, 1, farTree(), farLeaves(), early)
			if after.Version != afterPeer.Version || treeKeyAt(after, 1) != opKey(peer) {
				t.Errorf("a stale foreign op left version %d and tree %q, want %d and the peer's %q", after.Version, treeKeyAt(after, 1), afterPeer.Version, opKey(peer))
			}
			if n := treeLines(sess.Name()); n != 0 {
				t.Errorf("the log holds %d lines %q after a stale foreign op, want 0", n, treeRefusalLine(sess.Name()))
			}
			sendForeign(t, sess, x, 1)
			if n := treeLines(sess.Name()); n != 1 {
				t.Errorf("the log holds %d lines %q after a current foreign op, want 1", n, treeRefusalLine(sess.Name()))
			}
			v := sess.GetState().Version
			want := sessionTreeKey(sess, 1)
			after = sendTree(t, sess, x, 1, bspTree(bspSplit(1, 0.45, bspLeaf(1), bspLeaf(2))), map[int]string{1: a, 2: b}, early)
			if after.Version != v || treeKeyAt(after, 1) != want {
				t.Errorf("a stale op naming held windows left version %d and tree %q, want %d and %q", after.Version, treeKeyAt(after, 1), v, want)
			}
			sendForeign(t, sess, x, 1)
			if n := treeLines(sess.Name()); n != 1 {
				t.Errorf("the log holds %d lines %q after a stale held op and a foreign op, want 1", n, treeRefusalLine(sess.Name()))
			}
		})

		t.Run("g: an op refused as out of range does not end the slip", func(t *testing.T) {
			sess, a, b := tiledSession(t, d, "tree-range")
			c := attachTestClient(t, sess.Name())
			sendForeign(t, sess, c, 1)
			const ws = 50
			sendTree(t, sess, c, ws, bspTree(bspSplit(2, 0.6, bspLeaf(1), bspLeaf(2))), map[int]string{1: a, 2: b}, sess.GetState().Version)
			line := "Refused a layout tree from " + clientIDOf(t, c) + ": workspace 50 is out of range"
			waitLogged(t, line, func() bool { return logLines(line) == 1 })
			sendForeign(t, sess, c, 1)
			if n := treeLines(sess.Name()); n != 1 {
				t.Errorf("the log holds %d lines %q, want 1", n, treeRefusalLine(sess.Name()))
			}
		})

		t.Run("h: another client's op naming held windows does not end the slip", func(t *testing.T) {
			sess, a, b := tiledSession(t, d, "tree-peerheld")
			x := attachTestClient(t, sess.Name())
			p := attachTestClient(t, sess.Name())
			sendForeign(t, sess, x, 1)
			v := sess.GetState().Version
			after := sendTree(t, sess, p, 1, bspTree(bspSplit(2, 0.7, bspLeaf(1), bspLeaf(2))), map[int]string{1: a, 2: b}, v)
			if after.Version == v {
				t.Fatal("the peer's op naming the held windows was not applied")
			}
			sendForeign(t, sess, x, 1)
			if n := treeLines(sess.Name()); n != 1 {
				t.Errorf("the log holds %d lines %q, want 1", n, treeRefusalLine(sess.Name()))
			}
		})

		t.Run("i: ops with no origin are each logged", func(t *testing.T) {
			sess, _, _ := tiledSession(t, d, "tree-noorigin")
			want := sessionTreeKey(sess, 1)
			for i := 1; i <= 2; i++ {
				applyTree(t, sess, &LayoutTreePayload{Workspace: 1, Tree: farTree(), Leaves: farLeaves()})
				if got := sessionTreeKey(sess, 1); got != want {
					t.Errorf("op %d: the session's tree is %q, want its own %q", i, got, want)
				}
			}
			if n := treeLines(sess.Name()); n != 2 {
				t.Errorf("the log holds %d lines %q after two foreign ops with no origin, want 2", n, treeRefusalLine(sess.Name()))
			}
		})

		t.Run("k: two clients slipping in turn log a line each turn", func(t *testing.T) {
			sess, _, _ := tiledSession(t, d, "tree-turns")
			x := attachTestClient(t, sess.Name())
			p := attachTestClient(t, sess.Name())
			sendForeign(t, sess, x, 1)
			sendForeign(t, sess, p, 1)
			sendForeign(t, sess, x, 1)
			if n := treeLines(sess.Name()); n != 3 {
				t.Errorf("the log holds %d lines %q, want 3", n, treeRefusalLine(sess.Name()))
			}
		})
	})
}

// tiledSession is a session of the daemon's holding two windows, a and b, on
// workspace 1, with a tree for them there. The tree is applied directly and
// before any client attaches.
func tiledSession(t *testing.T, d *Daemon, stem string) (*Session, string, string) {
	t.Helper()
	sess := makeSessionWithWindow(t, d, foreignSessionName(stem))
	if _, err := sess.AddDaemonWindow("Second", nil); err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	st := sess.GetState()
	if len(st.Windows) != 2 {
		t.Fatalf("the session holds %d windows, want 2", len(st.Windows))
	}
	a, b := st.Windows[0].ID, st.Windows[1].ID
	seed := &LayoutTreePayload{Workspace: 1, Tree: bspTree(bspSplit(1, 0.3, bspLeaf(1), bspLeaf(2))), Leaves: map[int]string{1: a, 2: b}}
	if !applyTree(t, sess, seed) {
		t.Fatal("the session's first tree was not applied")
	}
	return sess, a, b
}

// farLeaves names two windows no session of the test holds.
func farLeaves() map[int]string {
	return map[int]string{1: "far-window-one", 2: "far-window-two"}
}

// farTree is another session's tree for farLeaves.
func farTree() *SerializedBSPTree {
	return bspTree(bspSplit(2, 0.6, bspLeaf(1), bspLeaf(2)))
}

// farPush is the session's state with a window list naming none of its
// windows.
func farPush(sess *Session) *SessionState {
	push := sess.GetState()
	push.Windows = []WindowState{{ID: "far-window-one", PTYID: "far-pty-one", Title: "far", Workspace: 1, Width: 40, Height: 12}}
	push.FocusedWindowID = "far-window-one"
	return push
}

// stalePush sends push from c built before a rename of the session, so the
// stale path merges it, and checks that the session kept its windows.
func stalePush(t *testing.T, sess *Session, c *TUIClient, push *SessionState) {
	t.Helper()
	held := windowPTYs(sess.GetState().Windows)
	push.BaseVersion = sess.GetState().Version
	if err := sess.SetDisplayName("renamed " + sess.Name()); err != nil {
		t.Fatal(err)
	}
	after := stateOnceCounted(t, sess, c, pushFrom(t, c, push))
	if got := windowPTYs(after.Windows); strings.Join(got, " ") != strings.Join(held, " ") {
		t.Fatalf("the session lists %v after a stale push, want its own %v", got, held)
	}
}

// sendTree sends a tree op from c and returns the session's state once the op
// is counted. The count is taken under the lock the op is decided under, so
// any line the op logs from there is in the log by then.
func sendTree(t *testing.T, sess *Session, c *TUIClient, ws int, tree *SerializedBSPTree, leaves map[int]string, base int) *SessionState {
	t.Helper()
	if err := c.SendLayoutTree(ws, tree, leaves, base); err != nil {
		t.Fatalf("SendLayoutTree: %v", err)
	}
	return stateOnceCounted(t, sess, c, c.pushSeq.Load())
}

// sendForeign sends farTree for ws from c, built on the session's version as
// it stands, so it is never stale.
func sendForeign(t *testing.T, sess *Session, c *TUIClient, ws int) *SessionState {
	t.Helper()
	return sendTree(t, sess, c, ws, farTree(), farLeaves(), sess.GetState().Version)
}

// treeKeyAt is the state's tree for ws with the leaves named by window.
func treeKeyAt(st *SessionState, ws int) string {
	return TreeKey(st.WorkspaceTrees[ws], SessionTreeNames(st))
}

// treeRefusalLine is the daemon's log line for a tree op it refused as
// another session's tree.
func treeRefusalLine(session string) string {
	return "Refused a layout tree for " + session + ": it names none of the session's windows"
}

// logLines is how many lines of the log buffer hold text.
func logLines(text string) int {
	n := 0
	for _, e := range GetLogEntries(0) {
		if strings.Contains(e.Message, text) {
			n++
		}
	}
	return n
}

// treeLines is how many lines of the log buffer are the tree refusal line for
// session.
func treeLines(session string) int {
	return logLines(treeRefusalLine(session))
}

// slipLines is every refusal line for session: the tree's and the window
// list's.
func slipLines(session string) int {
	return treeLines(session) + refusalCount(session)
}

// noTreeLine fails the test when the log holds a tree refusal line for sess.
func noTreeLine(t *testing.T, sess *Session) {
	t.Helper()
	if n := treeLines(sess.Name()); n != 0 {
		t.Errorf("the log holds %d lines %q, want none", n, treeRefusalLine(sess.Name()))
	}
}

// clientIDOf is the ID the daemon gave c's connection, which its log lines
// about the connection name.
func clientIDOf(t *testing.T, c *TUIClient) string {
	t.Helper()
	id := c.selfID.Load()
	if id == nil || *id == "" {
		t.Fatal("the client has no connection ID")
	}
	return *id
}

// layoutRefusalsFrom is how many lines of the log buffer are the daemon's
// "Refused a layout tree from" line for c's connection.
func layoutRefusalsFrom(t *testing.T, c *TUIClient) int {
	t.Helper()
	return logLines("Refused a layout tree from " + clientIDOf(t, c) + ":")
}

// waitSync is syncTaker.next with its wait stretched under the race detector.
func waitSync(t *testing.T, s *syncTaker, trigger string) syncSeen {
	t.Helper()
	deadline := time.After(5 * time.Second * testDeadlineScale)
	for {
		select {
		case seen := <-s.ch:
			if seen.trigger == trigger {
				return seen
			}
		case <-deadline:
			t.Fatalf("no %q state sync within %v", trigger, 5*time.Second*testDeadlineScale)
		}
	}
}

// waitLogged waits until done reports true, failing with what when it never
// does.
func waitLogged(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second * testDeadlineScale)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("the log never held %q", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
