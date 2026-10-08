package tuie2e

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

// webHandshake sends a WebSocket handshake to the tuios-web server at addr,
// with host in the Host header and in the Origin, as a browser on that name
// would. It returns the status code of the answer: 101 means a session.
func webHandshake(t *testing.T, addr, host string) int {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	fmt.Fprintf(conn, "GET /ws HTTP/1.1\r\nHost: %s\r\nOrigin: http://%s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", host, host, base64.StdEncoding.EncodeToString(nonce))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read the handshake answer for Host %s: %v", host, err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// TestWebAllowHostLetsAProxyNameIn runs tuios-web on loopback with
// --allow-host term.example and --no-auth, the way it runs behind a reverse
// proxy on the same machine.
//
// A handshake whose Host is term.example must get a session: the proxy
// forwards that name. A handshake whose Host is a name that is not on the
// list must get a 403: that is a DNS rebinding page, whose name now resolves
// to 127.0.0.1. A handshake on the loopback address is the positive half for
// the refusal, so a server that refuses everything cannot pass.
//
// sip v0.8.5 checks the Host header before tuios-web's own middleware runs.
// If tuios-web does not hand its --allow-host names to sip, the proxy name
// gets a 403. See NEGATIVE_CONTROLS.md.
func TestWebAllowHostLetsAProxyNameIn(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[startup]\nopen_default_window = true\n")
	addr, _ := startTuiosWeb(t, base, "--ephemeral", "--no-auth", "--allow-host", "term.example")
	_, port, _ := net.SplitHostPort(addr)

	for _, tc := range []struct {
		host string
		want int
	}{
		{addr, http.StatusSwitchingProtocols},
		{"localhost:" + port, http.StatusSwitchingProtocols},
		{"term.example:" + port, http.StatusSwitchingProtocols},
		{"term.example", http.StatusSwitchingProtocols},
		{"evil.example:" + port, http.StatusForbidden},
		{"term.example.evil.example:" + port, http.StatusForbidden},
	} {
		if got := webHandshake(t, addr, tc.host); got != tc.want {
			t.Errorf("a handshake with Host %s got %d, want %d", tc.host, got, tc.want)
		}
	}
}
