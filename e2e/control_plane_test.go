//go:build e2e

// Package e2e drives a real tuios daemon end to end over its unix socket,
// exercising the JSON verb control plane and session resurrection the way an
// external scripting client would: raw socket writes, no in-process shortcuts.
//
// These tests are behind the "e2e" build tag because they spawn real daemons
// and real shells. Run them with:
//
//	GOFLAGS="$(scripts/goflags.sh)" go build -o /tmp/tuios ./cmd/tuios
//	TUIOS_BIN=/tmp/tuios go test -tags e2e ./e2e/...
//
// When TUIOS_BIN is unset the tests build the binary themselves into a temp
// directory, so a bare "go test -tags e2e ./e2e/..." also works.
//
// Every test runs in a hermetic environment: XDG_RUNTIME_DIR (which holds the
// daemon socket and pid file) and XDG_STATE_HOME (which holds resurrection
// state) are redirected into the test's TempDir, so the developer's real
// daemon, sessions, and saved state are never touched.
package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/hostsync"
)

type env struct {
	t      *testing.T
	bin    string
	dirs   map[string]string
	socket string
}

func newEnv(t *testing.T) *env {
	t.Helper()

	bin := os.Getenv("TUIOS_BIN")
	if bin == "" {
		bin = buildTuios(t)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("tuios binary not usable at %q: %v", bin, err)
	}

	base := t.TempDir()
	dirs := map[string]string{}
	for _, key := range []string{
		"XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_STATE_HOME",
		"XDG_CACHE_HOME", "XDG_DATA_HOME",
	} {
		dir := filepath.Join(base, key)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", key, err)
		}
		dirs[key] = dir
	}

	e := &env{
		t:      t,
		bin:    bin,
		dirs:   dirs,
		socket: filepath.Join(dirs["XDG_RUNTIME_DIR"], "tuios", "tuios.sock"),
	}
	t.Cleanup(e.killServer)
	return e
}

func buildTuios(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "tuios")
	goflags, err := hostsync.NoinlineGoFlags("..", os.Getenv("GOFLAGS"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", out, "./cmd/tuios")
	cmd.Dir = ".."
	cmd.Env = append(os.Environ(), "GOFLAGS="+goflags)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building tuios: %v\n%s", err, b)
	}
	return out
}

func (e *env) environ() []string {
	out := append([]string{}, os.Environ()...)
	filtered := out[:0]
	for _, kv := range out {
		name := strings.SplitN(kv, "=", 2)[0]
		if name == "TUIOS_SOCKET" {
			continue
		}
		if _, isOverridden := e.dirs[name]; !isOverridden {
			filtered = append(filtered, kv)
		}
	}
	out = filtered
	for k, v := range e.dirs {
		out = append(out, k+"="+v)
	}
	return append(out, "SHELL=/bin/sh", "TERM=xterm-256color")
}

func (e *env) run(args ...string) (string, error) {
	e.t.Helper()
	cmd := exec.Command(e.bin, args...)
	cmd.Env = e.environ()
	b, err := cmd.CombinedOutput()
	return string(b), err
}

