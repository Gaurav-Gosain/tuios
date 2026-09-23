//go:build !js

package app

import "charm.land/ssh"

// SSHConn is the SSH session a `tuios ssh` client is served over. It is its
// own name so the browser build, which has no SSH server, can leave the SSH
// library out of its download. See ssh_conn_js.go.
type SSHConn = ssh.Session

// sshClientTerm is the TERM the SSH client sent in its pty request.
func sshClientTerm(s SSHConn) (string, bool) {
	pty, _, ok := s.Pty()
	return pty.Term, ok
}
