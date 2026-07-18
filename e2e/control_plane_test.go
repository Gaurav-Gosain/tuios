//go:build e2e

// Package e2e drives a real tuios daemon end to end over its unix socket,
// exercising the JSON verb control plane and session resurrection the way an
// external scripting client would: raw socket writes, no in-process shortcuts.
//
// These tests are behind the "e2e" build tag because they spawn real daemons
// and real shells. Run them with:
//
//	go build -o /tmp/tuios ./cmd/tuios
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
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// harness
// ---------------------------------------------------------------------------

// env is one isolated tuios installation: a binary plus a private set of XDG
// directories. Everything a test does routes through it.
type env struct {
	t      *testing.T
	bin    string
	dirs   map[string]string
	socket string
}

// newEnv locates or builds the tuios binary and prepares isolated XDG dirs.
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
		t:    t,
		bin:  bin,
		dirs: dirs,
		// The daemon socket path is derived from XDG_RUNTIME_DIR.
		socket: filepath.Join(dirs["XDG_RUNTIME_DIR"], "tuios", "tuios.sock"),
	}
	t.Cleanup(e.killServer)
	return e
}

// buildTuios compiles the binary under test once per test that needs it.
func buildTuios(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "tuios")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/tuios")
	cmd.Dir = ".."
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building tuios: %v\n%s", err, b)
	}
	return out
}

// environ returns the isolated environment for a tuios subprocess.
func (e *env) environ() []string {
	out := append([]string{}, os.Environ()...)
	// Drop any inherited XDG vars so ours are authoritative.
	filtered := out[:0]
	for _, kv := range out {
		if _, isOverridden := e.dirs[strings.SplitN(kv, "=", 2)[0]]; !isOverridden {
			filtered = append(filtered, kv)
		}
	}
	out = filtered
	for k, v := range e.dirs {
		out = append(out, k+"="+v)
	}
	// A deterministic POSIX shell keeps pane content predictable.
	return append(out, "SHELL=/bin/sh", "TERM=xterm-256color")
}

// run executes a tuios subcommand and returns its combined output.
func (e *env) run(args ...string) (string, error) {
	e.t.Helper()
	cmd := exec.Command(e.bin, args...)
	cmd.Env = e.environ()
	b, err := cmd.CombinedOutput()
	return string(b), err
}

