package session

import (
	"net"
	"sync"
	"testing"
	"time"
)

// Deterministic race regressions for detach_client.go. Each lands one event
// at a fixed point inside an attach through attachSnapshotTaken, the hook
// that runs between the attach's snapshot and its reply, with the session's
// attach lock held.

// dialTUI connects and handshakes a TUI client without attaching it.
func dialTUI(t *testing.T, sp string) *TUIClient {
	t.Helper()
	conn, err := net.DialTimeout("unix", sp, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c := NewTUIClient()
	c.conn = conn
	if err := c.handshake("test", 80, 24, nil); err != nil {
		_ = conn.Close()
		t.Fatalf("handshake: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// setAttachHook installs fn as attachSnapshotTaken for the rest of the test.
func setAttachHook(t *testing.T, fn func()) {
	t.Helper()
	attachSnapshotTaken.Store(&fn)
	t.Cleanup(func() { attachSnapshotTaken.Store(nil) })
}

// TestAPanicInsideAnAttachLeavesTheSessionUnlocked panics once inside an
// attach, while the session's attach lock is held. The connection's recover
// drops that client, and the next attach to the session must return.
//
// NEGATIVE CONTROL: with the deferred unlock in handleAttach replaced by the
// plain unlock after the reply, the second attach never returns.
func TestAPanicInsideAnAttachLeavesTheSessionUnlocked(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "panic")

	var once sync.Once
	setAttachHook(t, func() {
		once.Do(func() { panic("injected inside an attach") })
	})

	first := dialTUI(t, sp)
	if _, err := first.AttachSession("panic", false, 80, 24); err == nil {
		t.Fatal("the attach that panicked returned a session")
	}

	second := dialTUI(t, sp)
	done := make(chan error, 1)
	go func() {
		_, err := second.AttachSession("panic", false, 80, 24)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the attach after the panic failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the attach after a panic inside another attach never returned: the session is still locked")
	}
}

// TestADetachInsideAnAttachLeavesTheAttachingClientAlone runs detach-client
// -s inside a second client's attach. The first client is fully attached and
// is detached. The second has not had its reply, so it is not touched: its
// attach returns the session, and it is attached once, with no notice ahead
// of its reply.
//
// NEGATIVE CONTROL: with the repliedSession check cut from markOthers and
// detachClientFrom, the second attach fails with "unexpected response: 30"
// and the daemon still lists the client as attached: a zombie.
func TestADetachInsideAnAttachLeavesTheAttachingClientAlone(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "inside")
	first := attachTUI(t, sp, "inside")
	first.StartReadLoop()

	verbs, err := DialVerbClientAt(sp, "test")
	if err != nil {
		t.Fatalf("dial verbs: %v", err)
	}
	t.Cleanup(func() { _ = verbs.Close() })

	var once sync.Once
	hookErr := make(chan error, 1)
	setAttachHook(t, func() {
		once.Do(func() {
			_, err := verbs.Call("detach-client", map[string]any{"session": "inside"})
			hookErr <- err
		})
	})

	second := dialTUI(t, sp)
	if _, err := second.AttachSession("inside", false, 80, 24); err != nil {
		t.Fatalf("an attach with a detach-client inside it failed: %v", err)
	}
	select {
	case err := <-hookErr:
		if err != nil {
			t.Fatalf("detach-client inside the attach: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the detach-client inside the attach never ran")
	}

	deadline := time.Now().Add(5 * time.Second)
	for first.DetachedReason() == "" {
		if time.Now().After(deadline) {
			t.Fatal("the fully attached client was not detached")
		}
		time.Sleep(10 * time.Millisecond)
	}
	attached := 0
	for _, c := range d.listClients() {
		if c.Session == "inside" {
			attached++
		}
	}
	if attached != 1 {
		t.Fatalf("%d clients are listed on the session, want the second one only", attached)
	}
	if second.DetachedReason() != "" {
		t.Fatalf("the client whose attach the detach landed in was detached: %q", second.DetachedReason())
	}
}

// TestADetachNoticeBeforeTheHandlerIsDelivered detaches a client after its
// read loop starts and before the app registers its session-ended handler,
// which is the window between client.StartReadLoop and WireDaemonClient. The
// notice must reach the handler once it is registered.
//
// NEGATIVE CONTROL: with the pending notice in OnSessionEnded never
// delivered, the handler is never called.
func TestADetachNoticeBeforeTheHandlerIsDelivered(t *testing.T) {
	_, sp := startTestDaemon(t)
	verbs, err := DialVerbClientAt(sp, "test")
	if err != nil {
		t.Fatalf("dial verbs: %v", err)
	}
	t.Cleanup(func() { _ = verbs.Close() })
	if _, err := verbs.Call("new-session", map[string]any{"name": "pending"}); err != nil {
		t.Fatalf("new-session: %v", err)
	}

	c := attachTUI(t, sp, "pending")
	c.StartReadLoop()
	if _, err := verbs.Call("detach-client", map[string]any{"session": "pending"}); err != nil {
		t.Fatalf("detach-client: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for c.DetachedReason() == "" {
		if time.Now().After(deadline) {
			t.Fatal("the client never read the detach notice")
		}
		time.Sleep(10 * time.Millisecond)
	}

	got := make(chan string, 1)
	c.OnSessionEnded(func(name, reason string) { got <- reason })
	select {
	case reason := <-got:
		if reason != DetachedByCommandMessage {
			t.Fatalf("the handler got %q, want %q", reason, DetachedByCommandMessage)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a detach notice that came before the handler was lost")
	}
}
