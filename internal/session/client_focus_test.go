package session

import (
	"net"
	"testing"
	"time"
)

// MsgClientFocus on the wire. The ways this could fail, written down before
// the test: the client sends a payload the daemon's handler cannot read, or
// the wrong focus.
func TestClientFocusReachesTheDaemon(t *testing.T) {
	for _, tc := range []struct {
		name    string
		focused bool
	}{
		{name: "focus in", focused: true},
		{name: "focus out", focused: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clientEnd, daemonEnd := net.Pipe()
			defer clientEnd.Close()
			defer daemonEnd.Close()
			c := NewTUIClient()
			c.conn = clientEnd

			sent := make(chan error, 1)
			go func() { sent <- c.ReportHostFocus(tc.focused) }()

			_ = daemonEnd.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
			msg, err := ReadMessage(daemonEnd)
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
