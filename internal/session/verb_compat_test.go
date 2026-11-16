package session

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// legacyDaemon is a faithful stand-in for a daemon built before the JSON verb
// protocol existed. It reproduces the only two behaviors that matter for
// compatibility detection:
//
//   - it answers the binary hello handshake with a welcome carrying its version,
//     which is how a new client learns what is actually running, and
//   - it reads every connection as binary frames, so a JSON request line is
//     decoded as a bogus length prefix and the connection is dropped, exactly as
//     the old read loop did on a framing error.
//
// This is deliberately not a mock of the new daemon with a feature switched off:
// it is the old wire behavior, so the test proves the client copes with the real
// upgrade scenario.
type legacyDaemon struct {
	ln       net.Listener
	version  string
	sessions []string
}

func startLegacyDaemon(t *testing.T, version string, sessions ...string) *legacyDaemon {
	t.Helper()

	// Point GetSocketPath at an isolated runtime dir so the fake daemon takes
	// the place of the real one for this test only.
	runtimeDir := testutil.RuntimeDir(t)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	socketPath, err := GetSocketPath()
	if err != nil {
		t.Fatalf("GetSocketPath: %v", err)
	}

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	d := &legacyDaemon{ln: ln, version: version, sessions: sessions}
	t.Cleanup(func() { _ = ln.Close() })

	go d.serve()
	return d
}

func (d *legacyDaemon) serve() {
	for {
		conn, err := d.ln.Accept()
		if err != nil {
			return
		}
		go d.handle(conn)
	}
}

func (d *legacyDaemon) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	for {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		msg, err := ReadMessage(conn)
		if err != nil {
			// The old read loop returned here, closing the connection. A JSON
			// request line lands in exactly this branch: '{' is 0x7b, so the
			// length prefix reads as ~2GB and fails the 16MB sanity check.
			return
		}
		if msg.Type != MsgHello {
			continue
		}
		welcome, err := NewMessage(MsgWelcome, &WelcomePayload{
			Version:      d.version,
			SessionNames: d.sessions,
		})
		if err != nil {
			return
		}
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := WriteMessage(conn, welcome); err != nil {
			return
		}
	}
}

// TestHandshakeAgainstLegacyDaemonReportsMismatch: a new CLI against a daemon
// from before the JSON protocol used to fail with a bare "failed to read
// response: connection reset by peer". It gets a mismatch with the fix.
func TestHandshakeAgainstLegacyDaemonReportsMismatch(t *testing.T) {
	startLegacyDaemon(t, "0.9.0", "work", "notes")

	_, err := DialVerbClientAs("1.4.0")
	if err == nil {
		t.Fatal("expected the handshake against a pre-JSON daemon to fail")
	}

	var mismatch *ProtocolMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("expected a *ProtocolMismatchError, got %T: %v", err, err)
	}

	if mismatch.ClientVersion != "1.4.0" {
		t.Errorf("client version = %q, want 1.4.0", mismatch.ClientVersion)
	}

	// The message is the whole point: it must say what failed, why, and the
	// exact command that fixes it.
	msg := mismatch.Error()
	for _, want := range []string{
		"The running TUIOS daemon does not speak this client's control protocol",
		"client 1.4.0",
		"upgraded while the daemon kept running",
		"tuios kill-server",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("mismatch message missing %q\n--- message ---\n%s", want, msg)
		}
	}

	// It must not surface as the raw transport error a user cannot act on.
	if strings.Contains(msg, "failed to read response") {
		t.Errorf("mismatch message leaked the raw transport error:\n%s", msg)
	}
}
