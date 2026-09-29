package config

import (
	"slices"
	"testing"
)

// j and k are new window-mode defaults (issue #231). A fresh config gets them.
func TestJKFocusByDefault(t *testing.T) {
	r := NewKeybindRegistry(DefaultConfig())
	for key, want := range map[string]string{"h": "snap_left", "j": "focus_down", "k": "focus_up", "l": "snap_right"} {
		if got := r.GetAction(key); got != want {
			t.Errorf("window-mode %s runs %q, want %q", key, got, want)
		}
	}
}

// A config that already binds j or k in any window-mode table keeps its own
// binding. The layout table would win the key, so the new default yields.
func TestJKDefaultsYieldToAUserBinding(t *testing.T) {
	for _, tc := range []struct{ section, body string }{
		{"window_management", "[keybindings.window_management]\nnext_window = [\"j\"]\nprev_window = [\"k\"]\n"},
		{"layout", "[keybindings.layout]\nswap_down = [\"j\"]\nswap_up = [\"k\"]\n"},
		{"navigation", "[keybindings.navigation]\nnext_window = [\"j\"]\nprev_window = [\"k\"]\n"},
	} {
		t.Run(tc.section, func(t *testing.T) {
			cfg, err := ParseUserConfig([]byte(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			r := NewKeybindRegistry(cfg)
			if got := r.GetAction("j"); got == "focus_down" {
				t.Errorf("j was taken from the user's binding by focus_down")
			}
			if got := r.GetAction("k"); got == "focus_up" {
				t.Errorf("k was taken from the user's binding by focus_up")
			}
			var yielded []string
			for _, y := range cfg.YieldedDefaults {
				if y.Section == "layout" {
					yielded = append(yielded, y.Action+"="+y.Key)
				}
			}
			slices.Sort(yielded)
			if want := []string{"focus_down=j", "focus_up=k"}; !slices.Equal(yielded, want) {
				t.Errorf("YieldedDefaults for layout = %v, want %v", yielded, want)
			}
		})
	}

	// A config that binds neither key gets both defaults.
	cfg, err := ParseUserConfig([]byte("[keybindings.layout]\nsnap_left = [\"h\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := NewKeybindRegistry(cfg).GetAction("j"); got != "focus_down" {
		t.Errorf("an older config without j gets %q for j, want focus_down", got)
	}
}
