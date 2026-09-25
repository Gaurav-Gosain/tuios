package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestAltShiftKeysSpellWhatTheTerminalSends pins the spelling of alt+shift
// chords as terminals deliver them, against the bindings that name them.
//
// A letter arrives from both the legacy escape prefix and the Kitty protocol
// as a lowercase code carrying ModAlt|ModShift, which stringifies as
// "alt+shift+n"; spelling the binding "alt+N" instead would silently normalize
// to plain alt+n, which is already bound to next-window. A digit has no case
// to fold, so alt+shift+<digit> reaches the registry in three spellings
// depending on what the host terminal speaks, and all of them have to find the
// same action. The unshifted chords must survive the aliasing.
func TestAltShiftKeysSpellWhatTheTerminalSends(t *testing.T) {
	// alt+shift+<digit> is how the non-macOS defaults bind move_and_follow_N.
	// The macOS defaults spell the same action opt+shift+N, so on a macOS
	// machine the registry would hold no alt+ spelling of it. The subject here
	// is the wire spellings of one chord, which is the same question on both
	// platforms.
	t.Cleanup(config.ForceMacOSHost(false))
	registry := config.NewKeybindRegistry(config.DefaultConfig())

	for _, tc := range []struct {
		what string
		msg  tea.KeyPressMsg
		key  string
		want string
	}{
		{"letter", tea.KeyPressMsg{Code: 'n', ShiftedCode: 'N', Mod: tea.ModAlt | tea.ModShift}, "alt+shift+n", "next_session"},
		{"letter", tea.KeyPressMsg{Code: 'p', ShiftedCode: 'P', Mod: tea.ModAlt | tea.ModShift}, "alt+shift+p", "prev_session"},
		{"kitty digit", tea.KeyPressMsg{Code: '1', ShiftedCode: '!', Mod: tea.ModAlt | tea.ModShift}, "alt+shift+1", "move_and_follow_1"},
		{"legacy digit", tea.KeyPressMsg{Code: '!', Mod: tea.ModAlt}, "alt+!", "move_and_follow_1"},
		{"modifyOtherKeys digit", tea.KeyPressMsg{Code: '!', Mod: tea.ModAlt | tea.ModShift}, "alt+shift+!", "move_and_follow_1"},
		{"kitty digit", tea.KeyPressMsg{Code: '9', ShiftedCode: '(', Mod: tea.ModAlt | tea.ModShift}, "alt+shift+9", "move_and_follow_9"},
		{"legacy digit", tea.KeyPressMsg{Code: '(', Mod: tea.ModAlt}, "alt+(", "move_and_follow_9"},
	} {
		if got := tc.msg.String(); got != tc.key {
			t.Fatalf("%s: terminal spells the chord %q, want %q", tc.what, got, tc.key)
		}
		if got := registry.GetAction(tc.key); got != tc.want {
			t.Errorf("%s spelling %q resolved to %q, want %q", tc.what, tc.key, got, tc.want)
		}
		if !GetDispatcher().HasAction(tc.want) {
			t.Errorf("%s has no registered handler", tc.want)
		}
	}

	for _, tc := range []struct {
		key, want string
		lookup    func(string) string
	}{
		{"alt+n", "terminal_next_window", registry.GetTerminalModeAction},
		{"alt+p", "terminal_prev_window", registry.GetTerminalModeAction},
		{"alt+1", "switch_workspace_1", registry.GetAction},
		{"alt+9", "switch_workspace_9", registry.GetAction},
	} {
		if got := tc.lookup(tc.key); got != tc.want {
			t.Errorf("%s resolved to %q, want %q", tc.key, got, tc.want)
		}
	}
}
