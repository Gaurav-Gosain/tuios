package session

import (
	"bytes"
	"testing"
)

// TestHandoffDaemonRecordWithoutFallbackFD reads a daemon record written by an
// image that had no fallback_fd. The field is a descriptor, and zero is stdin:
// read as zero, the adopting image would take stdin for the old binary and
// close it.
func TestHandoffDaemonRecordWithoutFallbackFD(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record string
		want   int
	}{
		{"missing", `{"fallback":"/usr/bin/tuios","lock_fd":-1}`, -1},
		{"set", `{"fallback":"/proc/self/fd/7","fallback_fd":7,"lock_fd":-1}`, 7},
		{"zero", `{"fallback_fd":0,"lock_fd":-1}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			mw, err := newManifestWriter(&buf)
			if err != nil {
				t.Fatal(err)
			}
			if err := mw.json(handoffRecHeader, handoffHeader{Version: HandoffVersion}); err != nil {
				t.Fatal(err)
			}
			if err := mw.record(handoffRecDaemon, []byte(tc.record)); err != nil {
				t.Fatal(err)
			}
			if _, err := mw.finish(); err != nil {
				t.Fatal(err)
			}
			m, err := readHandoffManifest(&buf)
			if err != nil {
				t.Fatal(err)
			}
			if m.Daemon.FallbackFD != tc.want {
				t.Fatalf("fallback_fd read as %d, want %d", m.Daemon.FallbackFD, tc.want)
			}
		})
	}
}
