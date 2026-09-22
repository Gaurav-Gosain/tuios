package session

import (
	"strings"
	"testing"
)

// TestMessageTypeNameCoversEveryType walks every declared message type. The
// protocol log names each frame with MessageTypeName, and a type missing from
// the table used to print as Unknown(N), which is what state syncs, resizes
// and agent mail looked like in a capture.
func TestMessageTypeNameCoversEveryType(t *testing.T) {
	seen := make(map[string]MessageType)
	for mt := MsgHello; mt <= MsgDirListing; mt++ {
		name := MessageTypeName(mt)
		if name == "" || strings.HasPrefix(name, "Unknown") {
			t.Errorf("message type %d has no name, got %q", mt, name)
			continue
		}
		if prev, dup := seen[name]; dup {
			t.Errorf("message types %d and %d share the name %q", prev, mt, name)
		}
		seen[name] = mt
	}
}

// TestMessageTypeNameNamesLiveTraffic pins a few types that are sent on every
// attach, so a table that drifts out of order shows up as a wrong name rather
// than only as a missing one.
func TestMessageTypeNameNamesLiveTraffic(t *testing.T) {
	for _, tc := range []struct {
		mt   MessageType
		want string
	}{
		{MsgHello, "Hello"},
		{MsgStateSync, "StateSync"},
		{MsgSessionResize, "SessionResize"},
		{MsgAgentMail, "AgentMail"},
		{MsgDirListing, "DirListing"},
		{MsgPing, "Reserved(Ping)"},
	} {
		if got := MessageTypeName(tc.mt); got != tc.want {
			t.Errorf("MessageTypeName(%d) = %q, want %q", tc.mt, got, tc.want)
		}
	}
}

// TestMessageTypeNameOutOfRange keeps the fallback for a value no build has
// declared, which is what a peer from a newer build can send.
func TestMessageTypeNameOutOfRange(t *testing.T) {
	for _, mt := range []MessageType{0, MsgDirListing + 1, 255} {
		if got := MessageTypeName(mt); !strings.HasPrefix(got, "Unknown(") {
			t.Errorf("MessageTypeName(%d) = %q, want an Unknown fallback", mt, got)
		}
	}
}
