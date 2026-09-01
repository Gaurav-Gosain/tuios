package input

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
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

// TestLayoutChordPercentDigitsRunTheResize is the reachability test for issue
// #29. TestLayoutPrefixDigitsResolveToPercentActions proves the key names the
// width action and TestDefaultLayoutPrefixHasPercentageResize proves the
// defaults exist, but neither proves the action runs: the layout prefix was a
// hand-written switch, so a digit resolved to resize_width_N and then fell
// through to the branch that just dismissed the chord. Driving the real chord
// and watching the focused pane change width proves the dispatcher is reached.
func TestLayoutChordPercentDigitsRunTheResize(t *testing.T) {
	// Width 200 with a 100/100 split: every target leaves the yielding pane
	// at or above the minimum width, so the resize lands exactly on the
	// percentage (the 120-wide fixture would clamp 90% to 100 cells).
	const width, height = 200, 40
	for _, tc := range []struct {
		key       string
		wantWidth int
	}{
		{"5", 100}, // 200 * 50%
		{"6", 120}, // 200 * 60%
		{"7", 140}, // 200 * 70%
		{"8", 160}, // 200 * 80%
		{"9", 180}, // 200 * 90%
	} {
		o := layoutPrefixOS(t)
		left := &terminal.Window{ID: "left-1", Workspace: o.CurrentWorkspace, X: 0, Y: 0, Width: 100, Height: height, Tiled: true}
		right := &terminal.Window{ID: "right-2", Workspace: o.CurrentWorkspace, X: 100, Y: 0, Width: 100, Height: height, Tiled: true}
		o.Windows = []*terminal.Window{left, right}
		o.FocusedWindow = 1 // focus the RIGHT (boundary) pane
		o.AutoTiling = true
		o.Width = width
		o.Height = height
		o.LayoutPrefixActive = true
		o.PrefixActive = true

		o, _ = handleTerminalLayoutPrefix(press(tc.key), o)

		if right.Width != tc.wantWidth {
			t.Errorf("layout chord then %q: focused pane width = %d, want %d (resize_width_%s0)",
				tc.key, right.Width, tc.wantWidth, tc.key)
		}
	}
}

// TestLayoutChordClearsThePrefixForPercentDigits, so a resize digit does not
// leave the chord armed and swallow the next key either.
func TestLayoutChordClearsThePrefixForPercentDigits(t *testing.T) {
	o := layoutPrefixOS(t)
	o.Windows = []*terminal.Window{{ID: "w1", Workspace: o.CurrentWorkspace, X: 0, Y: 0, Width: 60, Height: 40, Tiled: true}}
	o.FocusedWindow = 0
	o.AutoTiling = true
	o.LayoutPrefixActive = true
	o.PrefixActive = true

	o, _ = handleTerminalLayoutPrefix(press("5"), o)

	if o.LayoutPrefixActive || o.PrefixActive {
		t.Errorf("the chord stayed armed: layout=%v prefix=%v", o.LayoutPrefixActive, o.PrefixActive)
	}
}
