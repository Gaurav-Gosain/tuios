//go:build !windows && !js

package session

import (
	"errors"
	"net"
	"syscall"
	"time"
)

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
				return false
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
		_ = conn.SetReadDeadline(time.Now())
		<-finished
		_ = conn.SetReadDeadline(time.Time{})
	}
}