func (e *env) mustRun(args ...string) string {
	e.t.Helper()
	out, err := e.run(args...)
	if err != nil {
		e.t.Fatalf("tuios %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func (e *env) killServer() {
	_, _ = e.run("kill-server")
	e.awaitDaemonGone(10 * time.Second)
	e.awaitStateSettled(3 * time.Second)
}

func (e *env) awaitStateSettled(timeout time.Duration) {
	dir := filepath.Join(e.dirs["XDG_STATE_HOME"], "tuios", "sessions")

	read := func() string {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return "<none>"
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		sort.Strings(names)
		return strings.Join(names, ",")
	}

	deadline := time.Now().Add(timeout)
	previous := read()
	for time.Now().Before(deadline) {
		time.Sleep(150 * time.Millisecond)
		current := read()
		if current == previous {
			return
		}
		previous = current
	}
}

func (e *env) awaitDaemonGone(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(e.socket); os.IsNotExist(err) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (e *env) waitForStateFile(session string, timeout time.Duration) string {
	e.t.Helper()
	path := filepath.Join(e.dirs["XDG_STATE_HOME"], "tuios", "sessions", session+".json")
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return path
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.t.Fatalf("resurrection state %s never appeared", path)
	return ""
}

func (e *env) waitForSocket(timeout time.Duration) {
	e.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", e.socket, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.t.Fatalf("daemon socket %s never became connectable", e.socket)
}

type verbConn struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

func (e *env) dial() *verbConn {
	e.t.Helper()
	conn, err := net.DialTimeout("unix", e.socket, 5*time.Second)
	if err != nil {
		e.t.Fatalf("dial %s: %v", e.socket, err)
	}
	e.t.Cleanup(func() { _ = conn.Close() })
	return &verbConn{t: e.t, conn: conn, r: bufio.NewReader(conn)}
}

func (c *verbConn) call(id int, verb string, params map[string]any, timeout time.Duration) map[string]any {
	c.t.Helper()

	req := map[string]any{"id": id, "verb": verb}
	if params != nil {
		req["params"] = params
	}
	line, err := json.Marshal(req)
	if err != nil {
		c.t.Fatalf("marshal request: %v", err)
	}

	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.conn.Write(append(line, '\n')); err != nil {
		c.t.Fatalf("write %s: %v", verb, err)
	}

	_ = c.conn.SetReadDeadline(time.Now().Add(timeout))
	raw, err := c.r.ReadBytes('\n')
	if err != nil {
		c.t.Fatalf("read response to %s: %v", verb, err)
	}

	var resp map[string]any
	if err := json.Unmarshal(raw, &resp); err != nil {
		c.t.Fatalf("decode response to %s: %v\nraw: %s", verb, err, raw)
	}
	if gotID, ok := resp["id"]; ok {
		if int(gotID.(float64)) != id {
			c.t.Fatalf("%s: response id = %v, want %d", verb, gotID, id)
		}
	}
	return resp
}

func (c *verbConn) result(id int, verb string, params map[string]any, timeout time.Duration) map[string]any {
	c.t.Helper()
	resp := c.call(id, verb, params, timeout)
	if e, ok := resp["error"]; ok {
		c.t.Fatalf("%s returned error: %v", verb, e)
	}
	res, ok := resp["result"].(map[string]any)
	if !ok {
		c.t.Fatalf("%s: response has no result object: %v", verb, resp)
	}
	return res
}

// TestHeadlessControlPlane is the core dogfood: create a detached session with
// no client ever attached, then drive it entirely over the JSON verb protocol.
// It proves the daemon owns window state and pane content on its own, which is
// the whole point of the control plane.
func TestHeadlessControlPlane(t *testing.T) {
	e := newEnv(t)

	e.mustRun("new", "--detach", "ctl")
	e.waitForSocket(10 * time.Second)

	c := e.dial()

	verbs := c.result(1, "list-verbs", nil, 5*time.Second)
	if _, ok := verbs["version"]; !ok {
		t.Fatalf("list-verbs did not report a protocol version: %v", verbs)
	}

	wl := c.result(2, "list-windows", map[string]any{"session": "ctl"}, 5*time.Second)
	initial := int(wl["total"].(float64))
	if initial < 1 {
		t.Fatalf("detached session has %d windows, want at least 1", initial)
	}

	created := c.result(3, "new-window", map[string]any{"session": "ctl", "name": "worker"}, 10*time.Second)
	winID, _ := created["window_id"].(string)
	if winID == "" {
		t.Fatalf("new-window returned no window_id: %v", created)
	}

	wl = c.result(4, "list-windows", map[string]any{"session": "ctl"}, 5*time.Second)
	if got := int(wl["total"].(float64)); got != initial+1 {
		t.Fatalf("after new-window: %d windows, want %d", got, initial+1)
	}
	if !strings.Contains(fmt.Sprint(wl["windows"]), "worker") {
		t.Fatalf("new window not listed by name: %v", wl["windows"])
	}

	c.result(5, "send-text", map[string]any{
		"session": "ctl",
		"window":  winID,
		"text":    "echo marker-$((21*2))\n",
	}, 5*time.Second)

	wr := c.result(6, "wait-for", map[string]any{
		"condition": "window-output",
		"session":   "ctl",
		"window":    winID,
		"pattern":   "marker-42",
		"timeout":   20000,
	}, 30*time.Second)
	if matched, _ := wr["matched"].(bool); !matched {
		t.Fatalf("wait-for did not match: %v", wr)
	}

	pane := c.result(7, "capture-pane", map[string]any{
		"session": "ctl",
		"window":  winID,
		"source":  "recent",
	}, 10*time.Second)
	content, _ := pane["content"].(string)
	if !strings.Contains(content, "marker-42") {
		t.Fatalf("capture-pane missing command output.\n--- pane ---\n%s", content)
	}

	c.result(8, "set-option", map[string]any{"session": "ctl", "key": "appearance.sidebar.position", "value": "right"}, 5*time.Second)
	opt := c.result(9, "get-option", map[string]any{"session": "ctl", "key": "appearance.sidebar.position"}, 5*time.Second)
	if got, _ := opt["value"].(string); got != "right" {
		t.Fatalf("get-option = %q, want \"right\"", got)
	}
	if got, _ := opt["source"].(string); got != "session" {
		t.Fatalf("get-option source = %q, want \"session\"", got)
	}

	c.result(10, "close-window", map[string]any{"session": "ctl", "window": winID}, 10*time.Second)
	wl = c.result(11, "list-windows", map[string]any{"session": "ctl"}, 5*time.Second)
	if got := int(wl["total"].(float64)); got != initial {
		t.Fatalf("after close-window: %d windows, want %d", got, initial)
	}

	c.result(12, "kill-session", map[string]any{"session": "ctl"}, 10*time.Second)
	sessions := c.result(13, "list-sessions", nil, 5*time.Second)
	if strings.Contains(fmt.Sprint(sessions["sessions"]), "\"ctl\"") {
		t.Fatalf("killed session still listed: %v", sessions["sessions"])
	}
}

// TestWaitForTimeoutIsTyped pins the failure contract a script depends on: a
// condition that never matches must come back as a typed "timeout" error
// rather than hanging or returning a generic failure.
func TestWaitForTimeoutIsTyped(t *testing.T) {
	e := newEnv(t)
	e.mustRun("new", "--detach", "tmo")
	e.waitForSocket(10 * time.Second)

	c := e.dial()
	resp := c.call(1, "wait-for", map[string]any{
		"condition": "window-output",
		"session":   "tmo",
		"pattern":   "this-never-appears-anywhere",
		"timeout":   1000,
	}, 15*time.Second)

	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("wait-for on an unmatchable pattern returned success: %v", resp)
	}
	if code, _ := errObj["code"].(string); code != "timeout" {
		t.Fatalf("error code = %q, want \"timeout\"", code)
	}
}

// TestUnknownVerbDoesNotKillConnection proves the dispatch loop is robust: a
// bad request is answered with a typed error and the same connection stays
// usable, which is what keeps a long lived scripting client alive.
func TestUnknownVerbDoesNotKillConnection(t *testing.T) {
	e := newEnv(t)
	e.mustRun("new", "--detach", "rbst")
	e.waitForSocket(10 * time.Second)

	c := e.dial()

	resp := c.call(1, "no-such-verb", nil, 5*time.Second)
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("unknown verb returned success: %v", resp)
	}
	if code, _ := errObj["code"].(string); code != "unknown_verb" {
		t.Fatalf("error code = %q, want \"unknown_verb\"", code)
	}

	if _, ok := c.result(2, "list-sessions", nil, 5*time.Second)["sessions"]; !ok {
		t.Fatal("connection unusable after an unknown verb")
	}
}

// TestKillServerIsSynchronous proves the contract scripts depend on: when
// kill-server returns, the daemon has finished. Session state is already on
// disk and the socket is already gone, with no polling in between.
//
// The old behaviour was fire and forget: kill-server sent SIGTERM and returned
// while the daemon was still saving, so 'kill-server && start-server' could
// start a new daemon that read state the old one had not finished writing.
func TestKillServerIsSynchronous(t *testing.T) {
	e := newEnv(t)

	e.mustRun("new", "--detach", "synckill")
	e.waitForSocket(10 * time.Second)

	c := e.dial()
	c.result(1, "new-window", map[string]any{"session": "synckill", "name": "durable"}, 10*time.Second)

	stateDir := filepath.Join(e.dirs["XDG_STATE_HOME"], "tuios", "sessions")
	statePath := filepath.Join(stateDir, "synckill.json")
	if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("clearing pre-shutdown state: %v", err)
	}

	e.mustRun("kill-server")

	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("kill-server returned before session state was saved: %v", err)
	}
	if _, err := os.Stat(e.socket); !os.IsNotExist(err) {
		t.Errorf("kill-server returned while the socket was still present: %v", err)
	}

	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("reading state: %v", err)
	}
	var saved map[string]any
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatalf("state written by shutdown is not valid JSON (%d bytes): %v", len(raw), err)
	}
	if !strings.Contains(string(raw), "durable") {
		t.Errorf("saved state is missing the window created before shutdown: %s", raw)
	}

	if out, err := e.run("kill-server"); err != nil {
		t.Errorf("second kill-server failed: %v (%s)", err, out)
	}
}

