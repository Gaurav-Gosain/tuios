package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

const linkDialTimeout = 5 * time.Second

const maxLinkHandshakeReply = 64 * 1024

// ErrLinkSocketMissing reports a daemon that holds links to a policy and whose
// link sockets cannot be reached. The proxy refuses the stream rather than
// dialling the daemon's own socket, which has no policy.
var ErrLinkSocketMissing = errors.New("the daemon here holds links to a policy and its link socket cannot be reached")

// DialForLink connects one link stream to this machine's daemon.
//
// It dials the link-human socket for a stream the hub vouched for, else the
// link socket, so the daemon marks the connection as from another machine. It
// then names the machine with the link-peer handshake: pinned when the proxy
// was started with --as, which wins over anything the hub said, else the name
// the hub gave for itself in the stream's open frame. The handshake goes first
// and is answered before a byte of the hub's is relayed, so nothing the hub
// sends can name the peer.
//
// A daemon from before the handshake answers it with unknown_verb. The proxy
// then dials again and relays without it, which is how that daemon always
// linked.
//
// A daemon with no link socket at all is either from before link sockets, and
// then the proxy uses its main socket as it always did, or it is a daemon that
// failed to open one. hello tells the two apart: a daemon that reports
// link_policy is refused rather than reached on a socket that would give the
// hub everything.
func DialForLink(socketPath string, open federation.StreamOpen, pinnedPeer string) (net.Conn, error) {
	agent := os.Getenv("SSH_AUTH_SOCK")
	peer, pinned := open.From, false
	if pinnedPeer != "" {
		peer, pinned = pinnedPeer, true
	}
	if open.Human {
		if conn, err := dialLinkSocket(LinkHumanSocketPath(socketPath), peer, pinned, agent); err == nil {
			return conn, nil
		}
	}
	conn, err := dialLinkSocket(LinkSocketPath(socketPath), peer, pinned, agent)
	if err == nil {
		return conn, nil
	}
	if _, ok := errors.AsType[*linkHandshakeError](err); ok {
		return nil, err
	}
	if daemonHoldsLinkPolicy(socketPath) {
		return nil, ErrLinkSocketMissing
	}
	return net.DialTimeout("unix", socketPath, linkDialTimeout)
}

type linkHandshakeError struct{ verr *verbError }

func (e *linkHandshakeError) Error() string { return "link-peer refused: " + e.verr.Error() }

func dialLinkSocket(path, peer string, pinned bool, agent string) (net.Conn, error) {
	conn, err := net.DialTimeout("unix", path, linkDialTimeout)
	if err != nil {
		return nil, err
	}
	token, verr, err := linkHandshake(conn, linkPeerParams(peer, pinned, agent))
	switch {
	case err != nil:
		_ = conn.Close()
		return nil, err
	case verr == nil:
		if token == "" {
			return conn, nil
		}
		return &linkConn{Conn: conn, path: path, peer: peer, pinned: pinned, token: token}, nil
	case verr.Code == ErrVerbUnknownVerb:
		_ = conn.Close()
		return net.DialTimeout("unix", path, linkDialTimeout)
	default:
		_ = conn.Close()
		return nil, &linkHandshakeError{verr: verr}
	}
}

func linkHandshake(conn net.Conn, params map[string]any) (string, *verbError, error) {
	req, err := jsonx.Marshal(verbRequest{
		ID:     json.RawMessage(`0`),
		Verb:   linkPolicyVerb,
		Params: mustJSON(params),
	})
	if err != nil {
		return "", nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(linkDialTimeout))
	defer func() { _ = conn.SetDeadline(time.Time{}) }()
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return "", nil, err
	}
	line, err := readLineUnbuffered(conn, maxLinkHandshakeReply)
	if err != nil {
		return "", nil, err
	}
	var resp struct {
		Error  *verbError `json:"error"`
		Result struct {
			CloseToken string `json:"close_token"`
		} `json:"result"`
	}
	if err := jsonx.Unmarshal(line, &resp); err != nil {
		return "", nil, errfmt.Errorf("the daemon's answer to link-peer cannot be read: %w", err)
	}
	return resp.Result.CloseToken, resp.Error, nil
}

type linkConn struct {
	net.Conn
	path   string
	peer   string
	pinned bool
	token  string
}

func (c *linkConn) NoteClosedOnPurpose() {
	conn, err := net.DialTimeout("unix", c.path, linkDialTimeout)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	params := linkPeerParams(c.peer, c.pinned, "")
	params["closes"] = c.token
	_, _, _ = linkHandshake(conn, params)
}

func (c *linkConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

func linkPeerParams(peer string, pinned bool, agent string) map[string]any {
	p := map[string]any{"peer": peer, "pinned": pinned}
	if agent != "" {
		p["ssh_auth_sock"] = agent
	}
	return p
}

func readLineUnbuffered(conn net.Conn, limit int) ([]byte, error) {
	var buf bytes.Buffer
	one := make([]byte, 1)
	for buf.Len() < limit {
		if _, err := conn.Read(one); err != nil {
			return nil, err
		}
		if one[0] == '\n' {
			return buf.Bytes(), nil
		}
		buf.WriteByte(one[0])
	}
	return nil, errors.New("the daemon's reply line is too long")
}

func daemonHoldsLinkPolicy(socketPath string) bool {
	conn, err := net.DialTimeout("unix", socketPath, linkDialTimeout)
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	req, _ := jsonx.Marshal(verbRequest{ID: json.RawMessage(`0`), Verb: "hello"})
	_ = conn.SetDeadline(time.Now().Add(linkDialTimeout))
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return false
	}
	line, err := readLineUnbuffered(conn, maxLinkHandshakeReply)
	if err != nil {
		return false
	}
	var resp struct {
		Result struct {
			LinkPolicy bool `json:"link_policy"`
		} `json:"result"`
	}
	return jsonx.Unmarshal(line, &resp) == nil && resp.Result.LinkPolicy
}

// ValidLinkPeerName reports whether name can be a peer name, for stdio-proxy's
// --as flag.
func ValidLinkPeerName(name string) error {
	if name == "" || len(name) > 64 || !linkPeerPattern().MatchString(name) {
		return errfmt.Errorf("%q is not a machine name: use letters, digits, dot, dash and underscore", name)
	}
	return nil
}
