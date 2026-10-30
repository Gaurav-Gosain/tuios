package session

import (
	"bytes"
	"strings"
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

// TestHandoffManifestV1Refused reads a manifest from v0.11.1, whose header
// says version 1 and whose daemon record is gob. Main once wrote a JSON daemon
// record under the same number, and an image reading the gob one as JSON
// failed with "invalid character". The header must refuse it first, and say
// to restart.
func TestHandoffManifestV1Refused(t *testing.T) {
	var buf bytes.Buffer
	mw, err := newManifestWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := mw.json(handoffRecHeader, handoffHeader{Version: 1, Writer: "0.11.1"}); err != nil {
		t.Fatal(err)
	}
	gobDaemon, err := encodeGob(struct{ SocketPath string }{"/tmp/tuios.sock"})
	if err != nil {
		t.Fatal(err)
	}
	if err := mw.record(handoffRecDaemon, gobDaemon); err != nil {
		t.Fatal(err)
	}
	if _, err := mw.finish(); err != nil {
		t.Fatal(err)
	}
	_, err = readHandoffManifest(&buf)
	if err == nil || !strings.Contains(err.Error(), "handoff v1") || !strings.Contains(err.Error(), "restart-server") {
		t.Fatalf("a v1 manifest read as %v, want a refusal of v1 that says to restart", err)
	}
}