// TestResurrectionAcrossDaemonRestart proves the cold start path: a session's
// windows come back after the daemon process dies, with live PTYs that still
// respond to control verbs. This is the resurrection feature's real contract,
// exercised across an actual process boundary rather than in process.
func TestResurrectionAcrossDaemonRestart(t *testing.T) {
	e := newEnv(t)

	e.mustRun("new", "--detach", "revive")
	e.waitForSocket(10 * time.Second)

	c := e.dial()
	c.result(1, "new-window", map[string]any{"session": "revive", "name": "phoenix"}, 10*time.Second)
	before := c.result(2, "list-windows", map[string]any{"session": "revive"}, 5*time.Second)
	wantTotal := int(before["total"].(float64))

	e.mustRun("kill-server")

	stateDir := filepath.Join(e.dirs["XDG_STATE_HOME"], "tuios", "sessions")
	e.awaitDaemonGone(15 * time.Second)
	e.waitForStateFile("revive", 15*time.Second)

	daemon := exec.Command(e.bin, "daemon")
	daemon.Env = e.environ()
	if err := daemon.Start(); err != nil {
		t.Fatalf("starting daemon: %v", err)
	}
	t.Cleanup(func() { _ = daemon.Process.Kill() })
	e.waitForSocket(15 * time.Second)

	c2 := e.dial()
	after := c2.result(1, "list-windows", map[string]any{"session": "revive"}, 10*time.Second)
	if got := int(after["total"].(float64)); got != wantTotal {
		t.Fatalf("after restart: %d windows, want %d", got, wantTotal)
	}
	if !strings.Contains(fmt.Sprint(after["windows"]), "phoenix") {
		t.Fatalf("named window did not survive restart: %v", after["windows"])
	}

	c2.result(2, "send-text", map[string]any{
		"session": "revive",
		"text":    "echo revived-$((6*7))\n",
	}, 5*time.Second)
	wr := c2.result(3, "wait-for", map[string]any{
		"condition": "window-output",
		"session":   "revive",
		"pattern":   "revived-42",
		"timeout":   20000,
	}, 30*time.Second)
	if matched, _ := wr["matched"].(bool); !matched {
		t.Fatalf("restored window's shell is not live: %v", wr)
	}

	c2.result(4, "kill-session", map[string]any{"session": "revive"}, 10*time.Second)
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		t.Fatalf("reading state dir: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() == "revive.json" {
			t.Fatal("killed session left resurrection state behind")
		}
	}
}

