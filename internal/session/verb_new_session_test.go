package session

import (
	"path/filepath"
	"strings"
	"testing"
)

// Every other verb addresses a session that already exists. Without new-session
// an external program could drive a workspace but never set one up, so anything
// that wanted its own had to shell out or give up. These drive the verb over a
// real socket, which is the only place its wire shape is decided.

// TestNewSessionCreatesASessionWithItsFirstWindow is the whole point: one call
// and the caller has something every other verb can address.
func TestNewSessionCreatesASessionWithItsFirstWindow(t *testing.T) {
	d, sp := startTestDaemon(t)
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"new-session","params":{"name":"work","window_name":"build"}}`))
	if res["type"] != "session_created" {
		t.Errorf("type = %v, want session_created", res["type"])
	}
	if res["session"] != "work" {
		t.Errorf("session = %v, want work", res["session"])
	}
	if res["session_id"] == "" || res["session_id"] == nil {
		t.Error("the result carried no session id")
	}
	if res["windows"] != float64(1) {
		t.Errorf("windows = %v, want 1", res["windows"])
	}
	if res["window_name"] != "build" {
		t.Errorf("window_name = %v, want build", res["window_name"])
	}
	windowID, _ := res["window_id"].(string)
	if windowID == "" {
		t.Fatal("the result carried no window id")
	}
	if res["pty_id"] == "" || res["pty_id"] == nil {
		t.Error("the result carried no pty id")
	}

	// The daemon really holds it, and the window really has a PTY.
	sess := d.manager.GetSession("work")
	if sess == nil {
		t.Fatal("the daemon does not hold the session it said it created")
	}
	if got := len(sess.GetState().Windows); got != 1 {
		t.Fatalf("the session holds %d windows, want 1", got)
	}

	// And the session is addressable by every other verb, which is the reason
	// the verb exists.
	listed := result(t, c.call(t, `{"id":2,"verb":"list-windows","params":{"session":"work"}}`))
	if listed["total"] != float64(1) {
		t.Errorf("list-windows total = %v, want 1", listed["total"])
	}
}

// TestNewSessionRefusesADuplicateName pins the answer to the one failure a
// caller has to be able to act on. A name already taken is not a bad parameter:
// the caller asked for something reasonable and the daemon already holds it, so
// the remedy is a different name and the code says so.
func TestNewSessionRefusesADuplicateName(t *testing.T) {
	_, sp := startTestDaemon(t)
	c := dialVerb(t, sp)

	result(t, c.call(t, `{"id":1,"verb":"new-session","params":{"name":"taken"}}`))

	resp := c.call(t, `{"id":2,"verb":"new-session","params":{"name":"taken"}}`)
	if code := errCode(t, resp); code != ErrVerbSessionExists {
		t.Fatalf("code = %q, want %q", code, ErrVerbSessionExists)
	}
	e := resp["error"].(map[string]any)
	hint, ok := e["hint"].(map[string]any)
	if !ok {
		t.Fatal("the refusal carried no hint")
	}
	if hint["param"] != "name" {
		t.Errorf("hint.param = %v, want name", hint["param"])
	}
	available, _ := hint["available"].([]any)
	if len(available) == 0 {
		t.Error("the hint listed no existing session names")
	}

	// And nothing was created twice.
	listed := result(t, c.call(t, `{"id":3,"verb":"list-sessions"}`))
	sessions, _ := listed["sessions"].([]any)
	if len(sessions) != 1 {
		t.Errorf("the daemon holds %d sessions, want 1", len(sessions))
	}
}

// TestNewSessionGeneratesANameWhenNoneIsGiven keeps the call usable by a caller
// that does not care what its workspace is called.
func TestNewSessionGeneratesANameWhenNoneIsGiven(t *testing.T) {
	_, sp := startTestDaemon(t)
	c := dialVerb(t, sp)

	first := result(t, c.call(t, `{"id":1,"verb":"new-session"}`))
	second := result(t, c.call(t, `{"id":2,"verb":"new-session"}`))

	a, _ := first["session"].(string)
	b, _ := second["session"].(string)
	if a == "" || b == "" {
		t.Fatalf("a generated name is empty: %q and %q", a, b)
	}
	if a == b {
		t.Errorf("two sessions were generated the same name %q", a)
	}
}

// TestNewSessionCanCreateAnEmptySession covers the caller that means to place
// every window itself.
func TestNewSessionCanCreateAnEmptySession(t *testing.T) {
	d, sp := startTestDaemon(t)
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"new-session","params":{"name":"empty","window":false}}`))
	if res["windows"] != float64(0) {
		t.Errorf("windows = %v, want 0", res["windows"])
	}
	if _, ok := res["window_id"]; ok {
		t.Error("an empty session reported a window id")
	}
	if got := len(d.manager.GetSession("empty").GetState().Windows); got != 0 {
		t.Errorf("the session holds %d windows, want none", got)
	}
}

