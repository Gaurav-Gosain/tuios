package config_test

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestAutoEnterTerminalOnFocusAcceptsALeftoverBool: the option was a bool
// before it took three values, and a config file still carrying the bool must
// decode rather than fail the whole file.
func TestAutoEnterTerminalOnFocusAcceptsALeftoverBool(t *testing.T) {
	for src, want := range map[string]config.AutoEnterTerminalPolicy{
		"true":  config.AutoEnterTerminalAll,
		"false": config.AutoEnterTerminalOff,
	} {
		cfg, err := config.ParseUserConfig([]byte("[appearance]\nauto_enter_terminal_on_focus = " + src + "\n"))
		if err != nil {
			t.Fatalf("%s: a leftover bool discarded the file: %v", src, err)
		}
		if got := cfg.Appearance.AutoEnterTerminalOnFocus; got != want {
			t.Errorf("%s decoded as %q, want %q", src, got, want)
		}
	}
}
