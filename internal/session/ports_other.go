//go:build !linux && !darwin

package session

const portsSupported = false

func paneTrees(roots map[int]paneProc) map[int]paneProc { return roots }

func listeningSockets(map[int]paneProc) []listenHit { return nil }
