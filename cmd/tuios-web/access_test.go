package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/sip"
)

// idleModel is the smallest program a session can run. These tests are about
// who gets through the handshake, not what they see after it.
type idleModel struct{}

func (idleModel) Init() tea.Cmd                       { return nil }
func (idleModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return idleModel{}, nil }
func (idleModel) View() tea.View                      { return tea.NewView("") }

// startAccessServer runs a real sip server on loopback, configured by access
// the way runWebServer configures it, and returns its port.
func startAccessServer(t *testing.T, access *webAccess) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	_, port, _ := net.SplitHostPort(l.Addr().String())
	_ = l.Close()

	cfg := sip.DefaultConfig()
	cfg.Host = "127.0.0.1"
	cfg.Port = port
	// No AllowInsecureNoTLS here: runWebServer sets it only for --insecure,
	// so apply must make a loopback password work on its own.
	access.apply(&cfg)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = sip.NewServer(cfg).ServeWithProgram(ctx, func(sess sip.Session) *tea.Program {
			return tea.NewProgram(idleModel{}, sip.MakeOptions(sess)...)
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	})

	deadline := time.Now().Add(10 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+port, time.Second)
		if err == nil {
			_ = c.Close()
			return port
		}
		if time.Now().After(deadline) {
			t.Fatalf("web server never listened: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// upgrade sends a WebSocket handshake for /ws to the server on port, naming
// host in the Host header and origin in the Origin header, the way a browser
// would. It returns the status code of the answer: 101 means a session.
func upgrade(t *testing.T, port, host, origin, auth string) int {
	t.Helper()
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	var req bytes.Buffer
	fmt.Fprintf(&req, "GET /ws HTTP/1.1\r\nHost: %s\r\n", host)
	if origin != "" {
		fmt.Fprintf(&req, "Origin: %s\r\n", origin)
	}
	if auth != "" {
		fmt.Fprintf(&req, "Authorization: Basic %s\r\n", base64.StdEncoding.EncodeToString([]byte(auth)))
	}
	req.WriteString("Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n")
	if _, err := conn.Write(req.Bytes()); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read handshake answer: %v", err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// TestLoopbackRefusesAForeignHost is the DNS rebinding case. A web page on
// evil.example whose name now resolves to 127.0.0.1 opens a WebSocket to the
// server. Origin and Host both say evil.example, so the same-origin rule
// passes. The Host check must stop it.
func TestLoopbackRefusesAForeignHost(t *testing.T) {
	access, err := planWebAccess(&bytes.Buffer{}, webAccessFlags{host: "localhost", port: "7681"})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	port := startAccessServer(t, access)

	evil := "evil.example:" + port
	if code := upgrade(t, port, evil, "http://"+evil, ""); code == http.StatusSwitchingProtocols {
		t.Fatalf("a rebinding page on %s got a session", evil)
	}

	for _, host := range []string{"localhost", "127.0.0.1", "[::1]"} {
		h := host + ":" + port
		if code := upgrade(t, port, h, "http://"+h, ""); code != http.StatusSwitchingProtocols {
			t.Errorf("a page on %s was refused with %d", h, code)
		}
	}
}

// TestAllowHostAddsAName: a reverse proxy on this machine forwards its own
// name in Host. --allow-host lets that name through, and the password still
// applies.
func TestAllowHostAddsAName(t *testing.T) {
	file := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(file, []byte("proxy-pw\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	access, err := planWebAccess(&bytes.Buffer{}, webAccessFlags{host: "127.0.0.1", port: "7681", passwordFile: file, allowHosts: []string{"term.example"}})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	port := startAccessServer(t, access)
	h := "term.example:" + port
	if code := upgrade(t, port, h, "http://"+h, "tuios:proxy-pw"); code != http.StatusSwitchingProtocols {
		t.Fatalf("an allowed host was refused with %d", code)
	}
	if code := upgrade(t, port, h, "http://"+h, ""); code != http.StatusUnauthorized {
		t.Fatalf("an allowed host with no password got %d, want 401", code)
	}
	other := "other.example:" + port
	if code := upgrade(t, port, other, "http://"+other, "tuios:proxy-pw"); code == http.StatusSwitchingProtocols {
		t.Fatal("a host that is not on the list got a session")
	}
}

// TestAllowHostRules: --allow-host needs a password or --no-auth, works only
// on a loopback bind, and takes a host name with no port.
func TestAllowHostRules(t *testing.T) {
	t.Setenv(webPasswordEnv, "")
	for name, tc := range map[string]struct {
		f    webAccessFlags
		fail bool
	}{
		"no password":        {webAccessFlags{host: "localhost", allowHosts: []string{"term.example"}}, true},
		"no-auth":            {webAccessFlags{host: "localhost", allowHosts: []string{"term.example"}, noAuth: true}, false},
		"random password":    {webAccessFlags{host: "localhost", allowHosts: []string{"term.example"}, randomPassword: true}, false},
		"with a port":        {webAccessFlags{host: "localhost", allowHosts: []string{"term.example:443"}, randomPassword: true}, true},
		"a URL":              {webAccessFlags{host: "localhost", allowHosts: []string{"https://term.example"}, randomPassword: true}, true},
		"on a network bind":  {webAccessFlags{host: "0.0.0.0", allowHosts: []string{"term.example"}, randomPassword: true}, true},
		"an IPv6 literal ok": {webAccessFlags{host: "localhost", allowHosts: []string{"[fd00::1]"}, randomPassword: true}, false},
	} {
		_, err := planWebAccess(&bytes.Buffer{}, tc.f)
		if tc.fail && err == nil {
			t.Errorf("%s: accepted", name)
		}
		if !tc.fail && err != nil {
			t.Errorf("%s: refused: %v", name, err)
		}
	}
}

// TestNetworkBindHasNoHostCheck: on a network bind the password is the guard.
// A LAN client that reaches the server by its address gets a session.
func TestNetworkBindHasNoHostCheck(t *testing.T) {
	a, err := planWebAccess(&bytes.Buffer{}, webAccessFlags{host: "0.0.0.0", port: "7681", randomPassword: true})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if a.hosts != nil {
		t.Fatalf("a network bind checks the Host header against %v", a.hosts)
	}
}

// TestLoopbackWithNoPasswordSaysOthersCanConnect: the one start line.
func TestLoopbackWithNoPasswordSaysOthersCanConnect(t *testing.T) {
	t.Setenv(webPasswordEnv, "")
	var out bytes.Buffer
	if _, err := planWebAccess(&out, webAccessFlags{host: "localhost"}); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !strings.Contains(out.String(), "other users on this machine can connect") || !strings.Contains(out.String(), "--random-password") {
		t.Fatalf("start line is %q", out.String())
	}
}

// TestPasswordGuardsTheSession drives the handshake with and without the
// password.
func TestPasswordGuardsTheSession(t *testing.T) {
	file := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(file, []byte("s3cret-horse\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	access, err := planWebAccess(&bytes.Buffer{}, webAccessFlags{host: "127.0.0.1", port: "7681", passwordFile: file})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	port := startAccessServer(t, access)
	h := "127.0.0.1:" + port

	if code := upgrade(t, port, h, "http://"+h, ""); code != http.StatusUnauthorized {
		t.Errorf("no password: got %d, want 401", code)
	}
	if code := upgrade(t, port, h, "http://"+h, "tuios:wrong"); code != http.StatusUnauthorized {
		t.Errorf("wrong password: got %d, want 401", code)
	}
	if code := upgrade(t, port, h, "http://"+h, "tuios:s3cret-horse"); code != http.StatusSwitchingProtocols {
		t.Errorf("right password: got %d, want 101", code)
	}
}

// TestNetworkBindNeedsAPassword: a bind outside this machine gives a shell to
// anyone who reaches the port. TLS encrypts the traffic but does not say who
// is connecting, so --auto-tls alone is not enough.
func TestNetworkBindNeedsAPassword(t *testing.T) {
	t.Setenv(webPasswordEnv, "")
	var out bytes.Buffer
	_, err := planWebAccess(&out, webAccessFlags{host: "0.0.0.0", port: "7681"})
	if err == nil {
		t.Fatal("a network bind with no password was accepted")
	}
	if !strings.Contains(out.String(), "--random-password") || !strings.Contains(out.String(), "--password-file") {
		t.Fatalf("the refusal does not say how to set a password:\n%s", out.String())
	}

	for name, f := range map[string]webAccessFlags{
		"random password": {host: "0.0.0.0", port: "7681", randomPassword: true},
		"no-auth":         {host: "0.0.0.0", port: "7681", noAuth: true},
	} {
		if _, err := planWebAccess(&bytes.Buffer{}, f); err != nil {
			t.Errorf("%s: refused: %v", name, err)
		}
	}

	// An empty host listens on every interface.
	if _, err := planWebAccess(&bytes.Buffer{}, webAccessFlags{host: "", port: "7681"}); err == nil {
		t.Error("an empty host, which listens on every interface, was served with no password")
	}
}

// TestRandomPasswordIsPrinted: the generated password is long and printed once,
// with a URL that carries it.
func TestRandomPasswordIsPrinted(t *testing.T) {
	a, err := planWebAccess(&bytes.Buffer{}, webAccessFlags{host: "0.0.0.0", port: "7681", randomPassword: true})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	var out bytes.Buffer
	a.announcePassword(&out)
	if len(a.password) < 20 {
		t.Fatalf("password %q is too short", a.password)
	}
	if !strings.Contains(out.String(), a.password) {
		t.Fatalf("the password is not printed:\n%s", out.String())
	}
	b, _ := planWebAccess(&bytes.Buffer{}, webAccessFlags{host: "0.0.0.0", port: "7681", randomPassword: true})
	if a.password == b.password {
		t.Fatal("two runs made the same password")
	}
}

// TestPasswordFromEnvLeavesTheEnvironment: panes inherit this process's
// environment. The password must not reach them.
func TestPasswordFromEnvLeavesTheEnvironment(t *testing.T) {
	t.Setenv(webPasswordEnv, "from-the-env")
	a, err := planWebAccess(&bytes.Buffer{}, webAccessFlags{host: "0.0.0.0", port: "7681"})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if a.password != "from-the-env" {
		t.Fatalf("password = %q", a.password)
	}
	if _, set := os.LookupEnv(webPasswordEnv); set {
		t.Fatalf("%s is still set, so every pane can read it", webPasswordEnv)
	}
}

// TestPasswordFileChecks: a file others can read, an empty file, and two
// password sources at once are all refused.
func TestPasswordFileChecks(t *testing.T) {
	t.Setenv(webPasswordEnv, "")
	dir := t.TempDir()

	empty := filepath.Join(dir, "empty")
	_ = os.WriteFile(empty, []byte("\n"), 0o600)
	if _, err := planWebAccess(&bytes.Buffer{}, webAccessFlags{host: "0.0.0.0", passwordFile: empty}); err == nil {
		t.Error("an empty password file was accepted")
	}

	if runtime.GOOS != "windows" {
		open := filepath.Join(dir, "open")
		_ = os.WriteFile(open, []byte("pw"), 0o600)
		_ = os.Chmod(open, 0o644)
		if _, err := planWebAccess(&bytes.Buffer{}, webAccessFlags{host: "0.0.0.0", passwordFile: open}); err == nil {
			t.Error("a password file others can read was accepted")
		}
	}

	good := filepath.Join(dir, "good")
	_ = os.WriteFile(good, []byte("pw"), 0o600)
	if _, err := planWebAccess(&bytes.Buffer{}, webAccessFlags{host: "0.0.0.0", passwordFile: good, randomPassword: true}); err == nil {
		t.Error("two password sources were accepted")
	}
	if _, err := planWebAccess(&bytes.Buffer{}, webAccessFlags{host: "0.0.0.0", passwordFile: good, noAuth: true}); err == nil {
		t.Error("a password with --no-auth was accepted")
	}
}
