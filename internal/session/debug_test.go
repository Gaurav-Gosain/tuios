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
