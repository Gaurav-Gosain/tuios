package main

import (
	"testing"

	"github.com/Gaurav-Gosain/sip"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// defaultRegistry is the keybind registry a user who has changed nothing gets.
func defaultRegistry(t *testing.T) *config.KeybindRegistry {
	t.Helper()
	return config.NewKeybindRegistry(config.DefaultConfig())
}

// findKey returns the button with a given label.
func findKey(keys []sip.MobileKey, label string) (sip.MobileKey, bool) {
	for _, k := range keys {
		if k.Label == label {
			return k, true
		}
	}
	return sip.MobileKey{}, false
}

// A rebound command moves its button with it. Anyone who swapped the keys
// around has to get buttons that still do what they say.
func TestMobileBarFollowsRebinds(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Keybindings.PrefixMode["prefix_new_window"] = []string{"w"}
	cfg.Keybindings.PrefixMode["prefix_close_window"] = []string{"f4"}
	cfg.Keybindings.PrefixMode["prefix_command_palette"] = []string{"ctrl+p"}

	_, rows := mobileBar(config.NewKeybindRegistry(cfg), "ctrl+a")
	keys := rows[0].Keys

	newWindow, ok := findKey(keys, "new")
	if !ok {
		t.Fatal("no new button")
	}
	if newWindow.Key != "w" {
		t.Errorf("new sends %q, want the rebound w", newWindow.Key)
	}
	// A key sip cannot encode drops its button instead of shipping a dead one.
	if _, ok := findKey(keys, "close"); ok {
		t.Error("close kept a button for f4, which sip's client cannot send")
	}
	// A modified second half is still one button: the leader, then Ctrl+P.
	cmds, ok := findKey(keys, "cmds")
	if !ok {
		t.Fatal("no cmds button for the ctrl+p binding")
	}
	if cmds.Key != "p" || !cmds.Ctrl || cmds.Alt || !cmds.Prefixed {
		t.Errorf("cmds sends %+v, want a prefixed ctrl+p", cmds)
	}
}

// Without a leader every chord button would type its key into a pane, so the
// row goes rather than shipping eleven buttons that misfire.
func TestMobileBarWithoutUsableLeader(t *testing.T) {
	prefix, rows := mobileBar(defaultRegistry(t), "f1")
	if !prefix.IsZero() {
		t.Errorf("unencodable leader still produced prefix %+v", prefix)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want only the typing row", len(rows))
	}
	for _, k := range rows[0].Keys {
		if k.Prefix || k.Prefixed {
			t.Errorf("typing row carries chord button %+v with no leader to send", k)
		}
	}
}
