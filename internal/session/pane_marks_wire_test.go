package session

import (
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

// dialPaneMarkClient attaches a raw client to session, with a hello that asks
// for pane marks or not, as a client from before them asks for none. It
// returns the client and whether the welcome offered them.
func dialPaneMarkClient(t *testing.T, socketPath, session string, asks bool) (*boundsClient, bool) {
	t.Helper()
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	c := &boundsClient{conn: conn}
	c.send(t, MsgHello, &HelloPayload{Version: "test", PreferredCodec: "gob", Protocol: ProtocolVersion, PaneMarks: asks})
	var welcome WelcomePayload
	if err := c.await(t, MsgWelcome).ParsePayload(&welcome); err != nil {
		t.Fatalf("parse welcome: %v", err)
	}
	c.send(t, MsgAttach, &AttachPayload{SessionName: session, Width: 120, Height: 40})
	c.await(t, MsgAttached)
	return c, welcome.PaneMarks
}

// paneMarksWithin reads every frame for d and returns the pane marks among
// them.
func paneMarksWithin(t *testing.T, c *boundsClient, d time.Duration) []PaneMarkPayload {
	t.Helper()
	var marks []PaneMarkPayload
	_ = c.conn.SetReadDeadline(time.Now().Add(d))
	for {
		msg, err := ReadMessage(c.conn)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				return marks
			}
			t.Fatalf("read: %v", err)
		}
		if msg.Type != MsgPaneMark {
			continue
		}
		var p PaneMarkPayload
		if err := msg.ParsePayload(&p); err != nil {
			t.Fatalf("parse pane mark: %v", err)
		}
		marks = append(marks, p)
	}
}

// awaitPaneMark writes cmd to the pane until a pane mark carrying want
// arrives, and fails after the deadline.
func awaitPaneMark(t *testing.T, c *boundsClient, pty *PTY, cmd string, want uint32) PaneMarkPayload {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second * testDeadlineScale)
	for time.Now().Before(deadline) {
		if _, err := pty.Write([]byte(cmd)); err != nil {
			t.Fatalf("write to pane: %v", err)
		}
		for _, m := range paneMarksWithin(t, c, 300*time.Millisecond) {
			if m.Marks&want != 0 {
				return m
			}
		}
	}
	t.Fatalf("no pane mark with bits %b arrived", want)
	return PaneMarkPayload{}
}

// TestPaneMarkWire is the pane mark push (pane_marks.go) on the wire, against
// a client from before it and a client of another session.
//
// A client that asks for marks and does not stream a pane hears its first
// output and its first bell, each once, and nothing more until a client
// subscribes to the pane or drops it. A client whose hello does not ask for
// marks is sent none: it would not know the message. A client attached to
// another session is sent none: it cannot see the pane.
//
// NEGATIVE CONTROL: with the paneMarks check dropped from paneMarkReader, the
// old client is sent a mark and the test fails. With the sessionID check
// dropped, the other session's client is. With the rearm dropped from
// handleUnsubscribePTY, the mark after the drop never arrives.
func TestPaneMarkWire(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	d, sp := startTestDaemon(t)
	mine := makeSessionWithWindow(t, d, "mine")
	makeSessionWithWindow(t, d, "other")
	pty := mine.GetPTY(mine.GetState().Windows[0].PTYID)
	if pty == nil {
		t.Fatal("the session's window has no pane")
	}

	reader, offered := dialPaneMarkClient(t, sp, "mine", true)
	if !offered {
		t.Fatal("the daemon's welcome did not offer PaneMarks")
	}
	old, _ := dialPaneMarkClient(t, sp, "mine", false)
	stranger, _ := dialPaneMarkClient(t, sp, "other", true)

	m := awaitPaneMark(t, reader, pty, "echo out\n", PaneMarkOutput)
	if m.PTYID != pty.ID || m.WindowID == "" {
		t.Fatalf("the mark names pane %q window %q, want pane %q and its window", m.PTYID, m.WindowID, pty.ID)
	}

	// Output again sends nothing: the edge was reported.
	if _, err := pty.Write([]byte("echo again\n")); err != nil {
		t.Fatal(err)
	}
	for _, m := range paneMarksWithin(t, reader, time.Second) {
		if m.Marks&PaneMarkOutput != 0 {
			t.Fatalf("a second output mark arrived before any subscription changed: %+v", m)
		}
	}

	// The bell has a bit of its own, so its first ring is still news.
	awaitPaneMark(t, reader, pty, "printf '\\a'\n", PaneMarkBell)

	// The reader looks at the pane, then stops. The next output is news again.
	// While it streams the pane, the pane's output and bell are not marked
	// for it, and they set the pane's bits.
	reader.send(t, MsgSubscribePTY, &SubscribePTYPayload{PTYID: pty.ID})
	reader.send(t, MsgPing, nil)
	reader.await(t, MsgPong)
	if _, err := pty.Write([]byte("echo seen; printf '\\a'\n")); err != nil {
		t.Fatal(err)
	}
	if marks := paneMarksWithin(t, reader, time.Second); len(marks) != 0 {
		t.Fatalf("a client that streams the pane was sent %d pane marks for it: %+v", len(marks), marks)
	}
	reader.send(t, MsgUnsubscribePTY, &UnsubscribePTYPayload{PTYID: pty.ID})
	reader.send(t, MsgPing, nil)
	reader.await(t, MsgPong)
	awaitPaneMark(t, reader, pty, "echo back\n", PaneMarkOutput)

	if marks := paneMarksWithin(t, old, 500*time.Millisecond); len(marks) != 0 {
		t.Errorf("a client whose hello did not ask for pane marks was sent %d: %+v", len(marks), marks)
	}
	// The other session's own pane may print its prompt, and that mark is
	// the stranger's to hear. A mark for this session's pane is not.
	for _, m := range paneMarksWithin(t, stranger, 500*time.Millisecond) {
		if m.PTYID == pty.ID {
			t.Errorf("a client of another session was sent a pane mark for this one: %+v", m)
		}
	}
}
