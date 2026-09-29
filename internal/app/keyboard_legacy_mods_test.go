package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// TestKittyLegacyFormSuperIsSuper is the super+f12 side note of issue #201.
// Under the kitty protocol Ghostty sends Cmd+F12 as CSI 24;9~, where 8 is
// Super. The decoder reads that parameter with the xterm bits and reports
// meta+f12, so a super+f12 binding could never match.
//
// Negative control: return k unchanged from fixKittyLegacyMods and the
// kitty rows fail with meta+f12.
func TestKittyLegacyFormSuperIsSuper(t *testing.T) {
	cases := []struct {
		name      string
		seq       string
		hostKitty bool
		want      string
	}{
		{"super f12 under kitty", "\x1b[24;9~", true, "super+f12"},
		{"super shift up under kitty", "\x1b[1;10A", true, "shift+super+up"},
		{"meta f12 under kitty", "\x1b[24;33~", true, "meta+f12"},
		{"alt f12 under kitty", "\x1b[24;3~", true, "alt+f12"},
		{"super v as CSI u", "\x1b[118;9u", true, "super+v"},
		{"xterm meta f12 without kitty", "\x1b[24;9~", false, "meta+f12"},
	}
	for _, c := range cases {
		var d uv.EventDecoder
		_, ev := d.Decode([]byte(c.seq))
		press, ok := ev.(uv.KeyPressEvent)
		if !ok {
			t.Fatalf("%s: %q decoded to %T, want a key press", c.name, c.seq, ev)
		}
		got := fixKittyLegacyMods(tea.Key(press), c.hostKitty).Keystroke()
		if got != c.want {
			t.Errorf("%s: %q reads as %q, want %q", c.name, c.seq, got, c.want)
		}
	}
}

// TestHostKeyModsFixOnlyWhenKittyIsOn checks the message wrapper the input
// path uses, for a press and a release.
func TestHostKeyModsFixOnlyWhenKittyIsOn(t *testing.T) {
	m := &OS{KeyboardEnhancementsEnabled: true}
	press := tea.KeyPressMsg{Code: tea.KeyF12, Mod: tea.ModMeta}
	if got := m.fixHostKeyMods(press).(tea.KeyPressMsg).String(); got != "super+f12" {
		t.Errorf("press reads as %q, want super+f12", got)
	}
	release := tea.KeyReleaseMsg{Code: tea.KeyF12, Mod: tea.ModMeta}
	if got := m.fixHostKeyMods(release).(tea.KeyReleaseMsg).String(); got != "super+f12" {
		t.Errorf("release reads as %q, want super+f12", got)
	}
	m.KeyboardEnhancementsEnabled = false
	if got := m.fixHostKeyMods(press).(tea.KeyPressMsg).String(); got != "meta+f12" {
		t.Errorf("without kitty the press reads as %q, want meta+f12", got)
	}
}

// TestKittyTextOnlyKeyIsText is the input method side of issue #255. Under
// report-all-keys with associated text the kitty protocol sends text that no
// key produced as key number 0, and the decoder read that as ctrl+space.
//
// Negative control: return k unchanged from fixKittyTextOnlyKey and every
// text row reads as ctrl+space.
func TestKittyTextOnlyKeyIsText(t *testing.T) {
	cases := []struct {
		name     string
		seq      string
		wantCode rune
		wantText string
		wantKey  string
	}{
		{"full-width comma", "\x1b[0;;65292u", '，', "，", "，"},
		{"full-width question mark, explicit mods", "\x1b[0;1;65311u", '？', "？", "？"},
		{"two ideographs in one commit", "\x1b[0;;20320:22909u", tea.KeyExtended, "你好", "你好"},
		{"emoji", "\x1b[0;;128077u", 0x1F44D, "👍", "👍"},
		{"real ctrl+space stays", "\x1b[32;5u", tea.KeySpace, "", "ctrl+space"},
		{"legacy NUL stays", "\x00", tea.KeySpace, "", "ctrl+space"},
	}
	for _, c := range cases {
		var d uv.EventDecoder
		_, ev := d.Decode([]byte(c.seq))
		press, ok := ev.(uv.KeyPressEvent)
		if !ok {
			t.Fatalf("%s: %q decoded to %T, want a key press", c.name, c.seq, ev)
		}
		m := &OS{KeyboardEnhancementsEnabled: true}
		got := m.fixHostKeyMods(tea.KeyPressMsg(press)).(tea.KeyPressMsg)
		if got.Code != c.wantCode || got.Text != c.wantText || got.String() != c.wantKey {
			t.Errorf("%s: %q reads as code %U text %q key %q, want code %U text %q key %q",
				c.name, c.seq, got.Code, got.Text, got.String(), c.wantCode, c.wantText, c.wantKey)
		}
	}
}