// mustRun executes a tuios subcommand and fails the test if it errors.
func (e *env) mustRun(args ...string) string {
	e.t.Helper()
	out, err := e.run(args...)
	if err != nil {
		e.t.Fatalf("tuios %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// killServer stops the isolated daemon and waits for it to actually exit.
//
// kill-server only sends SIGTERM and returns immediately, while the daemon then
// writes its final resurrection state into XDG_STATE_HOME. Returning before it
// finishes would race t.TempDir cleanup, which deletes that directory out from
// under the still-running daemon. Errors are ignored: this runs as cleanup and
// the server may already be gone.
func (e *env) killServer() {
	_, _ = e.run("kill-server")
	e.awaitDaemonGone(10 * time.Second)
}

// awaitDaemonGone blocks until the daemon has fully finished shutting down.
//
// It waits for the socket FILE to be removed, not merely for connections to be
// refused. The daemon closes its listener before saving resurrection state and
// only unlinks the socket afterwards (see Daemon.shutdown), so an unconnectable
// socket does not yet mean state has been persisted. Best effort: it returns on
// timeout rather than failing, since callers use it during cleanup.
func (e *env) awaitDaemonGone(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(e.socket); os.IsNotExist(err) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitForStateFile blocks until a session's resurrection state lands on disk.
// State is persisted by a periodic saver and by the final save during shutdown,
// so it appears asynchronously after kill-server returns.
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

// waitForSocket blocks until the daemon socket accepts a connection.
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

// ---------------------------------------------------------------------------
// raw JSON verb client
// ---------------------------------------------------------------------------

// verbConn speaks the line delimited JSON verb protocol over a raw socket,
// deliberately without using the in-repo client, so these tests exercise the
// real wire format an external tool would have to speak.
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

// call sends one request and returns the decoded response envelope. timeout
// bounds the read, so a wait-for that never resolves fails the test instead of
// hanging the suite.
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

// result asserts the response carried a result (not an error) and returns it.
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

// ---------------------------------------------------------------------------
// tests
// ---------------------------------------------------------------------------

// TestHeadlessControlPlane is the core dogfood: create a detached session with
// no client ever attached, then drive it entirely over the JSON verb protocol.
// It proves the daemon owns window state and pane content on its own, which is
// the whole point of the control plane.
func TestHeadlessControlPlane(t *testing.T) {
	e := newEnv(t)

	// 1. Create a headless session. --detach means no TUI is ever attached, so
	//    everything below is served from daemon owned state.
	e.mustRun("new", "--detach", "ctl")
	e.waitForSocket(10 * time.Second)

	c := e.dial()

	// 2. list-verbs doubles as a protocol handshake and version check.
	verbs := c.result(1, "list-verbs", nil, 5*time.Second)
	if _, ok := verbs["version"]; !ok {
		t.Fatalf("list-verbs did not report a protocol version: %v", verbs)
	}

	// 3. The detached session must already have its initial window, otherwise
	//    control verbs would have no target and the session would be unusable
	//    until someone attached.
	wl := c.result(2, "list-windows", map[string]any{"session": "ctl"}, 5*time.Second)
	initial := int(wl["total"].(float64))
	if initial < 1 {
		t.Fatalf("detached session has %d windows, want at least 1", initial)
	}

	// 4. Create a second window headlessly and confirm the count moved.
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

	// 5. Run a real command in the new window. The marker is computed by the
	//    shell ($((21*2)) -> 42), so seeing "marker-42" proves the shell
	//    actually executed it rather than our bytes merely being echoed.
	c.result(5, "send-text", map[string]any{
		"session": "ctl",
		"window":  winID,
		"text":    "echo marker-$((21*2))\n",
	}, 5*time.Second)

	// 6. wait-for is the scripting primitive that replaces a capture-pane poll
	//    loop. Blocking here proves the event stream actually fires on output.
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

	// 7. capture-pane must independently show the same result, rendered from
	//    the daemon side terminal emulator.
	pane := c.result(7, "capture-pane", map[string]any{
		"session": "ctl",
		"window":  winID,
		"source":  "recent",
	}, 10*time.Second)
	content, _ := pane["content"].(string)
	if !strings.Contains(content, "marker-42") {
		t.Fatalf("capture-pane missing command output.\n--- pane ---\n%s", content)
	}

	// 8. Options round trip through the daemon owned store.
	c.result(8, "set-option", map[string]any{"session": "ctl", "key": "mouse", "value": "on"}, 5*time.Second)
	opt := c.result(9, "get-option", map[string]any{"session": "ctl", "key": "mouse"}, 5*time.Second)
	if got, _ := opt["value"].(string); got != "on" {
		t.Fatalf("get-option = %q, want \"on\"", got)
	}

	// 9. close-window brings the count back down.
	c.result(10, "close-window", map[string]any{"session": "ctl", "window": winID}, 10*time.Second)
	wl = c.result(11, "list-windows", map[string]any{"session": "ctl"}, 5*time.Second)
	if got := int(wl["total"].(float64)); got != initial {
		t.Fatalf("after close-window: %d windows, want %d", got, initial)
	}

	// 10. kill-session tears it down and it disappears from the listing.
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

	// The connection must still serve a good request afterwards.
	if _, ok := c.result(2, "list-sessions", nil, 5*time.Second)["sessions"]; !ok {
		t.Fatal("connection unusable after an unknown verb")
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

	// Add a distinctively named window so we can prove identity survives, not
	// merely that some window count matched.
	c := e.dial()
	c.result(1, "new-window", map[string]any{"session": "revive", "name": "phoenix"}, 10*time.Second)
	before := c.result(2, "list-windows", map[string]any{"session": "revive"}, 5*time.Second)
	wantTotal := int(before["total"].(float64))

	// Resurrection state is written by a periodic saver and on shutdown; the
	// clean kill-server path performs the final save. kill-server only sends
	// SIGTERM and returns before that save completes, so both the state file
	// and the socket teardown have to be waited for.
	e.mustRun("kill-server")

	// The state file must actually exist, otherwise the restore below would be
	// vacuously true.
	stateDir := filepath.Join(e.dirs["XDG_STATE_HOME"], "tuios", "sessions")
	e.awaitDaemonGone(15 * time.Second)
	e.waitForStateFile("revive", 15*time.Second)

	// Start a fresh daemon process. It restores sessions before accepting
	// clients, so the very first verb call must already see them.
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

	// The restored windows must have *live* shells, not just replayed metadata.
	// Running a command through a restored PTY is the only convincing proof.
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

	// An explicit kill must make a session non resurrectable, otherwise killed
	// sessions would come back from the dead on the next daemon start.
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

	// Following the hint must actually work.
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

	// A client claiming a protocol this daemon cannot serve is refused with the
	// typed code rather than being allowed to proceed.
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
		// Every example must be a parseable request for this verb.
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

	// The verbs a scripting client depends on must all be present.
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

	// No daemon has been started in this hermetic environment.
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
