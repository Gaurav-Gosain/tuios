package config

import (
	"fmt"
	"slices"
)

// profile: a set of defaults on top of the low-level options.
//
//	profile = "simple"   # simple | keyboard (default)
//
// Simple is for a person new to terminals. Every key goes to the pane
// (window_mode off), the dock sits at the bottom with a row of buttons, a
// right-click opens a menu, and the UI calls a workspace a tab. Keyboard is
// today's tuios and changes nothing.
//
// A profile only fills what the file leaves out, so a key the person sets
// always wins. It is applied on parse and is part of the baseline, so a save
// writes none of it back: changing the profile is one line.

// The profiles.
const (
	ProfileKeyboard = "keyboard"
	ProfileSimple   = "simple"
)

// Profiles are the accepted values of profile.
var Profiles = []string{ProfileKeyboard, ProfileSimple}

// simpleDock is the dock of the Simple profile: the tabs on the left, the
// minimized panes in the middle, and the buttons, clock and session controls
// on the right. No mode pill, trail, tape or meters.
var (
	simpleDockLeft  = []string{DockComponentWorkspaces}
	simpleDockRight = []string{DockComponentNotifications, DockComponentButtons, DockComponentClock, DockComponentSessionControls}
)

// applyProfile fills the defaults of cfg.Profile that data leaves out.
func applyProfile(cfg *UserConfig, data []byte) {
	if cfg.Profile != ProfileSimple {
		return
	}
	raw, err := parseLayer(data)
	if err != nil {
		return
	}
	unset := func(path ...string) bool {
		_, ok := lookupPath(raw, path)
		return !ok
	}
	if cfg.Keybindings.WindowMode == nil {
		off := false
		cfg.Keybindings.WindowMode = &off
	}
	if unset("appearance", "dockbar_position") {
		cfg.Appearance.DockbarPosition = "bottom"
	}
	if cfg.Appearance.RightClickOpensMenu == nil {
		on := true
		cfg.Appearance.RightClickOpensMenu = &on
	}
	// New panes tile, so a pane never hides behind another one.
	if unset("startup", "tiled") {
		cfg.Startup.Tiled = true
	}
	if unset("appearance", "window_button_style") {
		cfg.Appearance.WindowButtonStyle = "pill"
	}
	if unset("appearance", "window_button_position") {
		cfg.Appearance.WindowButtonPosition = "right"
	}
	if cfg.Dock.Left == nil {
		l := slices.Clone(simpleDockLeft)
		cfg.Dock.Left = &l
	}
	if cfg.Dock.Right == nil {
		r := slices.Clone(simpleDockRight)
		cfg.Dock.Right = &r
	}
}

// Simple reports whether cfg uses the Simple profile.
func (c *UserConfig) Simple() bool { return c != nil && c.Profile == ProfileSimple }

// validateProfile warns about a profile tuios does not know. It reads as
// keyboard.
func validateProfile(cfg *UserConfig, result *ValidationResult) {
	if cfg.Profile == "" || slices.Contains(Profiles, cfg.Profile) {
		return
	}
	result.Warnings = append(result.Warnings, ValidationError{Field: "profile",
		Message: fmt.Sprintf("The profile %q is not known. Use simple or keyboard. tuios uses keyboard.", cfg.Profile)})
}
