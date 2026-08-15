package main

import "testing"

// The leader button is the one key on the bar derived from configuration, so
// a rebound leader has to reach the phone as the key the user rebound it to.
func TestLeaderMobileKey(t *testing.T) {
	tests := []struct {
		leader           string
		ok               bool
		key              string
		ctrl, alt, shift bool
	}{
		{leader: "ctrl+b", ok: true, key: "b", ctrl: true},
		{leader: "ctrl+a", ok: true, key: "a", ctrl: true},
		{leader: "alt+shift+w", ok: true, key: "w", alt: true, shift: true},
		{leader: "ctrl+space", ok: true, key: " ", ctrl: true},
		{leader: "esc", ok: true, key: "Escape"},
		// sip's client has no encoding for a function key, and a button that
		// sends nothing is worse than one that is absent.
		{leader: "f1"},
		{leader: "ctrl+f5"},
		{leader: "hyper+q"},
		{leader: ""},
	}

	for _, tt := range tests {
		got, ok := leaderMobileKey(tt.leader)
		if ok != tt.ok {
			t.Errorf("leaderMobileKey(%q) ok = %v, want %v", tt.leader, ok, tt.ok)
			continue
		}
		if !tt.ok {
			continue
		}
		if got.Key != tt.key || got.Ctrl != tt.ctrl || got.Alt != tt.alt || got.Shift != tt.shift {
			t.Errorf("leaderMobileKey(%q) = {Key:%q Ctrl:%v Alt:%v Shift:%v}, want {Key:%q Ctrl:%v Alt:%v Shift:%v}",
				tt.leader, got.Key, got.Ctrl, got.Alt, got.Shift,
				tt.key, tt.ctrl, tt.alt, tt.shift)
		}
		if got.Mod != "" {
			t.Errorf("leaderMobileKey(%q) set Mod = %q; the leader is a keystroke, not a sticky modifier", tt.leader, got.Mod)
		}
	}
}

// A narrow phone shows the head of the bar without scrolling, and the leader
// is the key it should find there.
func TestMobileKeysLeadWithPrefix(t *testing.T) {
	keys := mobileKeys("ctrl+b")
	if len(keys) == 0 {
		t.Fatal("mobileKeys returned no keys")
	}
	if keys[0].Label != "pfx" {
		t.Errorf("first key = %q, want the prefix", keys[0].Label)
	}

	// An unusable leader drops its button and leaves the rest intact.
	fallback := mobileKeys("f1")
	if len(fallback) != len(keys)-1 {
		t.Errorf("unencodable leader gave %d keys, want %d", len(fallback), len(keys)-1)
	}
	for _, k := range fallback {
		if k.Label == "pfx" {
			t.Error("unencodable leader still produced a prefix button")
		}
	}
}

// Every button has to be one of the three shapes sip's client understands: a
// sticky modifier, or a key, and a labelled one either way.
func TestMobileKeysWellFormed(t *testing.T) {
	for _, k := range mobileKeys("ctrl+b") {
		if k.Label == "" {
			t.Errorf("unlabelled button: %+v", k)
		}
		if k.Mod == "" && k.Key == "" {
			t.Errorf("button %q sends nothing", k.Label)
		}
		if k.Mod != "" && k.Mod != "ctrl" && k.Mod != "alt" {
			t.Errorf("button %q has modifier %q; sip sticks only ctrl and alt", k.Label, k.Mod)
		}
	}
}
