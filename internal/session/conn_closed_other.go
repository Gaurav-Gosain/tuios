//go:build windows || js

package session

import "net"

func connPeerClosed(net.Conn) bool { return false }

func watchPeerClose(net.Conn) (<-chan struct{}, func()) { return nil, func() {} }
