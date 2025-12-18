// Package main implements TUIOS - Terminal UI Operating System.
// TUIOS is a terminal-based window manager that provides a modern interface
// for managing multiple terminal sessions with workspace support, tiling modes,
// and comprehensive keyboard/mouse interactions.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/fang"
	tint "github.com/lrstanley/bubbletint/v2"
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
	noAnimations      bool
)

func main() {
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
		RunE: func(_ *cobra.Command, _ []string) error {
			if previewTheme != "" {
				return previewThemeColors(previewTheme)
			}

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
	rootCmd.PersistentFlags().BoolVar(&noAnimations, "no-animations", false, "Disable UI animations for instant transitions")

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
		RunE: func(_ *cobra.Command, _ []string) error {
			return runSSHServer(sshHost, sshPort, sshKeyPath)
		},
	}

	sshCmd.Flags().StringVar(&sshPort, "port", "2222", "SSH server port")
	sshCmd.Flags().StringVar(&sshHost, "host", "localhost", "SSH server host")
	sshCmd.Flags().StringVar(&sshKeyPath, "key-path", "", "Path to SSH host key (auto-generated if not specified)")

	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Manage TUIOS configuration",
		Long:  `Manage TUIOS configuration file and settings`,
	}

	configPathCmd := &cobra.Command{
		Use:   "path",
		Short: "Print configuration file path",
		Long:  `Print the path to the TUIOS configuration file`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return printConfigPath()
		},
	}

	configEditCmd := &cobra.Command{
		Use:   "edit",
		Short: "Edit configuration in $EDITOR",
		Long: `Open the TUIOS configuration file in your default editor

The editor is determined by checking $EDITOR, $VISUAL, or common editors
like vim, vi, nano, and emacs in that order.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return editConfigFile()
		},
	}

	configResetCmd := &cobra.Command{
		Use:   "reset",
		Short: "Reset configuration to defaults",
		Long: `Reset the TUIOS configuration file to default settings

This will overwrite your existing configuration after confirmation.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return resetConfigToDefaults()
		},
	}

	configCmd.AddCommand(configPathCmd, configEditCmd, configResetCmd)

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
		RunE: func(_ *cobra.Command, _ []string) error {
			return listKeybindings()
		},
	}

	keybindsCustomCmd := &cobra.Command{
		Use:   "list-custom",
		Short: "List customized keybindings",
		Long: `Display only keybindings that differ from defaults

Shows a comparison of default and custom keybindings.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return listCustomKeybindings()
		},
	}

	keybindsCmd.AddCommand(keybindsListCmd, keybindsCustomCmd)

	var tapeVisible bool

	tapeCmd := &cobra.Command{
		Use:   "tape",
		Short: "Manage and run .tape automation scripts",
		Long: `Manage and execute .tape automation scripts for TUIOS

Tape files allow you to automate interactions with TUIOS by specifying
sequences of commands, key presses, and delays. Execute scripts in
interactive mode (visible TUI) to watch automation happen in real-time.`,
		Example: `  # Run tape with visible TUI (watch it happen)
  tuios tape play demo.tape

  # Validate tape file syntax
  tuios tape validate demo.tape`,
	}

	tapePlayCmd := &cobra.Command{
		Use:   "play <file.tape>",
		Short: "Run a tape file in interactive mode",
		Long: `Execute a tape script while displaying the TUIOS TUI

In interactive mode, you can see the automation happening in real-time
in the terminal UI. Press Ctrl+P to pause/resume playback.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runTapeInteractive(args[0])
		},
	}

	tapeValidateCmd := &cobra.Command{
		Use:   "validate <file.tape>",
		Short: "Validate a tape file without running it",
		Long:  `Check if a tape file is syntactically correct`,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return validateTapeFile(args[0])
		},
	}

	tapeListCmd := &cobra.Command{
		Use:   "list",
		Short: "List all saved tape recordings",
		Long:  `Display all tape files in the TUIOS data directory`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return listTapeFiles()
		},
	}

	tapeDirCmd := &cobra.Command{
		Use:   "dir",
		Short: "Show the tape recordings directory path",
		Long:  `Print the path where tape recordings are stored`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return showTapeDirectory()
		},
	}

	tapeDeleteCmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a tape recording",
		Long:  `Delete a tape file from the recordings directory`,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return deleteTapeFile(args[0])
		},
	}

	tapeShowCmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Display the contents of a tape file",
		Long:  `Print the contents of a tape recording to stdout`,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return showTapeFile(args[0])
		},
	}

	tapePlayCmd.Flags().BoolVarP(&tapeVisible, "visible", "v", true, "Show TUI during playback")

	tapeCmd.AddCommand(tapePlayCmd, tapeValidateCmd, tapeListCmd, tapeDirCmd, tapeDeleteCmd, tapeShowCmd)

	var createIfMissing bool

	attachCmd := &cobra.Command{
		Use:   "attach [session-name]",
		Short: "Attach to a TUIOS session",
		Long: `Attach to an existing TUIOS session.

If no session name is provided, attaches to the most recent session.
The session must already exist (use 'tuios new' to create one).

This requires the TUIOS daemon to be running.`,
		Example: `  # Attach to the most recent session
  tuios attach

  # Attach to a named session
  tuios attach mysession

  # Attach and create if session doesn't exist
  tuios attach mysession -c`,
		Aliases: []string{"a"},
		RunE: func(_ *cobra.Command, args []string) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			return runAttach(name, createIfMissing)
		},
	}
	attachCmd.Flags().BoolVarP(&createIfMissing, "create", "c", false, "Create session if it doesn't exist")

	newCmd := &cobra.Command{
		Use:   "new [session-name]",
		Short: "Create a new TUIOS session",
		Long: `Create a new persistent TUIOS session and attach to it.

This starts a new session in the daemon (starting the daemon if needed)
and immediately attaches you to it.

Sessions persist even when you detach, allowing you to reconnect later
with 'tuios attach'.`,
		Example: `  # Create a new session with auto-generated name
  tuios new

  # Create a named session
  tuios new mysession`,
		Aliases: []string{"n"},
		RunE: func(_ *cobra.Command, args []string) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			return runNewSession(name)
		},
	}

	lsCmd := &cobra.Command{
		Use:   "ls",
		Short: "List TUIOS sessions",
		Long: `List all active TUIOS sessions.

Shows session names, window counts, and whether clients are attached.`,
		Example: `  tuios ls`,
		Aliases: []string{"list-sessions"},
		RunE: func(_ *cobra.Command, _ []string) error {
			return runListSessions()
		},
	}

	killSessionCmd := &cobra.Command{
		Use:   "kill-session <session-name>",
		Short: "Kill a TUIOS session",
		Long: `Terminate a TUIOS session and all its windows.

This will close all windows in the session and disconnect any attached clients.`,
		Example: `  tuios kill-session mysession`,
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runKillSession(args[0])
		},
	}

	startDaemonCmd := &cobra.Command{
		Use:   "start-server",
		Short: "Start the TUIOS daemon",
		Long: `Start the TUIOS daemon in the background.

The daemon manages persistent sessions. It starts automatically when
you create or attach to a session, so you typically don't need to
run this command manually.`,
		Example: `  tuios start-server`,
		Hidden:  true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runDaemon(false)
		},
	}

	var daemonLogLevel string
	daemonCmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run the TUIOS daemon in the foreground",
		Long: `Run the TUIOS daemon in the foreground.

This is useful for debugging. Normally the daemon runs in the background.

Debug log levels:
  off      - No debug output (default)
  errors   - Only error messages
  basic    - Connection events and errors
  messages - All protocol messages except PTY I/O
  verbose  - All messages including PTY I/O
  trace    - Full payload hex dumps`,
		Example: `  tuios daemon
  tuios daemon --log-level=messages
  tuios daemon --log-level=verbose`,
		RunE: func(_ *cobra.Command, _ []string) error {
			if daemonLogLevel != "" {
				session.SetDebugLevel(session.ParseDebugLevel(daemonLogLevel))
			}
			return runDaemon(true)
		},
	}
	daemonCmd.Flags().StringVar(&daemonLogLevel, "log-level", "", "Debug log level: off, errors, basic, messages, verbose, trace")

	killDaemonCmd := &cobra.Command{
		Use:   "kill-server",
		Short: "Stop the TUIOS daemon",
		Long: `Stop the TUIOS daemon.

This will terminate all sessions and disconnect all clients.`,
		Example: `  tuios kill-server`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runKillDaemon()
		},
	}

	rootCmd.AddCommand(sshCmd, configCmd, keybindsCmd, tapeCmd)
	rootCmd.AddCommand(attachCmd, newCmd, lsCmd, killSessionCmd)
	rootCmd.AddCommand(startDaemonCmd, daemonCmd, killDaemonCmd)

	if err := fang.Execute(
		context.Background(),
		rootCmd,
		fang.WithVersion(fmt.Sprintf("%s\nCommit: %s\nBuilt: %s\nBy: %s", version, commit, date, builtBy)),
	); err != nil {
		os.Exit(1)
	}
}
