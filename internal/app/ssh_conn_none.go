//go:build js || slim

package app

// SSHConn is never set in the browser build or in tuios-slim, which serve
// no SSH.
type SSHConn interface{}

func sshClientTerm(SSHConn) (string, bool) { return "", false }
