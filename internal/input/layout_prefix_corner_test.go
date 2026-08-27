package input

import (
	"strings"
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

// TestLayoutChordDigitsReachTheCornerHandler. The switch in
// handleTerminalLayoutPrefix is hand written, so an action added to the
// layout_prefix table is invisible until someone adds a case; without one the
// key falls through to the branch that just dismisses the chord.
//
// Tiling on is the observable branch: it reports why nothing moved. Reaching
// that message proves the key resolved to snap_corner_N and that the case
// matched, without asserting on window geometry.
//
// Negative control, run and confirmed failing: remove the four snap_corner
// cases from handleTerminalLayoutPrefix and every digit here falls to the
// default branch, which raises nothing.
func TestLayoutChordDigitsReachTheCornerHandler(t *testing.T) {
	for _, digit := range []string{"1", "2", "3", "4"} {
		o := layoutPrefixOS(t)
		o.AutoTiling = true
		o.LayoutPrefixActive = true

		o, _ = handleTerminalLayoutPrefix(press(digit), o)

		if len(o.Notifications) == 0 {
			t.Errorf("the layout chord then %q did nothing and said nothing", digit)
			continue
		}
		msg := o.Notifications[len(o.Notifications)-1].Message
		if !strings.Contains(msg, "tiling off") {
			t.Errorf("the layout chord then %q said %q", digit, msg)
		}
	}
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

// TestBareDigitsNoLongerCornerSnap in window mode: that is the whole point of
// the move, and the registry is what decides it.
//
// Negative control, run and confirmed failing: put snap_corner_N back on the
// bare digits in getDefaultLayoutKeybinds and every digit here resolves to it.
func TestBareDigitsNoLongerCornerSnap(t *testing.T) {
	o := layoutPrefixOS(t)
	for _, digit := range []string{"1", "2", "3", "4"} {
		got := o.KeybindRegistry.GetAction(digit)
		if strings.HasPrefix(got, "snap_corner") {
			t.Errorf("%q in window mode still runs %q", digit, got)
		}
		if want := "select_window_" + digit; got != want {
			t.Errorf("%q in window mode runs %q, want %q", digit, got, want)
		}
	}
}
