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
