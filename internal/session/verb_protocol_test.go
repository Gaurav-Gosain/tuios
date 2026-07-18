package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
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
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

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
	_ = c.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.conn.Write([]byte(line + "\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// readResp reads and decodes one response line.
func (c *verbConn) readResp(t *testing.T) map[string]any {
	t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
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

func TestVerbListVerbs(t *testing.T) {
	_, sp := startTestDaemon(t)
	c := dialVerb(t, sp)

	resp := c.call(t, `{"id":1,"verb":"list-verbs"}`)
	if resp["id"] != float64(1) {
		t.Errorf("id not echoed: %v", resp["id"])
	}
	res := result(t, resp)
	if res["type"] != "verb_list" {
		t.Errorf("type = %v, want verb_list", res["type"])
	}
	if res["version"] != float64(VerbProtocolVersion) {
		t.Errorf("version = %v, want %d", res["version"], VerbProtocolVersion)
	}
	verbs, ok := res["verbs"].([]any)
	if !ok || len(verbs) != len(verbRegistry) {
		t.Fatalf("verbs list wrong: %v", res["verbs"])
	}
}

func TestVerbListSessions(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "alpha")
	makeSessionWithWindow(t, d, "beta")

	c := dialVerb(t, sp)
	res := result(t, c.call(t, `{"id":"s","verb":"list-sessions"}`))
	if res["type"] != "session_list" {
		t.Errorf("type = %v", res["type"])
	}
	sessions, ok := res["sessions"].([]any)
	if !ok || len(sessions) != 2 {
		t.Fatalf("want 2 sessions, got %v", res["sessions"])
	}
}

func TestVerbSessionInfoAndListWindows(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")

	c := dialVerb(t, sp)

	info := result(t, c.call(t, `{"verb":"session-info","params":{"session":"work"}}`))
	if info["type"] != "session_info" || info["session_name"] != "work" {
		t.Errorf("session-info wrong: %v", info)
	}

	wins := result(t, c.call(t, `{"verb":"list-windows","params":{"session":"work"}}`))
	if wins["type"] != "window_list" {
		t.Errorf("list-windows type = %v", wins["type"])
	}
	if wins["total"] != float64(1) {
		t.Errorf("total windows = %v, want 1", wins["total"])
	}
}

func TestVerbNewAndCloseWindowHeadless(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess, err := d.manager.CreateSession("empty", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	c := dialVerb(t, sp)

	created := result(t, c.call(t, `{"verb":"new-window","params":{"session":"empty","name":"build"}}`))
	if created["type"] != "window_created" {
		t.Errorf("type = %v", created["type"])
	}
	winID, _ := created["window_id"].(string)
	if winID == "" {
		t.Fatalf("no window_id in %v", created)
	}
	if got := len(sess.GetState().Windows); got != 1 {
		t.Fatalf("session window count = %d, want 1", got)
	}

	closed := result(t, c.call(t, fmt.Sprintf(`{"verb":"close-window","params":{"session":"empty","window":%q}}`, winID)))
	if closed["type"] != "ok" {
		t.Errorf("close type = %v", closed["type"])
	}
	if got := len(sess.GetState().Windows); got != 0 {
		t.Fatalf("session window count after close = %d, want 0", got)
	}
}

func TestVerbSendTextAndCapturePane(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "cap")

	c := dialVerb(t, sp)

	ok := result(t, c.call(t, `{"verb":"send-text","params":{"session":"cap","text":"echo tuios-marker\n"}}`))
	if ok["type"] != "ok" {
		t.Errorf("send-text type = %v", ok["type"])
	}

	// Poll capture-pane for the echoed marker; the shell echoes asynchronously.
	found := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		res := result(t, c.call(t, `{"verb":"capture-pane","params":{"session":"cap","source":"recent"}}`))
		if res["type"] != "pane_content" {
			t.Fatalf("capture type = %v", res["type"])
		}
		content, _ := res["content"].(string)
		if len(content) > 0 && containsMarker(content) {
			found = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !found {
		t.Error("capture-pane never returned the echoed marker")
	}
}

func containsMarker(s string) bool {
	for i := 0; i+len("tuios-marker") <= len(s); i++ {
		if s[i:i+len("tuios-marker")] == "tuios-marker" {
			return true
		}
	}
	return false
}

func TestVerbSendKeysHeadless(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "keys")

	c := dialVerb(t, sp)
	res := result(t, c.call(t, `{"verb":"send-keys","params":{"session":"keys","keys":"ctrl+c"}}`))
	if res["type"] != "ok" {
		t.Errorf("send-keys type = %v", res["type"])
	}

	// A missing keys field is an invalid_params error.
	code := errCode(t, c.call(t, `{"verb":"send-keys","params":{"session":"keys"}}`))
	if code != ErrVerbInvalidParams {
		t.Errorf("missing keys code = %q, want %q", code, ErrVerbInvalidParams)
	}
}

func TestVerbResize(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "rz")

	c := dialVerb(t, sp)
	res := result(t, c.call(t, `{"verb":"resize","params":{"session":"rz","width":100,"height":40}}`))
	if res["type"] != "resized" || res["width"] != float64(100) || res["height"] != float64(40) {
		t.Errorf("resize result = %v", res)
	}

	code := errCode(t, c.call(t, `{"verb":"resize","params":{"session":"rz","width":0,"height":40}}`))
	if code != ErrVerbInvalidParams {
		t.Errorf("bad resize code = %q", code)
	}
}

func TestVerbOptionsRoundTrip(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "opt")

	c := dialVerb(t, sp)

	set := result(t, c.call(t, `{"verb":"set-option","params":{"session":"opt","key":"theme","value":"dracula"}}`))
	if set["type"] != "option_set" || set["value"] != "dracula" {
		t.Errorf("set-option result = %v", set)
	}

	get := result(t, c.call(t, `{"verb":"get-option","params":{"session":"opt","key":"theme"}}`))
	if get["value"] != "dracula" {
		t.Errorf("get-option value = %v", get["value"])
	}

	code := errCode(t, c.call(t, `{"verb":"get-option","params":{"session":"opt","key":"missing"}}`))
	if code != ErrVerbOptionNotFound {
		t.Errorf("missing option code = %q, want %q", code, ErrVerbOptionNotFound)
	}
}

