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

func startTestDaemon(t *testing.T) (*Daemon, string) {
	t.Helper()
	return startTestDaemonSized(t, "")
}

func startTestDaemonSized(t *testing.T, policy string) (*Daemon, string) {
	t.Helper()
	return startTestDaemonWith(t, &DaemonConfig{WindowSize: policy})
}

func startTestDaemonWith(t *testing.T, cfg *DaemonConfig) (*Daemon, string) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))

	t.Cleanup(useResurrectionDir(t.TempDir()))

	cfg.Version, cfg.DisableAutoRestore = "test", true
	d := NewDaemon(cfg)
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

func (d *Daemon) setApprovalPeer(place peerPlacer) {
	if place == nil {
		d.approvalPeer.Store(nil)
		return
	}
	d.approvalPeer.Store(&place)
}

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

func (c *verbConn) send(t *testing.T, line string) {
	t.Helper()
	_ = c.conn.SetWriteDeadline(time.Now().Add(3 * time.Second * testDeadlineScale))
	if _, err := c.conn.Write([]byte(line + "\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
}

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

func (c *verbConn) call(t *testing.T, line string) map[string]any {
	t.Helper()
	c.send(t, line)
	return c.readResp(t)
}

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

func errCode(t *testing.T, resp map[string]any) string {
	t.Helper()
	e, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error, got: %v", resp)
	}
	code, _ := e["code"].(string)
	return code
}

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
