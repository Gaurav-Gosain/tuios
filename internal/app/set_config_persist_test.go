package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A live appearance change has to survive the next one.
//
// The hand-written setters wrote the global the renderer reads and nothing
// else, while every registry option funnels m.UserConfig back through
// ApplyAppearanceConfig on its way in. So a caller that set a border style and
// then a theme got its border style back: the struct still said rounded and
// re-applying it won. get-option kept answering with the value the caller had
// asked for, which is the part that makes it hard to see, so the check here is
// on the global the renderer actually draws from.
func TestSetConfigSurvivesTheNextSet(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		value string
		read  func() string
	}{
		{"border style", "appearance.border_style", "thick", func() string { return config.BorderStyle }},
		{"dockbar position", "appearance.dockbar_position", "top", func() string { return config.DockbarPosition }},
		{"window button style", "appearance.window_button_style", config.WindowButtonStyles[1], func() string { return config.WindowButtonStyle }},
		{"window button position", "appearance.window_button_position", config.WindowButtonPositions[1], func() string { return config.WindowButtonPosition }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &OS{UserConfig: config.DefaultConfig()}
			config.ApplyAppearanceConfig(m.UserConfig)

			if err := m.SetConfig(tt.path, tt.value); err != nil {
				t.Fatalf("SetConfig(%q, %q) = %v", tt.path, tt.value, err)
			}
			if got := tt.read(); got != tt.value {
				t.Fatalf("after the set, the global is %q, want %q", got, tt.value)
			}

			// Any other registry option will do; the sidebar's width is a
			// plain int that touches nothing this test reads.
			if err := m.SetConfig("appearance.sidebar.width", "30"); err != nil {
				t.Fatalf("second SetConfig = %v", err)
			}
			if got := tt.read(); got != tt.value {
				t.Errorf("a later set reverted it to %q, want %q still", got, tt.value)
			}
		})
	}
}

// The animations switch is the same bug with a bool, and it is the one setter
// whose recorded value is not the string it was handed: "toggle" resolves to
// whichever of the two it lands on.
func TestToggleAnimationsSurvivesTheNextSet(t *testing.T) {
	m := &OS{UserConfig: config.DefaultConfig()}
	config.ApplyAppearanceConfig(m.UserConfig)

	before := config.AnimationsEnabled
	if err := m.SetConfig("appearance.animations_enabled", "toggle"); err != nil {
		t.Fatalf("toggle = %v", err)
	}
	toggled := config.AnimationsEnabled
	if toggled == before {
		t.Fatalf("toggle did not change anything (still %v)", toggled)
	}

	if err := m.SetConfig("appearance.sidebar.width", "30"); err != nil {
		t.Fatalf("second SetConfig = %v", err)
	}
	if config.AnimationsEnabled != toggled {
		t.Errorf("a later set reverted animations to %v, want %v still", config.AnimationsEnabled, toggled)
	}
}
