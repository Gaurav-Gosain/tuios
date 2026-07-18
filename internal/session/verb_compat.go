package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
	"time"
)

// This file detects the one failure that has no good error message anywhere
// else: a newly upgraded CLI talking to a daemon that is still running the old
// binary.
//
// The JSON verb protocol distinguishes itself from the binary protocol by its
// first byte (see detectJSONClient). A daemon that predates the JSON protocol
// has no such detection: it reads the '{' of the first request line as the high
// byte of a big-endian length prefix, computes an absurd frame length, gives up,
// and closes the connection. The client sees "connection reset by peer" or a
// bare EOF, which explains nothing and suggests nothing.
//
// So the client opens with a hello handshake. Three outcomes tell it everything:
//
//   - a hello result: the daemon speaks this protocol, and reports its version.
//   - unknown_verb: a JSON-speaking daemon from before the handshake existed.
//     Usable, but old; its version comes from the legacy binary handshake.
//   - the connection dies with nothing read: a daemon from before the JSON
//     protocol. Its version also comes from the legacy binary handshake, which
//     every daemon since the beginning answers.
//
// Only the last case is fatal, and it is reported as a ProtocolMismatchError
// carrying both versions and the command that fixes it.

// clientHandshakeTimeout bounds the handshake exchange. It is short because the
// daemon is a local process on a unix socket: anything slower is a hang, and a
// hang here would stall every CLI command.
const clientHandshakeTimeout = 5 * time.Second

// DaemonHandshake is what a daemon reports about itself during the hello
// exchange.
type DaemonHandshake struct {
	// Protocol is the verb protocol version the daemon speaks. Zero means the
	// daemon answered unknown_verb, i.e. it predates the handshake verb.
	Protocol int `json:"protocol"`
	// MinProtocol is the oldest protocol version the daemon still serves.
	MinProtocol int `json:"min_protocol"`
	// DaemonVersion is the daemon's build version.
	DaemonVersion string `json:"daemon_version"`
	// PID is the daemon process id, so a human can find it.
	PID int `json:"pid"`
	// Sessions is how many sessions the daemon currently holds, which is what a
	// user wants to know before being told to restart it.
	Sessions int `json:"sessions"`
}

// ProtocolMismatchError reports that the running daemon cannot speak the control
// protocol this client uses. It is the specific, actionable form of what would
// otherwise surface as "connection reset by peer".
type ProtocolMismatchError struct {
	// ClientVersion is this binary's version, when the caller supplied one.
	ClientVersion string
	// DaemonVersion is the running daemon's version, when it could be learned
	// from the legacy handshake. Empty when even that failed.
	DaemonVersion string
	// DaemonPID identifies the process to restart, when known.
	DaemonPID int
	// Sessions is how many sessions the daemon holds, when known, so the
	// message can say what restarting it costs.
	Sessions int
	// ClientProtocol and DaemonProtocol are the two protocol versions. A
	// DaemonProtocol of 0 means the daemon does not speak the JSON protocol at
	// all.
	ClientProtocol int
	DaemonProtocol int
	// Cause is the underlying transport or protocol error, kept for wrapping.
	Cause error
}

func (e *ProtocolMismatchError) Error() string {
	var b strings.Builder
	b.WriteString("the running TUIOS daemon does not speak this CLI's control protocol")

	switch {
	case e.DaemonVersion != "" && e.ClientVersion != "":
		fmt.Fprintf(&b, " (daemon %s, CLI %s)", e.DaemonVersion, e.ClientVersion)
	case e.DaemonVersion != "":
		fmt.Fprintf(&b, " (daemon %s)", e.DaemonVersion)
	case e.ClientVersion != "":
		fmt.Fprintf(&b, " (CLI %s)", e.ClientVersion)
	}

	b.WriteString(".\nMost likely cause: TUIOS was upgraded while the daemon kept running, so the old daemon is still serving the socket.")
	b.WriteString("\nFix: run 'tuios kill-server', then run this command again.")
	if e.Sessions > 0 {
		fmt.Fprintf(&b, "\nNote: the daemon is holding %d session(s); they are saved and restored when it restarts (see 'tuios resurrect').", e.Sessions)
	}
	if e.DaemonPID > 0 {
		fmt.Fprintf(&b, "\nDaemon PID: %d.", e.DaemonPID)
	}
	return b.String()
}

