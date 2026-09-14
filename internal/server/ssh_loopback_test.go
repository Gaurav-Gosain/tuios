package server

import (
	"net"
	"testing"
)

// The native clipboard fallback is only safe for a session that arrived from
// this machine, so this predicate is what keeps a remote peer's clipboard out
// of reach. See isLoopbackAddr.
func TestIsLoopbackAddr(t *testing.T) {
	cases := []struct {
		name string
		addr net.Addr
		want bool
	}{
		{"v4 loopback", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 41234}, true},
		{"v6 loopback", &net.TCPAddr{IP: net.ParseIP("::1"), Port: 41234}, true},
		{"a remote peer", &net.TCPAddr{IP: net.ParseIP("10.0.0.5"), Port: 41234}, false},
		{"a link-local peer is not loopback", &net.TCPAddr{IP: net.ParseIP("169.254.1.1"), Port: 41234}, false},
		{"nil is not loopback", nil, false},
		{"a unix socket has no port to split, so it is local", &net.UnixAddr{Name: "/run/user/1000/tuios.sock", Net: "unix"}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLoopbackAddr(tc.addr); got != tc.want {
				t.Fatalf("isLoopbackAddr(%v) = %v, want %v", tc.addr, got, tc.want)
			}
		})
	}
}
