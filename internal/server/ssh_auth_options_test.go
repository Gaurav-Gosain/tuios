package server

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gossh "golang.org/x/crypto/ssh"
)

// TestKeyWithOptionsIsNotAdmitted drives a real server with keys that carry
// authorized_keys options. TUIOS cannot apply command=, from= or restrict, and
// a key that was restricted in the file must not get the full session the
// restriction was written to prevent. A plain key in the same file still works.
func TestKeyWithOptionsIsNotAdmitted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SSH integration test in short mode")
	}

	plain, plainLine := newTestKey(t)
	withCommand, commandLine := newTestKey(t)
	withFrom, fromLine := newTestKey(t)
	restricted, restrictLine := newTestKey(t)

	file := "command=\"/bin/true\" " + commandLine +
		"from=\"10.255.255.1\" " + fromLine +
		"restrict " + restrictLine +
		plainLine
	keysFile := writeFile(t, filepath.Join(t.TempDir(), "authorized_keys"), file, 0o600)
	addr := startAuthenticatedServer(t, keysFile)

	for _, tc := range []struct {
		name   string
		signer gossh.Signer
	}{
		{"command=", withCommand},
		{"from=", withFrom},
		{"restrict", restricted},
	} {
		t.Run("a key with "+tc.name+" is refused", func(t *testing.T) {
			client, err := dialSSH(addr, []gossh.AuthMethod{gossh.PublicKeys(tc.signer)})
			if err == nil {
				_ = client.Close()
				t.Fatalf("a key restricted with %s got a full session", tc.name)
			}
		})
	}

	t.Run("a plain key in the same file is admitted", func(t *testing.T) {
		client, err := dialSSH(addr, []gossh.AuthMethod{gossh.PublicKeys(plain)})
		if err != nil {
			t.Fatalf("a plain key was turned away: %v", err)
		}
		_ = client.Close()
	})
}

// TestFileOfOnlyRestrictedKeysIsAnError: a keys file whose every key carries
// options admits nobody, so startup says so instead of serving a port that
// refuses everyone.
func TestFileOfOnlyRestrictedKeysIsAnError(t *testing.T) {
	_, line := newTestKey(t)
	path := writeFile(t, filepath.Join(t.TempDir(), "authorized_keys"), "restrict "+line, 0o600)
	_, err := LoadAuthorizedKeys(path)
	if err == nil {
		t.Fatal("a file with only restricted keys was accepted")
	}
	if !strings.Contains(err.Error(), "options") {
		t.Fatalf("error does not name the options: %v", err)
	}
}

// TestNoSilentFallbackToSSHAuthorizedKeys: ~/.ssh/authorized_keys is sshd's
// file, often written with restrictions for other tools. TUIOS reads it only
// when --authorized-keys names it.
func TestNoSilentFallbackToSSHAuthorizedKeys(t *testing.T) {
	_, line := newTestKey(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home: %v", err)
	}
	sshPath := writeFile(t, filepath.Join(home, ".ssh", "authorized_keys"), line, 0o600)

	keys, err := LoadAuthorizedKeys("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if keys.Enabled() {
		t.Fatalf("TUIOS read %s without being asked to", keys.Path)
	}

	for _, host := range []string{"192.168.1.31", "127.0.0.1", "localhost"} {
		if _, err := PlanSSHAuth(host, "", false); !errors.Is(err, ErrNoSSHAuth) {
			t.Fatalf("a bind on %s with only ~/.ssh/authorized_keys was not refused: %v", host, err)
		}
	}

	// Naming the file is the opt in.
	keys, err = LoadAuthorizedKeys(sshPath)
	if err != nil || !keys.Enabled() {
		t.Fatalf("--authorized-keys %s did not turn authentication on: %v", sshPath, err)
	}
}

// TestEmptyHostIsNotLoopback: an empty host binds every interface, so it is
// held to the network rule.
func TestEmptyHostIsNotLoopback(t *testing.T) {
	if _, err := PlanSSHAuth("", "", false); !errors.Is(err, ErrNoSSHAuth) {
		t.Fatalf("an empty host, which listens on every interface, was served with no keys: %v", err)
	}
}
