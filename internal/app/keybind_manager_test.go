package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// keybindOS builds a model with a registry and the overlay open, which is the
// only state these cases need.
func keybindOS(t *testing.T) *OS {
	t.Helper()
	cfg := config.DefaultConfig()
	m := &OS{UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)}
	m.OpenKeybindManager()
	return m
}

func TestOpenBuildsTheReportOnce(t *testing.T) {
	m := keybindOS(t)
	if len(m.KeybindReport().Bindings) == 0 {
		t.Fatal("opening the overlay must build the report")
	}
	// Closing drops it, so a reopen reads the pane again rather than showing
	// what was true last time.
	m.CloseKeybindManager()
	if m.ShowKeybindManager {
		t.Error("close must clear the flag")
	}
	if len(m.KeybindReport().Bindings) != 0 {
		t.Error("close must drop the report")
	}
}

// The recorder's whole contract: armed, a key is data; disarmed, it is a
// command. If arming survived a capture there would be no key left that means
// "stop", because every one of them is a key the recorder must be able to
// record.
func TestRecorderArmsOneKeyAtATime(t *testing.T) {
	m := keybindOS(t)
	m.KeybindArm()
	if !m.KeybindArmed() {
		t.Fatal("KeybindArm must arm")
	}
	m.KeybindCapture("esc")
	if m.KeybindArmed() {
		t.Fatal("capturing must disarm, or there is no way to stop recording")
	}
	key, fate := m.KeybindCaptured()
	if key != "esc" {
		t.Errorf("captured %q, want esc", key)
	}
	if len(fate.Acts) == 0 {
		t.Error("esc is bound in several scopes and the recorder must say so")
	}
}

// Leaving the Record tab disarms. An armed recorder that survived a tab switch
// would swallow the next keystroke on a surface that is not showing it.
func TestLeavingTheRecordTabDisarms(t *testing.T) {
	m := keybindOS(t)
	m.KeybindArm()
	m.KeybindSetTab(KeybindTabConflicts)
	if m.KeybindArmed() {
		t.Fatal("switching away from Record must disarm")
	}
}

func TestArmingFromABindingCarriesTheTarget(t *testing.T) {
	m := keybindOS(t)
	rows := m.FilteredKeybindRows()
	if len(rows) == 0 {
		t.Fatal("no bindings to arm from")
	}
	m.KeybindArmFor(rows[0].Section, rows[0].Action)
	section, action := m.KeybindBindTarget()
	if section != rows[0].Section || action != rows[0].Action {
		t.Errorf("target = %q/%q, want %q/%q", section, action, rows[0].Section, rows[0].Action)
	}
	// Arming from the Record tab is inspect-only.
	m.KeybindArm()
	if section, action := m.KeybindBindTarget(); section != "" || action != "" {
		t.Errorf("a bare arm must carry no target, got %q/%q", section, action)
	}
}

// Binding writes to the config, reloads the registry, and rebuilds the report,
// so the overlay shows the consequence of the binding rather than the state
// before it.
func TestBindingTakesEffectWithoutARestart(t *testing.T) {
	m := keybindOS(t)
	m.KeybindArmFor(config.SectionTerminalMode, "terminal_next_window")
	m.KeybindCapture("ctrl+alt+f9")
	m.KeybindCommitBinding()

	keys := m.UserConfig.Keybindings.TerminalMode["terminal_next_window"]
	var found bool
	for _, k := range keys {
		if k == "ctrl+alt+f9" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the key was not written to the config: %v", keys)
	}
	if len(keys) < 2 {
		t.Error("binding must append an alternative, not replace what was there")
	}
	if got := m.KeybindRegistry.GetTerminalModeAction("ctrl+alt+f9"); got != "terminal_next_window" {
		t.Errorf("the registry resolves the new key to %q; it was not reloaded", got)
	}
	// And the freshly bound key is now in the report's swallow set, because
	// terminal_mode keys never reach the pane.
	var swallowed bool
	for _, s := range m.KeybindReport().Swallowed {
		if s.Key == "ctrl+alt+f9" {
			swallowed = true
		}
	}
	if !swallowed {
		t.Error("the report must be rebuilt so the new binding's consequence is visible")
	}
}

// Binding the same key twice is a no-op rather than a duplicate entry.
func TestBindingAKeyTwiceDoesNothing(t *testing.T) {
	m := keybindOS(t)
	m.KeybindArmFor(config.SectionTerminalMode, "terminal_next_window")
	m.KeybindCapture("ctrl+alt+f10")
	m.KeybindCommitBinding()
	before := len(m.UserConfig.Keybindings.TerminalMode["terminal_next_window"])

	m.KeybindArmFor(config.SectionTerminalMode, "terminal_next_window")
	m.KeybindCapture("ctrl+alt+f10")
	m.KeybindCommitBinding()
	if after := len(m.UserConfig.Keybindings.TerminalMode["terminal_next_window"]); after != before {
		t.Errorf("rebinding the same key grew the list from %d to %d", before, after)
	}
}

