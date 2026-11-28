package main

import (
	"net"
	"slices"
	"strings"
	"syscall"
	"testing"
)

func TestParseProcAddr(t *testing.T) {
	for _, c := range []struct {
		in   string
		ip   string
		port int
	}{
		{"0100007F:1F90", "127.0.0.1", 8080},
		{"00000000000000000000000001000000:0050", "::1", 80},
	} {
		ip, port, ok := parseProcAddr(c.in)
		if !ok || !ip.Equal(net.ParseIP(c.ip)) || port != c.port {
			t.Errorf("parseProcAddr(%q) = %v, %d, %v; want %s, %d", c.in, ip, port, ok, c.ip, c.port)
		}
	}
}

// TestSocketOwnersMatchesTheWholeTuple: rows that share the local port with
// the peer's socket, a TIME_WAIT row of the same tuple, and a row of another
// destination must not count. Two live rows for one tuple are ambiguous.
func TestSocketOwnersMatchesTheWholeTuple(t *testing.T) {
	peer := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 40000} // 0100007F:9C40
	us := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}    // 0100007F:1F90
	table := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 0100007F:9C40 0100007F:1F91 01 00000000:00000000 00:00000000 00000000  1001        0 11 1\n" + // another destination
		"   1: 0100007F:9C40 0100007F:1F90 06 00000000:00000000 00:00000000 00000000     0        0 0 1\n" + // TIME_WAIT
		"   2: 0100007F:9C40 0100007F:1F90 01 00000000:00000000 00:00000000 00000000  1000        0 12 1\n"
	if got := socketOwners(table, peer, us); !slices.Equal(got, []string{"1000"}) {
		t.Fatalf("socketOwners = %v, want [1000]", got)
	}
	// A client that shut its write side is in FIN_WAIT2 (05) and still counts.
	finWait := strings.Replace(table, "1F90 01 00000000:00000000 00:00000000 00000000  1000", "1F90 05 00000000:00000000 00:00000000 00000000  1000", 1)
	if got := socketOwners(finWait, peer, us); !slices.Equal(got, []string{"1000"}) {
		t.Fatalf("socketOwners in FIN_WAIT2 = %v, want [1000]", got)
	}
	twice := table + "   3: 0100007F:9C40 0100007F:1F90 01 00000000:00000000 00:00000000 00000000  1001        0 13 1\n"
	if got := socketOwners(twice, peer, us); len(got) != 2 {
		t.Fatalf("socketOwners with two live rows = %v, want both, so the check refuses", got)
	}
}

// TestForwardPeerIsUsWithASharedLocalPort: two client sockets bound to one
// local port, each to another listener. The check must find this user on
// both, which a match on the local address alone could not tell apart.
func TestForwardPeerIsUsWithASharedLocalPort(t *testing.T) {
	var lns []net.Listener
	for range 2 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		lns = append(lns, ln)
	}
	reuse := func(_, _ string, c syscall.RawConn) error {
		var serr error
		err := c.Control(func(fd uintptr) {
			serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
		})
		if err != nil {
			return err
		}
		return serr
	}
	first, err := (&net.Dialer{Control: reuse}).Dial("tcp", lns[0].Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	local := first.LocalAddr().(*net.TCPAddr)
	second, err := (&net.Dialer{Control: reuse, LocalAddr: local}).Dial("tcp", lns[1].Addr().String())
	if err != nil {
		t.Skipf("this kernel does not let two sockets share a local port: %v", err)
	}
	defer second.Close()
	for i, ln := range lns {
		c, err := ln.Accept()
		if err != nil {
			t.Fatal(err)
		}
		if !forwardPeerIsUs(c) {
			t.Errorf("listener %d: the peer on the shared port %d is not seen as this user", i, local.Port)
		}
		_ = c.Close()
	}
}
