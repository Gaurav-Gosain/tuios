package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

// fakeResolver answers the way the system resolver of a cgo build does.
type fakeResolver map[string][]string

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	list, ok := f[host]
	if !ok {
		return nil, errors.New("no such host")
	}
	var out []netip.Addr
	for _, s := range list {
		out = append(out, netip.MustParseAddr(s))
	}
	return out, nil
}

// glibc getaddrinfo reads inet_aton forms, and /etc/hosts gives names to
// loopback addresses. These are the answers a cgo build on Linux gets.
var cgoResolver = fakeResolver{
	"0":               {"0.0.0.0"},
	"0x0":             {"0.0.0.0"},
	"127.1":           {"127.0.0.1"},
	"0177.0.0.1":      {"127.0.0.1"},
	"127.000.000.001": {"127.0.0.1"},
	"localhost":       {"::1", "127.0.0.1"},
	"localhost.":      {"127.0.0.1"},
	"LOCALHOST":       {"127.0.0.1"},
	"ip6-localhost":   {"::1"},
	"arch-btw":        {"127.0.1.1"},
	"lan-name":        {"192.168.1.20"},
	"split-name":      {"127.0.0.1", "192.168.1.20"},
	"wild-and-lan":    {"0.0.0.0", "192.168.1.20"},
	"link-name":       {"fe80::1"},
}

// TestBindHostWildcardSpellings: every spelling that binds every interface
// must meet the --no-auth refusal, however the resolver reads it.
func TestBindHostWildcardSpellings(t *testing.T) {
	t.Setenv(webPasswordEnv, "")
	for _, host := range []string{"0", "0x0", "::%lo", "0.0.0.0", "::"} {
		bind, err := resolveBindHost(context.Background(), cgoResolver, host)
		if err != nil {
			t.Errorf("--host %s: %v", host, err)
			continue
		}
		_, err = planWebAccess(&bytes.Buffer{}, webAccessFlags{host: bind, port: "7681", noAuth: true})
		if !errors.Is(err, errNoAuthWildcard) {
			t.Errorf("--host %s binds %s: --no-auth got %v, want the wildcard refusal", host, bind, err)
		}
	}
}

// TestBindHostLoopbackSpellings: every spelling that binds loopback must
// reach sip as an IP address that sip itself reads as loopback, so that sip
// checks the Host header. sip parses the bind host with net.ParseIP.
func TestBindHostLoopbackSpellings(t *testing.T) {
	for _, host := range []string{"localhost", "127.1", "0177.0.0.1", "127.000.000.001", "localhost.", "LOCALHOST", "ip6-localhost", "arch-btw", "::1%lo", "[::1%lo]", "::ffff:127.0.0.1"} {
		bind, err := resolveBindHost(context.Background(), cgoResolver, host)
		if err != nil {
			t.Errorf("--host %s: %v", host, err)
			continue
		}
		if ip := net.ParseIP(bind); ip == nil || !ip.IsLoopback() {
			t.Errorf("--host %s reaches sip as %q, which is not an IP address that sip reads as loopback", host, bind)
		}
		if !isLoopbackHost(bind) {
			t.Errorf("--host %s binds %q, which the access rules do not read as loopback", host, bind)
		}
	}
	if bind, _ := resolveBindHost(context.Background(), cgoResolver, "localhost"); bind != "127.0.0.1" {
		t.Errorf("localhost binds %q, want 127.0.0.1, the first IPv4 address as net.Listen picks", bind)
	}
}

// TestBindHostOthers: a network name binds its address, a zone that picks an
// interface stays, a mix of kinds is refused, a host with a port is refused,
// and the empty host is left to sip, which binds it to localhost and checks
// the Host header.
func TestBindHostOthers(t *testing.T) {
	for host, want := range map[string]string{
		"lan-name":     "192.168.1.20",
		"192.168.1.20": "192.168.1.20",
		"fe80::1%eth0": "fe80::1%eth0",
		"[::1]":        "::1",
		"":             "",
	} {
		if got, err := resolveBindHost(context.Background(), cgoResolver, host); err != nil || got != want {
			t.Errorf("--host %q: got %q, %v, want %q", host, got, err, want)
		}
	}
	for _, host := range []string{"split-name", "wild-and-lan", "no-such-name", "link-name",
		"127.0.0.1:7681", "[::1]:7681", "0.0.0.0:7681", "192.168.1.20:7681", "localhost:7681"} {
		if got, err := resolveBindHost(context.Background(), cgoResolver, host); err == nil {
			t.Errorf("--host %s: got %q, want a refusal", host, got)
		}
	}
}
