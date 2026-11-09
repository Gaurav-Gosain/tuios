package session

import (
	"net"
	"testing"
	"time"
)

// TestScrollColumnsFollowTheClientsHello (S7, wire compatibility): a push
// from a TUI that predates ScrollColumnOps still moves the session's
// scrolling columns, as every push did before, so a pair with an older client
// keeps working by the old rule. A push from a TUI that offers the op does
// not move them; its MsgScrollColumns op does.
//
// How this could pass wrongly, written down first:
//   - The push could be refused for another reason and the columns stay: the
//     old client's push must move them, by the same kind of push.
//   - The op could be applied and nothing published: the session's state is
//     read after it.
func TestScrollColumnsFollowTheClientsHello(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "cols")
	if _, err := sess.AddDaemonWindow("Second", nil); err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	st := sess.GetState()
	a, b := st.Windows[0].ID, st.Windows[1].ID
	two := []SerializedScrollColumn{{Windows: []string{a}}, {Windows: []string{b}}}
	one := []SerializedScrollColumn{{Windows: []string{a, b}}}
	cols := func() []SerializedScrollColumn { return sess.GetState().WorkspaceScrollColumns[1] }

	dial := func(ops bool) *boundsClient {
		t.Helper()
		conn, err := net.DialTimeout("unix", sp, 5*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		c := &boundsClient{conn: conn}
		c.send(t, MsgHello, &HelloPayload{Version: "test", PreferredCodec: "gob", Protocol: ProtocolVersion, LayoutTreeOps: true, ScrollColumnOps: ops})
		var w WelcomePayload
		if err := c.await(t, MsgWelcome).ParsePayload(&w); err != nil {
			t.Fatalf("parse welcome: %v", err)
		}
		if !w.ScrollColumnOps {
			t.Fatalf("the welcome did not offer ScrollColumnOps")
		}
		c.send(t, MsgAttach, &AttachPayload{SessionName: "cols", Width: 120, Height: 40})
		c.await(t, MsgAttached)
		return c
	}
	push := func(c *boundsClient, with []SerializedScrollColumn) {
		t.Helper()
		st := sess.GetState()
		st.BaseVersion = st.Version
		st.WorkspaceScrollColumns = map[int][]SerializedScrollColumn{1: with}
		c.send(t, MsgUpdateState, st)
		c.noError(t)
	}
	waitCols := func(what string, want []SerializedScrollColumn) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !sameColumns(cols(), want) {
			if time.Now().After(deadline) {
				t.Fatalf("%s: the session holds %+v, want %+v", what, cols(), want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	old := dial(false)
	push(old, two)
	waitCols("a push from a client before ScrollColumnOps", two)

	cur := dial(true)
	push(cur, one)
	time.Sleep(200 * time.Millisecond)
	if !sameColumns(cols(), two) {
		t.Fatalf("a push from a client that sends ops moved the columns to %+v", cols())
	}
	cur.send(t, MsgScrollColumns, &ScrollColumnsPayload{Workspace: 1, Columns: one})
	waitCols("the op", one)
}
