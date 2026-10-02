package session

import (
	"errors"
	"fmt"
)

// HostConnectionInfo is what the local daemon said about the host as it opened
// the connection. The version and protocol are what the host reported at the
// link handshake, carried for a message, never trusted for a decision.
type HostConnectionInfo struct {
	Host          string `json:"host"`
	DaemonVersion string `json:"daemon_version"`
	Protocol      int    `json:"protocol"`
}

// HostHandshakeError reports a host whose daemon answered the connection and
// then refused the binary handshake, or could not be understood. It is the
// version skew case for an attach: the link is up, the control protocol
// matched, and the attach protocol did not. The message names the machine.
type HostHandshakeError struct {
	Host string
	Info HostConnectionInfo
	Err  error
}

func (e *HostHandshakeError) Error() string {
	if _, ok := errors.AsType[*ProtocolMismatchError](e.Err); ok {
		return fmt.Sprintf("tuios on %s speaks a different attach protocol. Upgrade tuios on %s or on this machine. %v",
			e.Host, e.Host, e.Err)
	}
	if isConnectionGone(e.Err) {
		return fmt.Sprintf("tuios on %s closed the connection before it answered. Its daemon may have stopped. Run 'tuios hosts' to see the link.", e.Host)
	}
	return fmt.Sprintf("tuios on %s did not accept this client. %v", e.Host, e.Err)
}

// HostConnectError reports a connection to a host that the local daemon could
// not open. Code is the daemon's stable error code, so a caller can tell an
// unknown name from a host that is down, and Host names the machine.
type HostConnectError struct {
	Host    string
	Code    string
	Message string
	Hint    *VerbHint
}

func (e *HostConnectError) Error() string {
	return e.Message
}
