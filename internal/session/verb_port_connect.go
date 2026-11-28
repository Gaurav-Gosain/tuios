package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"syscall"
	"time"
)

// port-connect: a TCP connection to a port on this machine's loopback, for
// tuios forward on another machine.
//
// It has the shape of open-pane: one verb on a fresh connection, one reply,
// and then the connection is the TCP connection, raw bytes in both
// directions. The asking machine reaches this daemon through its link, so the
// bytes ride the link's own ssh and need no ssh -L, which the [hosts] options
// refuse.
//
// Only 127.0.0.1 and ::1 are dialed. A port on another address of this
// machine, or on another machine, is not reachable through this verb. Over a
// link it needs open, as exec does: a command run here could reach the same
// port with curl.

// ErrVerbPortClosed reports a port that nothing on this machine's loopback
// answers on.
const ErrVerbPortClosed = "port_closed"

// ErrVerbPortUnreachable reports a port that did not answer for another
// reason: the dial timed out, or a firewall or a security policy refused it.
const ErrVerbPortUnreachable = "port_unreachable"

const portConnectDialTimeout = 5 * time.Second

func portVerbs() map[string]verbEntry {
	return map[string]verbEntry{
		"port-connect": {
			stream: &verbStream{},
			scope:  scopeDeny,
			link:   needOpen,
			params: []verbParam{
				{Name: "port", Type: "int", Required: true},
			},
			returns: []verbParam{
				{Name: "port", Type: "int"},
				{Name: "address", Type: "string"},
			},
			handler: (*Daemon).verbPortConnect,
		},
	}
}

// verbPortConnect dials the port and hands this connection to the relay once
// the reply has been written.
func (d *Daemon) verbPortConnect(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Port int `json:"port"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Port < 1 || p.Port > 65535 {
		return nil, invalidParam("port", "port must be a number from 1 to 65535.")
	}
	tcp, addr, err := dialLoopback(d.ctx, p.Port)
	if err != nil {
		if connRefused(err) {
			return nil, newVerbError(ErrVerbPortClosed, "Nothing listens on port "+strconv.Itoa(p.Port)+" on this machine.")
		}
		return nil, newVerbError(ErrVerbPortUnreachable, "Port "+strconv.Itoa(p.Port)+" on this machine did not answer: "+err.Error())
	}
	LogBasic("Client %s connected to port %d on this machine", cs.clientID, p.Port)
	cs.takeover = func(br *bufio.Reader) {
		d.relayPort(cs, br, tcp)
	}
	// The reply can fail to go out, and then the takeover never runs.
	cs.abandon = func() { _ = tcp.Close() }
	return map[string]any{"type": "port", "port": p.Port, "address": addr}, nil
}

// connRefused reports a dial that nothing answered: ECONNREFUSED, or
// WSAECONNREFUSED (10061) on Windows, which errors.Is does not map.
func connRefused(err error) bool {
	if errno, ok := errors.AsType[syscall.Errno](err); ok {
		return errno == syscall.ECONNREFUSED || errno == 10061
	}
	return false
}

// dialLoopback connects to port on 127.0.0.1, or on ::1 when nothing answers
// on 127.0.0.1.
func dialLoopback(ctx context.Context, port int) (net.Conn, string, error) {
	dialer := net.Dialer{Timeout: portConnectDialTimeout}
	var first error
	for _, host := range []string{"127.0.0.1", "::1"} {
		c, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err == nil {
			return c, host, nil
		}
		if first == nil {
			first = err
		}
	}
	return nil, "", first
}

// relayPort copies bytes between the caller's connection and the TCP
// connection, and passes on a half-close in each direction, as ssh -L does:
// a client that shuts its write side still gets the answer. As in
// relayHostConnection, neither direction has a deadline: a connection can be
// silent for hours and still be healthy.
func (d *Daemon) relayPort(cs *connState, br *bufio.Reader, tcp net.Conn) {
	d.relayHalfClose(cs, br, tcp)
}
