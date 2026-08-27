package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// selectKeybindRow puts the cursor on the first row matching want, and reports
// the row. It filters rather than scanning so the case exercises the path the
// user does: type into the filter, then act on what is under the cursor.
func selectKeybindRow(t *testing.T, m *OS, query string, match func(config.Binding) bool) config.Binding {
	t.Helper()
	m.KeybindSetQuery(query)
	rows := m.FilteredKeybindRows()
	for i, b := range rows {
		if match(b) {
			m.keybinds.selected = i
			return b
		}
	}
	t.Fatalf("no row matching %q among %d rows", query, len(rows))
	return config.Binding{}
}

// TestUnbindSelectedTakesOneKeyFromOneAction. The gesture on a list row is the
// narrow one: the action keeps its other keys and every other action keeps
// theirs.
//
// Negative control: point KeybindUnbindSelected at UnbindAction instead of
// UnbindKey and this fails on the surviving-key check.
func TestUnbindSelectedTakesOneKeyFromOneAction(t *testing.T) {
	m := keybindOS(t)
	before := slices.Clone(m.UserConfig.Keybindings.WindowManagement["close_window"])
	if len(before) < 2 {
		t.Skip("close_window no longer has two default keys")
	}

	row := selectKeybindRow(t, m, "close_window", func(b config.Binding) bool {
		return b.Action == "close_window" && b.Key == before[0]
	})
	m.KeybindUnbindSelected()

	got := m.UserConfig.Keybindings.WindowManagement["close_window"]
	if slices.Contains(got, row.Key) {
		t.Errorf("close_window still holds %q", row.Key)
	}
	if len(got) != len(before)-1 {
		t.Errorf("close_window = %v, want one key fewer than %v", got, before)
	}
}

// TestUnbindingTheLastKeyLeavesTheActionRebindable is the case that makes
// unbinding safe to try: the overlay binds a key by selecting an action's row,
// so an action that vanished when it lost its last key could not be given a new
// one from here.
//
// Negative control: this fails on the tree before Binding.Unbound existed. The
// row is gone from FilteredKeybindRows and there is nothing left to arm.
func TestUnbindingTheLastKeyLeavesTheActionRebindable(t *testing.T) {
	m := keybindOS(t)
	for _, key := range slices.Clone(m.UserConfig.Keybindings.WindowManagement["close_window"]) {
		row := selectKeybindRow(t, m, "close_window", func(b config.Binding) bool {
			return b.Action == "close_window" && b.Key == key
		})
		m.KeybindUnbindSelected()
		_ = row
	}

	row := selectKeybindRow(t, m, "close_window", func(b config.Binding) bool {
		return b.Action == "close_window"
	})
	if !row.Unbound {
		t.Fatalf("close_window row is not marked unbound: %+v", row)
	}

	// And it can be given a key again, from that row.
	m.KeybindArmFor(row.Section, row.Action)
	m.KeybindCapture("ctrl+alt+w")
	m.KeybindCommitBinding()
	if got := m.UserConfig.Keybindings.WindowManagement["close_window"]; !slices.Contains(got, "ctrl+alt+w") {
		t.Errorf("close_window = %v after rebinding from its unbound row", got)
	}
}

// TestFreeCapturedKeyHandsTheKeyToThePane is the recorder's short path: record
// the key your program wants, be told tuios takes it, take it back.
//
// Negative control: have KeybindFreeCapturedKey call UnbindKey on one action
// rather than FreeKey, and this fails, because the key is bound in two scopes
// and the pane still would not see it.
func TestFreeCapturedKeyHandsTheKeyToThePane(t *testing.T) {
	m := keybindOS(t)
	m.UserConfig.Keybindings.TerminalMode["terminal_next_window"] = []string{"ctrl+alt+j"}
	m.UserConfig.Keybindings.Global["command_palette"] =
		append(m.UserConfig.Keybindings.Global["command_palette"], "ctrl+alt+j")
	m.KeybindRegistry.Reload(m.UserConfig)

	m.KeybindArm()
	m.KeybindCapture("ctrl+alt+j")
	if _, fate := m.KeybindCaptured(); fate.Free {
		t.Fatal("the key must start out taken, or the case proves nothing")
	}

	m.KeybindFreeCapturedKey()
	if _, fate := m.KeybindCaptured(); !fate.Free {
		t.Errorf("ctrl+alt+j is still taken after being freed: %+v", fate)
	}
	if m.KeybindArmed() {
		t.Error("freeing a key must leave the recorder disarmed")
	}
}

