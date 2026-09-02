package config

import (
	"testing"
)

// Percentage resizing of the focused pane (issue #29): the layout prefix
// (leader L) carries digit binds for width percentages and shift+digit binds
// for height, and the registry resolves them.

// TestDefaultLayoutPrefixHasPercentageResize pins the issue #29 defaults.
func TestDefaultLayoutPrefixHasPercentageResize(t *testing.T) {
	cfg := DefaultConfig()
	lp := cfg.Keybindings.LayoutPrefix

	wantBinds := map[string]string{
		"resize_width_50":  "5",
		"resize_width_60":  "6",
		"resize_width_70":  "7",
		"resize_width_80":  "8",
		"resize_width_90":  "9",
		"resize_height_50": "shift+5",
		"resize_height_60": "shift+6",
		"resize_height_70": "shift+7",
		"resize_height_80": "shift+8",
		"resize_height_90": "shift+9",
	}
	for action, wantKey := range wantBinds {
		keys, ok := lp[action]
		if !ok {
			t.Errorf("layout prefix missing default bind for %q (issue #29)", action)
			continue
		}
		found := false
		for _, k := range keys {
			if k == wantKey {
				found = true
			}
		}
		if !found {
			t.Errorf("%q bound to %v, want %q in the set", action, keys, wantKey)
		}
	}
}

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
