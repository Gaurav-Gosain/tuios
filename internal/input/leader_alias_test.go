package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestLeaderKeyAcceptsModifierAliases is issue #201 at the input path: the
// leader is written with a macOS alias and the key event carries Bubble Tea's
// name for the modifier.
//
// Negative control: compare msg.String() with the leader through
// strings.EqualFold, as isLeaderKey did, and every alias row fails.
func TestLeaderKeyAcceptsModifierAliases(t *testing.T) {
	altF12 := tea.KeyPressMsg{Code: tea.KeyF12, Mod: tea.ModAlt}
	for _, mac := range []bool{true, false} {
		restore := config.ForceMacOSHost(mac)
		for _, leader := range []string{"opt+f12", "option+f12", "Opt+F12", "alt+f12"} {
			s := &config.Settings{LeaderKey: leader}
			if !isLeaderKey(altF12, s, 0) {
				t.Errorf("macOS=%v: leader %q does not match %q", mac, leader, altF12.String())
			}
		}
		s := &config.Settings{LeaderKey: "opt+f12"}
		if isLeaderKey(tea.KeyPressMsg{Code: tea.KeyF12}, s, 0) {
			t.Errorf("macOS=%v: leader opt+f12 matches a bare f12", mac)
		}
		restore()
	}
}
