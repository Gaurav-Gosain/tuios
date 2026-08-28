package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adrg/xdg"
	gossh "golang.org/x/crypto/ssh"
)

// newTestKey returns a throwaway signer and the authorized_keys line for it.
// Nothing here touches the developer's own keys: TestMain redirects HOME, and
// every key below is generated for the one test that uses it.
func newTestKey(t *testing.T) (gossh.Signer, string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	return signer, string(gossh.MarshalAuthorizedKey(signer.PublicKey()))
}

// writeFile writes one file and returns its path.
func writeFile(t *testing.T, path, content string, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(path, 0o600)
		_ = os.Remove(path)
	})
	return path
}

// TestLoadAuthorizedKeys covers the awkward files. Each one is a way the file
// can be wrong, and the point of the test is that they are told apart: a file
// nobody can read must never be reported as "no keys are configured", because
// that answer is what lets a bind run unauthenticated.
func TestLoadAuthorizedKeys(t *testing.T) {
	_, keyLine := newTestKey(t)
	dir := t.TempDir()

	t.Run("no file anywhere means nothing is configured", func(t *testing.T) {
		keys, err := LoadAuthorizedKeys("")
		if err != nil {
			t.Fatalf("expected no error with no file, got %v", err)
		}
		if keys.Enabled() {
			t.Fatalf("found keys where no file exists: %+v", keys)
		}
	})

	t.Run("a file of keys turns authentication on", func(t *testing.T) {
		path := writeFile(t, filepath.Join(dir, "good"), "# a comment\n\n"+keyLine, 0o600)
		keys, err := LoadAuthorizedKeys(path)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if !keys.Enabled() || len(keys.Keys) != 1 {
			t.Fatalf("want one key from %s, got %+v", path, keys)
		}
	})

	t.Run("an empty file is a mistake, not an absent file", func(t *testing.T) {
		path := writeFile(t, filepath.Join(dir, "empty"), "", 0o600)
		_, err := LoadAuthorizedKeys(path)
		if err == nil {
			t.Fatal("an empty keys file was accepted as if no file existed")
		}
		if !strings.Contains(err.Error(), "holds no keys") {
			t.Fatalf("error does not say the file is empty: %v", err)
		}
	})

	t.Run("a file of only comments holds no keys", func(t *testing.T) {
		path := writeFile(t, filepath.Join(dir, "comments"), "# nothing here\n\n#\n", 0o600)
		if _, err := LoadAuthorizedKeys(path); err == nil {
			t.Fatal("a file with no key in it was accepted")
		}
	})

	t.Run("a malformed line names its line number", func(t *testing.T) {
		path := writeFile(t, filepath.Join(dir, "malformed"), "# note\n"+keyLine+"ssh-ed25519 not-a-key\n", 0o600)
		_, err := LoadAuthorizedKeys(path)
		if err == nil {
			t.Fatal("a malformed key line was accepted")
		}
		if !strings.Contains(err.Error(), "line 3") {
			t.Fatalf("error does not name line 3: %v", err)
		}
	})

	t.Run("an unreadable file is an error, not an absent file", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root reads everything, so this file would not be unreadable")
		}
		path := writeFile(t, filepath.Join(dir, "unreadable"), keyLine, 0o000)
		_, err := LoadAuthorizedKeys(path)
		if err == nil {
			t.Fatal("an unreadable keys file was accepted as if no file existed")
		}
		if !strings.Contains(err.Error(), "cannot read") {
			t.Fatalf("error does not say the file could not be read: %v", err)
		}
	})

	t.Run("a named file that does not exist is an error", func(t *testing.T) {
		_, err := LoadAuthorizedKeys(filepath.Join(dir, "absent"))
		if err == nil {
			t.Fatal("a missing --authorized-keys file was accepted")
		}
	})

	t.Run("a key that is present but does not match is refused", func(t *testing.T) {
		path := writeFile(t, filepath.Join(dir, "other"), keyLine, 0o600)
		keys, err := LoadAuthorizedKeys(path)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		stranger, _ := newTestKey(t)
		for _, k := range keys.Keys {
			if string(gossh.MarshalAuthorizedKey(k)) == string(gossh.MarshalAuthorizedKey(stranger.PublicKey())) {
				t.Fatal("a key that was never in the file matched one that was")
			}
		}
	})
}

