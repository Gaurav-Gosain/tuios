package session

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestKittyQueryResponse pins the daemon's answer to each of the probes kitten
// icat opens with, for a pane on this machine and one on another, with and
// without a client drawing the session from another machine.
//
// The rule it holds: direct transmission is always OK, and a medium that sends
// a path is OK only when the path will be read on the machine it names a file
// on. Anything else is refused so the guest streams the bytes instead.
func TestKittyQueryResponse(t *testing.T) {
	probes := []struct {
		name string
		apc  string
	}{
		{"direct", "a=q,f=24,s=1,v=1,S=3,i=1;MTIz"},
		{"temp file", "a=q,f=24,t=t,s=1,v=1,S=10,i=2;L3RtcC9hLnBuZw"},
		{"shared memory", "a=q,f=24,t=s,s=1,v=1,S=18,i=3;aWNhdC1KWkhHTDJDNlhNQVFP"},
		{"file", "a=q,f=24,t=f,s=1,v=1,i=4;L3RtcC9hLnBuZw"},
	}
	refused := func(id string) string {
		return "\x1b_Gi=" + id + ";" + kittyFileMediumRefusal + "\x1b\\"
	}
	ok := func(id string) string { return "\x1b_Gi=" + id + ";OK\x1b\\" }

	tests := []struct {
		name         string
		remotePane   bool
		linkedViewer bool
		want         []string // per probe, in order
	}{
		{"local pane, local clients", false, false, []string{ok("1"), ok("2"), ok("3"), ok("4")}},
		{"pane on another machine", true, false, []string{ok("1"), refused("2"), refused("3"), refused("4")}},
		{"client on another machine", false, true, []string{ok("1"), refused("2"), refused("3"), refused("4")}},
		{"both", true, true, []string{ok("1"), refused("2"), refused("3"), refused("4")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Session{}
			s.SetLinkedViewer(tt.linkedViewer)
			for i, p := range probes {
				cmd, err := vt.ParseKittyCommand([]byte(p.apc))
				if err != nil || cmd == nil {
					t.Fatalf("%s: parse: %v", p.name, err)
				}
				if got := string(s.kittyQueryResponse(cmd, tt.remotePane)); got != tt.want[i] {
					t.Errorf("%s: answered %q, want %q", p.name, got, tt.want[i])
				}
			}
		})
	}
}

// TestRefreshLinkedViewer checks the daemon marks a session as drawn from
// another machine exactly while a TUI client that arrived over a link is
// attached to it, and not because of one attached to a different session.
func TestRefreshLinkedViewer(t *testing.T) {
	m := NewManager()
	m.SetSocketPath(t.TempDir() + "/sock")
	s, err := m.CreateSession("kittyq", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer s.Stop()
	d := &Daemon{manager: m, clients: map[string]*connState{}}

	d.clients["local"] = &connState{clientID: "local", sessionID: s.ID, isTUIClient: true}
	d.clients["linked-elsewhere"] = &connState{clientID: "linked-elsewhere", sessionID: "another", isTUIClient: true, viaLink: true}
	d.refreshLinkedViewer(s.ID)
	if s.linkedViewer.Load() {
		t.Fatal("marked as drawn from another machine by a local client and another session's linked one")
	}

	d.clients["linked"] = &connState{clientID: "linked", sessionID: s.ID, isTUIClient: true, viaLink: true}
	d.refreshLinkedViewer(s.ID)
	if !s.linkedViewer.Load() {
		t.Fatal("a client attached over a link is drawing the session, and it is not marked")
	}

	delete(d.clients, "linked")
	d.refreshLinkedViewer(s.ID)
	if s.linkedViewer.Load() {
		t.Fatal("still marked after the linked client left")
	}
}

// TestKittyQueryResponseQuiet checks q= is honoured the way kitty honours it.
// The daemon used to answer OK whatever the guest asked for, q=2 included.
func TestKittyQueryResponseQuiet(t *testing.T) {
	tests := []struct {
		name       string
		apc        string
		remotePane bool
		want       string
	}{
		{"q=1 suppresses OK", "a=q,q=1,i=1,s=1,v=1,f=24;AAAA", false, ""},
		{"q=1 still sends an error", "a=q,q=1,t=t,i=2,s=1,v=1,f=24;L3RtcC9hLnBuZw", true, "\x1b_Gi=2;" + kittyFileMediumRefusal + "\x1b\\"},
		{"q=2 suppresses OK", "a=q,q=2,i=1,s=1,v=1,f=24;AAAA", false, ""},
		{"q=2 suppresses an error", "a=q,q=2,t=t,i=2,s=1,v=1,f=24;L3RtcC9hLnBuZw", true, ""},
		{"no id is still answered", "a=q,s=1,v=1,f=24;AAAA", false, "\x1b_GOK\x1b\\"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := vt.ParseKittyCommand([]byte(tt.apc))
			if err != nil || cmd == nil {
				t.Fatalf("parse: %v", err)
			}
			s := &Session{}
			if got := string(s.kittyQueryResponse(cmd, tt.remotePane)); got != tt.want {
				t.Errorf("answered %q, want %q", got, tt.want)
			}
		})
	}
}
