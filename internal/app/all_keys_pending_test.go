package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestAllKeysPending pins when a key after the leader may be missing its
// base-layout key: only once the host has shown it grants report-all keys,
// and only while its last answer says that mode is not yet in effect.
func TestAllKeysPending(t *testing.T) {
	o := &OS{}
	if o.AllKeysPending() {
		t.Fatal("pending before the host ever answered")
	}

	// A host that answers without report-all keys never grants it, so there
	// is nothing to wait for.
	o.NoteKeyboardEnhancements(tea.KeyboardEnhancementsMsg{Flags: 5})
	if o.hostGrantedAllKeys || o.AllKeysPending() {
		t.Fatal("a host that never granted report-all keys counts as pending")
	}

	o.NoteKeyboardEnhancements(tea.KeyboardEnhancementsMsg{Flags: 29})
	if !o.hostGrantedAllKeys {
		t.Fatal("a grant of report-all keys was not recorded")
	}
	if o.AllKeysPending() {
		t.Fatal("pending while report-all keys is in effect")
	}

	// Back to the pane's flags: the next switch is waited for.
	o.NoteKeyboardEnhancements(tea.KeyboardEnhancementsMsg{Flags: 5})
	if !o.hostGrantedAllKeys || !o.AllKeysPending() {
		t.Fatal("not pending after the host dropped report-all keys")
	}

	o.NoteKeyboardEnhancements(tea.KeyboardEnhancementsMsg{Flags: 29})
	if o.AllKeysPending() {
		t.Fatal("still pending after the host switched back")
	}
}

// TestPaneKeysDown pins the press record key releases are matched against.
func TestPaneKeysDown(t *testing.T) {
	o := &OS{}
	if _, ok := o.TakePaneKeyDown('a'); ok {
		t.Fatal("a key no pane saw pressed counts as down")
	}
	o.NotePaneKeyDown('a', "w1")
	if id, ok := o.TakePaneKeyDown('a'); !ok || id != "w1" {
		t.Fatalf("take = %q, %v, want w1, true", id, ok)
	}
	if _, ok := o.TakePaneKeyDown('a'); ok {
		t.Fatal("a release was matched twice")
	}
}
