package session

// DaemonFiles are the files a daemon leaves when it ends without a
// shutdown of its own: its sockets and its pid file. A daemon removes them
// itself when it stops; kill-server removes them after it had to terminate
// one.
func DaemonFiles(socketPath string) []string {
	return []string{
		LinkSocketPath(socketPath),
		LinkHumanSocketPath(socketPath),
		HerdrSocketPath(socketPath),
		socketPath + ".pid",
		socketPath,
	}
}
