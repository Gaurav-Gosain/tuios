package input

import (
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A verb the model can do and the keyboard cannot reach is a verb nobody has.
// These are about the wiring, not about what the verb does.

func unbindInputOS(t *testing.T) *app.OS {
	t.Helper()
	cfg := config.DefaultConfig()
	o := &app.OS{UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)}
	o.OpenKeybindManager()
	return o
}

// ctrlKey is a control chord as the handler reads it: msg.String() returns
// "ctrl+d" for Code 'd' with ModCtrl set.
func ctrlKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl}
}

// TestCtrlDUnbindsTheSelectedRow.
//
// Negative control: remove the "ctrl+d" case from handleKeybindManagerInput and
// this fails, because the key falls through to the Bindings tab's filter
// grammar and does nothing at all.
func TestCtrlDUnbindsTheSelectedRow(t *testing.T) {
	o := unbindInputOS(t)
	// Filtered to one action so the row under the cursor is known.
	o.KeybindSetQuery("toggle_zoom")
	rows := o.FilteredKeybindRows()
	if len(rows) == 0 {
		t.Fatal("no toggle_zoom row to unbind")
	}
	key := rows[0].Key

	o, _ = HandleKeyPress(ctrlKey('d'), o)

	if got := o.UserConfig.Keybindings.WindowManagement["toggle_zoom"]; slices.Contains(got, key) {
		t.Errorf("toggle_zoom still holds %q after ctrl+d", key)
	}
}

// TestCtrlXFreesTheSelectedKeyEverywhere. The wide verb needs its own key, or
// the narrow one has to guess which the user meant.
//
// Negative control: remove the "ctrl+x" case and this fails: the prefix_mode
// claim survives.
func TestCtrlXFreesTheSelectedKeyEverywhere(t *testing.T) {
	o := unbindInputOS(t)
	o.UserConfig.Keybindings.WindowManagement["toggle_zoom"] = []string{"ctrl+alt+z"}
	o.UserConfig.Keybindings.PrefixMode["prefix_fullscreen"] = []string{"ctrl+alt+z"}
	o.KeybindRegistry.Reload(o.UserConfig)
	o.OpenKeybindManagerWith("ctrl+alt+z")
	if len(o.FilteredKeybindRows()) == 0 {
		t.Fatal("no row for ctrl+alt+z")
	}

	o, _ = HandleKeyPress(ctrlKey('x'), o)

	for _, section := range []map[string][]string{
		o.UserConfig.Keybindings.WindowManagement,
		o.UserConfig.Keybindings.PrefixMode,
	} {
		for action, keys := range section {
			if slices.Contains(keys, "ctrl+alt+z") {
				t.Errorf("%s still holds ctrl+alt+z after ctrl+x", action)
			}
		}
	}
}

// TestCtrlDOnTheRecordTabFreesTheCapturedKey. The recorder names a key and
// nothing else, so the only unbind it can mean is the wide one.
//
// Negative control: have the ctrl+d case always call KeybindUnbindSelected and
// this fails, since there is no selected binding on the Record tab and nothing
// happens.
func TestCtrlDOnTheRecordTabFreesTheCapturedKey(t *testing.T) {
	o := unbindInputOS(t)
	o.UserConfig.Keybindings.TerminalMode["terminal_next_window"] = []string{"ctrl+alt+j"}
	o.KeybindRegistry.Reload(o.UserConfig)
	o.OpenKeybindManager()

	o.KeybindArm()
	o, _ = HandleKeyPress(ctrlKey('j'), o) // arming makes this data, not a command
	if key, _ := o.KeybindCaptured(); key == "" {
		t.Fatal("the armed recorder did not capture the key")
	}

	o, _ = HandleKeyPress(ctrlKey('d'), o)
	if _, fate := o.KeybindCaptured(); !fate.Free {
		t.Errorf("ctrl+d on the Record tab left the key taken: %+v", fate)
	}
}

// TestAnArmedRecorderStillRecordsCtrlD. The new gestures must not become keys
// the recorder cannot record: while armed there are no commands at all, which
// is the property that makes arming one-shot safe.
//
// Negative control: put the ctrl+d case above the armed check and this fails.
func TestAnArmedRecorderStillRecordsCtrlD(t *testing.T) {
	o := unbindInputOS(t)
	o.KeybindArm()
	o, _ = HandleKeyPress(ctrlKey('d'), o)
	if key, _ := o.KeybindCaptured(); key != "ctrl+d" {
		t.Errorf("the armed recorder captured %q, want ctrl+d", key)
	}
}
