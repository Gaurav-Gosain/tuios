package session

import (
	"net"
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

// awaitTreeOps reads state broadcasts until one says the tree ops are on or
// off as wanted, and returns its Version.
func (c *boundsClient) awaitTreeOps(t *testing.T, want bool) int {
	t.Helper()
	for {
		var sync StateSyncPayload
		if err := c.await(t, MsgStateSync).ParsePayload(&sync); err != nil {
			t.Fatalf("parse state sync: %v", err)
		}
		if sync.State != nil && sync.State.LayoutTreeOps == want {
			return sync.State.Version
		}
	}
}

// TestTreeOpsOffWhileAnOlderClientIsAttached: the session's tree ops are on
// while every attached client sends them. A client too old for them attaching
// mid-session turns them off for everyone, in one broadcast at one Version,
// and its leaving turns them on again the same way.
//
// Negative control: with refreshTreeOps a no-op, the older client's attach
// reply says the ops are on and the current client never hears them go off.
func TestTreeOpsOffWhileAnOlderClientIsAttached(t *testing.T) {
	_, socketPath := startTestDaemon(t)

	current, st := dialTreeOpsClient(t, socketPath, "gate", true)
	if !st.LayoutTreeOps {
		t.Fatal("a session with only a current client attached has tree ops off")
	}

	older, st := dialTreeOpsClient(t, socketPath, "gate", false)
	if st.LayoutTreeOps {
		t.Fatal("the older client's attach reply says tree ops are on")
	}
	off := current.awaitTreeOps(t, false)

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