// TestAuthorizedKeysSearchOrder proves the two default locations, and which one
// wins. TestMain has already pointed HOME and XDG_CONFIG_HOME at a throwaway
// tree, so these are the real production paths and not a mock of them.
func TestAuthorizedKeysSearchOrder(t *testing.T) {
	_, configLine := newTestKey(t)
	_, sshLine := newTestKey(t)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home: %v", err)
	}
	sshPath := filepath.Join(home, ".ssh", "authorized_keys")
	configPath := filepath.Join(xdg.ConfigHome, ConfigAuthorizedKeys)

	// ~/.ssh/authorized_keys alone is enough.
	writeFile(t, sshPath, sshLine, 0o600)
	keys, err := LoadAuthorizedKeys("")
	if err != nil {
		t.Fatalf("load with only ~/.ssh: %v", err)
	}
	if keys.Path != sshPath {
		t.Fatalf("want the ~/.ssh file, got %q", keys.Path)
	}

	// The TUIOS file wins when both exist.
	writeFile(t, configPath, configLine, 0o600)
	keys, err = LoadAuthorizedKeys("")
	if err != nil {
		t.Fatalf("load with both files: %v", err)
	}
	if keys.Path != configPath {
		t.Fatalf("want the tuios file to win, got %q", keys.Path)
	}
}

// TestPlanSSHAuth is the gate, table driven the way cmd/tuios-web's
// TestCheckTransportSecurity is, and over the same three axes: what the address
// is, what is configured, and whether the operator opted out.
func TestPlanSSHAuth(t *testing.T) {
	_, keyLine := newTestKey(t)
	withKeys := writeFile(t, filepath.Join(t.TempDir(), "authorized_keys"), keyLine, 0o600)

	tests := []struct {
		name              string
		host              string
		path              string
		noAuth            bool
		wantErr           bool
		wantAuthenticated bool
	}{
		{name: "loopback with no keys runs unauthenticated", host: "localhost"},
		{name: "empty host is loopback", host: ""},
		{name: "127.0.0.1 with no keys runs unauthenticated", host: "127.0.0.1"},
		{name: "::1 with no keys runs unauthenticated", host: "::1"},
		{name: "LAN bind with no keys refuses", host: "192.168.1.31", wantErr: true},
		{name: "wildcard bind with no keys refuses", host: "0.0.0.0", wantErr: true},
		{name: "keys satisfy a LAN bind", host: "192.168.1.31", path: withKeys, wantAuthenticated: true},
		{name: "no-auth satisfies a LAN bind", host: "192.168.1.31", noAuth: true},
		{name: "keys apply on loopback too", host: "127.0.0.1", path: withKeys, wantAuthenticated: true},
		{name: "no-auth wins over a keys file", host: "127.0.0.1", path: withKeys, noAuth: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := PlanSSHAuth(tt.host, tt.path, tt.noAuth)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected a refusal, got none")
				}
				if !errors.Is(err, ErrNoSSHAuth) {
					t.Fatalf("refusal is not the sentinel the command line looks for: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no refusal, got %v", err)
			}
			if plan.Authenticated() != tt.wantAuthenticated {
				t.Fatalf("authenticated is %v, want %v", plan.Authenticated(), tt.wantAuthenticated)
			}
			if !tt.wantAuthenticated && plan.Warning == "" {
				t.Fatal("an unauthenticated bind printed no warning")
			}
			if tt.wantAuthenticated && plan.Warning != "" {
				t.Fatalf("an authenticated bind warned anyway: %q", plan.Warning)
			}
		})
	}
}

// TestPlanSSHAuthWarningSaysWhatItGivesAway checks the one string an operator
// of an open server ever sees.
func TestPlanSSHAuthWarningSaysWhatItGivesAway(t *testing.T) {
	plan, err := PlanSSHAuth("127.0.0.1", "", false)
	if err != nil {
		t.Fatalf("loopback refused: %v", err)
	}
	for _, want := range []string{
		"does not check who connects",
		"open a shell",
		filepath.Join(xdg.ConfigHome, ConfigAuthorizedKeys),
	} {
		if !strings.Contains(plan.Warning, want) {
			t.Errorf("warning never mentions %q:\n%s", want, plan.Warning)
		}
	}
}

// TestStartSSHServerRefusesNetworkBindWithNoKeys proves the refusal happens
// before anything listens. A server that binds first and warns afterwards is
// still an open port for as long as the operator takes to read.
func TestStartSSHServerRefusesNetworkBindWithNoKeys(t *testing.T) {
	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- StartSSHServer(ctx, &SSHServerConfig{
			Host:      "0.0.0.0",
			Port:      port,
			KeyPath:   filepath.Join(t.TempDir(), "host_key"),
			Ephemeral: true,
			Version:   "test",
		})
	}()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("a wildcard bind with no keys was served")
		}
		if !errors.Is(err, ErrNoSSHAuth) {
			t.Fatalf("refused for the wrong reason: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the server did not refuse a wildcard bind with no keys")
	}

	// And nothing is listening on the port it was asked for.
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), time.Second)
	if err == nil {
		_ = conn.Close()
		t.Fatal("the refused bind left a listener behind")
	}
}

