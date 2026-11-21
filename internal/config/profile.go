package config

import (
	"fmt"
	"slices"
)

// The profiles.
const (
	ProfileKeyboard = "keyboard"
	ProfileSimple   = "simple"
)

// Profiles are the accepted values of profile.
var Profiles = []string{ProfileKeyboard, ProfileSimple}

var (
	simpleDockLeft  = []string{DockComponentWorkspaces}
	simpleDockRight = []string{DockComponentNotifications, DockComponentButtons, DockComponentClock, DockComponentSessionControls}
)

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

func validateProfile(cfg *UserConfig, result *ValidationResult) {
	if cfg.Profile == "" || slices.Contains(Profiles, cfg.Profile) {
		return
	}
	result.Warnings = append(result.Warnings, ValidationError{Field: "profile",
		Message: fmt.Sprintf("The profile %q is not known. Use simple or keyboard. tuios uses keyboard.", cfg.Profile)})
}
