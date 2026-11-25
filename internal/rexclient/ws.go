// Package rexclient talks to a Rex server: the control connection's JSON
// requests and events, and a block's terminal data stream. It covers what
// tuios rex-attach needs and nothing more.
//
// Rex speaks WebSocket over a Unix socket or plain TCP: /api/control with
// the rex.control.v1 subprotocol, and /api/data with rex.data.terminal.v1.
package rexclient

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6455 names SHA-1 for the accept key
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// The WebSocket opcodes Rex uses.
const (
	opContinuation = 0
	opText         = 1
	opBinary       = 2
	opClose        = 8
	opPing         = 9
	opPong         = 10
)

// maxMessage bounds one message from the server. Rex's control limit is
// 1 MiB, and a data message is smaller.
const maxMessage = 16 << 20

// websocketGUID is the RFC 6455 key suffix.
const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// conn is a client WebSocket over a stream connection. Reads happen on one
// goroutine at a time. Writes take mu, so a pong and a message do not mix.
type conn struct {
	c  net.Conn
	r  *bufio.Reader
	mu sync.Mutex
	// CloseCode is the code of the server's close frame, 0 for none.
	closeCode int
}

// dialWS opens a WebSocket at path with the subprotocol.
func dialWS(e Endpoint, path, protocol string) (*conn, error) {
	c, err := e.dial()
	if err != nil {
		return nil, err
	}
	var key [16]byte
	_, _ = rand.Read(key[:])
	k := base64.StdEncoding.EncodeToString(key[:])
	req := "GET " + path + " HTTP/1.1\r\nHost: " + e.hostHeader() + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + k + "\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Protocol: " + protocol + "\r\n\r\n"
	if _, err := io.WriteString(c, req); err != nil {
		_ = c.Close()
		return nil, errfmt.Errorf("failed to send the upgrade to %s: %w", path, err)
	}
	r := bufio.NewReader(c)
	resp, err := http.ReadResponse(r, nil)
	if err != nil {
		_ = c.Close()
		return nil, errfmt.Errorf("failed to read the upgrade answer of %s: %w", path, err)
	}
	_ = resp.Body.Close()
	sum := sha1.Sum([]byte(k + websocketGUID)) //nolint:gosec // RFC 6455
	if resp.StatusCode != http.StatusSwitchingProtocols || resp.Header.Get("Sec-Websocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]) {
		_ = c.Close()
		return nil, errfmt.Errorf("the Rex server refused the upgrade of %s: %s", path, resp.Status)
	}
	return &conn{c: c, r: r}, nil
}

