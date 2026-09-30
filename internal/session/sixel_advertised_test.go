package session

import (
	"net"
	"testing"
	"time"
)

// dialGraphicsClient attaches a raw client whose hello states its terminal's
// graphics.
func dialGraphicsClient(t *testing.T, socketPath, session string, sixel, kitty bool) *boundsClient {
	t.Helper()
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	c := &boundsClient{conn: conn}
	c.send(t, MsgHello, &HelloPayload{Version: "test", PreferredCodec: "gob", Protocol: ProtocolVersion,
		SixelGraphics: sixel, KittyGraphics: kitty})
	c.await(t, MsgWelcome)
	c.send(t, MsgAttach, &AttachPayload{SessionName: session, CreateNew: true, Width: 120, Height: 40})
	c.await(t, MsgAttached)
	return c
}

func waitSixelAdvertised(t *testing.T, d *Daemon, name string, want bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second * testDeadlineScale)
	for {
		s := d.manager.GetSession(name)
		if s != nil && s.SixelAdvertised() == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: sixel advertised = %v, want %v", what, !want, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestSixelAdvertisedFollowsAttachedClients: a pane is told it can draw sixel
// while any attached client's terminal draws it, and not while only terminals
// without it are attached. With nobody attached the last answer stands.
func TestSixelAdvertisedFollowsAttachedClients(t *testing.T) {
	d, socketPath := startTestDaemon(t)

	plain := dialGraphicsClient(t, socketPath, "img", false, false)
	waitSixelAdvertised(t, d, "img", false, "a plain client alone")

	sixel := dialGraphicsClient(t, socketPath, "img", true, false)
	waitSixelAdvertised(t, d, "img", true, "a sixel client beside a plain one")

	sixel.send(t, MsgDetach, struct{}{})
	waitSixelAdvertised(t, d, "img", false, "the sixel client left")

	again := dialGraphicsClient(t, socketPath, "img", true, false)
	waitSixelAdvertised(t, d, "img", true, "a sixel client again")

	plain.send(t, MsgDetach, struct{}{})
	again.send(t, MsgDetach, struct{}{})
	time.Sleep(50 * time.Millisecond)
	waitSixelAdvertised(t, d, "img", true, "nobody attached keeps the last answer")
}
