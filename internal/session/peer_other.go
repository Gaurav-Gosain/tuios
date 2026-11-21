//go:build !linux && !darwin

package session

import "net"

const peerPIDSupported = false

func peerUID(net.Conn) (int, bool) { return 0, false }

func readProcLineage(int) (int, int64, bool) { return 0, 0, false }

func readProcLink(int) (int, int64, uint64, bool) { return 0, 0, 0, false }
