package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
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

// TestSessionKeysReachTheActionFromBothModes checks the routing, not the switch:
// standalone has no other session to go to, so the proof that the key arrived is
// the hint it leaves behind. Terminal mode is the case that needs the check,
// since a main-section binding only fires there via isTerminalSafeAction.
func TestSessionKeysReachTheActionFromBothModes(t *testing.T) {
	for _, mode := range []app.Mode{app.WindowManagementMode, app.TerminalMode} {
		for _, msg := range []tea.KeyPressMsg{altShift('n'), altShift('p')} {
			o := twoPaneOS(t)
			o.Mode = mode
			o, _ = HandleKeyPress(msg, o)
			if len(o.Notifications) == 0 {
				t.Fatalf("%s in mode %v produced no response", msg.String(), mode)
			}
			if got := o.Notifications[len(o.Notifications)-1].Message; !strings.Contains(got, "No other sessions") {
				t.Errorf("%s in mode %v said %q", msg.String(), mode, got)
			}
		}
	}
}

// TestLeaderExploreTogglesRailFocus checks both halves of the toggle. The second
// half only works because the rail lets the leader through: it swallows every
// other unbound key, so ctrl+b could not otherwise start a chord from inside it.
func TestLeaderExploreTogglesRailFocus(t *testing.T) {
	prev := config.SidebarEnabled
	config.SidebarEnabled = true
	t.Cleanup(func() { config.SidebarEnabled = prev })

	leader := tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}
	explore := tea.KeyPressMsg{Code: 'e', Text: "e"}

	for _, mode := range []app.Mode{app.WindowManagementMode, app.TerminalMode} {
		o := twoPaneOS(t)
		o.Mode = mode

		o, _ = HandleKeyPress(leader, o)
		o, _ = HandleKeyPress(explore, o)
		if !o.SidebarFocused {
			t.Fatalf("ctrl+b e in mode %v did not focus the rail", mode)
		}

		o, _ = HandleKeyPress(leader, o)
		if !o.PrefixActive {
			t.Fatalf("the rail swallowed the leader in mode %v", mode)
		}
		o, _ = HandleKeyPress(explore, o)
		if o.SidebarFocused {
			t.Fatalf("ctrl+b e in mode %v did not leave the rail", mode)
		}
	}
}
