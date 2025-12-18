package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/adrg/xdg"
	"github.com/pelletier/go-toml/v2"
)

// UserConfig represents the user's custom configuration
type UserConfig struct {
	Appearance  AppearanceConfig  `toml:"appearance"`
	Keybindings KeybindingsConfig `toml:"keybindings"`
	Daemon      DaemonConfig      `toml:"daemon"`
}

// DaemonConfig holds daemon-related settings
type DaemonConfig struct {
	LogLevel     string `toml:"log_level"`     // Debug log level: off, errors, basic, messages, verbose, trace (default: off)
	DefaultCodec string `toml:"default_codec"` // Default protocol codec: gob, json (default: gob)
	SocketPath   string `toml:"socket_path"`   // Custom socket path (default: $XDG_RUNTIME_DIR/tuios/daemon.sock)
}

// AppearanceConfig holds appearance-related settings
type AppearanceConfig struct {
	BorderStyle       string `toml:"border_style"`        // Border style: rounded, normal, thick, double, hidden, block, ascii, outer-half-block, inner-half-block (borderless mode not yet implemented)
	HideWindowButtons bool   `toml:"hide_window_buttons"` // Hide window control buttons (minimize, maximize, close)
	ScrollbackLines   int    `toml:"scrollback_lines"`    // Number of lines to keep in scrollback buffer (default: 10000, min: 100, max: 1000000)
	DockbarPosition   string `toml:"dockbar_position"`    // Dockbar position: bottom, top, hidden
	PreferredShell    string `toml:"preferred_shell"`     // Preferred shell: if empty, auto-detect based on platform.
	AnimationsEnabled *bool  `toml:"animations_enabled"`  // Enable UI animations (default: true). Set to false for instant transitions.
	WhichKeyEnabled   *bool  `toml:"whichkey_enabled"`    // Show which-key popup after pressing leader key (default: true)
	WhichKeyPosition  string `toml:"whichkey_position"`   // Which-key popup position: bottom-right, bottom-left, top-right, top-left, center (default: bottom-right)
}

// KeybindingsConfig holds all keybinding configurations
type KeybindingsConfig struct {
	LeaderKey        string              `toml:"leader_key"` // Leader key for prefix commands (default: ctrl+b)
	WindowManagement map[string][]string `toml:"window_management"`
	Workspaces       map[string][]string `toml:"workspaces"`
	Layout           map[string][]string `toml:"layout"`
	ModeControl      map[string][]string `toml:"mode_control"`
	System           map[string][]string `toml:"system"`
	Navigation       map[string][]string `toml:"navigation"`
	RestoreMinimized map[string][]string `toml:"restore_minimized"`
	PrefixMode       map[string][]string `toml:"prefix_mode"`
	WindowPrefix     map[string][]string `toml:"window_prefix"`
	MinimizePrefix   map[string][]string `toml:"minimize_prefix"`
	WorkspacePrefix  map[string][]string `toml:"workspace_prefix"`
	DebugPrefix      map[string][]string `toml:"debug_prefix"`
	TapePrefix       map[string][]string `toml:"tape_prefix"`
}

