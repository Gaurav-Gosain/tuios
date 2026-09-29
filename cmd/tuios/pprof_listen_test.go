package main

import (
	"net"
	"testing"
)

// TestPprofWithNoHostListensOnLoopback: --pprof :6060 names no host. The
// profile server has no authentication and serves the command line, so with no
// host it listens on loopback only, not on every interface.
func TestPprofWithNoHostListensOnLoopback(t *testing.T) {
	for _, addr := range []string{":0"} {
		ln, err := listenPprof(addr)
		if err != nil {
			t.Fatalf("listen %q: %v", addr, err)
		}
		ip := ln.Addr().(*net.TCPAddr).IP
		_ = ln.Close()
		if !ip.IsLoopback() {
			t.Errorf("--pprof %q listens on %s, want a loopback address", addr, ip)
		}
	}
}

// TestPprofWithAHostKeepsIt: a host the user names is used as given.
func TestPprofWithAHostKeepsIt(t *testing.T) {
	ln, err := listenPprof("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	if got := ln.Addr().(*net.TCPAddr).IP.String(); got != "127.0.0.1" {
		t.Fatalf("listens on %s, want 127.0.0.1", got)
	}
}
