package federation

import "testing"

// The sign-in link is read from ssh's stderr, which anything on the far
// machine can write to: a shell rc file, a motd, the remote command. A click
// on the rail opens it, so this is a security boundary. The ways it can go
// wrong, each one a row below:
//
//   - a scheme other than https (http, javascript, file)
//   - a host that only starts or ends like Tailscale's (suffix, prefix, a
//     user part before an @)
//   - a lookalike in punycode
//   - an address on this machine (localhost, 127.0.0.1, a port)
//   - a Headscale origin that is set for the host, which must pass, and one
//     that is not, which must not
//   - a URL on the line after the words, which \s used to reach
func TestSignInURLAllowed(t *testing.T) {
	cases := []struct {
		url     string
		origins []string
		want    bool
	}{
		{"https://login.tailscale.com/a/l1d9c7e392d3020", nil, true},
		{"https://controlplane.tailscale.com/a/abc", nil, true},
		{"https://LOGIN.tailscale.com/a/abc", nil, true},
		{"http://login.tailscale.com/a/abc", nil, false},
		{"http://evil.example/a/abc", nil, false},
		{"https://evil.example/a/abc", nil, false},
		{"https://login.tailscale.com.evil.example/a/x", nil, false},
		{"https://evil-login.tailscale.com/a/x", nil, false},
		{"https://xn--lgin-tailscale-xyz.com/a/x", nil, false},
		{"https://xn--login-tailscle-9db.com/a/x", nil, false},
		{"https://login.tailscale.com@evil.example/a/x", nil, false},
		{"https://login.tailscale.com:8443/a/x", nil, false},
		{"http://127.0.0.1:631/admin", nil, false},
		{"http://localhost:8080/api/delete?id=1", nil, false},
		{"https://localhost/a/x", nil, false},
		{"javascript:alert(1)", nil, false},
		{"https://headscale.example/a/x", []string{"https://headscale.example"}, true},
		{"https://headscale.example/a/x", []string{"https://headscale.example/"}, true},
		{"https://headscale.example.evil/a/x", []string{"https://headscale.example"}, false},
		{"https://headscale.example/a/x", []string{"http://headscale.example"}, false},
		{"https://headscale.example/a/x", nil, false},
	}
	for _, c := range cases {
		if got := SignInURLAllowed(c.url, c.origins...); got != c.want {
			t.Errorf("SignInURLAllowed(%q, %q) = %v, want %v", c.url, c.origins, got, c.want)
		}
	}
}

func TestParseSSHGateRefusesAnUntrustedLink(t *testing.T) {
	banner := "# Tailscale SSH requires an additional check.\n# To authenticate, visit: http://127.0.0.1:631/admin\n"
	g := ParseSSHGate(banner)
	if g == nil || g.URL != "" || !g.Refused {
		t.Fatalf("ParseSSHGate kept an untrusted link: %+v", g)
	}
	// The words with nothing after them on their line: the next line's first
	// word is not the link.
	g = ParseSSHGate("# Tailscale SSH requires an additional check.\n# To authenticate, visit:\nhttps://login.tailscale.com/a/x\n")
	if g == nil || g.URL != "" {
		t.Fatalf("ParseSSHGate took a link from the next line: %+v", g)
	}
	g = ParseSSHGate("# Tailscale SSH requires an additional check.\n# To authenticate, visit: https://login.tailscale.com/a/x\n")
	if g == nil || g.URL != "https://login.tailscale.com/a/x" || g.Refused {
		t.Fatalf("ParseSSHGate refused Tailscale's own link: %+v", g)
	}
}
