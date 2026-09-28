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