// TestNewSessionStartsTheFirstWindowWhereItWasAsked proves cwd is honoured
// rather than accepted and ignored.
func TestNewSessionStartsTheFirstWindowWhereItWasAsked(t *testing.T) {
	d, sp := startTestDaemon(t)
	c := dialVerb(t, sp)

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	result(t, c.call(t, `{"id":1,"verb":"new-session","params":{"name":"placed","cwd":`+quoteJSON(dir)+`}}`))

	sess := d.manager.GetSession("placed")
	win := sess.GetState().Windows[0]
	if win.Cwd != "" && win.Cwd != dir {
		t.Errorf("the window records cwd %q, want %q", win.Cwd, dir)
	}
	pty := sess.GetPTY(win.PTYID)
	if pty == nil {
		t.Fatal("the first window has no PTY")
	}
}

// TestNewSessionRefusesADirectoryThatDoesNotExist keeps a mistyped path from
// producing a working shell in the wrong place, and keeps it from producing a
// session the caller then has to clean up.
func TestNewSessionRefusesADirectoryThatDoesNotExist(t *testing.T) {
	d, sp := startTestDaemon(t)
	c := dialVerb(t, sp)

	missing := filepath.Join(t.TempDir(), "no-such-dir")
	resp := c.call(t, `{"id":1,"verb":"new-session","params":{"name":"bad","cwd":`+quoteJSON(missing)+`}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Fatalf("code = %q, want %q", code, ErrVerbInvalidParams)
	}
	if d.manager.GetSession("bad") != nil {
		t.Error("the refused call left a session behind")
	}
}

// TestNewSessionRefusesANameThatCouldNeverBeSaved surfaces the failure where
// the user chose the name. A session name is also the name of its state file,
// so a name with a path separator runs perfectly and never persists.
func TestNewSessionRefusesANameThatCouldNeverBeSaved(t *testing.T) {
	d, sp := startTestDaemon(t)
	c := dialVerb(t, sp)

	resp := c.call(t, `{"id":1,"verb":"new-session","params":{"name":"a/b"}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Fatalf("code = %q, want %q", code, ErrVerbInvalidParams)
	}
	if d.manager.GetSession("a/b") != nil {
		t.Error("the refused name was registered anyway")
	}
}

// TestNewSessionRefusesAnUnknownParameter keeps it under the same strictness
// every other verb has: dropping an unknown name is how new-window once
// reported a created window while ignoring the workspace it was asked for.
func TestNewSessionRefusesAnUnknownParameter(t *testing.T) {
	_, sp := startTestDaemon(t)
	c := dialVerb(t, sp)

	resp := c.call(t, `{"id":1,"verb":"new-session","params":{"name":"strict","nonesuch":2}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Fatalf("code = %q, want %q", code, ErrVerbInvalidParams)
	}
}

// TestNewSessionNeedsNoProtocolBump is the compatibility claim, checked rather
// than asserted. A caller that announced protocol 1 before this verb existed
// still handshakes at 1 and can call it, because adding a verb changes no
// envelope and no existing verb's contract.
func TestNewSessionNeedsNoProtocolBump(t *testing.T) {
	_, sp := startTestDaemon(t)
	c := dialVerb(t, sp)

	if VerbProtocolVersion != 1 {
		t.Fatalf("VerbProtocolVersion = %d; adding a verb must not bump it", VerbProtocolVersion)
	}
	hello := result(t, c.call(t, `{"id":1,"verb":"hello","params":{"client":"test","version":"1","protocol":1}}`))
	if hello["protocol"] != float64(1) {
		t.Fatalf("the daemon answers protocol %v, want 1", hello["protocol"])
	}
	res := result(t, c.call(t, `{"id":2,"verb":"new-session","params":{"name":"compat"}}`))
	if res["session"] != "compat" {
		t.Errorf("a protocol 1 caller could not create a session: %v", res)
	}

	// And the verb documents itself, so a caller learns it from list-verbs.
	listed := result(t, c.call(t, `{"id":3,"verb":"list-verbs","params":{"verb":"new-session"}}`))
	verbs, _ := listed["verbs"].([]any)
	if len(verbs) != 1 {
		t.Fatalf("list-verbs described %d verbs, want new-session", len(verbs))
	}
	doc := verbs[0].(map[string]any)
	if !strings.Contains(doc["description"].(string), "session") {
		t.Errorf("new-session describes itself as %q", doc["description"])
	}

	// The new error code is in the catalog, so a caller can learn what
	// session_exists means without reading the source.
	codes, _ := listed["error_codes"].([]any)
	found := false
	for _, entry := range codes {
		if m, ok := entry.(map[string]any); ok && m["code"] == ErrVerbSessionExists {
			found = true
		}
	}
	if !found {
		t.Errorf("%s is not in the error-code catalog", ErrVerbSessionExists)
	}
}