// write sends one unfragmented, masked frame.
func (w *conn) write(op byte, payload []byte) error {
	var mask [4]byte
	_, _ = rand.Read(mask[:])
	head := make([]byte, 0, 14)
	head = append(head, 0x80|op)
	switch n := len(payload); {
	case n < 126:
		head = append(head, 0x80|byte(n))
	case n <= 0xffff:
		head = append(head, 0x80|126, byte(n>>8), byte(n))
	default:
		head = append(head, 0x80|127)
		head = binary.BigEndian.AppendUint64(head, uint64(n))
	}
	head = append(head, mask[:]...)
	frame := make([]byte, len(head)+len(payload))
	copy(frame, head)
	for i, b := range payload {
		frame[len(head)+i] = b ^ mask[i%4]
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err := w.c.Write(frame)
	return err
}

// errClosed is the server's close frame. conn.closeCode holds its code.
var errClosed = errors.New("the Rex server closed the connection")

// read returns the next text or binary message, answering pings on the way.
func (w *conn) read() (op byte, msg []byte, err error) {
	var data []byte
	var first byte
	for {
		var h [2]byte
		if _, err := io.ReadFull(w.r, h[:]); err != nil {
			return 0, nil, err
		}
		fin, code := h[0]&0x80 != 0, h[0]&0x0f
		if h[0]&0x70 != 0 || h[1]&0x80 != 0 {
			return 0, nil, errors.New("the Rex server sent a frame this client does not read")
		}
		n := uint64(h[1] & 0x7f)
		switch n {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(w.r, b[:]); err != nil {
				return 0, nil, err
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(w.r, b[:]); err != nil {
				return 0, nil, err
			}
			n = binary.BigEndian.Uint64(b[:])
		}
		if n+uint64(len(data)) > maxMessage {
			return 0, nil, errors.New("the Rex server sent a message over 16 MiB")
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(w.r, payload); err != nil {
			return 0, nil, err
		}
		switch code {
		case opPing:
			if err := w.write(opPong, payload); err != nil {
				return 0, nil, err
			}
			continue
		case opPong:
			continue
		case opClose:
			if len(payload) >= 2 {
				w.closeCode = int(binary.BigEndian.Uint16(payload[:2]))
			}
			_ = w.write(opClose, payload[:min(len(payload), 2)])
			return 0, nil, errClosed
		case opContinuation:
		default:
			first = code
		}
		data = append(data, payload...)
		if fin {
			return first, data, nil
		}
	}
}

// ready ends the handshake deadline.
func (w *conn) ready() { _ = w.c.SetDeadline(time.Time{}) }

// close sends a normal close and closes the connection.
func (w *conn) close() error {
	_ = w.write(opClose, []byte{0x03, 0xe8})
	return w.c.Close()
}

// Endpoint is where a Rex server listens: a Unix socket or a TCP address.
type Endpoint struct {
	Network string // "unix" or "tcp"
	Addr    string
}

// handshakeTimeout bounds the connect, the upgrade and the first message, so
// a host that is down or a server that does not answer fails with a message.
const handshakeTimeout = 10 * time.Second

// dial connects with a deadline that conn.ready clears after the first
// message.
func (e Endpoint) dial() (net.Conn, error) {
	c, err := net.DialTimeout(e.Network, e.Addr, handshakeTimeout)
	if err != nil {
		return nil, errfmt.Errorf("failed to reach the Rex server at %s: %w", e, err)
	}
	_ = c.SetDeadline(time.Now().Add(handshakeTimeout))
	return c, nil
}

func (e Endpoint) hostHeader() string {
	if e.Network == "unix" {
		return "localhost"
	}
	return e.Addr
}

// String names the endpoint for a message.
func (e Endpoint) String() string {
	if e.Network == "unix" {
		return "unix://" + e.Addr
	}
	return "http://" + e.Addr
}

// DefaultPort is the port of a Rex listener on a machine's tailnet address.
const DefaultPort = "7297"

// ParseEndpoint reads a target as monstar and tuios take it: "local" or empty
// for this machine's server (REX_SERVER, else the socket in runtimeDir),
// unix:///path or /path, http://host[:port], or host[:port]. HTTPS is not
// supported.
func ParseEndpoint(target, rexServer, runtimeDir string, uid int) (Endpoint, error) {
	if target == "" || target == "local" {
		if rexServer != "" && rexServer != "local" {
			return ParseEndpoint(rexServer, "", runtimeDir, uid)
		}
		if runtimeDir == "" {
			runtimeDir = "/run/user/" + strconv.Itoa(uid)
		}
		return Endpoint{"unix", runtimeDir + "/rex/server.sock"}, nil
	}
	if path, ok := strings.CutPrefix(target, "unix://"); ok {
		return Endpoint{"unix", path}, nil
	}
	if strings.HasPrefix(target, "/") {
		return Endpoint{"unix", target}, nil
	}
	port := DefaultPort
	if rest, ok := strings.CutPrefix(target, "http://"); ok {
		target, _, _ = strings.Cut(rest, "/")
		port = "80"
	} else if strings.Contains(target, "://") {
		return Endpoint{}, errfmt.Errorf("tuios reaches a Rex server at unix:// or http:// only, not at %q", target)
	}
	if target == "" {
		return Endpoint{}, errors.New("the Rex server address has no host")
	}
	if _, _, err := net.SplitHostPort(target); err != nil {
		target = net.JoinHostPort(strings.Trim(target, "[]"), port)
	}
	return Endpoint{"tcp", target}, nil
}