// DefaultConfig returns the default configuration
func DefaultConfig() *UserConfig {
	cfg := &UserConfig{
		Appearance: AppearanceConfig{
			BorderStyle:       "rounded",
			HideWindowButtons: false,
			ScrollbackLines:   10000,
			DockbarPosition:   "bottom",
			PreferredShell:    "",
		},
		Daemon: DaemonConfig{
			LogLevel:     "off",
			DefaultCodec: "gob",
			SocketPath:   "", // Empty means use default XDG path
		},
		Keybindings: KeybindingsConfig{
			LeaderKey: "ctrl+b",
			WindowManagement: map[string][]string{
				"new_window":      {"n"},
				"close_window":    {"w", "x"},
				"rename_window":   {"r"},
				"minimize_window": {"m"},
				"restore_all":     {"M"},
				"next_window":     {"tab"},
				"prev_window":     {"shift+tab"},
				"select_window_1": {"1"},
				"select_window_2": {"2"},
				"select_window_3": {"3"},
				"select_window_4": {"4"},
				"select_window_5": {"5"},
				"select_window_6": {"6"},
				"select_window_7": {"7"},
				"select_window_8": {"8"},
				"select_window_9": {"9"},
			},
			Workspaces: getDefaultWorkspaceKeybinds(),
			Layout: map[string][]string{
				"snap_left":                 {"h"},
				"snap_right":                {"l"},
				"snap_fullscreen":           {"f"},
				"unsnap":                    {"u"},
				"snap_corner_1":             {"1"},
				"snap_corner_2":             {"2"},
				"snap_corner_3":             {"3"},
				"snap_corner_4":             {"4"},
				"toggle_tiling":             {"t"},
				"swap_left":                 {"H", "ctrl+left"},
				"swap_right":                {"L", "ctrl+right"},
				"swap_up":                   {"K", "ctrl+up"},
				"swap_down":                 {"J", "ctrl+down"},
				"resize_master_shrink":      {"<", "shift+,"},
				"resize_master_grow":        {">", "shift+."},
				"resize_height_shrink":      {"{", "shift+["},
				"resize_height_grow":        {"}", "shift+]"},
				"resize_master_shrink_left": {","},
				"resize_master_grow_left":   {"."},
				"resize_height_shrink_top":  {"["},
				"resize_height_grow_top":    {"]"},
				// BSP tiling
				"split_horizontal": {"-"},
				"split_vertical":   {"|", "\\"},
				"rotate_split":     {"R"},
				"equalize_splits":  {"="},
				"preselect_left":   {"alt+h"},
				"preselect_right":  {"alt+l"},
				"preselect_up":     {"alt+k"},
				"preselect_down":   {"alt+j"},
			},
			ModeControl: map[string][]string{
				"enter_terminal_mode": {"i", "enter"},
				"enter_window_mode":   {"esc"},
				"toggle_help":         {"?"},
				"quit":                {"q"},
			},
			System: map[string][]string{
				// Debug commands (logs, cache stats) are accessed via Ctrl+B D submenu
				// and are not directly configurable as keybindings
			},
			Navigation: map[string][]string{
				"nav_up":       {"up"},
				"nav_down":     {"down"},
				"nav_left":     {"left"},
				"nav_right":    {"right"},
				"extend_up":    {"shift+up"},
				"extend_down":  {"shift+down"},
				"extend_left":  {"shift+left"},
				"extend_right": {"shift+right"},
			},
			RestoreMinimized: map[string][]string{
				"restore_minimized_1": {"shift+1", "!"},
				"restore_minimized_2": {"shift+2", "@"},
				"restore_minimized_3": {"shift+3", "#"},
				"restore_minimized_4": {"shift+4", "$"},
				"restore_minimized_5": {"shift+5", "%"},
				"restore_minimized_6": {"shift+6", "^"},
				"restore_minimized_7": {"shift+7", "&"},
				"restore_minimized_8": {"shift+8", "*"},
				"restore_minimized_9": {"shift+9", "("},
			},
			PrefixMode: map[string][]string{
				"prefix_new_window":       {"c"},
				"prefix_close_window":     {"x"},
				"prefix_rename_window":    {",", "r"},
				"prefix_next_window":      {"n", "tab"},
				"prefix_prev_window":      {"p", "shift+tab"},
				"prefix_select_0":         {"0"},
				"prefix_select_1":         {"1"},
				"prefix_select_2":         {"2"},
				"prefix_select_3":         {"3"},
				"prefix_select_4":         {"4"},
				"prefix_select_5":         {"5"},
				"prefix_select_6":         {"6"},
				"prefix_select_7":         {"7"},
				"prefix_select_8":         {"8"},
				"prefix_select_9":         {"9"},
				"prefix_toggle_tiling":    {"space"},
				"prefix_workspace":        {"w"},
				"prefix_minimize":         {"m"},
				"prefix_window":           {"t"},
				"prefix_detach":           {"d", "esc"},
				"prefix_selection":        {"["},
				"prefix_help":             {"?"},
				"prefix_debug":            {"D"},
				"prefix_tape":             {"T"},
				"prefix_quit":             {"q"},
				"prefix_fullscreen":       {"z"},
				"prefix_split_horizontal": {"-"},
				"prefix_split_vertical":   {"|", "\\"},
				"prefix_rotate_split":     {"R"},
				"prefix_equalize_splits":  {"="},
			},
			WindowPrefix: map[string][]string{
				"window_prefix_new":    {"n"},
				"window_prefix_close":  {"x"},
				"window_prefix_rename": {"r"},
				"window_prefix_next":   {"tab"},
				"window_prefix_prev":   {"shift+tab"},
				"window_prefix_tiling": {"t"},
				"window_prefix_cancel": {"esc"},
			},
			MinimizePrefix: map[string][]string{
				"minimize_prefix_focused":     {"m"},
				"minimize_prefix_restore_1":   {"1"},
				"minimize_prefix_restore_2":   {"2"},
				"minimize_prefix_restore_3":   {"3"},
				"minimize_prefix_restore_4":   {"4"},
				"minimize_prefix_restore_5":   {"5"},
				"minimize_prefix_restore_6":   {"6"},
				"minimize_prefix_restore_7":   {"7"},
				"minimize_prefix_restore_8":   {"8"},
				"minimize_prefix_restore_9":   {"9"},
				"minimize_prefix_restore_all": {"M"},
				"minimize_prefix_cancel":      {"esc"},
			},
			WorkspacePrefix: map[string][]string{
				"workspace_prefix_switch_1": {"1"},
				"workspace_prefix_switch_2": {"2"},
				"workspace_prefix_switch_3": {"3"},
				"workspace_prefix_switch_4": {"4"},
				"workspace_prefix_switch_5": {"5"},
				"workspace_prefix_switch_6": {"6"},
				"workspace_prefix_switch_7": {"7"},
				"workspace_prefix_switch_8": {"8"},
				"workspace_prefix_switch_9": {"9"},
				"workspace_prefix_move_1":   {"!"},
				"workspace_prefix_move_2":   {"@"},
				"workspace_prefix_move_3":   {"#"},
				"workspace_prefix_move_4":   {"$"},
				"workspace_prefix_move_5":   {"%"},
				"workspace_prefix_move_6":   {"^"},
				"workspace_prefix_move_7":   {"&"},
				"workspace_prefix_move_8":   {"*"},
				"workspace_prefix_move_9":   {"("},
				"workspace_prefix_cancel":   {"esc"},
			},
			DebugPrefix: map[string][]string{
				"debug_prefix_logs":       {"l"},
				"debug_prefix_cache":      {"c"},
				"debug_prefix_animations": {"a"},
				"debug_prefix_cancel":     {"esc"},
			},
			TapePrefix: map[string][]string{
				"tape_prefix_manager": {"m"},
				"tape_prefix_record":  {"r"},
				"tape_prefix_stop":    {"s"},
				"tape_prefix_cancel":  {"esc"},
			},
		},
	}
	return cfg
}

