package config_test

import (
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestPressesByActionAcrossSections covers the actions bound in more than one
// section. GetKeys answers with the first section that binds the action, so
// 'tuios keybinds list' showed launcher as "a", its key under the prefix, and
// not its default alt+space.
func TestPressesByActionAcrossSections(t *testing.T) {
	cfg := config.DefaultConfig()
	leader := cfg.Keybindings.LeaderKey
	presses := config.PressesByAction(config.NewKeybindRegistry(cfg))

	for action, want := range map[string][]string{
		"launcher":     {"alt+space", leader + " a"},
		"next_session": {"alt+shift+n", leader + " )"},
		"prev_session": {"alt+shift+p", leader + " ("},
	} {
		for _, press := range want {
			if !slices.Contains(presses[action], press) {
				t.Errorf("%s: presses %v do not include %q", action, presses[action], press)
			}
		}
		if slices.Contains(presses[action], "a") {
			t.Errorf("%s: presses %v include a bare prefix key", action, presses[action])
		}
	}
}

// TestPressesByActionSkipsRailKeys. new_window is n in window mode and t on the
// rail. The rail key works only while the rail has the keyboard, and a press
// list has no way to say so, so it must not be offered as a plain key.
func TestPressesByActionSkipsRailKeys(t *testing.T) {
	cfg := config.DefaultConfig()
	if !slices.Contains(cfg.Keybindings.Sidebar["new_window"], "t") {
		t.Fatal("the rail no longer binds t to new_window, so this case proves nothing")
	}
	presses := config.PressesByAction(config.NewKeybindRegistry(cfg))
	if got := presses["new_window"]; !slices.Equal(got, []string{"n"}) {
		t.Errorf("new_window presses = %v, want [n]", got)
	}
}
