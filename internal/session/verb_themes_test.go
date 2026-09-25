package session

import (
	"testing"
)

// The whole point of the check: a name that does not resolve fails, and says
// the name that would have.
func TestSetOptionRejectsAThemeThatDoesNotExist(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "rice")
	c := dialVerb(t, sp)

	resp := c.call(t, `{"verb":"set-option","params":{"session":"rice","key":"appearance.theme","value":"catppuccin-mocha"}}`)
	e := errorOf(t, resp)
	if e["code"] != ErrVerbInvalidParams {
		t.Fatalf("code is %v, want %s", e["code"], ErrVerbInvalidParams)
	}
	hint, _ := e["hint"].(map[string]any)
	if hint["did_you_mean"] != "catppuccin_mocha" {
		t.Errorf("did_you_mean is %v, want catppuccin_mocha", hint["did_you_mean"])
	}
}

// Empty is not a name that has to resolve: it is how a session says it wants
// the terminal's own colours, and clearing a theme has to stay reachable.
func TestSetOptionAcceptsAnEmptyTheme(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "rice")
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"verb":"set-option","params":{"session":"rice","key":"appearance.theme","value":""}}`))
	if res["key"] != "appearance.theme" {
		t.Fatalf("set the wrong key: %v", res["key"])
	}
}
