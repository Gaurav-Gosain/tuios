package app

import "testing"

// Renaming a session was reachable from the rail's r key and from the session
// switcher's ctrl+r, and from nowhere a pointer could find. The menu row that
// closes that gap is held to the rule the kill and accent rows already are: it
// renames the session the row names, and the editor says which one that is.

// TestARenameAddressesTheIdentityAndNotTheLabel: renaming twice must keep
// finding the same session, so the verb carries the name the session is
// addressed by while the label it sets is only ever a label.
func TestARenameAddressesTheIdentityAndNotTheLabel(t *testing.T) {
	verb, params, ok := renameVerb(RenameSession, "docs", "session-1", "Billing API")
	if !ok || verb != "set-session-name" {
		t.Fatalf("a session rename goes through %q (ok=%v)", verb, ok)
	}
	if params["session"] != "docs" {
		t.Errorf("the rename addresses %q, want the row's identity", params["session"])
	}
	if params["name"] != "Billing API" {
		t.Errorf("the rename sets %q", params["name"])
	}
}