// TestUnknownVerbHintsAtTheRightOne extends the robustness test above: a bad
// verb must not only be answered with a typed error, it must tell the caller how
// to recover. This is what lets an agent correct itself without a human.
func TestUnknownVerbHintsAtTheRightOne(t *testing.T) {
	e := newEnv(t)
	e.mustRun("new", "--detach", "hints")
	e.waitForSocket(10 * time.Second)

	c := e.dial()

	resp := c.call(1, "list-window", nil, 5*time.Second)
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("unknown verb returned success: %v", resp)
	}
	hint, ok := errObj["hint"].(map[string]any)
	if !ok {
		t.Fatalf("unknown verb carried no hint: %v", errObj)
	}
	if got, _ := hint["did_you_mean"].(string); got != "list-windows" {
		t.Errorf("did_you_mean = %q, want list-windows", got)
	}
	if got, _ := hint["verb"].(string); got != "list-verbs" {
		t.Errorf("hint verb = %q, want list-verbs", got)
	}

	suggested, _ := hint["did_you_mean"].(string)
	if _, ok := c.result(2, suggested, map[string]any{"session": "hints"}, 5*time.Second)["windows"]; !ok {
		t.Fatal("the suggested verb did not work")
	}
}

// TestSessionNotFoundListsWhatExists proves the most common agent mistake, a
// wrong session name, comes back with the live names and the closest match.
func TestSessionNotFoundListsWhatExists(t *testing.T) {
	e := newEnv(t)
	e.mustRun("new", "--detach", "alpha")
	e.mustRun("new", "--detach", "beta")
	e.waitForSocket(10 * time.Second)

	c := e.dial()
	resp := c.call(1, "list-windows", map[string]any{"session": "alfa"}, 5*time.Second)

	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected an error for an unknown session: %v", resp)
	}
	if code, _ := errObj["code"].(string); code != "session_not_found" {
		t.Fatalf("code = %v, want session_not_found", errObj["code"])
	}
	hint, ok := errObj["hint"].(map[string]any)
	if !ok {
		t.Fatalf("session_not_found carried no hint: %v", errObj)
	}
	if got, _ := hint["did_you_mean"].(string); got != "alpha" {
		t.Errorf("did_you_mean = %q, want alpha", got)
	}

	available, _ := hint["available"].([]any)
	found := map[string]bool{}
	for _, v := range available {
		if s, ok := v.(string); ok {
			found[s] = true
		}
	}
	if !found["alpha"] || !found["beta"] {
		t.Errorf("available = %v, want both live sessions", available)
	}
}

