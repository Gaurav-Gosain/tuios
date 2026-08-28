package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
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
		{"loopback needs nothing", sshServerFlags{host: "localhost", port: "2222"}, false},
		{"empty host is loopback", sshServerFlags{host: "", port: "2222"}, false},
		{"127.0.0.1 needs nothing", sshServerFlags{host: "127.0.0.1", port: "2222"}, false},
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

// TestCheckSSHAuthNamesTheRealFlags checks the most important string in this
// change. Someone who hits the refusal is stopped from starting a server, so
// the message has to carry the way forward, filled in with the address and port
// they typed.
func TestCheckSSHAuthNamesTheRealFlags(t *testing.T) {
	var out bytes.Buffer
	err := checkSSHAuth(&out, sshServerFlags{host: "192.168.1.31", port: "9000"})
	if err == nil {
		t.Fatal("expected a refusal")
	}
	advice := out.String()

	for _, want := range []string{
		filepath.Join(xdg.ConfigHome, "tuios", "authorized_keys"),
		"~/.ssh/authorized_keys",
		"--no-auth",
		"ssh -L 9000:localhost:9000",
		"192.168.1.31",
		"9000",
	} {
		if !strings.Contains(advice, want) {
			t.Errorf("advice never mentions %q:\n%s", want, advice)
		}
	}
	for _, want := range []string{"add a public key", "--no-auth"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error itself never mentions %q: %v", want, err)
		}
	}
}

// TestCheckSSHAuthReportsABrokenKeysFilePlainly keeps the menu of answers for
// the one question that has several. A file that cannot be read has one answer,
// so it gets the sentence and no menu.
func TestCheckSSHAuthReportsABrokenKeysFilePlainly(t *testing.T) {
	broken := filepath.Join(t.TempDir(), "authorized_keys")
	if err := os.WriteFile(broken, []byte("ssh-ed25519 not-a-key\n"), 0o600); err != nil {
		t.Fatalf("write keys: %v", err)
	}
	var out bytes.Buffer
	err := checkSSHAuth(&out, sshServerFlags{host: "0.0.0.0", port: "2222", authorizedKeys: broken})
	if err == nil {
		t.Fatal("a keys file that does not parse was accepted")
	}
	if out.Len() > 0 {
		t.Fatalf("printed the network advice for a broken file:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("error does not name the bad line: %v", err)
	}
}