func (e *ProtocolMismatchError) Unwrap() error { return e.Cause }

// handshake performs the hello exchange on a freshly dialed verb connection.
//
// It returns a *ProtocolMismatchError only when the daemon cannot speak the JSON
// protocol at all. A daemon that answers unknown_verb is older than the
// handshake but still fully usable, so that returns a zero-protocol handshake
// and no error: refusing to talk to it would break the compatibility the verb
// protocol promises.
func (c *VerbClient) handshake(clientVersion string) (*DaemonHandshake, error) {
	raw, err := c.Call("hello", map[string]any{
		"client":   "tuios",
		"version":  clientVersion,
		"protocol": VerbProtocolVersion,
	})
	if err == nil {
		var hs DaemonHandshake
		if uerr := json.Unmarshal(raw, &hs); uerr != nil {
			return nil, fmt.Errorf("failed to decode daemon handshake: %w", uerr)
		}
		return &hs, nil
	}

	var callErr *VerbCallError
	if errors.As(err, &callErr) {
		switch callErr.Code {
		case ErrVerbUnknownVerb:
			// A JSON daemon from before the handshake verb. Usable as-is.
			return &DaemonHandshake{}, nil
		case ErrVerbProtocolMismatch:
			// The daemon understood us and refused: it already knows both
			// versions, so report exactly what it said.
			return nil, &ProtocolMismatchError{
				ClientVersion:  clientVersion,
				ClientProtocol: VerbProtocolVersion,
				Cause:          err,
			}
		}
		// Any other error envelope means the daemon is speaking the protocol
		// fine and simply disliked this call; that is not a mismatch.
		return nil, err
	}

	if !isConnectionGone(err) {
		return nil, err
	}

	// The connection died without a response line, which is exactly what a
	// pre-JSON daemon does with a request line. Learn its version over the
	// legacy binary handshake, which every daemon has always answered.
	mismatch := &ProtocolMismatchError{
		ClientVersion:  clientVersion,
		ClientProtocol: VerbProtocolVersion,
		DaemonPID:      GetDaemonPID(),
		Cause:          err,
	}
	if legacy, lerr := probeLegacyDaemon(clientVersion); lerr == nil {
		mismatch.DaemonVersion = legacy.Version
		mismatch.Sessions = len(legacy.SessionNames)
	}
	return nil, mismatch
}

// isConnectionGone reports whether err means the peer hung up or reset the
// connection rather than answering. Those are the shapes a pre-JSON daemon
// produces when it chokes on a request line.
func isConnectionGone(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) {
		return true
	}
	// Fall back to the message for platforms and wrappers that do not expose a
	// comparable sentinel.
	msg := err.Error()
	return strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "closed network connection")
}

// probeLegacyDaemon opens a second connection and performs the binary hello
// handshake, which every daemon version understands, to learn the running
// daemon's version. It is only used to enrich a mismatch error, so any failure
// simply means the version stays unknown.
func probeLegacyDaemon(clientVersion string) (*WelcomePayload, error) {
	socketPath, err := GetSocketPath()
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout("unix", socketPath, clientHandshakeTimeout)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	msg, err := NewMessage(MsgHello, &HelloPayload{
		Version:        clientVersion,
		Term:           "dumb",
		Width:          80,
		Height:         24,
		PreferredCodec: "gob",
	})
	if err != nil {
		return nil, err
	}

	_ = conn.SetWriteDeadline(time.Now().Add(clientHandshakeTimeout))
	if err := WriteMessage(conn, msg); err != nil {
		return nil, err
	}

	_ = conn.SetReadDeadline(time.Now().Add(clientHandshakeTimeout))
	resp, _, err := ReadMessageWithCodec(conn)
	if err != nil {
		return nil, err
	}
	if resp.Type != MsgWelcome {
		return nil, fmt.Errorf("expected welcome, got message type %d", resp.Type)
	}

	var welcome WelcomePayload
	if err := resp.ParsePayload(&welcome); err != nil {
		return nil, err
	}
	return &welcome, nil
}
