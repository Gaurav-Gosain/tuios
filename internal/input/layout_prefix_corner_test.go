package input

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Corner snapping moved off the bare digits in window mode, where it shadowed
// select_window_1 through _4, onto the layout chord. A binding the config
// resolves and the dispatcher does not handle is a binding that silently does
// nothing, which is the failure this family already had once.

func layoutPrefixOS(t *testing.T) *app.OS {
	t.Helper()
	return osWithBindings(t, func(*config.KeybindingsConfig) {})
}

// TestLayoutChordClearsThePrefix, so a digit does not leave the chord armed and
// swallow the next key.
//
// Negative control: move the LayoutPrefixActive reset below the switch and the
// early returns in the corner cases leave it armed.
func TestLayoutChordClearsThePrefix(t *testing.T) {
	o := layoutPrefixOS(t)
	o.AutoTiling = true
	o.LayoutPrefixActive = true
	o.PrefixActive = true

	o, _ = handleTerminalLayoutPrefix(press("1"), o)

	if o.LayoutPrefixActive || o.PrefixActive {
		t.Errorf("the chord stayed armed: layout=%v prefix=%v", o.LayoutPrefixActive, o.PrefixActive)
	}
}
