package session

import (
	"bufio"
	"net"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// DialHostPort opens a connection to a TCP port on host's loopback. It goes
// through this machine's daemon and the link to host, and port-connect there
// makes the TCP connection. The returned connection carries raw bytes, and
// its CloseWrite passes a half-close on to the far TCP connection.
func DialHostPort(host, clientVersion string, port int) (net.Conn, error) {
	socketPath, err := GetSocketPath()
	if err != nil {
		return nil, errfmt.Errorf("failed to get socket path: %w", err)
	}
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return nil, errfmt.Errorf("failed to connect to daemon: %w", err)
	}
	c := &VerbClient{conn: conn, r: bufio.NewReader(conn)}
	if _, err := openHostConnectionHalf(conn, c.r, host); err != nil {
		_ = c.Close()
		return nil, err
	}
	hs, err := c.handshake(clientVersion)
	if err != nil {
		_ = c.Close()
		return nil, errfmt.Errorf("tuios on %s did not accept this client: %w", host, err)
	}
	c.daemon = hs
	c.host = host
	if _, err := c.Call("port-connect", map[string]int{"port": port}); err != nil {
		_ = c.Close()
		return nil, err
	}
	_ = c.conn.SetDeadline(time.Time{})
	return &bufferedConn{Conn: c.conn, r: c.r}, nil
}

// bufferedConn is a connection whose first bytes may already sit in the
// reader that read the reply line.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }

// CloseWrite shuts the write side of the connection to this machine's
// daemon, which passes it on to the far port.
func (b *bufferedConn) CloseWrite() error {
	if cw, ok := b.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return b.Conn.Close()
}
