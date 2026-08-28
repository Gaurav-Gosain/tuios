package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The Conflicts tab reported a problem and offered nothing to press, which
// teaches the reader to distrust the panel. These are about the verb it offers
// now.

// conflictOS opens the overlay on the Conflicts tab with one real cross-section
// collision in it: two actions in one scope on one key, in different tables.
func conflictOS(t *testing.T) *OS {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Keybindings.WindowManagement["select_window_1"] = []string{"1", "ctrl+alt+1"}
	cfg.Keybindings.Layout["snap_corner_1"] = []string{"1"}

	m := &OS{Settings: config.Global, UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)}
	m.OpenKeybindManager()
	m.KeybindSetTab(KeybindTabConflicts)
	if len(m.KeybindReport().Collisions) == 0 {
		t.Fatal("the fixture produced no collision")
	}
	return m
}

// TestResolvingAConflictRemovesOnlyTheDeadBindings, which is what makes the
// gesture safe to press: the losing bindings never fire, so removing them
// cannot change what any key does. It only makes config.toml say what the
// program was already doing.
//
// Negative control: have KeybindResolveSelectedConflict call FreeKey on the
// contested key and this fails, because snap_corner_1 loses the key too and the
// digit stops doing anything.
func TestResolvingAConflictRemovesOnlyTheDeadBindings(t *testing.T) {
	m := conflictOS(t)
	before := m.KeybindRegistry.GetAction("1")
	if before != "snap_corner_1" {
		t.Fatalf("the fixture resolves 1 to %q, want snap_corner_1", before)
	}

	m.KeybindResolveSelectedConflict()

	if got := m.KeybindRegistry.GetAction("1"); got != before {
		t.Errorf("resolving the conflict changed what 1 does: %q, was %q", got, before)
	}
	if got := m.UserConfig.Keybindings.WindowManagement["select_window_1"]; slices.Contains(got, "1") {
		t.Errorf("the dead binding survived: select_window_1 = %v", got)
	}
	// The loser's other keys are untouched: only the contested one goes.
	if got := m.UserConfig.Keybindings.WindowManagement["select_window_1"]; !slices.Contains(got, "ctrl+alt+1") {
		t.Errorf("resolving took a key that was not contested: select_window_1 = %v", got)
	}
}

// TestResolvingAConflictClearsItFromTheReport, so the panel reflects the edit
// rather than the state before it.
//
// Negative control: drop the keybindApply call and this fails, because the
// memoised report is the pre-edit one.
func TestResolvingAConflictClearsItFromTheReport(t *testing.T) {
	m := conflictOS(t)
	m.KeybindResolveSelectedConflict()
	for _, c := range m.KeybindReport().Collisions {
		if c.Key == "1" {
			t.Errorf("the conflict on 1 is still reported: %+v", c)
		}
	}
}

// TestResolvingSaysNothingChanged. The reassurance is the point: a user pressing
// a key on a panel full of warnings needs to know it did not move their
// keyboard.
//
// Negative control: report only the removal and this fails.
func TestResolvingAConflictSaysWhatSurvived(t *testing.T) {
	m := conflictOS(t)
	m.KeybindResolveSelectedConflict()
	msg := lastNotificationText(t, m)
	if !strings.Contains(msg, "snap_corner_1 keeps it") {
		t.Errorf("the message does not name the action that keeps the key: %q", msg)
	}
	if !strings.Contains(msg, "already did") {
		t.Errorf("the message does not say that nothing changed: %q", msg)
	}
}

// TestFreeingFromAConflictRowTakesTheKeyFromEveryone, so ctrl+x means the same
// thing on this tab as on the Bindings tab.
//
// Negative control: drop the KeybindSelectedConflict branch from
// KeybindFreeSelectedKey and this fails: nothing happens at all, because there
// is no selected binding on this tab.
func TestFreeingFromAConflictRowTakesTheKeyFromEveryone(t *testing.T) {
	m := conflictOS(t)
	m.KeybindFreeSelectedKey()

	if got := m.KeybindRegistry.GetAction("1"); got != "" {
		t.Errorf("1 still runs %q after being freed", got)
	}
	if got := m.UserConfig.Keybindings.Layout["snap_corner_1"]; slices.Contains(got, "1") {
		t.Errorf("the winner kept the key: snap_corner_1 = %v", got)
	}
}

// TestResolveDoesNothingWithoutAConflictRow, so the gesture cannot fire from a
// tab that names no collision.
//
// Negative control: read the selection without the tab check in
// KeybindSelectedConflict and this fails, because the Bindings tab's row index
// would be read as an index into the collision list.
func TestResolveDoesNothingWithoutAConflictRow(t *testing.T) {
	m := conflictOS(t)
	m.KeybindSetTab(KeybindTabBindings)
	if _, ok := m.KeybindSelectedConflict(); ok {
		t.Fatal("the Bindings tab reported a selected conflict")
	}
	if cmd := m.KeybindResolveSelectedConflict(); cmd != nil {
		t.Error("resolving fired from a tab with no conflict row")
	}
}

// TestTheConflictsPanelNamesItsVerb. A panel that reports a problem and offers
// nothing to press teaches the reader to distrust it, which is what the
// maintainer met on a config he had never edited.
//
// Both surfaces are checked because the detail box is the first thing a narrow
// screen sheds (see keybindShedOrder), and the subtitle survives it.
//
// Negative control: drop "ctrl+d" from either string and the matching half
// fails.
func TestTheConflictsPanelNamesItsVerb(t *testing.T) {
	m := conflictOS(t)
	if got := m.keybindTabSubtitle(); !strings.Contains(got, "ctrl+d") {
		t.Errorf("the Conflicts subtitle offers no gesture: %q", got)
	}
	detail := m.keybindDetail(0)
	if !strings.Contains(detail, "ctrl+d") {
		t.Errorf("the Conflicts detail box offers no gesture: %q", detail)
	}
	// And it says the gesture is safe, which is the part that decides whether
	// anyone presses it.
	if !strings.Contains(detail, "changes no key") {
		t.Errorf("the detail box does not say the fix is behaviour-preserving: %q", detail)
	}
}
