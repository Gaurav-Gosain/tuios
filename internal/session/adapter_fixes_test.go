package session

import (
	"strings"
	"testing"
)

// Fixes a native Collie adapter over the verb socket needs.

// TestSendKeysComma: Comma sends the "," that send-keys splits keys on.
func TestSendKeysComma(t *testing.T) {
	keys, err := parseSendKeys("a,Comma b comma", 0)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, k := range keys {
		b.Write(k.bytes(false))
	}
	if b.String() != "a,b," {
		t.Errorf("bytes %q, want a,b,", b.String())
	}
	if c, _ := sendKeysCanonical(keys); c != "a Comma b Comma" {
		t.Errorf("canonical %q", c)
	}
	k, err := parseKeyToken("alt+Comma")
	if err != nil || string(k.bytes(false)) != "\x1b," {
		t.Errorf("alt+Comma = %q, %v", k.bytes(false), err)
	}
}

// TestSessionIDSurvivesRestore saves a session, drops it, and restores it
// from its state file: it comes back with the same id. State from before ids
// were saved, and an id another session holds, get a new one.
func TestSessionIDSurvivesRestore(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "keep")
	id := sess.ID
	if err := sess.persist(sess.ResurrectionState()); err != nil {
		t.Fatal(err)
	}
	saved, err := LoadResurrectionState("keep")
	if err != nil {
		t.Fatal(err)
	}
	if saved.SessionID != id {
		t.Fatalf("the state file holds session_id %q, want %q", saved.SessionID, id)
	}
	if err := d.manager.DeleteSession("keep"); err != nil {
		t.Fatal(err)
	}
	back, err := d.restoreSession(saved)
	if err != nil {
		t.Fatal(err)
	}
	if back.ID != id {
		t.Errorf("restored id %q, want %q", back.ID, id)
	}

	// Old state has no id.
	old := *saved
	old.Name, old.SessionID = "old", ""
	fresh, err := d.restoreSession(&old)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.ID == "" || fresh.ID == id {
		t.Errorf("state with no id restored as %q", fresh.ID)
	}
	// An id a live session holds is not taken twice.
	dup := *saved
	dup.Name = "dup"
	other, err := d.restoreSession(&dup)
	if err != nil {
		t.Fatal(err)
	}
	if other.ID == id {
		t.Error("two live sessions share one id")
	}
}
