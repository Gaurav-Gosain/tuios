//go:build !windows && !js

package session

import (
	"errors"
	"net"
	"syscall"
)

// connPeerClosed reports whether the other end of conn has closed it, without
// reading anything off it: a peek that finds the end of the stream. A
// connection with bytes waiting, or none yet, is open. A connection that is
// not a socket this can peek at reads as open.
//
// A verb that blocks (wait-for) reads nothing from its connection while it
// waits, since the connection's reader is the loop that called it. This is
// how the wait learns its caller has gone and ends, rather than holding its
// goroutine and its event subscription until the timeout.
func connPeerClosed(conn net.Conn) bool {
	sc, ok := conn.(syscall.Conn)
	if !ok {
		return false
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return false
	}
	closed := false
	_ = raw.Read(func(fd uintptr) bool {
		var b [1]byte
		n, _, err := syscall.Recvfrom(int(fd), b[:], syscall.MSG_PEEK|syscall.MSG_DONTWAIT)
		switch {
		case err == nil:
			closed = n == 0
		case errors.Is(err, syscall.EAGAIN), errors.Is(err, syscall.EWOULDBLOCK), errors.Is(err, syscall.EINTR):
		default:
			closed = true
		}
		return true
	})
	return closed
}
