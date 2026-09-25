package session

import (
	"testing"
	"time"
)

// TestTypingKeepsASessionActive pins what "last active" means. The session
// listing shows it, and a CLI command given no session name targets the most
// recently active one, so a session someone is typing in has to read as
// active.
//
// It used to be recorded only as a side effect of the state sync a client sent
// after every keypress. That made it right by accident: the syncs carried no
// change and were suppressed once that cost was measured, and the timestamp
// would have gone stale in exactly the session being used.
//
// NEGATIVE CONTROL: fails without the TouchActive call in handleInput. Nothing
// else on the input path records activity, so the timestamp stays at the
// session's creation for as long as the typing goes on.
func TestTypingKeepsASessionActive(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "typing")
	ptyID := sess.ListPTYIDs()[0]

	// A creation time far enough back that a move is unambiguous.
	before := sess.LastActive()
	time.Sleep(10 * time.Millisecond)

	c := NewTUIClient()
	if err := c.Connect("test", 80, 24); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := c.AttachSession("typing", false, 80, 24); err != nil {
		t.Fatalf("attach: %v", err)
	}
	c.StartReadLoop()
	_ = sp

	// The attach itself records activity, so the question is asked after it.
	settled := sess.LastActive()
	time.Sleep(10 * time.Millisecond)

	if err := c.WritePTY(ptyID, []byte("x")); err != nil {
		t.Fatalf("write pty: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if sess.LastActive().After(settled) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("a keystroke reached the pane and the session's last-active time "+
				"did not move (still %v, was %v before the attach): a session being "+
				"typed in reads as idle in the listing and loses the unnamed-command "+
				"lookup to one nobody is using", sess.LastActive(), before)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
