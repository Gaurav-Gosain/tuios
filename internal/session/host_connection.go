package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

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

const maxHostConnectReply = 64 * 1024

func openHostConnectionOn(conn net.Conn, br *bufio.Reader, host string) (HostConnectionInfo, error) {
	return openHostConnectionAgent(conn, br, host, "")
}

func openHostConnectionAgent(conn net.Conn, br *bufio.Reader, host, agent string) (HostConnectionInfo, error) {
	params := map[string]string{"host": host}
	if agent != "" {
		params["ssh_auth_sock"] = agent
	}
	req, err := jsonx.Marshal(verbRequest{
		ID:     json.RawMessage(`1`),
		Verb:   "open-host-connection",
		Params: mustJSON(params),
	})
	if err != nil {
		return HostConnectionInfo{}, err
	}
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return HostConnectionInfo{}, errfmt.Errorf("cannot ask the daemon for a connection to %s: %w", host, err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	line, err := readLimitedLine(br, maxHostConnectReply)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		if isConnectionGone(err) {
			return HostConnectionInfo{}, &HostConnectError{
				Host:    host,
				Code:    ErrVerbProtocolMismatch,
				Message: "The daemon on this machine is too old to open a connection to " + host + ". Restart it with 'tuios kill-server'.",
			}
		}
		return HostConnectionInfo{}, errfmt.Errorf("no answer from the daemon on this machine about %s: %w", host, err)
	}
	var resp struct {
		Result *HostConnectionInfo `json:"result"`
		Error  *verbError          `json:"error"`
	}
	if err := jsonx.Unmarshal(line, &resp); err != nil {
		return HostConnectionInfo{}, errfmt.Errorf("the daemon on this machine sent a reply this build cannot read: %w", err)
	}
	if resp.Error != nil {
		e := &HostConnectError{Host: host, Code: resp.Error.Code, Message: resp.Error.Message, Hint: resp.Error.Hint}
		if resp.Error.Code == ErrVerbUnknownVerb {
			e.Message = "The daemon on this machine is older than this tuios and cannot open a connection to " + host + ". Restart it with 'tuios kill-server'."
		}
		return HostConnectionInfo{}, e
	}
	if resp.Result == nil {
		return HostConnectionInfo{}, errfmt.Errorf("the daemon on this machine answered with no result for %s", host)
	}
	return *resp.Result, nil
}

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