// startAuthenticatedServer starts the SSH server on loopback with keysFile as
// its authorized keys, and returns the address once the port answers.
func startAuthenticatedServer(t *testing.T, keysFile string) string {
	t.Helper()
	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- StartSSHServer(ctx, &SSHServerConfig{
			Host:               "127.0.0.1",
			Port:               port,
			KeyPath:            filepath.Join(t.TempDir(), "host_key"),
			AuthorizedKeysPath: keysFile,
			Ephemeral:          true, // no daemon: keep the test self-contained
			Version:            "test",
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-serveErr:
		case <-time.After(5 * time.Second):
			t.Log("server did not shut down within timeout")
		}
	})

	addr := net.JoinHostPort("127.0.0.1", port)
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			_ = conn.Close()
			return addr
		}
		if time.Now().After(deadline) {
			t.Fatalf("SSH server never started listening: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// dialSSH connects a real SSH client with the given auth methods.
func dialSSH(addr string, auth []gossh.AuthMethod) (*gossh.Client, error) {
	return gossh.Dial("tcp", addr, &gossh.ClientConfig{
		User:            "tester",
		Auth:            auth,
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
}

// TestSSHServerChecksTheKey drives the hole itself with a real SSH client.
//
// It does not assert that a handler was registered. The server this replaces
// had every one of its middlewares registered and still let anybody in, so the
// only proof worth having is a client that offers nothing being turned away and
// a client that offers the right key getting a session.
func TestSSHServerChecksTheKey(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SSH integration test in short mode")
	}

	authorized, authorizedLine := newTestKey(t)
	stranger, _ := newTestKey(t)
	keysFile := writeFile(t, filepath.Join(t.TempDir(), "authorized_keys"), authorizedLine, 0o600)
	addr := startAuthenticatedServer(t, keysFile)

	t.Run("a client with no key is refused", func(t *testing.T) {
		client, err := dialSSH(addr, nil)
		if err == nil {
			_ = client.Close()
			t.Fatal("a client offering no key was given a session")
		}
		if !strings.Contains(err.Error(), "unable to authenticate") &&
			!strings.Contains(err.Error(), "no supported methods") {
			t.Fatalf("refused for the wrong reason: %v", err)
		}
	})

	t.Run("a client with the wrong key is refused", func(t *testing.T) {
		client, err := dialSSH(addr, []gossh.AuthMethod{gossh.PublicKeys(stranger)})
		if err == nil {
			_ = client.Close()
			t.Fatal("a key that is not in the file was given a session")
		}
		if !strings.Contains(err.Error(), "unable to authenticate") {
			t.Fatalf("refused for the wrong reason: %v", err)
		}
	})

	t.Run("a client with an authorized key gets a session", func(t *testing.T) {
		client, err := dialSSH(addr, []gossh.AuthMethod{gossh.PublicKeys(authorized)})
		if err != nil {
			t.Fatalf("an authorized key was turned away: %v", err)
		}
		defer func() { _ = client.Close() }()
		assertPaintsAFrame(t, client)
	})

	t.Run("a key added while the server runs is let in", func(t *testing.T) {
		latecomer, latecomerLine := newTestKey(t)
		if err := os.WriteFile(keysFile, []byte(authorizedLine+latecomerLine), 0o600); err != nil {
			t.Fatalf("append key: %v", err)
		}
		client, err := dialSSH(addr, []gossh.AuthMethod{gossh.PublicKeys(latecomer)})
		if err != nil {
			t.Fatalf("a key added to the file was turned away: %v", err)
		}
		_ = client.Close()
	})
}

// TestSSHServerWithNoKeysStillTakesAKeylessClient is the other half of the
// promise: loopback with nothing configured keeps working, so the laptop user
// who has never written a keys file still attaches with a bare ssh command.
func TestSSHServerWithNoKeysStillTakesAKeylessClient(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SSH integration test in short mode")
	}

	addr := startAuthenticatedServer(t, "")
	client, err := dialSSH(addr, nil)
	if err != nil {
		t.Fatalf("zero-config loopback attach broke: %v", err)
	}
	defer func() { _ = client.Close() }()
	assertPaintsAFrame(t, client)
}

// assertPaintsAFrame proves the connection is a working TUIOS session rather
// than an open socket: it asks for a PTY, starts the shell, and waits for the
// first frame.
func assertPaintsAFrame(t *testing.T, client *gossh.Client) {
	t.Helper()
	sess, err := client.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer func() { _ = sess.Close() }()

	if err := sess.RequestPty("xterm-256color", 24, 80, gossh.TerminalModes{gossh.ECHO: 0}); err != nil {
		t.Fatalf("request pty: %v", err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := sess.Shell(); err != nil {
		t.Fatalf("start shell: %v", err)
	}

	got := make(chan int, 1)
	go func() {
		buf := make([]byte, 4096)
		total := 0
		for total == 0 {
			n, rerr := stdout.Read(buf)
			total += n
			if rerr != nil {
				break
			}
		}
		got <- total
	}()

	select {
	case n := <-got:
		if n == 0 {
			t.Fatal("the session produced no output")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for the session to paint")
	}
}