func TestVerbKillSession(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "doomed")

	c := dialVerb(t, sp)
	res := result(t, c.call(t, `{"verb":"kill-session","params":{"session":"doomed"}}`))
	if res["type"] != "ok" {
		t.Errorf("kill type = %v", res["type"])
	}
	if d.manager.GetSession("doomed") != nil {
		t.Error("session still present after kill-session")
	}

	code := errCode(t, c.call(t, `{"verb":"kill-session","params":{"session":"doomed"}}`))
	if code != ErrVerbSessionNotFound {
		t.Errorf("kill missing code = %q", code)
	}
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
		{"unknown-verb", `{"id":3,"verb":"teleport"}`, ErrVerbUnknownVerb},
		{"unknown-session", `{"verb":"list-windows","params":{"session":"nope"}}`, ErrVerbSessionNotFound},
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

// TestVerbIDEchoTypes verifies both numeric and string ids echo back verbatim.
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
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := dialVerb(t, sp)
			for j := 0; j < iters; j++ {
				resp := c.call(t, `{"verb":"list-windows","params":{"session":"shared"}}`)
				res, ok := resp["result"].(map[string]any)
				if !ok || res["type"] != "window_list" {
					errs <- fmt.Errorf("bad response: %v", resp)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestBinaryClientStillWorks verifies the existing binary protocol keeps working
// on the same daemon that now also speaks JSON (backward compatibility).
func TestBinaryClientStillWorks(t *testing.T) {
	d, _ := startTestDaemon(t)
	makeSessionWithWindow(t, d, "binary")

	client := NewClient(&ClientConfig{Version: "test"})
	if err := client.Connect(); err != nil {
		t.Fatalf("binary Connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	sessions, err := client.ListSessions()
	if err != nil {
		t.Fatalf("binary ListSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Name != "binary" {
		t.Fatalf("binary client got %v", sessions)
	}
}
