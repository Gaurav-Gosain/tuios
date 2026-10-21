//go:build !unix

package main

import (
	"net"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// liveDaemonSockets is the default daemon's socket when it accepts a
// connection. Native plugins do not run here.
func liveDaemonSockets() []string {
	sock, err := session.GetSocketPath()
	if err != nil {
		return nil
	}
	if conn, err := net.DialTimeout("unix", sock, time.Second); err == nil {
		_ = conn.Close()
		return []string{sock}
	}
	return nil
}