// getDefaultWorkspaceKeybinds returns platform-specific workspace keybindings
func getDefaultWorkspaceKeybinds() map[string][]string {
	// On macOS, use opt+N (which expands to alt+N and unicode via normalization)
	// On Linux/other, use alt+N
	var base map[string][]string

	if isMacOS() {
		// macOS users think in terms of Option key
		// The KeyNormalizer will expand opt+1 → [opt+1, alt+1, ¡]
		base = map[string][]string{
			"switch_workspace_1": {"opt+1"},
			"switch_workspace_2": {"opt+2"},
			"switch_workspace_3": {"opt+3"},
			"switch_workspace_4": {"opt+4"},
			"switch_workspace_5": {"opt+5"},
			"switch_workspace_6": {"opt+6"},
			"switch_workspace_7": {"opt+7"},
			"switch_workspace_8": {"opt+8"},
			"switch_workspace_9": {"opt+9"},
			"move_and_follow_1":  {"opt+shift+1"},
			"move_and_follow_2":  {"opt+shift+2"},
			"move_and_follow_3":  {"opt+shift+3"},
			"move_and_follow_4":  {"opt+shift+4"},
			"move_and_follow_5":  {"opt+shift+5"},
			"move_and_follow_6":  {"opt+shift+6"},
			"move_and_follow_7":  {"opt+shift+7"},
			"move_and_follow_8":  {"opt+shift+8"},
			"move_and_follow_9":  {"opt+shift+9"},
		}
	} else {
		// Linux and other platforms use alt
		base = map[string][]string{
			"switch_workspace_1": {"alt+1"},
			"switch_workspace_2": {"alt+2"},
			"switch_workspace_3": {"alt+3"},
			"switch_workspace_4": {"alt+4"},
			"switch_workspace_5": {"alt+5"},
			"switch_workspace_6": {"alt+6"},
			"switch_workspace_7": {"alt+7"},
			"switch_workspace_8": {"alt+8"},
			"switch_workspace_9": {"alt+9"},
			"move_and_follow_1":  {"alt+shift+1"},
			"move_and_follow_2":  {"alt+shift+2"},
			"move_and_follow_3":  {"alt+shift+3"},
			"move_and_follow_4":  {"alt+shift+4"},
			"move_and_follow_5":  {"alt+shift+5"},
			"move_and_follow_6":  {"alt+shift+6"},
			"move_and_follow_7":  {"alt+shift+7"},
			"move_and_follow_8":  {"alt+shift+8"},
			"move_and_follow_9":  {"alt+shift+9"},
		}
	}

	return base
}

