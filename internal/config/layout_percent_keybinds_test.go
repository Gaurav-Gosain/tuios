package config

import (
	"testing"
)

// Percentage resizing of the focused pane (issue #29): the layout prefix
// (leader L) carries digit binds for width percentages and shift+digit binds
// for height, and the registry resolves them.

// TestLayoutPrefixDigitsResolveToPercentActions drives the registry the way
// the input layer does: a digit under the layout prefix names the width
// action, a shift+digit the height action.
func TestLayoutPrefixDigitsResolveToPercentActions(t *testing.T) {
	cfg := DefaultConfig()
	reg := NewKeybindRegistry(cfg)

	for pct := 5; pct <= 9; pct++ {
		digit := string(rune('0' + pct))
		widthAction := "resize_width_" + digit + "0"
		heightAction := "resize_height_" + digit + "0"

		if got := reg.GetLayoutPrefixAction(digit); got != widthAction {
			t.Errorf("layout prefix key %q resolves to %q, want %q", digit, got, widthAction)
		}
		if got := reg.GetLayoutPrefixAction("shift+" + digit); got != heightAction {
			t.Errorf("layout prefix key shift+%q resolves to %q, want %q", digit, got, heightAction)
		}
	}
}