// TestHelloReportsTheProtocolRange exercises the handshake over the real socket,
// which is what a client uses to tell a usable daemon from one left over across
// an upgrade.
func TestHelloReportsTheProtocolRange(t *testing.T) {
	e := newEnv(t)
	e.mustRun("new", "--detach", "hello")
	e.waitForSocket(10 * time.Second)

	c := e.dial()
	res := c.result(1, "hello", map[string]any{
		"client":   "e2e",
		"version":  "test",
		"protocol": 1,
	}, 5*time.Second)

	if res["type"] != "hello" {
		t.Errorf("type = %v, want hello", res["type"])
	}
	protocol, _ := res["protocol"].(float64)
	if protocol < 1 {
		t.Errorf("protocol = %v, want at least 1", res["protocol"])
	}
	minProtocol, _ := res["min_protocol"].(float64)
	if minProtocol > protocol {
		t.Errorf("min_protocol %v exceeds protocol %v", minProtocol, protocol)
	}
	if pid, _ := res["pid"].(float64); pid <= 0 {
		t.Errorf("pid = %v, want the daemon's process id", res["pid"])
	}

	resp := c.call(2, "hello", map[string]any{"version": "9.9.9", "protocol": 9999}, 5*time.Second)
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("a future protocol was accepted: %v", resp)
	}
	if code, _ := errObj["code"].(string); code != "protocol_mismatch" {
		t.Errorf("code = %v, want protocol_mismatch", errObj["code"])
	}
	hint, _ := errObj["hint"].(map[string]any)
	if cmd, _ := hint["command"].(string); cmd != "tuios kill-server" {
		t.Errorf("hint command = %v, want tuios kill-server", hint["command"])
	}
}

