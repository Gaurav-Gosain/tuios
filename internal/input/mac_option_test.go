package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// onDarwin puts the macOS-only key paths under test on whatever machine runs
// them. The glyph tables are compiled in on every platform; only the guard that
// consults them is platform-dependent.
func onDarwin(t *testing.T) {
	t.Helper()
	prev := darwinHost
	darwinHost = true
	t.Cleanup(func() { darwinHost = prev })
}

// The reported bug: on macOS the Option key composes a character instead of
// setting Alt, so the alt+n bound to terminal_next_window never fires and the
// composed character is typed into the pane instead.
//
// Each case below is a real encoding of Option+n or Option+p. Which one a user
// gets depends on their terminal and its settings, and all of them have to reach
// the same action.
func TestMacOptionChordsReachTheirBinding(t *testing.T) {
	onDarwin(t)

	for _, tc := range []struct {
		what string
		msg  tea.KeyPressMsg
		want string
	}{
		{
			// Option as Meta / Esc+: the terminal sends ESC n and nothing is composed.
			what: "esc-prefixed meta",
			msg:  tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt},
			want: "terminal_next_window",
		},
		{
			// No Kitty protocol and no Option-as-Meta: the dead key spills its
			// tilde with no modifier at all.
			what: "composed glyph, bare",
			msg:  tea.KeyPressMsg{Code: '˜', Text: "˜"},
			want: "terminal_next_window",
		},
		{
			// Kitty protocol, no alternate-key reporting: Ghostty and kitty set
			// the Alt bit but still report the composed codepoint.
			what: "composed glyph with alt",
			msg:  tea.KeyPressMsg{Code: '˜', Mod: tea.ModAlt},
			want: "terminal_next_window",
		},
		{
			// Kitty protocol with alternate-key reporting: the base-layout code
			// says which key it really was.
			what: "composed glyph with base code",
			msg:  tea.KeyPressMsg{Code: '˜', BaseCode: 'n', Mod: tea.ModAlt},
			want: "terminal_next_window",
		},
		{
			// Num Lock is on by default on most keyboards and the Kitty protocol
			// reports it in the modifier field.
			what: "composed glyph with a lock modifier",
			msg:  tea.KeyPressMsg{Code: '˜', Mod: tea.ModAlt | tea.ModNumLock},
			want: "terminal_next_window",
		},
		{
			what: "option+p composes pi",
			msg:  tea.KeyPressMsg{Code: 'π', Text: "π"},
			want: "terminal_prev_window",
		},
	} {
		registry := config.NewKeybindRegistry(config.DefaultConfig())
		if got := lookupAction(tc.msg, registry.GetTerminalModeAction); got != tc.want {
			t.Errorf("%s (%q): resolved to %q, want %q", tc.what, tc.msg.String(), got, tc.want)
		}
	}
}

// Option+Shift+n composes the same tilde as the Option+n dead key. When the
// terminal reports the Shift bit they are still tellable apart, and the two are
// bound to different things.
func TestShiftedOptionChordPrefersTheShiftedBinding(t *testing.T) {
	onDarwin(t)
	registry := config.NewKeybindRegistry(config.DefaultConfig())

	shifted := tea.KeyPressMsg{Code: '˜', Mod: tea.ModAlt | tea.ModShift}
	if got := lookupAction(shifted, registry.GetAction); got != "next_session" {
		t.Errorf("opt+shift+n resolved to %q, want next_session", got)
	}
	// Without the Shift bit there is nothing to tell them apart, and the
	// unshifted reading is the one that keeps working.
	bare := tea.KeyPressMsg{Code: '˜', Mod: tea.ModAlt}
	if got := lookupAction(bare, registry.GetTerminalModeAction); got != "terminal_next_window" {
		t.Errorf("opt+n resolved to %q, want terminal_next_window", got)
	}
}

// Off darwin the same glyphs are ordinary characters that belong to the shell.
func TestComposedGlyphsAreNotChordsOffDarwin(t *testing.T) {
	prev := darwinHost
	darwinHost = false
	t.Cleanup(func() { darwinHost = prev })
	// The defaults and the key normalizer read the config package's own
	// platform, not this one. On a macOS machine the registry would otherwise
	// still be built from the macOS defaults, whose opt+ bindings expand to
	// exactly the glyphs this test says mean nothing here.
	t.Cleanup(config.ForceMacOSHost(false))

	registry := config.NewKeybindRegistry(config.DefaultConfig())
	for _, msg := range []tea.KeyPressMsg{
		{Code: '˜', Text: "˜"},
		{Code: 'π', Text: "π"},
		{Code: '¬', Text: "¬"},
	} {
		if got := lookupAction(msg, registry.GetTerminalModeAction); got != "" {
			t.Errorf("%q resolved to %q off darwin, want no action", msg.String(), got)
		}
	}
}

// An Option chord only stands in for a binding when Option is the only modifier
// involved. Ctrl+Alt+n is a different chord and macOS composes nothing for it.
func TestMacOptionChordIgnoresOtherModifiers(t *testing.T) {
	onDarwin(t)

	for _, msg := range []tea.KeyPressMsg{
		{Code: '˜', Mod: tea.ModAlt | tea.ModCtrl},
		{Code: '˜', Mod: tea.ModSuper},
	} {
		if chord, ok := macOptionChord(msg); ok {
			t.Errorf("%q was read as %q, want no chord", msg.String(), chord)
		}
	}
}
