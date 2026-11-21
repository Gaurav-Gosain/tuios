package session

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
)

// DaemonHandshake is what a daemon reports about itself during the hello
// exchange.
type DaemonHandshake struct {
	// Protocol is the verb protocol version the daemon speaks.
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
	// PaneGrants says the daemon holds calls from panes to their grants and
	// takes a pane's token with pane-grants. False for a daemon from before
	// pane grants.
	PaneGrants bool `json:"pane_grants"`
	// OS is the daemon's GOOS. Empty for a daemon from before it was sent.
	OS string `json:"os"`
}

// ProtocolMismatchError reports that the running daemon cannot speak the control
// protocol this client uses. It is the specific, actionable form of what would
// otherwise surface as "connection reset by peer".
type ProtocolMismatchError struct {
	// ClientVersion is this binary's version, when the caller supplied one.
	ClientVersion string
	// DaemonVersion is the running daemon's version, when it is known.
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
	b.WriteString("The running TUIOS daemon does not speak this client's control protocol")

	switch {
	case e.DaemonVersion != "" && e.ClientVersion != "":
		fmt.Fprintf(&b, " (daemon %s, client %s)", e.DaemonVersion, e.ClientVersion)
	case e.DaemonVersion != "":
		fmt.Fprintf(&b, " (daemon %s)", e.DaemonVersion)
	case e.ClientVersion != "":
		fmt.Fprintf(&b, " (client %s)", e.ClientVersion)
	}
	if e.DaemonProtocol > 0 && e.ClientProtocol > 0 {
		fmt.Fprintf(&b, ", protocol %d against %d", e.DaemonProtocol, e.ClientProtocol)
	}

	if e.DaemonProtocol > e.ClientProtocol && e.ClientProtocol > 0 {
		b.WriteString(".\nMost likely cause: the daemon is newer than this binary, so an older tuios is on the path.")
		b.WriteString("\nFix: upgrade tuios, or run 'tuios kill-server' and start again with the version you want.")
	} else {
		b.WriteString(".\nMost likely cause: TUIOS was upgraded while the daemon kept running, so the old daemon is still serving the socket.")
		b.WriteString("\nFix: run 'tuios kill-server', then run this command again.")
	}
	if e.Sessions > 0 {
		fmt.Fprintf(&b, "\nNote: the daemon is holding %s. %s saved and restored when it restarts (see 'tuios resurrect').", plural.Count(e.Sessions, "session"), plural.Word(e.Sessions, "It is", "They are"))
	}
	if e.DaemonPID > 0 {
		fmt.Fprintf(&b, "\nDaemon PID: %d.", e.DaemonPID)
	}
	return b.String()
}

func (e *ProtocolMismatchError) Unwrap() error { return e.Cause }

func (c *VerbClient) handshake(clientVersion string) (*DaemonHandshake, error) {
	termType, colorTerm := helloTerm()
	params := map[string]any{
		"client":     "tuios",
		"version":    clientVersion,
		"protocol":   VerbProtocolVersion,
		"term":       termType,
		"color_term": colorTerm,
	}
	raw, err := c.Call("hello", params)
	if err == nil {
		var hs DaemonHandshake
		if uerr := jsonx.Unmarshal(raw, &hs); uerr != nil {
			return nil, errfmt.Errorf("failed to decode daemon handshake: %w", uerr)
		}
		return &hs, nil
	}

	if callErr, ok := errors.AsType[*VerbCallError](err); ok {
		switch callErr.Code {
		case ErrVerbUnknownVerb, ErrVerbProtocolMismatch:
			return nil, &ProtocolMismatchError{
				ClientVersion:  clientVersion,
				ClientProtocol: VerbProtocolVersion,
				Cause:          err,
			}
		}
		return nil, err
	}

	if !isConnectionGone(err) {
		return nil, err
	}
	return nil, &ProtocolMismatchError{
		ClientVersion:  clientVersion,
		ClientProtocol: VerbProtocolVersion,
		DaemonPID:      GetDaemonPID(),
		Cause:          err,
	}
}

func isConnectionGone(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "closed network connection")
}