// TestListVerbsIsSelfDescribing proves an agent can learn the whole control
// surface from one call: every verb documented, with parameters typed and
// examples that parse.
func TestListVerbsIsSelfDescribing(t *testing.T) {
	e := newEnv(t)
	e.mustRun("new", "--detach", "introspect")
	e.waitForSocket(10 * time.Second)

	c := e.dial()
	res := c.result(1, "list-verbs", nil, 5*time.Second)

	verbs, ok := res["verbs"].([]any)
	if !ok || len(verbs) == 0 {
		t.Fatalf("list-verbs returned no verbs: %v", res)
	}
	if _, ok := res["error_codes"].([]any); !ok {
		t.Error("list-verbs returned no error-code catalog")
	}
	if _, ok := res["envelope"].(map[string]any); !ok {
		t.Error("list-verbs returned no envelope documentation")
	}

	seen := map[string]bool{}
	for _, raw := range verbs {
		v, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("verb entry is not an object: %v", raw)
		}
		name, _ := v["verb"].(string)
		seen[name] = true

		if desc, _ := v["description"].(string); desc == "" {
			t.Errorf("verb %q has no description", name)
		}
		if _, ok := v["params"].([]any); !ok {
			t.Errorf("verb %q has no params list", name)
		}
		examples, _ := v["examples"].([]any)
		for _, rawEx := range examples {
			ex, _ := rawEx.(string)
			var parsed map[string]any
			if err := json.Unmarshal([]byte(ex), &parsed); err != nil {
				t.Errorf("verb %q has an unparseable example %q: %v", name, ex, err)
				continue
			}
			if parsed["verb"] != name {
				t.Errorf("verb %q has an example for %v", name, parsed["verb"])
			}
		}
	}

	for _, want := range []string{
		"hello", "list-verbs", "list-sessions", "list-windows", "new-window",
		"close-window", "send-keys", "send-text", "capture-pane", "kill-session",
		"subscribe", "wait-for",
	} {
		if !seen[want] {
			t.Errorf("list-verbs omitted %q", want)
		}
	}
}

