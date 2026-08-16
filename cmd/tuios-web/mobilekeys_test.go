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

// The leader is the one chord every other button hangs off, so a rebound
// leader has to reach the phone as the key the user rebound it to.
func TestLeaderPrefix(t *testing.T) {
	tests := []struct {
		leader           string
		ok               bool
		key, code        string
		ctrl, alt, shift bool
	}{
		{leader: "ctrl+b", ok: true, key: "b", code: "KeyB", ctrl: true},
		{leader: "ctrl+a", ok: true, key: "a", code: "KeyA", ctrl: true},
		{leader: "alt+shift+w", ok: true, key: "w", code: "KeyW", alt: true, shift: true},
		{leader: "ctrl+space", ok: true, key: " ", code: "Space", ctrl: true},
		{leader: "esc", ok: true, key: "Escape", code: "Escape"},
		// sip's client has no encoding for a function key, and a chord that
		// sends nothing is worse than one that is absent.
		{leader: "f1"},
		{leader: "ctrl+f5"},
		{leader: "hyper+q"},
		{leader: ""},
	}

	for _, tt := range tests {
		got, ok := leaderPrefix(tt.leader)
		if ok != tt.ok {
			t.Errorf("leaderPrefix(%q) ok = %v, want %v", tt.leader, ok, tt.ok)
			continue
		}
		if !tt.ok {
			if !got.IsZero() {
				t.Errorf("leaderPrefix(%q) refused but still returned %+v", tt.leader, got)
			}
			continue
		}
		want := sip.MobilePrefix{Key: tt.key, Code: tt.code, Ctrl: tt.ctrl, Alt: tt.alt, Shift: tt.shift}
		if got != want {
			t.Errorf("leaderPrefix(%q) = %+v, want %+v", tt.leader, got, want)
		}
	}
}

// The bar is two rows: the chords, folded away by whoever only types, over the
// typing keys a phone keyboard does not have.
func TestMobileBarRows(t *testing.T) {
	prefix, rows := mobileBar(defaultRegistry(t), "ctrl+b")
	if prefix.IsZero() {
		t.Fatal("ctrl+b produced no prefix")
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if !rows[0].Collapsible {
		t.Error("the chord row does not fold away")
	}
	if rows[1].Collapsible {
		t.Error("the typing row folds away; it is why the bar exists")
	}
	if len(rows[1].Keys) != len(sip.DefaultMobileKeys()) {
		t.Errorf("typing row has %d keys, want sip's %d", len(rows[1].Keys), len(sip.DefaultMobileKeys()))
	}
	if rows[0].Keys[0].Label != "pfx" || !rows[0].Keys[0].Prefix {
		t.Errorf("chord row leads with %+v, want the prefix latch", rows[0].Keys[0])
	}
}

// The strip is twice as wide as a 390px phone, so it pans and the order decides
// what a thumb sees without panning. These six are the ones that earn it.
func TestMobileBarLeadsWithWhatAThumbNeeds(t *testing.T) {
	_, rows := mobileBar(defaultRegistry(t), "ctrl+b")

	want := []string{"pfx", "new", "close", "next", "prev", "zoom", "cmds"}
	for i, label := range want {
		if rows[0].Keys[i].Label != label {
			t.Errorf("button %d is %q, want %q", i, rows[0].Keys[i].Label, label)
		}
	}
}

// Every command in the row is one tap: the leader and the bound key, together.
func TestMobileBarChords(t *testing.T) {
	_, rows := mobileBar(defaultRegistry(t), "ctrl+b")
	keys := rows[0].Keys

	want := map[string]struct {
		key   string
		shift bool
	}{
		"new":    {key: "c"},
		"close":  {key: "x"},
		"tile":   {key: " "},
		"prev":   {key: "p"},
		"next":   {key: "n"},
		"zoom":   {key: "z"},
		"vsplit": {key: "|", shift: true},
		"hsplit": {key: "-"},
		"cmds":   {key: "P", shift: true},
		"config": {key: ","},
		"help":   {key: "?", shift: true},
	}
	if len(keys) != len(want)+1 {
		t.Errorf("chord row has %d buttons, want %d and the latch", len(keys), len(want))
	}
	for label, w := range want {
		got, ok := findKey(keys, label)
		if !ok {
			t.Errorf("no %q button", label)
			continue
		}
		if got.Key != w.key || got.Shift != w.shift {
			t.Errorf("%q sends {Key:%q Shift:%v}, want {Key:%q Shift:%v}", label, got.Key, got.Shift, w.key, w.shift)
		}
		if !got.Prefixed {
			t.Errorf("%q is not prefixed, so it types its key into the pane", label)
		}
		if got.Ctrl || got.Alt {
			t.Errorf("%q carries %+v; none of the default bindings is modified", label, got)
		}
	}
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

// A chord whose second half is itself modified is the case sip v0.7.0 fixed,
// and it is what the maintainer's own ctrl+p command palette needs.
func TestMobileBarKeepsAModifiedChord(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Keybindings.PrefixMode["prefix_split_vertical"] = []string{"alt+shift+v"}

	_, rows := mobileBar(config.NewKeybindRegistry(cfg), "ctrl+b")
	vsplit, ok := findKey(rows[0].Keys, "vsplit")
	if !ok {
		t.Fatal("no vsplit button for the alt+shift+v binding")
	}
	want := sip.MobileKey{
		Label: "vsplit", Title: vsplit.Title,
		Key: "v", Code: "KeyV", Alt: true, Shift: true, Prefixed: true,
	}
	if vsplit != want {
		t.Errorf("vsplit sends %+v, want %+v", vsplit, want)
	}
}

// An action bound to several keys takes the first one the browser can send.
func TestMobileBarPicksFirstSendableBinding(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Keybindings.PrefixMode["prefix_next_window"] = []string{"f9", "tab"}

	_, rows := mobileBar(config.NewKeybindRegistry(cfg), "ctrl+b")
	next, ok := findKey(rows[0].Keys, "next")
	if !ok {
		t.Fatal("no next button")
	}
	if next.Key != "Tab" {
		t.Errorf("next sends %q, want Tab, the first binding sip can encode", next.Key)
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

// Every button has to be one of the shapes sip's client understands.
func TestMobileBarWellFormed(t *testing.T) {
	_, rows := mobileBar(defaultRegistry(t), "ctrl+b")
	for _, row := range rows {
		for _, k := range row.Keys {
			if k.Label == "" {
				t.Errorf("unlabelled button: %+v", k)
			}
			if k.Mod == "" && k.Key == "" && !k.Prefix {
				t.Errorf("button %q sends nothing", k.Label)
			}
			if k.Mod != "" && k.Mod != "ctrl" && k.Mod != "alt" {
				t.Errorf("button %q has modifier %q; sip sticks only ctrl and alt", k.Label, k.Mod)
			}
		}
	}
}
