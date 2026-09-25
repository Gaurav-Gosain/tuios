package session

import (
	"bytes"
	"testing"
)

func TestKeysToBytes(t *testing.T) {
	tests := []struct {
		name    string
		keys    string
		literal bool
		raw     bool
		want    []byte
		wantErr bool
	}{
		{name: "literal text", keys: "echo hi", literal: true, want: []byte("echo hi")},
		{name: "raw text", keys: "a b", raw: true, want: []byte("a b")},
		{name: "enter", keys: "Enter", want: []byte("\r")},
		{name: "named sequence", keys: "h i Enter", want: []byte("hi\r")},
		{name: "comma separated", keys: "Escape,Tab", want: []byte("\x1b\t")},
		{name: "ctrl-c", keys: "ctrl+c", want: []byte{0x03}},
		{name: "ctrl-a", keys: "ctrl+a", want: []byte{0x01}},
		{name: "alt-x", keys: "alt+x", want: []byte{0x1b, 'x'}},
		{name: "arrow up", keys: "Up", want: []byte("\x1b[A")},
		{name: "prefix rejected", keys: "PREFIX", wantErr: true},
		{name: "bad ctrl", keys: "ctrl+shift", wantErr: true},
		{name: "empty", keys: "   ", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := keysToBytes(tc.keys, tc.literal, tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Errorf("keysToBytes(%q) = %v, want %v", tc.keys, got, tc.want)
			}
		})
	}
}

// newTestDaemonSession builds a daemon with one detached (no-client) session.
func newTestDaemonSession(t *testing.T) (*Daemon, *Session) {
	t.Helper()
	d := NewDaemon(&DaemonConfig{})
	sess, err := d.manager.CreateSession("headless", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	t.Cleanup(d.manager.Shutdown)
	return d, sess
}
