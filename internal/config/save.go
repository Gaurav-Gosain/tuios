package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adrg/xdg"
	"github.com/pelletier/go-toml/v2"
)

// configFileHeader is the comment block written at the top of a generated
// config file. Kept as a constant so createDefaultConfig and SaveUserConfig
// produce identical, well-documented files.
func configFileHeader(configPath string) string {
	var sb strings.Builder
	sb.WriteString("# TUIOS Configuration File\n")
	sb.WriteString("# This file allows you to customize appearance and keybindings\n")
	sb.WriteString("#\n")
	sb.WriteString("# Configuration location: " + configPath + "\n")
	sb.WriteString("# Documentation: https://github.com/Gaurav-Gosain/tuios\n")
	sb.WriteString("# For keybindings documentation, run: tuios keybinds list\n\n")

	sb.WriteString("# ============================================================================\n")
	sb.WriteString("# APPEARANCE SETTINGS\n")
	sb.WriteString("# ============================================================================\n")
	sb.WriteString("# Many of these can be changed live from the in-app settings page\n")
	sb.WriteString("# (open it with the leader key followed by ',').\n")
	sb.WriteString("#\n")
	sb.WriteString("# border_style: rounded, normal, thick, double, hidden, block, ascii,\n")
	sb.WriteString("#               outer-half-block, inner-half-block\n")
	sb.WriteString("# dockbar_position: bottom, top, hidden\n")
	sb.WriteString("# window_title_position: bottom, top, hidden\n")
	sb.WriteString("# theme: color theme name (e.g. dracula, nord); empty for terminal colors\n")
	sb.WriteString("# click_to_type: single (a click on a pane starts typing in it), double\n")
	sb.WriteString("#               (two clicks do), off (a click only focuses)\n")
	sb.WriteString("# [appearance.scrollbar]: style = thin, track; thumb/track = a one-cell glyph\n")
	sb.WriteString("#               (track also takes \"none\"); tint = border, muted, #RRGGBB\n")
	sb.WriteString("# ============================================================================\n\n")

	sb.WriteString("# ============================================================================\n")
	sb.WriteString("# KEYBINDINGS\n")
	sb.WriteString("# ============================================================================\n")
	sb.WriteString("# Set an action to [] to unbind it and hand the key back to the shell.\n")
	sb.WriteString("#\n")
	sb.WriteString("# [keybindings.terminal_mode] binds alt+arrows to move focus between panes.\n")
	sb.WriteString("# In readline, fish and zsh, alt+left and alt+right move the cursor a word\n")
	sb.WriteString("# at a time. If you want those back:\n")
	sb.WriteString("#\n")
	sb.WriteString("#   [keybindings.terminal_mode]\n")
	sb.WriteString("#   terminal_focus_left = []\n")
	sb.WriteString("#   terminal_focus_right = []\n")
	sb.WriteString("#\n")
	sb.WriteString("# alt+up and alt+down are unclaimed by the common shells, so they are the\n")
	sb.WriteString("# safer pair to keep.\n")
	sb.WriteString("#\n")
	sb.WriteString("# hold_window_mode binds a key that puts tuios in window-management mode for\n")
	sb.WriteString("# as long as it is physically held, and hands the previous mode back when it\n")
	sb.WriteString("# is let go:\n")
	sb.WriteString("#\n")
	sb.WriteString("#   [keybindings.mode_control]\n")
	sb.WriteString("#   hold_window_mode = [\"leftalt\"]\n")
	sb.WriteString("#\n")
	sb.WriteString("# It needs a terminal that speaks the Kitty keyboard protocol (Ghostty, kitty,\n")
	sb.WriteString("# WezTerm, foot, Alacritty; not Terminal.app), because nothing else reports\n")
	sb.WriteString("# that a key was released. Naming a modifier key (leftalt, rightalt, leftctrl,\n")
	sb.WriteString("# leftsuper) asks the terminal for one more thing on top: every keystroke in\n")
	sb.WriteString("# the session then arrives as an escape code. Any ordinary key (f13, scroll\n")
	sb.WriteString("# lock, a spare letter) avoids that. Unbound by default.\n")
	sb.WriteString("# ============================================================================\n\n")
	return sb.String()
}

// WriteConfigFile marshals cfg to TOML (with the documented header) and writes
// it to configPath, creating the parent directory as needed.
func WriteConfigFile(cfg *UserConfig, configPath string) error {
	if err := os.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(configFileHeader(configPath))
	if _, err := sb.Write(data); err != nil {
		return fmt.Errorf("failed to write config data: %w", err)
	}

	if err := os.WriteFile(configPath, []byte(sb.String()), 0o600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	return nil
}

// SaveUserConfig persists cfg to the user's config file at the standard XDG
// location. Used by the in-app settings page to make live changes durable.
func SaveUserConfig(cfg *UserConfig) error {
	configPath, err := xdg.ConfigFile("tuios/config.toml")
	if err != nil {
		return fmt.Errorf("failed to resolve config path: %w", err)
	}
	return WriteConfigFile(cfg, configPath)
}
