package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// startTestDaemon starts a real daemon listening on an isolated unix socket in a
// temp XDG_RUNTIME_DIR, so it does not touch the developer's live daemon, socket,
// or pid file. Resurrection state is redirected to a temp directory too, because
// the state dir is resolved at package init and cannot be redirected by setting
// an environment variable; without this, every test that creates a session writes
// a real state file into the developer's state directory. It returns the daemon
// and the socket path.
func startTestDaemon(t *testing.T) (*Daemon, string) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))

	t.Cleanup(useResurrectionDir(t.TempDir()))

	d := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true})
	if err := d.Start(); err != nil {
		t.Fatalf("daemon Start: %v", err)
	}
	t.Cleanup(d.Stop)

	sp, err := GetSocketPath()
	if err != nil {
		t.Fatalf("GetSocketPath: %v", err)
	}
	return d, sp
}

// setApprovalPeer stands place in for the process table that tells which pane
// a connection comes from. Nil goes back to the process table.
func (d *Daemon) setApprovalPeer(place peerPlacer) {
	if place == nil {
		d.approvalPeer.Store(nil)
		return
	}
	d.approvalPeer.Store(&place)
}

// verbConn is a raw JSON line client for the daemon socket.
type verbConn struct {
	conn net.Conn
	r    *bufio.Reader
}

func dialVerb(t *testing.T, socketPath string) *verbConn {
	t.Helper()
	conn, err := net.DialTimeout("unix", socketPath, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &verbConn{conn: conn, r: bufio.NewReader(conn)}
}

// send writes a raw line (a newline is appended).
func (c *verbConn) send(t *testing.T, line string) {
	t.Helper()
	_ = c.conn.SetWriteDeadline(time.Now().Add(3 * time.Second * testDeadlineScale))
	if _, err := c.conn.Write([]byte(line + "\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// readResp reads and decodes one response line.
func (c *verbConn) readResp(t *testing.T) map[string]any {
	t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(5 * time.Second * testDeadlineScale))
	line, err := c.r.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read: %v (partial=%q)", err, string(line))
	}
	var resp map[string]any
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("decode response %q: %v", string(line), err)
	}
	return resp
}

// call sends a request and returns the decoded response.
func (c *verbConn) call(t *testing.T, line string) map[string]any {
	t.Helper()
	c.send(t, line)
	return c.readResp(t)
}

// result extracts the result object, failing if the response carried an error.
func result(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	if e, ok := resp["error"]; ok && e != nil {
		t.Fatalf("expected result, got error: %v", e)
	}
	res, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("response has no result object: %v", resp)
	}
	return res
}

// errCode extracts the error code, failing if the response carried a result.
func errCode(t *testing.T, resp map[string]any) string {
	t.Helper()
	e, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error, got: %v", resp)
	}
	code, _ := e["code"].(string)
	return code
}

// makeSessionWithWindow creates a session holding one live daemon-owned window.
func makeSessionWithWindow(t *testing.T, d *Daemon, name string) *Session {
	t.Helper()
	sess, err := d.manager.CreateSession(name, &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := sess.AddDaemonWindow("Window", nil); err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	return sess
}

func TestVerbErrorCases(t *testing.T) {
	_, sp := startTestDaemon(t)
	c := dialVerb(t, sp)

	cases := []struct {
		name string
		line string
		code string
	}{
		{"malformed", `{"id":1,"verb":`, ErrVerbInvalidRequest},
		{"not-json", `this is not json`, ErrVerbInvalidRequest},
		{"missing-verb", `{"id":2}`, ErrVerbInvalidRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code := errCode(t, c.call(t, tc.line))
			if code != tc.code {
				t.Errorf("code = %q, want %q", code, tc.code)
			}
		})
	}
}

// TestVerbConnectionSurvivesBadLine verifies a malformed line does not desync or
// close the connection: a following valid request still works.
func TestVerbConnectionSurvivesBadLine(t *testing.T) {
	_, sp := startTestDaemon(t)
	c := dialVerb(t, sp)

	_ = errCode(t, c.call(t, `{"garbage`))
	res := result(t, c.call(t, `{"id":9,"verb":"list-verbs"}`))
	if res["type"] != "verb_list" {
		t.Errorf("connection did not recover; got %v", res)
	}
}

// TestVerbIDEcho verifies both numeric and string ids echo back verbatim.
func TestVerbIDEcho(t *testing.T) {
	_, sp := startTestDaemon(t)
	c := dialVerb(t, sp)

	if got := c.call(t, `{"id":7,"verb":"list-verbs"}`)["id"]; got != float64(7) {
		t.Errorf("numeric id = %v", got)
	}
	if got := c.call(t, `{"id":"req-42","verb":"list-verbs"}`)["id"]; got != "req-42" {
		t.Errorf("string id = %v", got)
	}
	// An absent id yields no id field on the response.
	if _, present := c.call(t, `{"verb":"list-verbs"}`)["id"]; present {
		t.Error("id present on response when request omitted it")
	}
}

// TestVerbConcurrentClients drives many independent JSON connections at once.
func TestVerbConcurrentClients(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "shared")

	const clients = 12
	const iters = 20
	var wg sync.WaitGroup
	errs := make(chan error, clients)
	for range clients {
		wg.Go(func() {
			c := dialVerb(t, sp)
			for range iters {
				resp := c.call(t, `{"verb":"list-windows","params":{"session":"shared"}}`)
				res, ok := resp["result"].(map[string]any)
				if !ok || res["type"] != "window_list" {
					errs <- fmt.Errorf("bad response: %v", resp)
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
