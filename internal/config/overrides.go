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

	// HideScrollbar overrides hiding the scrollbar thumb
	HideScrollbar bool

	// WindowButtonStyle overrides how the window controls are drawn
	WindowButtonStyle string

	// WindowButtonPosition overrides which end of the title bar they sit on
	WindowButtonPosition string

	// WindowTitlePosition overrides the window title position
	WindowTitlePosition string

	// HideClock overrides hiding the clock (deprecated, use ShowClock)
	HideClock bool

	// ShowClock enables the clock overlay
	ShowClock bool

	// ShowCPU enables the CPU graph in the dock
	ShowCPU bool

	// ShowRAM enables the RAM usage in the dock
	ShowRAM bool

	// SharedBorders enables shared borders between tiled windows
	SharedBorders bool

	// ScrollbackLines overrides the scrollback buffer size (0 means use default)
	ScrollbackLines int

	// NoAnimations disables UI animations
	NoAnimations bool

	// ConfirmQuit always shows quit confirmation dialog
	ConfirmQuit bool

	// ThemeName is the theme to load
	ThemeName string

	// ZoomMaxWidth caps the zoom mode width (0 = fullscreen)
	ZoomMaxWidth int
}

// ApplyOverrides layers explicit CLI flag values over the globals that
// ApplyAppearanceConfig set. Every entrypoint applies the config funnel
// first; a zero value here means the flag was not given and the config's
// value stands. Any new setting belongs in ApplyAppearanceConfig, and only
// needs a line here if a CLI flag can override it.
func ApplyOverrides(overrides Overrides) {
	if overrides.ASCIIOnly {
		UseASCIIOnly = true
	}
	if overrides.BorderStyle != "" {
		BorderStyle = overrides.BorderStyle
	}
	if overrides.DockbarPosition != "" {
		DockbarPosition = overrides.DockbarPosition
	}
	if overrides.HideWindowButtons {
		HideWindowButtons = true
	}
	if overrides.WindowButtonStyle != "" {
		WindowButtonStyle = overrides.WindowButtonStyle
	}
	if overrides.WindowButtonPosition != "" {
		WindowButtonPosition = overrides.WindowButtonPosition
	}
	if overrides.HideScrollbar {
		HideScrollbar = true
	}
	if overrides.WindowTitlePosition != "" {
		WindowTitlePosition = overrides.WindowTitlePosition
	}
	if overrides.HideClock {
		HideClock = true
	}
	if overrides.ShowClock {
		ShowClock = true
	}
	if overrides.ShowCPU {
		ShowCPU = true
	}
	if overrides.ShowRAM {
		ShowRAM = true
	}
	if overrides.SharedBorders {
		SharedBorders = true
	}
	if overrides.ScrollbackLines > 0 {
		ScrollbackLines = min(max(overrides.ScrollbackLines, 100), 1000000)
	}
	if overrides.NoAnimations {
		AnimationsEnabled = false
	}
	if overrides.ConfirmQuit {
		AlwaysConfirmQuit = true
	}
	if overrides.ThemeName != "" {
		if err := theme.Initialize(overrides.ThemeName); err != nil {
			log.Printf("Warning: Failed to load theme '%s': %v", overrides.ThemeName, err)
		}
	}
	if overrides.ZoomMaxWidth > 0 {
		ZoomMaxWidth = overrides.ZoomMaxWidth
	}
}
