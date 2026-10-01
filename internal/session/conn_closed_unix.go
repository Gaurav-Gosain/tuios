//go:build !windows && !js

package session

import (
	"errors"
	"net"
	"syscall"
	"time"
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

// watchPeerClose reports, on gone, when the other end of conn closes it. It
// is connPeerClosed made to wait: the peek runs in the runtime's poller, so
// the watch costs no wakeup while the connection stays quiet, and it reads
// nothing. Bytes that arrive end the watch without a report, because a peek
// cannot see past them. stop ends the watch and leaves the connection as it
// was. A connection that is not a socket is never reported.
func watchPeerClose(conn net.Conn) (gone <-chan struct{}, stop func()) {
	sc, ok := conn.(syscall.Conn)
	if !ok {
		return nil, func() {}
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return nil, func() {}
	}
	closed := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		peerClosed := false
		_ = raw.Read(func(fd uintptr) bool {
			var b [1]byte
			n, _, err := syscall.Recvfrom(int(fd), b[:], syscall.MSG_PEEK|syscall.MSG_DONTWAIT)
			switch {
			case err == nil:
				peerClosed = n == 0
			case errors.Is(err, syscall.EAGAIN), errors.Is(err, syscall.EWOULDBLOCK), errors.Is(err, syscall.EINTR):
				return false // nothing yet: sleep in the poller until readable
			default:
				peerClosed = true
			}
			return true
		})
		if peerClosed {
			close(closed)
		}
	}()
	return closed, func() {
		// A deadline in the past wakes the poller, and the read loop gets
		// its connection back with no deadline once the watch has ended.
		_ = conn.SetReadDeadline(time.Now())
		<-finished
		_ = conn.SetReadDeadline(time.Time{})
	}
}
