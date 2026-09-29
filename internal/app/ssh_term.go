package app

// SSHClientTerm is the TERM the SSH client sent in its pty request, and false
// when there is no session or no pty.
func SSHClientTerm(s SSHConn) (string, bool) {
	if s == nil {
		return "", false
	}
	return sshClientTerm(s)
}
