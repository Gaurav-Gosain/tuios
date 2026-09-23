package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// The proxy's half of a link connection: `tuios stdio-proxy` calls
// DialForLink once for every stream the hub opens.

// linkDialTimeout bounds one dial of a socket on this machine, and the
// handshake on it.
const linkDialTimeout = 5 * time.Second

// maxLinkHandshakeReply bounds the reply line the proxy reads.
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
	peer, pinned := open.From, false
	if pinnedPeer != "" {
		peer, pinned = pinnedPeer, true
	}
	if open.Human {
		if conn, err := dialLinkSocket(LinkHumanSocketPath(socketPath), peer, pinned); err == nil {
			return conn, nil
		}
	}
	conn, err := dialLinkSocket(LinkSocketPath(socketPath), peer, pinned)
	if err == nil {
		return conn, nil
	}
	var refused *linkHandshakeError
	if errors.As(err, &refused) {
		return nil, err
	}
	if daemonHoldsLinkPolicy(socketPath) {
		return nil, ErrLinkSocketMissing
	}
	return net.DialTimeout("unix", socketPath, linkDialTimeout)
}

// linkHandshakeError is a daemon refusing the handshake for a reason other
// than not knowing it. The stream is refused with it.
type linkHandshakeError struct{ verr *verbError }

func (e *linkHandshakeError) Error() string { return "link-peer refused: " + e.verr.Error() }

// dialLinkSocket dials one link socket and runs the handshake on it.
func dialLinkSocket(path, peer string, pinned bool) (net.Conn, error) {
	conn, err := net.DialTimeout("unix", path, linkDialTimeout)
	if err != nil {
		return nil, err
	}
	verr, err := linkHandshake(conn, peer, pinned)
	switch {
	case err != nil:
		_ = conn.Close()
		return nil, err
	case verr == nil:
		return conn, nil
	case verr.Code == ErrVerbUnknownVerb:
		// A daemon from before link policies. It has no policy to hold the
		// link to, so the connection is made again without the handshake.
		_ = conn.Close()
		return net.DialTimeout("unix", path, linkDialTimeout)
	default:
		_ = conn.Close()
		return nil, &linkHandshakeError{verr: verr}
	}
}

// linkHandshake sends link-peer on conn and reads the reply. It reads the reply
// a byte at a time, so no byte past the reply line is taken from the
// connection: the daemon may start the next reply only after the hub's first
// request, but a read-ahead buffer here would still be the wrong owner of it.
func linkHandshake(conn net.Conn, peer string, pinned bool) (*verbError, error) {
	req, err := json.Marshal(verbRequest{
		ID:     json.RawMessage(`0`),
		Verb:   linkPolicyVerb,
		Params: mustJSON(map[string]any{"peer": peer, "pinned": pinned}),
	})
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(linkDialTimeout))
	defer func() { _ = conn.SetDeadline(time.Time{}) }()
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return nil, err
	}
	line, err := readLineUnbuffered(conn, maxLinkHandshakeReply)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Error *verbError `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("the daemon's answer to link-peer cannot be read: %w", err)
	}
	return resp.Error, nil
}

// readLineUnbuffered reads up to and not past the next newline.
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

// daemonHoldsLinkPolicy asks the daemon on its own socket whether it is new
// enough to hold links to a policy. Any failure reads as no, which is the
// answer that keeps an older daemon reachable; a daemon that answers at all
// and says yes is not dialled on this socket for a link.
func daemonHoldsLinkPolicy(socketPath string) bool {
	conn, err := net.DialTimeout("unix", socketPath, linkDialTimeout)
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	req, _ := json.Marshal(verbRequest{ID: json.RawMessage(`0`), Verb: "hello"})
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
	return json.Unmarshal(line, &resp) == nil && resp.Result.LinkPolicy
}

// ValidLinkPeerName reports whether name can be a peer name, for stdio-proxy's
// --as flag.
func ValidLinkPeerName(name string) error {
	if name == "" || len(name) > 64 || !linkPeerPattern.MatchString(name) {
		return fmt.Errorf("%q is not a machine name: use letters, digits, dot, dash and underscore", name)
	}
	return nil
}
