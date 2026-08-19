package app

import "testing"

// Renaming a session was reachable from the rail's r key and from the session
// switcher's ctrl+r, and from nowhere a pointer could find. The menu row that
// closes that gap is held to the rule the kill and accent rows already are: it
// renames the session the row names, and the editor says which one that is.

// TestEverySessionRowOffersToRenameTheSessionItNames: unlike detach and the
// workspace switcher, a rename means something on any row, so it is offered on
// every one of them rather than dimmed off the ones this client is not in.
func TestEverySessionRowOffersToRenameTheSessionItNames(t *testing.T) {
	m := killSessionOS(t)

	for _, id := range []string{"docs", "session-1"} {
		_, items := m.sessionMenu(id)
		if !menuActions(items)["rename_session"] {
			t.Errorf("the menu for %q offers no runnable rename", id)
		}
	}
}

// TestTheRenameRowCarriesItsOwnSession walks the whole carry the way a click
// does: the menu opened on a row holds that row's session, taking the action
// hands the same session to the dispatcher, and nothing along the way
// substitutes the one this client happens to be attached to.
func TestTheRenameRowCarriesItsOwnSession(t *testing.T) {
	m := killSessionOS(t)
	m.IsDaemonSession = true

	m.openSidebarContextMenu(sidebarRowHit{Kind: sidebarRowSession, SessionID: "docs", WindowIndex: -1}, 4, 4)
	if m.ContextMenu == nil {
		t.Fatal("no menu opened on the session row")
	}
	m.ContextMenu.Selected = -1
	for i, it := range m.ContextMenu.Items {
		if it.Action == "rename_session" {
			m.ContextMenu.Selected = i
			break
		}
	}
	if m.ContextMenu.Selected < 0 {
		t.Fatal("the session menu carries no rename row")
	}
	if got := m.ContextMenuSelectedAction(); got != "rename_session" {
		t.Fatalf("selecting the rename row took %q", got)
	}
	if got := m.TakeMenuSession(); got != "docs" {
		t.Errorf("the rename was dispatched against %q, want docs", got)
	}
}

// TestTheRenameEditorSaysWhichSessionItIsAbout: the editor is reachable from
// any session's row, so a title reading only "rename session" left the user to
// work out whether it had landed on the row they pointed at. The buffer is the
// row's display label, not the attached session's.
func TestTheRenameEditorSaysWhichSessionItIsAbout(t *testing.T) {
	m := killSessionOS(t)

	m.BeginRenameSession("docs")
	if m.RenameTargetID != "docs" {
		t.Errorf("the editor targets %q, want the row's session", m.RenameTargetID)
	}
	if m.RenameBuffer != "Payments API" {
		t.Errorf("the editor is seeded with %q, want the row's own label", m.RenameBuffer)
	}
	if got, want := m.RenameDialogTitle(), "rename session Payments API"; got != want {
		t.Errorf("the editor is headed %q, want %q", got, want)
	}
}

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
