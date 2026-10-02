//go:build !slim

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckSSHAuth is the command-line half of the gate, and the twin of
// TestCheckTransportSecurity in cmd/tuios-web. TestMain has already pointed
// HOME and XDG_CONFIG_HOME at a throwaway tree, so "no keys" here means no keys
// and not the developer's own authorized_keys.
func TestCheckSSHAuth(t *testing.T) {
	keysFile := filepath.Join(t.TempDir(), "authorized_keys")
	// A real key line, so the file is accepted rather than reported as empty.
	const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIL5s0dUf1Y7oCkQOxJmMB0uNjcnKpkOClPZ0M5ZKgnQ7 tuios-test\n"
	if err := os.WriteFile(keysFile, []byte(key), 0o600); err != nil {
		t.Fatalf("write keys: %v", err)
	}

	tests := []struct {
		name    string
		flags   sshServerFlags
		wantErr bool
	}{
		{"loopback with no keys refuses", sshServerFlags{host: "localhost", port: "2222"}, true},
		{"no-auth satisfies loopback", sshServerFlags{host: "localhost", port: "2222", noAuth: true}, false},
		{"empty host listens on every interface and refuses", sshServerFlags{host: "", port: "2222"}, true},
		{"127.0.0.1 with no keys refuses", sshServerFlags{host: "127.0.0.1", port: "2222"}, true},
		{"LAN bind with no keys refuses", sshServerFlags{host: "192.168.1.31", port: "2222"}, true},
		{"wildcard bind with no keys refuses", sshServerFlags{host: "0.0.0.0", port: "2222"}, true},
		{"keys satisfy it", sshServerFlags{host: "192.168.1.31", port: "2222", authorizedKeys: keysFile}, false},
		{"no-auth satisfies it", sshServerFlags{host: "192.168.1.31", port: "2222", noAuth: true}, false},
		{"a named keys file that is missing refuses", sshServerFlags{host: "localhost", port: "2222", authorizedKeys: "/nope/authorized_keys"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := checkSSHAuth(&out, tt.flags)
			if tt.wantErr && err == nil {
				t.Fatalf("expected a refusal, got none")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected no refusal, got %v", err)
			}
			if !tt.wantErr && out.Len() > 0 {
				t.Fatalf("printed advice for a bind it accepted: %q", out.String())
			}
		})
	}
}

// TestCheckSSHAuthOffersThreeWaysOut: a loopback bind with no keys file is
// refused, and the refusal names the three ways to start.
func TestCheckSSHAuthOffersThreeWaysOut(t *testing.T) {
	var out bytes.Buffer
	if err := checkSSHAuth(&out, sshServerFlags{host: "localhost", port: "2222"}); err == nil {
		t.Fatal("a loopback bind with no keys file was served")
	}
	for _, want := range []string{
		"authorized_keys\n",
		"--authorized-keys ~/.ssh/authorized_keys",
		"--no-auth",
		"every user on this machine",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out.String())
		}
	}
}
