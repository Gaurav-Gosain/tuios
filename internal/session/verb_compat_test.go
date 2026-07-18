package session

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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
	runtimeDir := t.TempDir()
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
		msg, _, err := ReadMessageWithCodec(conn)
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
			Codec:        "gob",
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

// TestHandshakeAgainstLegacyDaemonReportsMismatch is the regression test for the
// bug this work exists to kill: a new CLI against an old still-running daemon
// used to fail with a bare "failed to read response: connection reset by peer".
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

	if mismatch.DaemonVersion != "0.9.0" {
		t.Errorf("daemon version = %q, want 0.9.0 (learned over the legacy handshake)", mismatch.DaemonVersion)
	}
	if mismatch.ClientVersion != "1.4.0" {
		t.Errorf("client version = %q, want 1.4.0", mismatch.ClientVersion)
	}
	if mismatch.Sessions != 2 {
		t.Errorf("sessions = %d, want 2", mismatch.Sessions)
	}

	// The message is the whole point: it must say what failed, why, and the
	// exact command that fixes it, naming both versions.
	msg := mismatch.Error()
	for _, want := range []string{
		"does not speak this CLI's control protocol",
		"daemon 0.9.0",
		"CLI 1.4.0",
		"upgraded while the daemon kept running",
		"tuios kill-server",
		"2 session(s)",
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

// TestHandshakeAgainstCurrentDaemonSucceeds pins the happy path: a current
// daemon answers hello with its protocol range and identity.
func TestHandshakeAgainstCurrentDaemonSucceeds(t *testing.T) {
	startTestDaemon(t)

	client, err := DialVerbClientAs("1.4.0")
	if err != nil {
		t.Fatalf("handshake against the current daemon failed: %v", err)
	}
	defer func() { _ = client.Close() }()

	hs := client.Daemon()
	if hs == nil {
		t.Fatal("expected a handshake result")
	}
	if hs.Protocol != VerbProtocolVersion {
		t.Errorf("protocol = %d, want %d", hs.Protocol, VerbProtocolVersion)
	}
	if hs.MinProtocol != MinVerbProtocolVersion {
		t.Errorf("min protocol = %d, want %d", hs.MinProtocol, MinVerbProtocolVersion)
	}
	if hs.DaemonVersion != "test" {
		t.Errorf("daemon version = %q, want test", hs.DaemonVersion)
	}
	if hs.PID != os.Getpid() {
		t.Errorf("pid = %d, want %d", hs.PID, os.Getpid())
	}
}

// TestHelloRejectsNewerClientProtocol covers the other direction of the
// handshake: a daemon that is asked for a protocol it does not implement says so
// with the protocol_mismatch code and the kill-server remedy, instead of
// accepting the call and misbehaving later.
func TestHelloRejectsNewerClientProtocol(t *testing.T) {
	_, socketPath := startTestDaemon(t)
	c := dialVerb(t, socketPath)

	resp := c.call(t, `{"id":1,"verb":"hello","params":{"client":"tuios","version":"9.9.9","protocol":99}}`)
	e := errorOf(t, resp)

	if code, _ := e["code"].(string); code != ErrVerbProtocolMismatch {
		t.Fatalf("code = %v, want %s", e["code"], ErrVerbProtocolMismatch)
	}
	hint, ok := e["hint"].(map[string]any)
	if !ok {
		t.Fatalf("expected a hint on a protocol mismatch, got %v", e)
	}
	if cmd, _ := hint["command"].(string); cmd != "tuios kill-server" {
		t.Errorf("hint command = %v, want tuios kill-server", hint["command"])
	}
	if detail, _ := hint["detail"].(string); !strings.Contains(detail, "9.9.9") {
		t.Errorf("hint detail should name the client version, got %v", hint["detail"])
	}
}

// TestHandshakeToleratesDaemonWithoutHelloVerb proves the handshake is a
// compatibility check and not a new requirement: a daemon that speaks the verb
// protocol but has never heard of hello answers unknown_verb, and the client
// treats it as usable rather than refusing to talk to it.
func TestHandshakeToleratesDaemonWithoutHelloVerb(t *testing.T) {
	startTestDaemon(t)

	// Temporarily remove the hello verb from the registry to reproduce a daemon
	// built before the handshake existed.
	entry := verbRegistry["hello"]
	delete(verbRegistry, "hello")
	t.Cleanup(func() { verbRegistry["hello"] = entry })

	client, err := DialVerbClientAs("1.4.0")
	if err != nil {
		t.Fatalf("a daemon without the hello verb must still be usable, got: %v", err)
	}
	defer func() { _ = client.Close() }()

	if hs := client.Daemon(); hs == nil || hs.Protocol != 0 {
		t.Errorf("expected a zero-protocol handshake for a pre-handshake daemon, got %+v", hs)
	}

	// And it must actually work, not just connect.
	if _, err := client.Call("list-sessions", nil); err != nil {
		t.Errorf("list-sessions against a pre-handshake daemon failed: %v", err)
	}
}

// TestDialVerbClientReportsMissingDaemon checks the plain "nothing is listening"
// case still produces a connect error rather than a mismatch, so the two states
// stay distinguishable to the CLI.
func TestDialVerbClientReportsMissingDaemon(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	_, err := DialVerbClientAs("1.4.0")
	if err == nil {
		t.Fatal("expected an error dialing a socket that does not exist")
	}
	var mismatch *ProtocolMismatchError
	if errors.As(err, &mismatch) {
		t.Fatalf("a missing daemon must not be reported as a protocol mismatch: %v", err)
	}
	if !strings.Contains(err.Error(), "failed to connect to daemon") {
		t.Errorf("error = %q, want a connect failure", err)
	}
}

// TestProbeLegacyDaemonIgnoresStaleSocket makes sure the version probe cannot
// hang or panic when the socket file exists but nothing is listening, which is
// the stale-socket state the CLI also has to explain.
func TestProbeLegacyDaemonIgnoresStaleSocket(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)

	socketPath, err := GetSocketPath()
	if err != nil {
		t.Fatalf("GetSocketPath: %v", err)
	}
	if err := os.WriteFile(socketPath, nil, 0o600); err != nil {
		t.Fatalf("write stale socket: %v", err)
	}
	if filepath.Dir(socketPath) != filepath.Join(runtimeDir, "tuios") {
		t.Fatalf("socket path %q escaped the isolated runtime dir", socketPath)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = probeLegacyDaemon("1.4.0")
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("probeLegacyDaemon hung on a stale socket")
	}
}

// TestVerbCallErrorCarriesHint checks the hint survives the client decode, so
// the CLI can render the remedy the daemon named rather than inventing one.
func TestVerbCallErrorCarriesHint(t *testing.T) {
	d, _ := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")

	client, err := DialVerbClientAs("test")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	_, err = client.Call("list-windows", map[string]any{"session": "wrok"})
	if err == nil {
		t.Fatal("expected an error for an unknown session")
	}
	var callErr *VerbCallError
	if !errors.As(err, &callErr) {
		t.Fatalf("expected a *VerbCallError, got %T", err)
	}
	if callErr.Code != ErrVerbSessionNotFound {
		t.Errorf("code = %q, want %q", callErr.Code, ErrVerbSessionNotFound)
	}
	if callErr.Hint == nil {
		t.Fatal("hint did not survive the client decode")
	}
	if callErr.Hint.DidYouMean != "work" {
		t.Errorf("did_you_mean = %q, want work", callErr.Hint.DidYouMean)
	}
	if !slices.Contains(callErr.Hint.Available, "work") {
		t.Errorf("available = %v, want it to contain work", callErr.Hint.Available)
	}
}
