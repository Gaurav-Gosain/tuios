package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// The client half of open-host-connection: what a client does to a fresh
// connection to its own daemon to turn it into a connection to another
// machine's daemon. TUIClient.ConnectThroughHost and DialVerbClientThroughHost
// both start here and then carry on exactly as they would on a local socket.

// HostConnectionInfo is what the local daemon said about the host as it opened
// the connection. The version and protocol are what the host reported at the
// link handshake, carried for a message, never trusted for a decision.
type HostConnectionInfo struct {
	Host          string `json:"host"`
	DaemonVersion string `json:"daemon_version"`
	Protocol      int    `json:"protocol"`
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

// maxHostConnectReply bounds the local daemon's reply line. The local daemon is
// trusted, and a bound costs nothing.
const maxHostConnectReply = 64 * 1024

// openHostConnectionOn sends open-host-connection on a freshly dialed
// connection to the local daemon and reads its reply. On success the
// connection is, from here on, a connection to the host's daemon. br must be
// the only reader of conn and is left holding nothing past the reply.
func openHostConnectionOn(conn net.Conn, br *bufio.Reader, host string) (HostConnectionInfo, error) {
	req, err := json.Marshal(verbRequest{
		ID:     json.RawMessage(`1`),
		Verb:   "open-host-connection",
		Params: json.RawMessage(fmt.Sprintf(`{"host":%q}`, host)),
	})
	if err != nil {
		return HostConnectionInfo{}, err
	}
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return HostConnectionInfo{}, fmt.Errorf("cannot ask the daemon for a connection to %s: %w", host, err)
	}
	// The daemon waits for the host's first attempt to settle before it
	// answers, which is bounded by its own budget. This is that budget plus
	// room.
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	line, err := readLimitedLine(br, maxHostConnectReply)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		if isConnectionGone(err) {
			// A daemon from before this verb speaks JSON and answers an unknown
			// verb with an error line, so a closed connection is a daemon from
			// before the JSON protocol altogether.
			return HostConnectionInfo{}, &HostConnectError{
				Host:    host,
				Code:    ErrVerbProtocolMismatch,
				Message: "The daemon on this machine is too old to open a connection to " + host + ". Restart it with 'tuios kill-server'.",
			}
		}
		return HostConnectionInfo{}, fmt.Errorf("no answer from the daemon on this machine about %s: %w", host, err)
	}
	var resp struct {
		Result *HostConnectionInfo `json:"result"`
		Error  *verbError          `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return HostConnectionInfo{}, fmt.Errorf("the daemon on this machine sent a reply this build cannot read: %w", err)
	}
	if resp.Error != nil {
		e := &HostConnectError{Host: host, Code: resp.Error.Code, Message: resp.Error.Message, Hint: resp.Error.Hint}
		if resp.Error.Code == ErrVerbUnknownVerb {
			e.Message = "The daemon on this machine is older than this tuios and cannot open a connection to " + host + ". Restart it with 'tuios kill-server'."
		}
		return HostConnectionInfo{}, e
	}
	if resp.Result == nil {
		return HostConnectionInfo{}, fmt.Errorf("the daemon on this machine answered with no result for %s", host)
	}
	return *resp.Result, nil
}

// readLimitedLine reads one newline-terminated line of at most limit bytes.
func readLimitedLine(br *bufio.Reader, limit int) ([]byte, error) {
	var out []byte
	for {
		chunk, err := br.ReadSlice('\n')
		out = append(out, chunk...)
		if len(out) > limit {
			return nil, errors.New("the reply line is too long")
		}
		if err == nil {
			return out, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if len(out) > 0 && errors.Is(err, io.EOF) {
			return out, nil
		}
		return nil, err
	}
}