// isMacOS detects if the current platform is macOS
func isMacOS() bool {
	// Check runtime.GOOS first (most reliable)
	if runtime.GOOS == "darwin" {
		return true
	}
	// Fallback to environment variables
	return strings.Contains(strings.ToLower(os.Getenv("GOOS")), "darwin") ||
		strings.Contains(strings.ToLower(os.Getenv("OSTYPE")), "darwin")
}

// LoadUserConfig loads the user configuration from XDG config directory
func LoadUserConfig() (*UserConfig, error) {
	// Try to find existing config file
	configPath, err := xdg.SearchConfigFile("tuios/config.toml")
	if err != nil {
		// Config doesn't exist, create default
		return createDefaultConfig()
	}

	// Read and parse config file
	// #nosec G304 - configPath is from XDG search, reading user config is intentional
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg UserConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Validate and fill in missing sections with defaults
	defaultCfg := DefaultConfig()
	fillMissingAppearance(&cfg, defaultCfg)
	fillMissingDaemon(&cfg, defaultCfg)
	fillMissingKeybinds(&cfg, defaultCfg)

	// Validate configuration
	validation := ValidateConfig(&cfg)
	if validation.HasErrors() {
		// Log all errors
		for _, err := range validation.Errors {
			fmt.Fprintf(os.Stderr, "Config error in [%s]: %s - %s\n", err.Field, err.Key, err.Message)
		}
		return nil, fmt.Errorf("configuration has %d error(s), please fix and restart", len(validation.Errors))
	}

	// Log warnings (non-fatal)
	if validation.HasWarnings() {
		for _, warn := range validation.Warnings {
			fmt.Fprintf(os.Stderr, "Config warning in [%s]: %s - %s\n", warn.Field, warn.Key, warn.Message)
		}
	}

	return &cfg, nil
}

