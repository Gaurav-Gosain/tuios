// Package main implements TUIOS - Terminal UI Operating System.
// TUIOS is a terminal-based window manager that provides a modern interface
// for managing multiple terminal sessions with workspace support, tiling modes,
// and comprehensive keyboard/mouse interactions.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"runtime/pprof"
	"strings"
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/server"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/charmbracelet/fang"
	"github.com/charmbracelet/lipgloss/v2"
	"github.com/charmbracelet/lipgloss/v2/table"
	tint "github.com/lrstanley/bubbletint/v2"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
)

// Version information (set by goreleaser)
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
	builtBy = "unknown"
)

// Global flags
var (
	debugMode         bool
	cpuProfile        string
	asciiOnly         bool
	themeName         string
	listThemes        bool
	previewTheme      string
	borderStyle       string
	dockbarPosition   string
	hideWindowButtons bool
	scrollbackLines   int
	showKeys          bool
)

func main() {
	// Root command
	rootCmd := &cobra.Command{
		Use:   "tuios",
		Short: "Terminal UI Operating System",
		Long: `TUIOS - Terminal UI Operating System

A terminal-based window manager that provides a modern interface for managing
multiple terminal sessions with workspace support, tiling modes, and
comprehensive keyboard/mouse interactions.`,
		Example: `  # Run TUIOS
  tuios

  # Run with debug logging
  tuios --debug

  # Run with ASCII-only mode (no Nerd Font icons)
  tuios --ascii-only

  # Run with CPU profiling
  tuios --cpuprofile cpu.prof

  # Run with a specific theme
  tuios --theme dracula

  # List all available themes
  tuios --list-themes

  # Preview a theme's colors
  tuios --preview-theme dracula

  # Interactively select theme with fzf and preview
  tuios --theme $(tuios --list-themes | fzf --preview 'tuios --preview-theme {}')

  # Run as SSH server
  tuios ssh --port 2222

  # Edit configuration
  tuios config edit

  # List all keybindings
  tuios keybinds list`,
		Version: version,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Handle --preview-theme flag
			if previewTheme != "" {
				return previewThemeColors(previewTheme)
			}

			// Handle --list-themes flag
			if listThemes {
				tint.NewDefaultRegistry()
				themes := tint.TintIDs()
				for _, theme := range themes {
					fmt.Println(theme)
				}
				return nil
			}
			return runLocal()
		},
		SilenceUsage: true,
	}

	// Global flags
	rootCmd.PersistentFlags().BoolVar(&debugMode, "debug", false, "Enable debug logging")
	rootCmd.PersistentFlags().StringVar(&cpuProfile, "cpuprofile", "", "Write CPU profile to file")
	rootCmd.PersistentFlags().BoolVar(&asciiOnly, "ascii-only", false, "Use ASCII characters instead of Nerd Font icons")
	rootCmd.PersistentFlags().StringVar(&themeName, "theme", "", "Color theme to use (e.g., dracula, nord, tokyonight). Leave empty to use standard terminal colors without theming")
	rootCmd.PersistentFlags().BoolVar(&listThemes, "list-themes", false, "List all available themes and exit")
	rootCmd.PersistentFlags().StringVar(&previewTheme, "preview-theme", "", "Preview a theme's 16 ANSI colors")
	rootCmd.PersistentFlags().StringVar(&borderStyle, "border-style", "", "Window border style: rounded, normal, thick, double, hidden, block, ascii, outer-half-block, inner-half-block (default: from config or rounded)")
	rootCmd.PersistentFlags().StringVar(&dockbarPosition, "dockbar-position", "", "Dockbar position: bottom, top, hidden (default: from config or bottom)")
	rootCmd.PersistentFlags().BoolVar(&hideWindowButtons, "hide-window-buttons", false, "Hide window control buttons (minimize, maximize, close)")
	rootCmd.PersistentFlags().IntVar(&scrollbackLines, "scrollback-lines", 0, "Number of lines to keep in scrollback buffer (default: from config or 10000, min: 100, max: 1000000)")
	rootCmd.PersistentFlags().BoolVar(&showKeys, "show-keys", false, "Enable showkeys overlay to display pressed keys")

	// SSH command variables
	var sshPort, sshHost, sshKeyPath string

	sshCmd := &cobra.Command{
		Use:   "ssh",
		Short: "Run TUIOS as SSH server",
		Long: `Run TUIOS as an SSH server

Allows remote connections to TUIOS via SSH. The server will generate
a host key automatically if not specified.`,
		Example: `  # Start SSH server on default port
  tuios ssh

  # Start on custom port
  tuios ssh --port 2222

  # Specify custom host key
  tuios ssh --key-path /path/to/host_key`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSSHServer(sshHost, sshPort, sshKeyPath)
		},
	}

	sshCmd.Flags().StringVar(&sshPort, "port", "2222", "SSH server port")
	sshCmd.Flags().StringVar(&sshHost, "host", "localhost", "SSH server host")
	sshCmd.Flags().StringVar(&sshKeyPath, "key-path", "", "Path to SSH host key (auto-generated if not specified)")

	// Config command group
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Manage TUIOS configuration",
		Long:  `Manage TUIOS configuration file and settings`,
	}

	configPathCmd := &cobra.Command{
		Use:   "path",
		Short: "Print configuration file path",
		Long:  `Print the path to the TUIOS configuration file`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return printConfigPath()
		},
	}

	configEditCmd := &cobra.Command{
		Use:   "edit",
		Short: "Edit configuration in $EDITOR",
		Long: `Open the TUIOS configuration file in your default editor

The editor is determined by checking $EDITOR, $VISUAL, or common editors
like vim, vi, nano, and emacs in that order.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return editConfigFile()
		},
	}

	configResetCmd := &cobra.Command{
		Use:   "reset",
		Short: "Reset configuration to defaults",
		Long: `Reset the TUIOS configuration file to default settings

This will overwrite your existing configuration after confirmation.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return resetConfigToDefaults()
		},
	}

	configCmd.AddCommand(configPathCmd, configEditCmd, configResetCmd)

	// Keybinds command group
	keybindsCmd := &cobra.Command{
		Use:     "keybinds",
		Aliases: []string{"keys", "kb"},
		Short:   "View keybinding configuration",
		Long:    `View and inspect TUIOS keybinding configuration`,
	}

	keybindsListCmd := &cobra.Command{
		Use:   "list",
		Short: "List all keybindings",
		Long:  `Display all configured keybindings in a formatted table`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return listKeybindings()
		},
	}

	keybindsCustomCmd := &cobra.Command{
		Use:   "list-custom",
		Short: "List customized keybindings",
		Long: `Display only keybindings that differ from defaults

Shows a comparison of default and custom keybindings.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return listCustomKeybindings()
		},
	}

	keybindsCmd.AddCommand(keybindsListCmd, keybindsCustomCmd)

	// Add subcommands to root
	rootCmd.AddCommand(sshCmd, configCmd, keybindsCmd)

	// Execute with fang
	if err := fang.Execute(
		context.Background(),
		rootCmd,
		fang.WithVersion(fmt.Sprintf("%s\nCommit: %s\nBuilt: %s\nBy: %s", version, commit, date, builtBy)),
	); err != nil {
		os.Exit(1)
	}
}

// filterMouseMotion filters out redundant mouse motion events to reduce CPU usage
// Only passes through mouse motion during drag/resize operations
func filterMouseMotion(model tea.Model, msg tea.Msg) tea.Msg {
	// Allow all non-motion events through
	if _, ok := msg.(tea.MouseMotionMsg); !ok {
		return msg
	}

	// Type assert to our OS model
	os, ok := model.(*app.OS)
	if !ok {
		return msg
	}

	// Allow motion events during active interactions
	if os.Dragging || os.Resizing {
		return msg
	}

	// Allow motion events during text selection
	if os.SelectionMode {
		focusedWindow := os.GetFocusedWindow()
		if focusedWindow != nil && focusedWindow.IsSelecting {
			return msg
		}
	}

	// Allow motion events when in terminal mode with alt screen apps (vim, htop, etc.)
	if os.Mode == app.TerminalMode {
		focusedWindow := os.GetFocusedWindow()
		if focusedWindow != nil && focusedWindow.IsAltScreen {
			return msg
		}
	}

	// Filter out motion events when not interacting
	return nil
}

func runLocal() error {
	// Handle global flags
	if debugMode {
		_ = os.Setenv("TUIOS_DEBUG_INTERNAL", "1")
		fmt.Println("Debug mode enabled")
	}

	// Set ASCII-only mode if requested
	if asciiOnly {
		config.UseASCIIOnly = true
	}

	// Load user configuration first to get defaults
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		log.Printf("Warning: Failed to load config, using defaults: %v", err)
		userConfig = config.DefaultConfig()
	}

	// Apply appearance settings from config
	if borderStyle == "" {
		// Use config file setting if flag not provided
		config.BorderStyle = userConfig.Appearance.BorderStyle
	} else {
		// CLI flag overrides config
		config.BorderStyle = borderStyle
	}

	if dockbarPosition == "" {
		// Use config file setting if flag not provided
		config.DockbarPosition = userConfig.Appearance.DockbarPosition
	} else {
		// CLI flag overrides config
		config.DockbarPosition = dockbarPosition
	}

	// Apply hide window buttons setting
	// CLI flag OR config setting will hide buttons
	config.HideWindowButtons = hideWindowButtons || userConfig.Appearance.HideWindowButtons

	// Apply scrollback lines setting
	// CLI flag overrides config, with validation
	finalScrollbackLines := userConfig.Appearance.ScrollbackLines
	if scrollbackLines > 0 {
		// CLI flag provided, validate and use it
		if scrollbackLines < 100 {
			finalScrollbackLines = 100
		} else if scrollbackLines > 1000000 {
			finalScrollbackLines = 1000000
		} else {
			finalScrollbackLines = scrollbackLines
		}
	}
	// Store in a variable that will be used when creating windows
	config.ScrollbackLines = finalScrollbackLines

	// Apply leader key setting from config
	if userConfig.Keybindings.LeaderKey != "" {
		config.LeaderKey = userConfig.Keybindings.LeaderKey
	}

	// Start CPU profiling if requested
	if cpuProfile != "" {
		// #nosec G304 - cpuProfile is user-provided flag for profiling, intentional
		f, err := os.Create(cpuProfile)
		if err != nil {
			return fmt.Errorf("could not create CPU profile: %w", err)
		}
		defer func() {
			if closeErr := f.Close(); closeErr != nil {
				log.Printf("Warning: failed to close CPU profile file: %v", closeErr)
			}
		}()

		if err := pprof.StartCPUProfile(f); err != nil {
			return fmt.Errorf("could not start CPU profile: %w", err)
		}
		defer pprof.StopCPUProfile()
	}

	// Set up the input handler to break circular dependency
	app.SetInputHandler(input.HandleInput)

	// Create keybind registry from loaded config
	keybindRegistry := config.NewKeybindRegistry(userConfig)

	// Log config path for user reference
	if debugMode {
		configPath, _ := config.GetConfigPath()
		log.Printf("Configuration: %s", configPath)
	}

	// Initialize theme before starting Bubble Tea
	if err := theme.Initialize(themeName); err != nil {
		log.Printf("Warning: Failed to load theme '%s': %v", themeName, err)
		log.Printf("Falling back to default theme")
	}

	// Start with no windows - user will create the first one
	initialOS := &app.OS{
		FocusedWindow:        -1,                               // No focused window initially
		WindowExitChan:       make(chan string, 10),            // Buffer for window exit signals
		MouseSnapping:        false,                            // Disable mouse snapping by default
		MasterRatio:          0.5,                              // Default 50/50 split for tiling
		CurrentWorkspace:     1,                                // Start on workspace 1
		NumWorkspaces:        9,                                // Support 9 workspaces (1-9)
		WorkspaceFocus:       make(map[int]int),                // Initialize workspace focus memory
		WorkspaceLayouts:     make(map[int][]app.WindowLayout), // Initialize layout storage
		WorkspaceHasCustom:   make(map[int]bool),               // Initialize custom layout tracker
		WorkspaceMasterRatio: make(map[int]float64),            // Initialize per-workspace master ratio
		PendingResizes:       make(map[string][2]int),          // Track pending PTY resizes
		KeybindRegistry:      keybindRegistry,                  // User-configurable keybindings
		ShowKeys:             showKeys,                         // Enable showkeys overlay if flag set
		RecentKeys:           []app.KeyEvent{},                 // Initialize empty key history
		KeyHistoryMaxSize:    5,                                // Default: show last 5 keys
	}

	// Initialize the Bubble Tea program with optimal settings
	// Note: AltScreen, MouseMode, and ReportFocus are now configured in View() method
	// Note: Keyboard enhancements and uniform key layout are automatically negotiated with the terminal
	p := tea.NewProgram(
		initialOS,
		tea.WithFPS(config.NormalFPS),     // Set target FPS
		tea.WithoutSignalHandler(),        // We handle signals ourselves
		tea.WithFilter(filterMouseMotion), // Filter unnecessary mouse motion events
	)

	// Handle shutdown signals for graceful cleanup
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		// Signal received - trigger graceful shutdown
		// Send quit message to the program
		p.Send(tea.QuitMsg{})
	}()

	// Run the program
	finalModel, err := p.Run()

	// Cleanup after program exits (before checking error)
	// This ensures cleanup happens even if p.Run() returns an error
	if finalOS, ok := finalModel.(*app.OS); ok {
		finalOS.Cleanup()
	}

	// Restore terminal to sane state after cleanup
	// This handles cases where bubbletea's cleanup might not be complete
	// Send reset sequence: ESC c (full reset)
	// Also explicitly disable any remaining modes
	fmt.Print("\033c")       // Full terminal reset
	fmt.Print("\033[?1000l") // Disable mouse tracking
	fmt.Print("\033[?1002l") // Disable button event mouse tracking
	fmt.Print("\033[?1003l") // Disable all motion mouse tracking
	fmt.Print("\033[?1004l") // Disable focus reporting
	fmt.Print("\033[?1006l") // Disable SGR mouse mode
	fmt.Print("\033[?25h")   // Show cursor
	fmt.Print("\033[?47l")   // Exit alternate screen (if still active)
	fmt.Print("\033[0m")     // Reset text attributes
	fmt.Print("\r\n")        // Newline to prevent prompt corruption
	_ = os.Stdout.Sync()     // Ensure all output is flushed

	if err != nil {
		return fmt.Errorf("program error: %w", err)
	}

	return nil
}

func runSSHServer(sshHost, sshPort, sshKeyPath string) error {
	// Handle global flags
	if debugMode {
		_ = os.Setenv("TUIOS_DEBUG_INTERNAL", "1")
		fmt.Println("Debug mode enabled")
	}

	// Set ASCII-only mode if requested
	if asciiOnly {
		config.UseASCIIOnly = true
	}

	// Set up the input handler to break circular dependency
	app.SetInputHandler(input.HandleInput)

	// Initialize theme before starting SSH server
	if err := theme.Initialize(themeName); err != nil {
		log.Printf("Warning: Failed to load theme '%s': %v", themeName, err)
		log.Printf("Falling back to default theme")
	}

	// SSH server implementation
	log.Printf("Starting TUIOS SSH server on %s:%s", sshHost, sshPort)

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Println("Shutting down SSH server...")
		cancel()
	}()

	// Start SSH server
	if err := server.StartSSHServer(ctx, sshHost, sshPort, sshKeyPath); err != nil {
		return fmt.Errorf("SSH server error: %w", err)
	}
	return nil
}

// previewThemeColors displays a preview of the theme's 16 ANSI colors
func previewThemeColors(themeName string) error {
	// Initialize theme
	if err := theme.Initialize(themeName); err != nil {
		return fmt.Errorf("failed to initialize theme: %w", err)
	}

	// Get current theme
	currentTheme := theme.Current()
	if currentTheme == nil {
		return fmt.Errorf("theme '%s' not found", themeName)
	}

	// Print theme name
	fmt.Printf("Theme: %s\n\n", themeName)

	// Get ANSI palette
	palette := theme.GetANSIPalette()

	// Color names for the 16 ANSI colors
	colorNames := []string{
		"Black", "Red", "Green", "Yellow",
		"Blue", "Magenta", "Cyan", "White",
		"Bright Black", "Bright Red", "Bright Green", "Bright Yellow",
		"Bright Blue", "Bright Magenta", "Bright Cyan", "Bright White",
	}

	// Print normal colors (0-7)
	fmt.Println("Normal Colors (0-7):")
	for i := range 8 {
		c := palette[i]
		r, g, b, _ := c.RGBA()
		r8, g8, b8 := uint8(r>>8), uint8(g>>8), uint8(b>>8)

		// Use true color (24-bit RGB) escape codes to show the actual theme colors
		fmt.Printf("  \033[48;2;%d;%d;%dm    \033[0m  %-14s #%02x%02x%02x\n", r8, g8, b8, colorNames[i], r8, g8, b8)
	}

	fmt.Println()

	// Print bright colors (8-15)
	fmt.Println("Bright Colors (8-15):")
	for i := 8; i < 16; i++ {
		c := palette[i]
		r, g, b, _ := c.RGBA()
		r8, g8, b8 := uint8(r>>8), uint8(g>>8), uint8(b>>8)

		// Use true color (24-bit RGB) escape codes to show the actual theme colors
		fmt.Printf("  \033[48;2;%d;%d;%dm    \033[0m  %-14s #%02x%02x%02x\n", r8, g8, b8, colorNames[i], r8, g8, b8)
	}

	return nil
}

// printConfigPath prints the config file path
func printConfigPath() error {
	path, err := config.GetConfigPath()
	if err != nil {
		return fmt.Errorf("could not determine config path: %w", err)
	}
	fmt.Println(path)
	return nil
}

// editConfigFile opens the config file in $EDITOR
func editConfigFile() error {
	// Get config path
	configPath, err := config.GetConfigPath()
	if err != nil {
		return fmt.Errorf("could not determine config path: %w", err)
	}

	// Ensure config file exists (create default if needed)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		fmt.Printf("Config file doesn't exist, creating default at: %s\n", configPath)
		_, err := config.LoadUserConfig()
		if err != nil {
			return fmt.Errorf("could not create config file: %w", err)
		}
	}

	// Get editor from environment
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		// Try common editors
		for _, e := range []string{"vim", "vi", "nano", "emacs"} {
			if _, err := exec.LookPath(e); err == nil {
				editor = e
				break
			}
		}
	}
	if editor == "" {
		return fmt.Errorf("no editor found. Please set $EDITOR environment variable")
	}

	// Open editor
	// #nosec G204 - editor is intentionally user-controlled via $EDITOR
	cmd := exec.Command(editor, configPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to open editor: %w", err)
	}
	return nil
}

// resetConfigToDefaults resets the configuration file to default settings
func resetConfigToDefaults() error {
	// Get config path
	configPath, err := config.GetConfigPath()
	if err != nil {
		return fmt.Errorf("could not determine config path: %w", err)
	}

	// Check if config exists and ask for confirmation
	if _, err := os.Stat(configPath); err == nil {
		fmt.Printf("Warning: This will overwrite your existing configuration at:\n")
		fmt.Printf("  %s\n\n", configPath)
		fmt.Printf("Are you sure you want to reset to defaults? (yes/no): ")

		var response string
		_, _ = fmt.Scanln(&response)
		response = strings.ToLower(strings.TrimSpace(response))

		if response != "yes" && response != "y" {
			fmt.Println("Reset cancelled.")
			return nil
		}
	}

	// Create default config
	defaultCfg := config.DefaultConfig()

	// Marshal to TOML with header
	var sb strings.Builder
	sb.WriteString("# TUIOS Configuration File\n")
	sb.WriteString("# This file allows you to customize keybindings\n")
	sb.WriteString("# Edit keybindings by modifying the arrays of keys for each action\n")
	sb.WriteString("# Multiple keys can be bound to the same action\n")
	sb.WriteString("#\n")
	sb.WriteString("# Configuration location: " + configPath + "\n")
	sb.WriteString("# Documentation: https://github.com/Gaurav-Gosain/tuios\n\n")

	data, err := toml.Marshal(defaultCfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if _, err := sb.Write(data); err != nil {
		return fmt.Errorf("failed to write config data: %w", err)
	}

	// Write to file
	if err := os.WriteFile(configPath, []byte(sb.String()), 0o600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	fmt.Printf("Configuration reset to defaults\n")
	fmt.Printf("  Location: %s\n", configPath)
	fmt.Println("\nYou can customize it with: tuios config edit")
	return nil
}

// listKeybindings prints all configured keybindings in a pretty table
func listKeybindings() error {
	// Load user config
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		fmt.Fprintln(os.Stderr, "Using default keybindings...")
		userConfig = config.DefaultConfig()
	}

	// Create keybind registry
	registry := config.NewKeybindRegistry(userConfig)

	// Print keybindings using lipgloss table
	printKeybindingsTable(registry)
	return nil
}

// generateWorkspaceActions generates all workspace switching and move actions (1-9)
func generateWorkspaceActions() []string {
	actions := []string{}
	// Add all 9 workspace switches
	for i := 1; i <= 9; i++ {
		actions = append(actions, fmt.Sprintf("switch_workspace_%d", i))
	}
	// Add all 9 move and follow actions
	for i := 1; i <= 9; i++ {
		actions = append(actions, fmt.Sprintf("move_and_follow_%d", i))
	}
	return actions
}

// printKeybindingsTable prints keybindings in a pretty table format
func printKeybindingsTable(registry *config.KeybindRegistry) {
	// Define table styles
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("12")).
		Padding(0, 1)

	cellStyle := lipgloss.NewStyle().
		Padding(0, 1)

	// Sections to display
	sections := []struct {
		Title   string
		Actions []string
	}{
		{
			Title: "Window Management",
			Actions: []string{
				"new_window", "close_window", "rename_window",
				"minimize_window", "restore_all",
				"next_window", "prev_window",
			},
		},
		{
			Title:   "Workspaces",
			Actions: generateWorkspaceActions(),
		},
		{
			Title: "Layout",
			Actions: []string{
				"snap_left", "snap_right", "snap_fullscreen", "unsnap",
				"toggle_tiling", "swap_left", "swap_right", "swap_up", "swap_down",
			},
		},
		{
			Title: "Modes",
			Actions: []string{
				"enter_terminal_mode", "enter_window_mode",
				"toggle_help", "quit",
			},
		},
		{
			Title: "Selection",
			Actions: []string{
				"toggle_selection", "toggle_selection_term",
				"copy_selection", "paste_clipboard", "clear_selection",
			},
		},
		{
			Title: "System",
			Actions: []string{
				"toggle_logs", "toggle_cache_stats",
			},
		},
	}

	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14")).Render("TUIOS Keybindings"))
	fmt.Println()

	for _, section := range sections {
		// Create table for this section
		rows := [][]string{}

		for _, action := range section.Actions {
			keys := registry.GetKeys(action)
			if len(keys) == 0 {
				continue // Skip unbound actions
			}

			// Get description
			desc := config.ActionDescriptions[action]
			if desc == "" {
				desc = action
			}

			// Format keys
			keysStr := strings.Join(keys, ", ")
			rows = append(rows, []string{keysStr, desc})
		}

		if len(rows) == 0 {
			continue // Skip empty sections
		}

		// Create table with rounded borders
		t := table.New().
			Border(lipgloss.RoundedBorder()).
			BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("8"))).
			Headers("Keys", "Action").
			Rows(rows...).
			StyleFunc(func(row, col int) lipgloss.Style {
				if row == -1 {
					return headerStyle
				}
				return cellStyle
			})

		// Print section
		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11")).Render(section.Title))
		fmt.Println(t.Render())
		fmt.Println()
	}

	// Print note about Ctrl+B prefix
	note := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8")).
		Italic(true).
		Render("Note: Ctrl+B is the prefix key (not configurable). Press Ctrl+B followed by another key for prefix commands.")
	fmt.Println(note)
	fmt.Println()
}

// listCustomKeybindings shows only the keybindings that differ from defaults
func listCustomKeybindings() error {
	// Load user config
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		return fmt.Errorf("error loading config: %w", err)
	}

	// Load default config
	defaultConfig := config.DefaultConfig()

	// Find customizations
	customizations := findCustomizations(userConfig, defaultConfig)

	if len(customizations) == 0 {
		fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("No custom keybindings configured. All keybindings are using defaults."))
		fmt.Println()
		fmt.Println("Run 'tuios keybinds list' to see all keybindings.")
		return nil
	}

	// Define table styles
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("12")).
		Padding(0, 1)

	cellStyle := lipgloss.NewStyle().
		Padding(0, 1)

	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14")).Render("Custom Keybindings"))
	fmt.Println()

	// Build table rows
	rows := [][]string{}
	for _, custom := range customizations {
		rows = append(rows, []string{
			custom.Action,
			custom.DefaultKeys,
			custom.CustomKeys,
		})
	}

	// Create table with rounded borders
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("8"))).
		Headers("Action", "Default", "Custom").
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == -1 {
				return headerStyle
			}
			return cellStyle
		})

	fmt.Println(t.Render())
	fmt.Println()

	note := lipgloss.NewStyle().
		Foreground(lipgloss.Color("11")).
		Render(fmt.Sprintf("Found %d customized keybinding(s)", len(customizations)))
	fmt.Println(note)
	fmt.Println()
	return nil
}

// Customization represents a customized keybinding
type Customization struct {
	Action      string
	DefaultKeys string
	CustomKeys  string
}

// findCustomizations finds all keybindings that differ from defaults
func findCustomizations(userCfg, defaultCfg *config.UserConfig) []Customization {
	var customizations []Customization

	// Helper to compare sections
	compareSections := func(userSection, defaultSection map[string][]string) {
		for action, defaultKeys := range defaultSection {
			userKeys, exists := userSection[action]
			if !exists {
				continue // Using default
			}

			// Check if different from default
			if !stringSlicesEqual(userKeys, defaultKeys) {
				customizations = append(customizations, Customization{
					Action:      formatActionName(action),
					DefaultKeys: strings.Join(defaultKeys, ", "),
					CustomKeys:  strings.Join(userKeys, ", "),
				})
			}
		}
	}

	// Compare all sections
	compareSections(userCfg.Keybindings.WindowManagement, defaultCfg.Keybindings.WindowManagement)
	compareSections(userCfg.Keybindings.Workspaces, defaultCfg.Keybindings.Workspaces)
	compareSections(userCfg.Keybindings.Layout, defaultCfg.Keybindings.Layout)
	compareSections(userCfg.Keybindings.ModeControl, defaultCfg.Keybindings.ModeControl)
	compareSections(userCfg.Keybindings.System, defaultCfg.Keybindings.System)
	compareSections(userCfg.Keybindings.PrefixMode, defaultCfg.Keybindings.PrefixMode)
	compareSections(userCfg.Keybindings.WindowPrefix, defaultCfg.Keybindings.WindowPrefix)
	compareSections(userCfg.Keybindings.MinimizePrefix, defaultCfg.Keybindings.MinimizePrefix)
	compareSections(userCfg.Keybindings.WorkspacePrefix, defaultCfg.Keybindings.WorkspacePrefix)

	return customizations
}

// stringSlicesEqual checks if two string slices are equal
func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// formatActionName formats an action name for display
func formatActionName(action string) string {
	// Use description if available
	if desc, ok := config.ActionDescriptions[action]; ok {
		return desc
	}
	// Otherwise format the action name
	return strings.ReplaceAll(action, "_", " ")
}
