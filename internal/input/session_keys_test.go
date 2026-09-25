package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// altShift builds the key event a terminal actually delivers for alt+shift+<r>.
// Both the legacy escape prefix and the Kitty protocol decode to a lowercase code
// carrying ModAlt|ModShift, which stringifies as "alt+shift+n"; spelling the
// binding "alt+N" instead would silently normalize to plain alt+n, which is
// already bound to next-window.
func altShift(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, ShiftedCode: r - 32, Mod: tea.ModAlt | tea.ModShift}
}

func TestAltShiftKeysSpellWhatTheTerminalSends(t *testing.T) {
	cfg := config.DefaultConfig()
	registry := config.NewKeybindRegistry(cfg)

	for _, tc := range []struct {
		msg  tea.KeyPressMsg
		key  string
		want string
	}{
		{altShift('n'), "alt+shift+n", "next_session"},
		{altShift('p'), "alt+shift+p", "prev_session"},
	} {
		if got := tc.msg.String(); got != tc.key {
			t.Fatalf("terminal spells the chord %q, but the binding is written %q", got, tc.key)
		}
		if got := registry.GetAction(tc.msg.String()); got != tc.want {
			t.Errorf("%s resolved to %q, want %q", tc.key, got, tc.want)
		}
		if !GetDispatcher().HasAction(tc.want) {
			t.Errorf("%s has no registered handler", tc.want)
		}
	}

	// The shifted chords must not have swallowed the unshifted ones.
	for key, want := range map[string]string{"alt+n": "terminal_next_window", "alt+p": "terminal_prev_window"} {
		if got := registry.GetTerminalModeAction(key); got != want {
			t.Errorf("%s resolved to %q, want %q", key, got, want)
		}
	}
}

// A digit has no case to fold, so alt+shift+<digit> reaches the registry in
// three different spellings depending on what the host terminal speaks, and all
// three have to find the same action.
func TestAltShiftDigitsResolveInEverySpellingATerminalSends(t *testing.T) {
	// alt+shift+<digit> is how the non-macOS defaults bind move_and_follow_N.
	// The macOS defaults spell the same action opt+shift+N, so on a macOS
	// machine the registry would hold no alt+ spelling of it and this would
	// assert against a binding the config does not have. The subject here is
	// the three wire spellings of one chord, which is the same question on
	// both platforms.
	t.Cleanup(config.ForceMacOSHost(false))
	registry := config.NewKeybindRegistry(config.DefaultConfig())

	for _, tc := range []struct {
		what string
		msg  tea.KeyPressMsg
		key  string
		want string
	}{
		{"kitty", tea.KeyPressMsg{Code: '1', ShiftedCode: '!', Mod: tea.ModAlt | tea.ModShift}, "alt+shift+1", "move_and_follow_1"},
		{"legacy", tea.KeyPressMsg{Code: '!', Mod: tea.ModAlt}, "alt+!", "move_and_follow_1"},
		{"modifyOtherKeys", tea.KeyPressMsg{Code: '!', Mod: tea.ModAlt | tea.ModShift}, "alt+shift+!", "move_and_follow_1"},
		{"kitty", tea.KeyPressMsg{Code: '9', ShiftedCode: '(', Mod: tea.ModAlt | tea.ModShift}, "alt+shift+9", "move_and_follow_9"},
		{"legacy", tea.KeyPressMsg{Code: '(', Mod: tea.ModAlt}, "alt+(", "move_and_follow_9"},
	} {
		if got := tc.msg.String(); got != tc.key {
			t.Fatalf("%s terminal spells the chord %q, want %q", tc.what, got, tc.key)
		}
		if got := registry.GetAction(tc.key); got != tc.want {
			t.Errorf("%s spelling %q resolved to %q, want %q", tc.what, tc.key, got, tc.want)
		}
		if !GetDispatcher().HasAction(tc.want) {
			t.Errorf("%s has no registered handler", tc.want)
		}
	}

	// Aliasing the shifted chords must leave the unshifted digits alone.
	for key, want := range map[string]string{"alt+1": "switch_workspace_1", "alt+9": "switch_workspace_9"} {
		if got := registry.GetAction(key); got != want {
			t.Errorf("%s resolved to %q, want %q", key, got, want)
		}
	}
}
