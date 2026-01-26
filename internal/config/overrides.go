package config

import (
	"log"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Overrides contains CLI flag values that can override user config.
// Zero values indicate the flag was not set and should use the user config default.
type Overrides struct {
	// ASCIIOnly uses ASCII characters instead of Nerd Font icons
	ASCIIOnly bool

	// BorderStyle overrides the window border style
	BorderStyle string

	// DockbarPosition overrides the dockbar position
	DockbarPosition string

	// HideWindowButtons overrides hiding window control buttons
	HideWindowButtons bool

	// WindowTitlePosition overrides the window title position
	WindowTitlePosition string

	// HideClock overrides hiding the clock
	HideClock bool

	// ScrollbackLines overrides the scrollback buffer size (0 means use default)
	ScrollbackLines int

	// NoAnimations disables UI animations
	NoAnimations bool

	// ThemeName is the theme to load
	ThemeName string
}

// ApplyOverrides applies CLI flag overrides to global config, falling back to user config defaults.
// If userConfig is nil, only CLI flag values (when set) are applied.
func ApplyOverrides(overrides Overrides, userConfig *UserConfig) {
	// ASCII Only - simple flag override
	if overrides.ASCIIOnly {
		UseASCIIOnly = true
	}

	// Border Style - CLI flag takes precedence, otherwise use user config
	if overrides.BorderStyle != "" {
		BorderStyle = overrides.BorderStyle
	} else if userConfig != nil && userConfig.Appearance.BorderStyle != "" {
		BorderStyle = userConfig.Appearance.BorderStyle
	}

	// Dockbar Position - CLI flag takes precedence, otherwise use user config
	if overrides.DockbarPosition != "" {
		DockbarPosition = overrides.DockbarPosition
	} else if userConfig != nil && userConfig.Appearance.DockbarPosition != "" {
		DockbarPosition = userConfig.Appearance.DockbarPosition
	}

	// Hide Window Buttons - OR of CLI flag and user config
	if userConfig != nil {
		HideWindowButtons = overrides.HideWindowButtons || userConfig.Appearance.HideWindowButtons
	} else {
		HideWindowButtons = overrides.HideWindowButtons
	}

	// Window Title Position - CLI flag takes precedence, otherwise use user config
	if overrides.WindowTitlePosition != "" {
		WindowTitlePosition = overrides.WindowTitlePosition
	} else if userConfig != nil && userConfig.Appearance.WindowTitlePosition != "" {
		WindowTitlePosition = userConfig.Appearance.WindowTitlePosition
	}

	// Hide Clock - OR of CLI flag and user config
	if userConfig != nil {
		HideClock = overrides.HideClock || userConfig.Appearance.HideClock
	} else {
		HideClock = overrides.HideClock
	}

	// Scrollback Lines - CLI flag takes precedence, otherwise use user config
	if overrides.ScrollbackLines > 0 {
		// Clamp to valid range
		lines := overrides.ScrollbackLines
		if lines < 100 {
			lines = 100
		} else if lines > 1000000 {
			lines = 1000000
		}
		ScrollbackLines = lines
	} else if userConfig != nil && userConfig.Appearance.ScrollbackLines > 0 {
		ScrollbackLines = userConfig.Appearance.ScrollbackLines
	}

	// Leader Key - only from user config
	if userConfig != nil && userConfig.Keybindings.LeaderKey != "" {
		LeaderKey = userConfig.Keybindings.LeaderKey
	}

	// Animations - disabled by flag
	if overrides.NoAnimations {
		AnimationsEnabled = false
	}

	// Theme - initialize if specified
	if overrides.ThemeName != "" {
		if err := theme.Initialize(overrides.ThemeName); err != nil {
			log.Printf("Warning: Failed to load theme '%s': %v", overrides.ThemeName, err)
		}
	}
}
