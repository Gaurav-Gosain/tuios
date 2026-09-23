//go:build js

package app

// SSHConn is never set in the browser build, which serves no SSH.
type SSHConn interface{}

func sshClientTerm(SSHConn) (string, bool) { return "", false }
