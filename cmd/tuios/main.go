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
	debugMode           bool
	cpuProfile          string
	asciiOnly           bool
	themeName           string
	listThemes          bool
	previewTheme        string
	borderStyle         string
	dockbarPosition     string
	hideWindowButtons   bool
	scrollbackLines     int
	showKeys            bool
	noAnimations        bool
	windowTitlePosition string
	hideClock           bool
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
	rootCmd.PersistentFlags().StringVar(&windowTitlePosition, "window-title-position", "", "Window title position: bottom, top, hidden (default: from config or bottom)")
	rootCmd.PersistentFlags().BoolVar(&hideClock, "hide-clock", false, "Hide the clock overlay")

	var sshPort, sshHost, sshKeyPath, sshDefaultSession string
	var sshEphemeral bool

	sshCmd := &cobra.Command{
		Use:   "ssh",
		Short: "Run TUIOS as SSH server",
		Long: `Run TUIOS as an SSH server

Allows remote connections to TUIOS via SSH. The server will generate
a host key automatically if not specified.

By default, SSH sessions connect to the TUIOS daemon for persistent sessions.
Session selection priority:
  1. --default-session flag (if specified)
  2. SSH username (if not generic like "tuios", "root", "anonymous")
  3. SSH command argument (e.g., "ssh host attach mysession")
  4. First available session or create new

Use --ephemeral for standalone sessions (legacy behavior).`,
		Example: `  # Start SSH server on default port
  tuios ssh

  # Start on custom port
  tuios ssh --port 2222

  # Specify custom host key
  tuios ssh --key-path /path/to/host_key

  # Use a default session for all connections
  tuios ssh --default-session mysession

  # Run in ephemeral mode (standalone, no daemon)
  tuios ssh --ephemeral`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runSSHServer(sshHost, sshPort, sshKeyPath, sshDefaultSession, sshEphemeral)
		},
	}

	sshCmd.Flags().StringVar(&sshPort, "port", "2222", "SSH server port")
	sshCmd.Flags().StringVar(&sshHost, "host", "localhost", "SSH server host")
	sshCmd.Flags().StringVar(&sshKeyPath, "key-path", "", "Path to SSH host key (auto-generated if not specified)")
	sshCmd.Flags().StringVar(&sshDefaultSession, "default-session", "", "Default session name for all connections")
	sshCmd.Flags().BoolVar(&sshEphemeral, "ephemeral", false, "Run in ephemeral mode (standalone, no daemon)")

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

	// Remote control commands
	var sendKeysSession string
	var sendKeysLiteral bool
	var sendKeysRaw bool
	sendKeysCmd := &cobra.Command{
		Use:   "send-keys <keys>",
		Short: "Send keystrokes to a running TUIOS session",
		Long: `Send keystrokes to a running TUIOS session.

By default, keys are sent to TUIOS itself (for window management, mode switching, etc).
Use --literal to send keys directly to the focused terminal PTY.
Use --raw to send each character as a separate key (no splitting on spaces).

Key format (default mode):
  - Single keys: "i", "n", "Enter", "Escape", "Space"  
  - Key combos: "ctrl+b", "alt+1", "shift+Enter" (case-insensitive)
  - Sequences: space or comma separated, e.g. "ctrl+b q" or "ctrl+b,q"

Special tokens:
  - $PREFIX or PREFIX: expands to configured leader key (default: ctrl+b)

Modifiers: ctrl, alt, shift, super, meta

Special keys: Enter, Return, Space, Tab, Escape, Esc, Backspace, Delete,
              Up, Down, Left, Right, Home, End, PageUp, PageDown, F1-F12`,
		Example: `  # Enter terminal mode (press 'i')
  tuios send-keys i

  # Press Enter
  tuios send-keys Enter

  # Trigger prefix key followed by 'q' (quit)
  tuios send-keys "ctrl+b q"
  tuios send-keys "$PREFIX q"

  # Multiple keys: prefix + new window
  tuios send-keys "ctrl+b,n"

  # Send Ctrl+C to TUIOS
  tuios send-keys ctrl+c

  # Send literal text directly to terminal PTY
  tuios send-keys --literal "echo hello"

  # Send text with spaces (each char is a key, spaces included)
  tuios send-keys --raw "hello world"

  # Send to a specific session
  tuios send-keys --session mysession Escape`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runSendKeys(sendKeysSession, args[0], sendKeysLiteral, sendKeysRaw)
		},
	}
	sendKeysCmd.Flags().StringVarP(&sendKeysSession, "session", "s", "", "Target session (default: most recently active)")
	sendKeysCmd.Flags().BoolVarP(&sendKeysLiteral, "literal", "l", false, "Send keys directly to terminal PTY (bypass TUIOS)")
	sendKeysCmd.Flags().BoolVarP(&sendKeysRaw, "raw", "r", false, "Treat each character as a separate key (no splitting on space/comma)")
	_ = sendKeysCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	// Add completion for send-keys
	sendKeysCmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return getSendKeysCompletions(toComplete), cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var runCommandSession string
	var runCommandList bool
	var runCommandJSON bool
	runCommandCmd := &cobra.Command{
		Use:   "run-command <command> [args...]",
		Short: "Execute a tape command in a running TUIOS session",
		Long: `Execute a tape command in a running TUIOS session.

This allows you to control TUIOS remotely by executing tape commands.
Use --list to see all available commands.
Use --json to get machine-readable output for scripting.`,
		Example: `  # Create a new window
  tuios run-command NewWindow

  # Create a window and get its ID (for scripting)
  tuios run-command --json NewWindow "My Window"

  # Switch to workspace 2
  tuios run-command SwitchWorkspace 2

  # Toggle tiling mode
  tuios run-command ToggleTiling

  # Change dockbar position
  tuios run-command SetDockbarPosition top

  # List all available commands
  tuios run-command --list`,
		Args: cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			if runCommandList {
				listAvailableCommands()
				return nil
			}
			if len(args) == 0 {
				return fmt.Errorf("command name required (use --list to see available commands)")
			}
			return runCommand(runCommandSession, args[0], args[1:], runCommandJSON)
		},
	}
	runCommandCmd.Flags().StringVarP(&runCommandSession, "session", "s", "", "Target session (default: most recently active)")
	runCommandCmd.Flags().BoolVar(&runCommandList, "list", false, "List all available commands")
	runCommandCmd.Flags().BoolVar(&runCommandJSON, "json", false, "Output result as JSON (for scripting)")
	_ = runCommandCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	// Add completion for run-command
	runCommandCmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			// First argument: command name
			return getRunCommandCompletions(toComplete), cobra.ShellCompDirectiveNoFileComp
		}
		// Second+ arguments depend on the command
		return getRunCommandArgCompletions(args[0], len(args), toComplete), cobra.ShellCompDirectiveNoFileComp
	}

	var setConfigSession string
	setConfigCmd := &cobra.Command{
		Use:   "set-config <path> <value>",
		Short: "Set a configuration option in a running TUIOS session",
		Long: `Set a configuration option in a running TUIOS session at runtime.

Supported configuration paths:
  dockbar_position     - Dockbar position: top, bottom, left, right
  border_style         - Border style: rounded, normal, thick, double, hidden, block, ascii
  animations           - Enable animations: true, false, toggle
  hide_window_buttons  - Hide window buttons: true, false`,
		Example: `  # Change dockbar position
  tuios set-config dockbar_position top

  # Change border style
  tuios set-config border_style rounded

  # Toggle animations
  tuios set-config animations toggle

  # Hide window buttons
  tuios set-config hide_window_buttons true`,
		Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			return runSetConfig(setConfigSession, args[0], args[1])
		},
	}
	setConfigCmd.Flags().StringVarP(&setConfigSession, "session", "s", "", "Target session (default: most recently active)")
	_ = setConfigCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	// Add completion for set-config
	setConfigCmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			// First argument: config path
			return getConfigPathCompletions(toComplete), cobra.ShellCompDirectiveNoFileComp
		}
		if len(args) == 1 {
			// Second argument: value (depends on the path)
			return getConfigValueCompletions(args[0], toComplete), cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var tapeExecSession string
	tapeExecCmd := &cobra.Command{
		Use:   "exec <file.tape>",
		Short: "Execute a tape file in a running session",
		Long: `Execute a tape file in a running TUIOS session.

For single tape commands, use: tuios run-command <Command> [args...]`,
		Example: `  # Execute a tape file
  tuios tape exec demo.tape
  tuios tape exec ./examples/advanced_demo.tape

  # Execute in a specific session
  tuios tape exec --session mysession demo.tape`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runTapeExec(tapeExecSession, args[0])
		},
	}
	tapeExecCmd.Flags().StringVarP(&tapeExecSession, "session", "s", "", "Target session (default: most recently active)")
	_ = tapeExecCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	// Add exec to tape command group
	tapeCmd.AddCommand(tapeExecCmd)

	// Logs command for debugging daemon
	var logsCount int
	var logsClear bool
	var logsFollow bool
	logsCmd := &cobra.Command{
		Use:   "logs",
		Short: "View daemon logs",
		Long: `View recent log entries from the TUIOS daemon.

This is useful for debugging issues with remote commands, sessions, and PTY handling.
Logs are stored in a ring buffer (1000 entries by default).`,
		Example: `  # View last 50 log entries
  tuios logs

  # View last 100 log entries
  tuios logs -n 100

  # View all stored log entries
  tuios logs --all

  # Clear logs after viewing
  tuios logs --clear

  # Follow logs (continuously show new entries)
  tuios logs -f`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runGetLogs(logsCount, logsClear, logsFollow)
		},
	}
	logsCmd.Flags().IntVarP(&logsCount, "lines", "n", 50, "Number of log entries to show (0 or --all for all)")
	logsCmd.Flags().BoolVar(&logsClear, "clear", false, "Clear logs after viewing")
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "Follow logs (continuously show new entries)")
	logsCmd.Flags().Bool("all", false, "Show all log entries")

	// Inspection commands for scripting and hackability
	var listWindowsSession string
	var listWindowsJSON bool
	listWindowsCmd := &cobra.Command{
		Use:   "list-windows",
		Short: "List all windows in the session",
		Long: `List all windows in the running TUIOS session.

Shows window ID, title, workspace, focused state, and more.
Use --json for machine-readable output that can be used for scripting.`,
		Example: `  # List all windows (table format)
  tuios list-windows

  # List as JSON for scripting
  tuios list-windows --json

  # Use with jq to get focused window ID
  tuios list-windows --json | jq '.focused_window_id'`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return queryWindows(listWindowsSession, listWindowsJSON)
		},
	}
	listWindowsCmd.Flags().StringVarP(&listWindowsSession, "session", "s", "", "Target session (default: most recently active)")
	listWindowsCmd.Flags().BoolVar(&listWindowsJSON, "json", false, "Output as JSON")
	_ = listWindowsCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	var getWindowSession string
	var getWindowJSON bool
	getWindowCmd := &cobra.Command{
		Use:   "get-window [id-or-name]",
		Short: "Get detailed info about a window",
		Long: `Get detailed information about a specific window.

If no ID or name is provided, returns info about the focused window.
Use --json for machine-readable output.`,
		Example: `  # Get focused window info
  tuios get-window

  # Get window by name
  tuios get-window "Server"

  # Get window by ID (from list-windows)
  tuios get-window abc123-def456

  # Get as JSON for scripting
  tuios get-window --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return runCommand(getWindowSession, "GetWindow", args, getWindowJSON)
			}
			return runCommand(getWindowSession, "GetWindow", nil, getWindowJSON)
		},
	}
	getWindowCmd.Flags().StringVarP(&getWindowSession, "session", "s", "", "Target session (default: most recently active)")
	getWindowCmd.Flags().BoolVar(&getWindowJSON, "json", false, "Output as JSON")
	_ = getWindowCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	var sessionInfoSession string
	var sessionInfoJSON bool
	sessionInfoCmd := &cobra.Command{
		Use:   "session-info",
		Short: "Get current session information",
		Long: `Get detailed information about the current TUIOS session.

Shows mode, workspace, tiling state, theme, and more.
Use --json for machine-readable output.`,
		Example: `  # Get session info (table format)
  tuios session-info

  # Get as JSON for scripting
  tuios session-info --json

  # Use with jq to check if tiling is enabled
  tuios session-info --json | jq '.tiling_enabled'`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return querySession(sessionInfoSession, sessionInfoJSON)
		},
	}
	sessionInfoCmd.Flags().StringVarP(&sessionInfoSession, "session", "s", "", "Target session (default: most recently active)")
	sessionInfoCmd.Flags().BoolVar(&sessionInfoJSON, "json", false, "Output as JSON")
	_ = sessionInfoCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	rootCmd.AddCommand(sshCmd, configCmd, keybindsCmd, tapeCmd)
	rootCmd.AddCommand(attachCmd, newCmd, lsCmd, killSessionCmd)
	rootCmd.AddCommand(startDaemonCmd, daemonCmd, killDaemonCmd)
	rootCmd.AddCommand(sendKeysCmd, runCommandCmd, setConfigCmd, logsCmd)
	rootCmd.AddCommand(listWindowsCmd, getWindowCmd, sessionInfoCmd)

	if err := fang.Execute(
		context.Background(),
		rootCmd,
		fang.WithVersion(fmt.Sprintf("%s\nCommit: %s\nBuilt: %s\nBy: %s", version, commit, date, builtBy)),
	); err != nil {
		os.Exit(1)
	}
}