// createDefaultConfig creates a default config file in the user's config directory
func createDefaultConfig() (*UserConfig, error) {
	cfg := DefaultConfig()

	// Get config file path
	configPath, err := xdg.ConfigFile("tuios/config.toml")
	if err != nil {
		return nil, fmt.Errorf("failed to get config path: %w", err)
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(configPath), 0750); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	// Marshal config to TOML
	data, err := toml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config: %w", err)
	}

	// Build config file with header comments and marshaled data
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
	sb.WriteString("# border_style: Window border style\n")
	sb.WriteString("#   Options: rounded, normal, thick, double, hidden, block, ascii,\n")
	sb.WriteString("#            outer-half-block, inner-half-block\n")
	sb.WriteString("#   Default: rounded\n")
	sb.WriteString("#\n")
	sb.WriteString("# dockbar_position: Position of the dockbar\n")
	sb.WriteString("#   Options: bottom, top, hidden\n")
	sb.WriteString("#   Default: bottom\n")
	sb.WriteString("#\n")
	sb.WriteString("# hide_window_buttons: Hide window control buttons (minimize, maximize, close)\n")
	sb.WriteString("#   Options: true, false\n")
	sb.WriteString("#   Default: false\n")
	sb.WriteString("#\n")
	sb.WriteString("# scrollback_lines: Number of lines to keep in scrollback buffer\n")
	sb.WriteString("#   Range: 100 to 1000000\n")
	sb.WriteString("#   Default: 10000\n")
	sb.WriteString("# ============================================================================\n\n")

	if _, err := sb.Write(data); err != nil {
		return nil, fmt.Errorf("failed to write config data: %w", err)
	}

	// Write to file
	if err := os.WriteFile(configPath, []byte(sb.String()), 0600); err != nil {
		return nil, fmt.Errorf("failed to write config file: %w", err)
	}

	return cfg, nil
}

// fillMissingAppearance fills in any missing appearance settings with defaults
func fillMissingAppearance(cfg, defaultCfg *UserConfig) {
	if cfg.Appearance.BorderStyle == "" {
		cfg.Appearance.BorderStyle = defaultCfg.Appearance.BorderStyle
	}

	if cfg.Appearance.DockbarPosition == "" {
		cfg.Appearance.DockbarPosition = defaultCfg.Appearance.DockbarPosition
	}

	// Note: HideWindowButtons defaults to false (zero value)
	// In borderless mode, buttons are hidden automatically regardless of this setting

	// Validate and set scrollback lines (min: 100, max: 1000000)
	if cfg.Appearance.ScrollbackLines <= 0 {
		cfg.Appearance.ScrollbackLines = defaultCfg.Appearance.ScrollbackLines
	} else if cfg.Appearance.ScrollbackLines < 100 {
		cfg.Appearance.ScrollbackLines = 100
	} else if cfg.Appearance.ScrollbackLines > 1000000 {
		cfg.Appearance.ScrollbackLines = 1000000
	}

	// AnimationsEnabled defaults to true (nil means use default)
	// Only set global if explicitly configured
	if cfg.Appearance.AnimationsEnabled != nil {
		AnimationsEnabled = *cfg.Appearance.AnimationsEnabled
	}

	// WhichKeyEnabled defaults to true (nil means use default)
	if cfg.Appearance.WhichKeyEnabled != nil {
		WhichKeyEnabled = *cfg.Appearance.WhichKeyEnabled
	}

	// WhichKeyPosition defaults to bottom-right
	if cfg.Appearance.WhichKeyPosition != "" {
		WhichKeyPosition = cfg.Appearance.WhichKeyPosition
	}
}

// fillMissingDaemon fills in any missing daemon settings with defaults
func fillMissingDaemon(cfg, defaultCfg *UserConfig) {
	if cfg.Daemon.LogLevel == "" {
		cfg.Daemon.LogLevel = defaultCfg.Daemon.LogLevel
	}
	if cfg.Daemon.DefaultCodec == "" {
		cfg.Daemon.DefaultCodec = defaultCfg.Daemon.DefaultCodec
	}
	// SocketPath defaults to empty (use XDG default), so we don't override it
}

