package session

import (
	"strings"
	"testing"
)

// ringHas reports whether any entry in the log ring contains sub.
func ringHas(sub string) bool {
	for _, e := range GetLogEntries(0) {
		if strings.Contains(e.Message, sub) {
			return true
		}
	}
	return false
}

// ringDump renders the ring for a failure message.
func ringDump() string {
	var b strings.Builder
	for _, e := range GetLogEntries(0) {
		b.WriteString("  [" + e.Level + "] " + e.Message + "\n")
	}
	return b.String()
}

// TestVerbRefusalIsLogged is item 3. The caller already learns why its call
// failed. The daemon kept no memory of it, so a harness author debugging a
// wrapper could see their own traffic and not the daemon's reading of it.
func TestVerbRefusalIsLogged(t *testing.T) {
	restoreLevel(t, DebugOff)
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "refuse")
	c := dialVerb(t, sp)

	ClearLogBuffer()
	resp := c.call(t, `{"id":1,"verb":"new-window","params":{"session":"refuse","nonesuch":2}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Fatalf("code = %q, want %q", code, ErrVerbInvalidParams)
	}

	if !ringHas("Verb new-window refused for client ") {
		t.Fatalf("no refusal line for the verb name and client:\n%s", ringDump())
	}
	if !ringHas(ErrVerbInvalidParams) {
		t.Fatalf("refusal line carries no code:\n%s", ringDump())
	}
}

// TestUnknownVerbRefusalIsLogged covers the lookup failure, which is the one a
// caller written against a newer or older daemon actually hits.
func TestUnknownVerbRefusalIsLogged(t *testing.T) {
	restoreLevel(t, DebugOff)
	_, sp := startTestDaemon(t)
	c := dialVerb(t, sp)

	ClearLogBuffer()
	if code := errCode(t, c.call(t, `{"id":1,"verb":"teleport-window"}`)); code != ErrVerbUnknownVerb {
		t.Fatalf("code = %q, want %q", code, ErrVerbUnknownVerb)
	}
	if !ringHas("Verb teleport-window refused for client ") {
		t.Fatalf("no refusal line for an unknown verb:\n%s", ringDump())
	}
}

// TestVerbRefusalLineHoldsNoMessage keeps the level boundary. A refusal message
// quotes what the caller sent, which for a path or a title is content, and
// basic records identifiers and codes only.
func TestVerbRefusalLineHoldsNoMessage(t *testing.T) {
	restoreLevel(t, DebugOff)
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "quiet")
	c := dialVerb(t, sp)

	ClearLogBuffer()
	resp := c.call(t, `{"id":1,"verb":"stash-put","params":{"session":"quiet","path":"/home/ada/private/keys.txt"}}`)
	// The message the caller gets does name the path. That is the point: the
	// caller may see it and the daemon's own log may not.
	e, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("stash-put of a missing file was not refused: %v", resp)
	}
	if msg, _ := e["message"].(string); !strings.Contains(msg, "/home/ada/private") {
		t.Fatalf("the refusal message does not name the path, so this test proves nothing: %q", msg)
	}

	if ringHas("/home/ada/private") {
		t.Fatalf("the refusal line quoted a caller path:\n%s", ringDump())
	}
	if !ringHas("Verb stash-put refused for client ") {
		t.Fatalf("no refusal line at all:\n%s", ringDump())
	}
}

// TestSuccessIsNotLoggedAsARefusal keeps the line to one per error response. A
// working call must not produce one.
func TestSuccessIsNotLoggedAsARefusal(t *testing.T) {
	restoreLevel(t, DebugOff)
	_, sp := startTestDaemon(t)
	c := dialVerb(t, sp)

	ClearLogBuffer()
	result(t, c.call(t, `{"id":1,"verb":"list-verbs","params":{"verb":"list-verbs"}}`))

	if ringHas("refused for client") {
		t.Fatalf("a successful call logged a refusal:\n%s", ringDump())
	}
}

// TestSetLogLevelTakesEffectWithoutARestart is item 4. The level used to be read
// once at startup, so the only way to look at a fault in more detail was a
// restart, and a restart ends the run the fault was in.
func TestSetLogLevelTakesEffectWithoutARestart(t *testing.T) {
	restoreLevel(t, DebugOff)
	_, sp := startTestDaemon(t)
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"set-option","params":{"key":"daemon.log_level","value":"messages"}}`))
	if res["applied"] != true {
		t.Fatalf("set-option daemon.log_level reported applied=%v", res["applied"])
	}
	if res["scope"] != "daemon" {
		t.Fatalf("scope = %v, want daemon", res["scope"])
	}
	if GetDebugLevel() != DebugMessages {
		t.Fatalf("daemon level is %s after the call, want messages", GetDebugLevel())
	}

	// And back down again, which is the half that makes it usable.
	result(t, c.call(t, `{"id":2,"verb":"set-option","params":{"key":"daemon.log_level","value":"off"}}`))
	if GetDebugLevel() != DebugOff {
		t.Fatalf("daemon level is %s after lowering it, want off", GetDebugLevel())
	}
}

// TestSetLogLevelWorksWithNoSession checks the case the verb is most needed in:
// a daemon that serves no session can still be the thing that is wrong.
func TestSetLogLevelWorksWithNoSession(t *testing.T) {
	restoreLevel(t, DebugOff)
	_, sp := startTestDaemon(t)
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"set-option","params":{"key":"daemon.log_level","value":"basic"}}`))
	if res["value"] != "basic" {
		t.Fatalf("value = %v, want basic", res["value"])
	}
	if GetDebugLevel() != DebugBasic {
		t.Fatalf("daemon level is %s, want basic", GetDebugLevel())
	}
}

// TestSetLogLevelRejectsAnUnknownLevel keeps the registry's validation in front
// of the apply, so a typo cannot silently turn logging off.
func TestSetLogLevelRejectsAnUnknownLevel(t *testing.T) {
	restoreLevel(t, DebugBasic)
	_, sp := startTestDaemon(t)
	c := dialVerb(t, sp)

	if code := errCode(t, c.call(t, `{"id":1,"verb":"set-option","params":{"key":"daemon.log_level","value":"louder"}}`)); code != ErrVerbInvalidParams {
		t.Fatalf("code = %q, want %q", code, ErrVerbInvalidParams)
	}
	if GetDebugLevel() != DebugBasic {
		t.Fatalf("a refused value changed the level to %s", GetDebugLevel())
	}
}
