package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/adrg/xdg"
	"github.com/pelletier/go-toml/v2"
)

// ConfigFileHeader is the comment block written at the top of a generated
// config file. Exported so `tuios config reset` writes the same guidance a
// first-run config gets; it used to keep its own shorter copy, so resetting
// quietly threw away the notes on which keys cost you what.
func ConfigFileHeader(configPath string) string {
	return configFileHeader(configPath)
}

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
	sb.WriteString("# window_button_style: pill, dots (macOS traffic lights)\n")
	sb.WriteString("# window_button_position: right, left. Independent of the style; set both\n")
	sb.WriteString("#               to dots and left for the full macOS arrangement\n")
	sb.WriteString("# theme: color theme name (e.g. dracula, nord); empty for terminal colors\n")
	sb.WriteString("# click_to_type: single (a click on a pane starts typing in it), double\n")
	sb.WriteString("#               (two clicks do), off (a click only focuses)\n")
	sb.WriteString("# auto_enter_terminal_on_focus: true (a keyboard focus command that\n")
	sb.WriteString("#               actually moves focus also enters terminal mode), false\n")
	sb.WriteString("#               (focus stays in window-management mode)\n")
	sb.WriteString("# [appearance.scrollbar]: style = thin, track; thumb/track = a one-cell glyph\n")
	sb.WriteString("#               (track also takes \"none\"); tint = border, muted, #RRGGBB\n")
	sb.WriteString("# ============================================================================\n\n")

	sb.WriteString("# ============================================================================\n")
	sb.WriteString("# KEYBINDINGS\n")
	sb.WriteString("# ============================================================================\n")
	sb.WriteString("# Set an action to [] to unbind it and hand the key back to the shell.\n")
	sb.WriteString("# An empty list and a missing line are not the same thing. A line this file\n")
	sb.WriteString("# does not have gets its default back the next time tuios starts; an action\n")
	sb.WriteString("# set to [] stays empty.\n")
	sb.WriteString("#\n")
	sb.WriteString("# You do not have to edit this by hand. In tuios, open the keybind manager\n")
	sb.WriteString("# (leader then k, or the command palette) and press ctrl+d on a binding to\n")
	sb.WriteString("# remove it, or ctrl+x to take its key off every action. From a shell:\n")
	sb.WriteString("#\n")
	sb.WriteString("#   tuios keybinds unbind close_window w   # one key off one action\n")
	sb.WriteString("#   tuios keybinds free alt+left           # off every action\n")
	sb.WriteString("#\n")
	sb.WriteString("# `tuios keybinds doctor` says which binding in each scope is live, what\n")
	sb.WriteString("# clashes with what, and which keys never reach the program in the pane.\n")
	sb.WriteString("#\n")
	sb.WriteString("# [keybindings.global] acts in window mode and terminal mode alike. It binds\n")
	sb.WriteString("# ctrl+p to the command palette and alt+space to the launcher, which costs\n")
	sb.WriteString("# you fish's history-back and readline's set-mark. To move or drop either:\n")
	sb.WriteString("#\n")
	sb.WriteString("#   [keybindings.global]\n")
	sb.WriteString("#   command_palette = [\"ctrl+shift+p\"]\n")
	sb.WriteString("#   launcher = []\n")
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

	sb.WriteString("# ============================================================================\n")
	sb.WriteString("# DOCK COMPONENTS\n")
	sb.WriteString("# ============================================================================\n")
	sb.WriteString("# The dock is three ordered lists of component names. Leave [dock] out\n")
	sb.WriteString("# entirely and the bar draws what it always drew; write the lists to\n")
	sb.WriteString("# reorder it, and omit a name to drop that segment.\n")
	sb.WriteString("#\n")
	sb.WriteString("#   [dock]\n")
	sb.WriteString("#   left   = [\"mode\", \"workspaces\", \"trail\", \"tape\"]\n")
	sb.WriteString("#   center = [\"windows\"]\n")
	sb.WriteString("#   right  = [\"notifications\", \"copy-help\", \"cpu\", \"ram\", \"clock\", \"session-controls\"]\n")
	sb.WriteString("#\n")
	sb.WriteString("# A cell of your own is a command whose first line of stdout is the text:\n")
	sb.WriteString("#\n")
	sb.WriteString("#   [dock.custom.branch]\n")
	sb.WriteString("#   command = \"git branch --show-current\"\n")
	sb.WriteString("#   refresh = \"event:after-focus-change\"   # or once, push, or \"30s\"\n")
	sb.WriteString("#\n")
	sb.WriteString("# and then \"custom/branch\" in one of the lists above. A component that\n")
	sb.WriteString("# fails is hidden rather than left showing a stale value;\n")
	sb.WriteString("# `tuios list-dock-components` says which and why.\n")
	sb.WriteString("# ============================================================================\n\n")
	return sb.String()
}

// renderConfigFile is cfg as the bytes of a config file, header and all. It
// touches memory only, which is what lets a caller that must not block do this
// half of a save itself.
func renderConfigFile(cfg *UserConfig, configPath string) ([]byte, error) {
	data, err := toml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(configFileHeader(configPath))
	if _, err := sb.Write(data); err != nil {
		return nil, fmt.Errorf("failed to write config data: %w", err)
	}
	return []byte(sb.String()), nil
}

// writeConfigBytes puts already-rendered bytes at configPath, creating the
// parent directory as needed.
func writeConfigBytes(data []byte, configPath string) error {
	if err := os.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	return nil
}

// WriteConfigFile marshals cfg to TOML (with the documented header) and writes
// it to configPath, creating the parent directory as needed.
func WriteConfigFile(cfg *UserConfig, configPath string) error {
	data, err := renderConfigFile(cfg, configPath)
	if err != nil {
		return err
	}
	return writeConfigBytes(data, configPath)
}

// saveSeq numbers renders and saveDone the newest one that has landed, so a
// write held up behind another cannot put an older config back.
var (
	saveMu   sync.Mutex
	saveSeq  atomic.Uint64
	saveDone atomic.Uint64
)

// RenderUserConfig reads cfg into the bytes of a config file and hands back the
// function that writes them. The split exists because the caller is the Update
// goroutine: rendering is memory and can happen there, the file write cannot.
//
// Reading cfg here rather than in the returned function is also what makes this
// safe without a deep copy. The config is the model's own and goes on being
// edited; a writer holding the pointer would be marshalling a struct changing
// underneath it.
//
// The returned function is safe to call from anywhere and from several places at
// once. Writes are serialised and stamped, so when two saves are in flight the
// older one gives way rather than overwriting the newer.
func RenderUserConfig(cfg *UserConfig) (func() error, error) {
	configPath, err := xdg.ConfigFile("tuios/config.toml")
	if err != nil {
		return nil, fmt.Errorf("failed to resolve config path: %w", err)
	}
	data, err := renderConfigFile(cfg, configPath)
	if err != nil {
		return nil, err
	}
	gen := saveSeq.Add(1)
	return func() error {
		saveMu.Lock()
		defer saveMu.Unlock()
		if gen < saveDone.Load() {
			return nil
		}
		if err := writeConfigBytes(data, configPath); err != nil {
			return err
		}
		saveDone.Store(gen)
		return nil
	}, nil
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