// TestFreeingAKeyReportsWhatStillHoldsIt. The leader is not in any table, so
// freeing it removes nothing; saying "freed" would send the user back to their
// program to find the key still gone.
//
// Negative control: drop the keybindStillHeld branch in KeybindFreeKey and this
// fails, because the notification would claim a success that did not happen.
func TestFreeingAKeyReportsWhatStillHoldsIt(t *testing.T) {
	m := keybindOS(t)
	leader := m.UserConfig.Keybindings.LeaderKey
	m.KeybindFreeKey(leader)

	msg := lastNotificationText(t, m)
	if !strings.Contains(msg, "leader_key") {
		t.Errorf("freeing the leader said %q; it must name the setting that moves it", msg)
	}
}

// TestFreeingAnUnclaimedKeySaysSoWithoutWriting: a key nothing binds is already
// the pane's, and reporting a removal that did not happen is a lie the user
// cannot check.
//
// Negative control: have KeybindFreeKey report success unconditionally and this
// fails.
func TestFreeingAnUnclaimedKeySaysSoWithoutWriting(t *testing.T) {
	m := keybindOS(t)
	m.KeybindFreeKey("ctrl+alt+shift+f9")
	msg := lastNotificationText(t, m)
	if !strings.Contains(msg, "Nothing binds") {
		t.Errorf("freeing an unclaimed key said %q", msg)
	}
	if strings.Contains(msg, "Freed") {
		t.Errorf("freeing an unclaimed key claimed to have removed something: %q", msg)
	}
}

// TestUnbindDoesNothingOnAnAlreadyUnboundRow, and says why rather than
// reporting a removal.
//
// Negative control: drop the b.Unbound branch in KeybindUnbindSelected and this
// fails with "Could not unbind", which reads as a fault rather than as a state.
func TestUnbindDoesNothingOnAnAlreadyUnboundRow(t *testing.T) {
	m := keybindOS(t)
	m.UserConfig.Keybindings.UnbindAction(config.SectionWindowManagement, "close_window")
	m.KeybindRegistry.Reload(m.UserConfig)
	m.keybinds.report = m.buildKeybindReport()
	m.keybinds.filtered = nil

	selectKeybindRow(t, m, "close_window", func(b config.Binding) bool {
		return b.Action == "close_window"
	})
	m.KeybindUnbindSelected()
	if msg := lastNotificationText(t, m); !strings.Contains(msg, "already has no key") {
		t.Errorf("unbinding an unbound action said %q", msg)
	}
}

// TestUnbindRefreshesTheReport. The overlay must show the state after the edit,
// not before it, or the row the user just changed still reads as bound.
//
// Negative control: drop the keybindApply call from KeybindUnbindSelected and
// this fails, because the memoised filtered list and the report would both be
// the pre-edit ones.
func TestUnbindRefreshesTheReport(t *testing.T) {
	m := keybindOS(t)
	key := m.UserConfig.Keybindings.WindowManagement["toggle_zoom"][0]
	selectKeybindRow(t, m, "toggle_zoom", func(b config.Binding) bool {
		return b.Action == "toggle_zoom"
	})
	m.KeybindUnbindSelected()

	m.KeybindSetQuery("toggle_zoom")
	for _, b := range m.FilteredKeybindRows() {
		if b.Action == "toggle_zoom" && b.Key == key {
			t.Fatalf("the overlay still lists toggle_zoom on %q after the unbind", key)
		}
	}
}

// lastNotificationText is the newest notification's message, so a case can
// assert on what the user was told rather than only on what changed.
func lastNotificationText(t *testing.T, m *OS) string {
	t.Helper()
	if len(m.Notifications) == 0 {
		t.Fatal("nothing was reported to the user")
	}
	return m.Notifications[len(m.Notifications)-1].Message
}
