package session

import (
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

// Wire compatibility of MsgClientFocus. A daemon from before it refuses the
// type with an error message, which the client would show to the person, so
// the client must send it only to a daemon whose welcome offered it.
//
// The ways this could fail, written down before the test:
//   - the client sends the report to a daemon that did not offer it;
//   - the client sends it to one that did, but with a payload the daemon's
//     handler cannot read, or the wrong focus;
//   - a report made before the daemon was known to support it is lost, so the
//     next one carries a stale value.
func TestClientFocusIsSentOnlyToADaemonThatOffersIt(t *testing.T) {
	for _, tc := range []struct {
		name    string
		offered bool
		focused bool
	}{
		{name: "older daemon, focus in", offered: false, focused: true},
		{name: "older daemon, focus out", offered: false, focused: false},
		{name: "daemon offers it, focus in", offered: true, focused: true},
		{name: "daemon offers it, focus out", offered: true, focused: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clientEnd, daemonEnd := net.Pipe()
			defer clientEnd.Close()
			defer daemonEnd.Close()
			c := NewTUIClient()
			c.conn = clientEnd
			c.focusSupported = tc.offered

			sent := make(chan error, 1)
			go func() { sent <- c.ReportHostFocus(tc.focused) }()

			_ = daemonEnd.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
			msg, err := ReadMessage(daemonEnd)
			if !tc.offered {
				if err == nil {
					t.Fatalf("the client sent message %s to a daemon that did not offer it", MessageTypeName(msg.Type))
				}
				if !errors.Is(err, os.ErrDeadlineExceeded) {
					t.Fatalf("read: %v", err)
				}
				if err := <-sent; err != nil {
					t.Fatalf("ReportHostFocus: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("the daemon received nothing: %v", err)
			}
			if err := <-sent; err != nil {
				t.Fatalf("ReportHostFocus: %v", err)
			}
			if msg.Type != MsgClientFocus {
				t.Fatalf("got %s, want ClientFocus", MessageTypeName(msg.Type))
			}
			d := &Daemon{}
			cs := &connState{}
			if err := d.handleClientFocus(cs, msg); err != nil {
				t.Fatalf("the daemon could not read the report: %v", err)
			}
			want := focusOut
			if tc.focused {
				want = focusIn
			}
			if cs.hostFocus != want {
				t.Fatalf("the daemon recorded %d, want %d", cs.hostFocus, want)
			}
		})
	}
}