// fillMissingKeybinds fills in any missing keybindings with defaults
func fillMissingKeybinds(cfg, defaultCfg *UserConfig) {
	// Initialize nil maps
	if cfg.Keybindings.WindowManagement == nil {
		cfg.Keybindings.WindowManagement = make(map[string][]string)
	}
	if cfg.Keybindings.Workspaces == nil {
		cfg.Keybindings.Workspaces = make(map[string][]string)
	}
	if cfg.Keybindings.Layout == nil {
		cfg.Keybindings.Layout = make(map[string][]string)
	}
	if cfg.Keybindings.ModeControl == nil {
		cfg.Keybindings.ModeControl = make(map[string][]string)
	}
	if cfg.Keybindings.System == nil {
		cfg.Keybindings.System = make(map[string][]string)
	}
	if cfg.Keybindings.Navigation == nil {
		cfg.Keybindings.Navigation = make(map[string][]string)
	}
	if cfg.Keybindings.RestoreMinimized == nil {
		cfg.Keybindings.RestoreMinimized = make(map[string][]string)
	}
	if cfg.Keybindings.PrefixMode == nil {
		cfg.Keybindings.PrefixMode = make(map[string][]string)
	}
	if cfg.Keybindings.WindowPrefix == nil {
		cfg.Keybindings.WindowPrefix = make(map[string][]string)
	}
	if cfg.Keybindings.MinimizePrefix == nil {
		cfg.Keybindings.MinimizePrefix = make(map[string][]string)
	}
	if cfg.Keybindings.WorkspacePrefix == nil {
		cfg.Keybindings.WorkspacePrefix = make(map[string][]string)
	}
	if cfg.Keybindings.DebugPrefix == nil {
		cfg.Keybindings.DebugPrefix = make(map[string][]string)
	}
	if cfg.Keybindings.TapePrefix == nil {
		cfg.Keybindings.TapePrefix = make(map[string][]string)
	}

	// Set default leader key if not specified
	if cfg.Keybindings.LeaderKey == "" {
		cfg.Keybindings.LeaderKey = defaultCfg.Keybindings.LeaderKey
	}

	// Fill in missing keys with defaults
	fillMapDefaults(cfg.Keybindings.WindowManagement, defaultCfg.Keybindings.WindowManagement)
	fillMapDefaults(cfg.Keybindings.Workspaces, defaultCfg.Keybindings.Workspaces)
	fillMapDefaults(cfg.Keybindings.Layout, defaultCfg.Keybindings.Layout)
	fillMapDefaults(cfg.Keybindings.ModeControl, defaultCfg.Keybindings.ModeControl)
	fillMapDefaults(cfg.Keybindings.System, defaultCfg.Keybindings.System)
	fillMapDefaults(cfg.Keybindings.Navigation, defaultCfg.Keybindings.Navigation)
	fillMapDefaults(cfg.Keybindings.RestoreMinimized, defaultCfg.Keybindings.RestoreMinimized)
	fillMapDefaults(cfg.Keybindings.PrefixMode, defaultCfg.Keybindings.PrefixMode)
	fillMapDefaults(cfg.Keybindings.WindowPrefix, defaultCfg.Keybindings.WindowPrefix)
	fillMapDefaults(cfg.Keybindings.MinimizePrefix, defaultCfg.Keybindings.MinimizePrefix)
	fillMapDefaults(cfg.Keybindings.WorkspacePrefix, defaultCfg.Keybindings.WorkspacePrefix)
	fillMapDefaults(cfg.Keybindings.DebugPrefix, defaultCfg.Keybindings.DebugPrefix)
	fillMapDefaults(cfg.Keybindings.TapePrefix, defaultCfg.Keybindings.TapePrefix)
}

func fillMapDefaults(target, defaults map[string][]string) {
	for k, v := range defaults {
		if _, exists := target[k]; !exists {
			target[k] = v
		}
	}
}

// GetConfigPath returns the path to the config file
func GetConfigPath() (string, error) {
	path, err := xdg.SearchConfigFile("tuios/config.toml")
	if err != nil {
		// Return where it would be created
		return xdg.ConfigFile("tuios/config.toml")
	}
	return path, nil
}
