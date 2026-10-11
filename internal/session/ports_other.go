//go:build !linux && !darwin

package session

// portsSupported says list-ports can read listening sockets here. It cannot:
// this system has neither procfs nor lsof.
const portsSupported = false

func paneTrees(roots map[int]paneProc) map[int]paneProc { return roots }

func listeningSockets(map[int]paneProc) []listenHit { return nil }