// Committing with no target must not write anything: arming from the Record tab
// is for finding out what a key does, and most visits end there.
func TestCommitWithNoTargetWritesNothing(t *testing.T) {
	m := keybindOS(t)
	m.KeybindArm()
	m.KeybindCapture("ctrl+alt+f11")
	if cmd := m.KeybindCommitBinding(); cmd != nil {
		t.Error("an inspect-only capture must not persist anything")
	}
	for _, keys := range m.UserConfig.Keybindings.TerminalMode {
		for _, k := range keys {
			if k == "ctrl+alt+f11" {
				t.Fatal("a capture with no bind target wrote to the config")
			}
		}
	}
}

// The filter is memoised against the query, and the memo has to notice when the
// query changes or the list goes stale under it.
func TestFilterMemoTracksTheQuery(t *testing.T) {
	m := keybindOS(t)
	all := len(m.FilteredKeybindRows())
	if all == 0 {
		t.Fatal("no bindings")
	}

	m.KeybindSetQuery("close")
	narrowed := m.FilteredKeybindRows()
	if len(narrowed) == 0 || len(narrowed) >= all {
		t.Fatalf("filtering by 'close' matched %d of %d rows", len(narrowed), all)
	}
	// The same query twice must give the same answer, which is what the memo is
	// for; a different one must not.
	if again := m.FilteredKeybindRows(); len(again) != len(narrowed) {
		t.Errorf("memo returned %d rows then %d for the same query", len(narrowed), len(again))
	}
	m.KeybindSetQuery("")
	if restored := len(m.FilteredKeybindRows()); restored != all {
		t.Errorf("clearing the query gave %d rows, want %d; the memo went stale", restored, all)
	}
}

// The query searches the chord as well as the action, so a user who knows what
// they press can find it without knowing what it is called.
func TestFilterMatchesTheChordAndTheAction(t *testing.T) {
	m := keybindOS(t)
	m.KeybindSetQuery("ctrl+b")
	for _, b := range m.FilteredKeybindRows() {
		if strings.Contains(b.Press, "ctrl+b") {
			return
		}
	}
	t.Error("filtering by a chord must find bindings that use it")
}

func TestTabsWrapInBothDirections(t *testing.T) {
	m := keybindOS(t)
	m.KeybindSetTab(KeybindTabBindings)
	m.KeybindStepTab(-1)
	if m.KeybindTab != KeybindTabRecord {
		t.Errorf("stepping back from the first tab must wrap to the last, got %d", m.KeybindTab)
	}
	m.KeybindStepTab(1)
	if m.KeybindTab != KeybindTabBindings {
		t.Errorf("stepping forward from the last tab must wrap to the first, got %d", m.KeybindTab)
	}
}

// Selection is clamped to the active tab's own list, since the tabs list
// different things and a row index does not carry across them.
func TestSelectionResetsAcrossTabs(t *testing.T) {
	m := keybindOS(t)
	m.KeybindMove(20)
	if m.KeybindSelected() == 0 {
		t.Fatal("moving must move the selection")
	}
	m.KeybindSetTab(KeybindTabConflicts)
	if m.KeybindSelected() != 0 {
		t.Errorf("selection = %d after a tab switch, want 0", m.KeybindSelected())
	}
}

func TestMoveClampsToTheList(t *testing.T) {
	m := keybindOS(t)
	m.KeybindMove(-5)
	if got := m.KeybindSelected(); got != 0 {
		t.Errorf("selection = %d, want 0: it must not go negative", got)
	}
	m.KeybindMove(10000)
	if got, n := m.KeybindSelected(), len(m.FilteredKeybindRows()); got != n-1 {
		t.Errorf("selection = %d, want %d: it must not run past the end", got, n-1)
	}
}

// A model with no registry must not panic; the overlay is reachable from the
// palette before a config has necessarily loaded.
func TestOverlaySurvivesAMissingRegistry(t *testing.T) {
	m := &OS{}
	m.OpenKeybindManager()
	m.KeybindArm()
	m.KeybindCapture("ctrl+g")
	m.KeybindMove(1)
	m.KeybindSetTab(KeybindTabGuests)
	if rows := m.FilteredKeybindRows(); len(rows) != 0 {
		t.Errorf("a model with no registry has no bindings, got %d", len(rows))
	}
}