// TestCLIExplainsAMissingDaemon covers the CLI half over a real process: with no
// daemon running, the message must name the fix rather than leaking a socket
// error.
func TestCLIExplainsAMissingDaemon(t *testing.T) {
	e := newEnv(t)

	out, err := e.run("list-windows")
	if err == nil {
		t.Fatalf("expected list-windows to fail with no daemon, got: %s", out)
	}
	for _, want := range []string{"daemon is not running", "Most likely cause", "Fix:", "tuios new"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestCLIExplainsAMissingSession covers the other most common CLI mistake: a
// session name that does not exist must list the ones that do.
func TestCLIExplainsAMissingSession(t *testing.T) {
	e := newEnv(t)
	e.mustRun("new", "--detach", "real")
	e.waitForSocket(10 * time.Second)

	out, err := e.run("list-windows", "--session", "reel")
	if err == nil {
		t.Fatalf("expected a failure for an unknown session, got: %s", out)
	}
	for _, want := range []string{"reel", "Did you mean", "real", "Fix:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestAgentsTalkToEachOtherHeadless is the cross-agent surface driven the way an
// orchestrating agent would drive it: over the socket, on a session no client
// ever attached to, with nothing borrowed from the daemon's internals.
//
// It exists because every unit test for this feature is inside the package that
// implements it. The claim that it works from outside, and works detached, is
// only worth something if something outside checks it.
func TestAgentsTalkToEachOtherHeadless(t *testing.T) {
	e := newEnv(t)
	e.mustRun("new", "--detach", "crew")
	e.waitForSocket(10 * time.Second)
	e.mustRun("new-window", "-s", "crew", "orchestrator", "--no-focus")
	e.mustRun("new-window", "-s", "crew", "review", "--no-focus")

	c := e.dial()

	res := c.result(1, "list-agents", map[string]any{"session": "crew"}, 5*time.Second)
	if res["total"] != float64(0) {
		t.Fatalf("a session where nothing reported listed %v agents", res["total"])
	}
	res = c.result(2, "list-agents", map[string]any{"session": "crew", "all": true}, 5*time.Second)
	agents, _ := res["agents"].([]any)
	if len(agents) != 3 {
		t.Fatalf("--all listed %d windows, want the three that exist", len(agents))
	}

	var orch, review string
	for _, raw := range agents {
		row := raw.(map[string]any)
		switch row["name"] {
		case "orchestrator":
			orch = row["window_id"].(string)
		case "review":
			review = row["window_id"].(string)
		}
	}
	if orch == "" || review == "" {
		t.Fatalf("list-agents did not name the panes that were created: %v", agents)
	}

	c.result(3, "set-agent-state", map[string]any{
		"session": "crew", "window": review, "state": "idle",
		"harness": "claude-code", "message": "ready",
	}, 5*time.Second)

	res = c.result(4, "list-agents", map[string]any{"session": "crew"}, 5*time.Second)
	agents, _ = res["agents"].([]any)
	if len(agents) != 1 {
		t.Fatalf("a pane that reported did not show up as an agent: %v", res)
	}
	if row := agents[0].(map[string]any); row["ready"] != true || row["harness_id"] != "claude-code" {
		t.Errorf("agent row is wrong: %v", row)
	}

	sent := c.result(5, "send-agent-message", map[string]any{
		"session": "crew", "to": review, "from": orch,
		"subject": "retest", "text": "rebased onto main",
	}, 5*time.Second)
	if sent["kind"] != "message" {
		t.Errorf("kind = %v, want message", sent["kind"])
	}

	res = c.result(6, "list-agents", map[string]any{"session": "crew"}, 5*time.Second)
	if row := res["agents"].([]any)[0].(map[string]any); row["unread"] != float64(1) {
		t.Errorf("unread = %v, want 1", row["unread"])
	}

	res = c.result(7, "read-agent-messages", map[string]any{
		"session": "crew", "to": review, "unread": true,
	}, 5*time.Second)
	if res["untrusted"] != true {
		t.Error("a read did not flag its content untrusted")
	}
	msgs, _ := res["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("read %d messages, want 1", len(msgs))
	}
	m := msgs[0].(map[string]any)
	if m["text"] != "rebased onto main" || m["was_unread"] != true {
		t.Errorf("message did not survive the round trip: %v", m)
	}
	res = c.result(8, "read-agent-messages", map[string]any{
		"session": "crew", "to": review, "unread": true,
	}, 5*time.Second)
	if n, _ := res["messages"].([]any); len(n) != 0 {
		t.Errorf("the message was still unread after being read: %v", res)
	}

	waiter := e.dial()
	done := make(chan map[string]any, 1)
	go func() {
		done <- waiter.result(9, "wait-for", map[string]any{
			"condition": "agent-message", "session": "crew",
			"window": orch, "timeout": 15000,
		}, 20*time.Second)
	}()
	time.Sleep(300 * time.Millisecond)
	c.result(10, "send-agent-message", map[string]any{
		"session": "crew", "to": orch, "from": review,
		"subject": "answer", "text": "two risks, both in decode",
	}, 5*time.Second)

	select {
	case got := <-done:
		if got["matched"] != true || got["subject"] != "answer" {
			t.Errorf("the wait did not match the message that was sent: %v", got)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("wait-for agent-message never returned")
	}

	hook := e.dial()
	go func() {
		time.Sleep(300 * time.Millisecond)
		hook.call(20, "set-agent-state", map[string]any{
			"session": "crew", "window": review, "state": "working", "harness": "claude-code",
		}, 5*time.Second)
		time.Sleep(300 * time.Millisecond)
		hook.call(21, "set-agent-state", map[string]any{
			"session": "crew", "window": review, "state": "idle", "harness": "claude-code",
		}, 5*time.Second)
	}()
	res = c.result(11, "ask-agent", map[string]any{
		"session": "crew", "window": review, "from": orch,
		"text": "echo e2e_ask_reply", "settle": 900, "timeout": 20000,
	}, 30*time.Second)
	if res["untrusted"] != true {
		t.Error("a reply did not flag itself untrusted")
	}
	if reply, _ := res["reply"].(string); !strings.Contains(reply, "e2e_ask_reply") {
		t.Errorf("reply did not carry what the pane printed: %q", reply)
	}

	resp := c.call(12, "send-agent-message", map[string]any{
		"session": "crew", "to": orch, "from": orch, "text": "self",
	}, 5*time.Second)
	errObj, ok := resp["error"].(map[string]any)
	if !ok || errObj["code"] != "loop_refused" {
		t.Errorf("a self-addressed message was not refused with loop_refused: %v", resp)
	}
}
