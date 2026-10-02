// Package main implements TUIOS, the Terminal UI Operating System.
// TUIOS is a terminal-based window manager that provides a modern interface
// for managing multiple terminal sessions with workspace support, tiling modes,
// and comprehensive keyboard/mouse interactions.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/cliflags"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/fang"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
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
	debugMode      bool
	cpuProfile     string
	pprofAddr      string
	listThemes     bool
	previewTheme   string
	skillTopic     string
	standaloneMode bool
	// interfaceFlags is the appearance and interface flags, shared by every
	// command that renders the TUI. See registerInterfaceFlags.
	interfaceFlags cliflags.Interface
)

func main() {
	// The build identity, handed to internal/app before anything can crash.
	// A crash report that cannot say which build produced it cannot be placed
	// against a commit, and internal/app cannot read these vars itself.
	app.SetBuildStamp(version, commit)

	// Run through a link with another program's name (tmux, herdr), this
	// binary is that program. See runAsLinkedProgram.
	if code, ok := runAsLinkedProgram(os.Args[0], os.Args[1:]); ok {
		os.Exit(code)
	}

	rootCmd := newRootCommand()
	rootCmd.SetArgs(skillArgs(rootCmd, os.Args[1:]))

	// Command failures are printed here rather than by fang, which would query
	// the terminal for its background color first and stall for seconds when
	// nothing answers. See errorStyles.
	var cmdErr error
	interceptErrors(rootCmd, &cmdErr)

	if err := fang.Execute(
		context.Background(),
		rootCmd,
		fang.WithVersion(versionReport()),
		fang.WithErrorHandler(diagnosticErrorHandler),
	); err != nil {
		os.Exit(1)
	}
	if code := exitStatus(cmdErr); code != 0 {
		os.Exit(code)
	}
}

// newRootCommand builds the whole command tree. It is separate from main so a
// test can resolve a command line against the real tree rather than against a
// second description of it that would drift.
func newRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "tuios",
		Short: "Terminal UI Operating System",
		Long: `TUIOS: Terminal UI Operating System

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
  tuios keybinds list

  # Print the agent skill for driving tuios from a pane
  tuios --skill

  # Print one topic of it, or all of it
  tuios --skill fleet
  tuios --skill all`,
		Version: version,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// The skill is printed before anything else can decide to draw: it is
			// a document, and a caller asking for it never wants the interface.
			if cmd.Flags().Changed("skill") {
				text, err := skillDocument(skillTopic)
				if err != nil {
					return err
				}
				fmt.Print(text)
				return nil
			}

			if previewTheme != "" {
				return previewThemeColors(previewTheme)
			}

			if listThemes {
				theme.EnsureRegistry()
				themes := tint.TintIDs()
				for _, t := range themes {
					fmt.Println(t)
				}
				return nil
			}
			return runLocal()
		},
		SilenceUsage: true,
	}

	rootCmd.PersistentFlags().BoolVar(&debugMode, "debug", false, "Enable debug logging")
	rootCmd.PersistentFlags().StringVar(&cpuProfile, "cpuprofile", "", "Write CPU profile to file")
	rootCmd.PersistentFlags().StringVar(&pprofAddr, "pprof", "", "Serve /debug/pprof profiles on this address, for example :6060. With no host, it listens on 127.0.0.1 only. The profiles have no password")

	// Local to the root command: the skill describes tuios as a whole, and the
	// theme listing and preview are root-level actions that print and exit, so
	// offering them on every subcommand would only add noise to their help.
	//
	// --skill takes an optional topic. A bare --skill prints the core, and
	// skillArgs turns "--skill TOPIC" into "--skill=TOPIC" before cobra sees
	// it, because an optional value only binds with "=", and a topic such as
	// mcp or hosts is also the name of a subcommand.
	rootCmd.Flags().StringVar(&skillTopic, "skill", "", "Print the agent skill for driving tuios from a pane and exit; --skill TOPIC prints one topic, --skill all prints every topic")
	rootCmd.Flags().Lookup("skill").NoOptDefVal = "core"
	// The way out of startup.daemon for one run. It is on the root command
	// because that is the only command the setting changes.
	rootCmd.Flags().BoolVar(&standaloneMode, "standalone", false, "Run a standalone session without the daemon, overriding startup.daemon (TUIOS_NO_DAEMON=1 does the same for a whole shell)")
	rootCmd.Flags().BoolVar(&listThemes, "list-themes", false, "List all available themes and exit")
	rootCmd.Flags().StringVar(&previewTheme, "preview-theme", "", "Preview a theme's 16 ANSI colors")

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

	configApplyCmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply config.toml to the running daemon now",
		Long: `Apply config.toml to the running daemon now, including changes that give panes or other machines more.

The daemon applies a change to the file at once only where it gives less. A change that gives more waits for this command or a daemon restart. Run it from a terminal outside tuios. It is refused from inside a pane.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			client, err := dialVerb()
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			raw, err := client.Call("apply-config", nil)
			if err != nil {
				return explainVerbError("apply-config", err)
			}
			fmt.Print(describeConfigApplied(raw))
			return nil
		},
	}

	configCmd.AddCommand(configPathCmd, configEditCmd, configResetCmd, configApplyCmd)

	keybindsCmd := &cobra.Command{
		Use:     "keybinds",
		Aliases: []string{"keys", "kb"},
		Short:   "View keybinding configuration",
		Long:    `View and inspect TUIOS keybinding configuration`,
	}

	keybindsListCmd := &cobra.Command{
		Use:   "list",
		Short: "List the common keybindings",
		Long: `Display the common keybindings, as configured, in formatted tables.
tuios keybinds doctor lists every scope.`,
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

	var (
		keybindsJSON  bool
		keybindsGuest string
	)

	keybindsDoctorCmd := &cobra.Command{
		Use:   "doctor",
		Short: "Report keybind conflicts",
		Long: `Report every key claimed twice, every key tuios takes from the pane,
and every one of those a common program wants.

Each finding carries the evidence it rests on: certain (tuios's own routing),
observed (read from a pane), or reference (a list of common program defaults,
not detection). --json emits the same analysis the keybind overlay draws.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return keybindsDoctor(keybindsJSON, keybindsGuest)
		},
	}
	keybindsDoctorCmd.Flags().BoolVar(&keybindsJSON, "json", false, "emit the report as JSON")
	keybindsDoctorCmd.Flags().StringVar(&keybindsGuest, "guest", "", "treat this program as the one running in the pane")

	keybindsExplainCmd := &cobra.Command{
		Use:   "explain <key>",
		Short: "Say what tuios does with one key",
		Long: `Print every scope the key acts in, whether the pane's program would
receive it, the terminal-level pair it belongs to, and which common programs
bind it. This prints the same answer the overlay's key recorder shows.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return keybindsExplain(args[0], keybindsJSON, keybindsGuest)
		},
	}
	keybindsExplainCmd.Flags().BoolVar(&keybindsJSON, "json", false, "emit the answer as JSON")
	keybindsExplainCmd.Flags().StringVar(&keybindsGuest, "guest", "", "treat this program as the one running in the pane")

	keybindsUnbindCmd := &cobra.Command{
		Use:   "unbind <action> [key]",
		Short: "Take a key off one action",
		Long: `Take a key off one action and write the change to config.toml.

Name a key to remove that one. Name none and the action loses every key it has.
An action with no keys is written as an empty list, which is different from
leaving it out of the file: an action the file does not mention gets its default
back at the next load, and an empty list does not.

This changes one action. To stop tuios taking a key at all, so the program in
your pane receives it, use ` + "`tuios keybinds free`" + `.`,
		Example: `  # Stop w closing a window, leaving x
  tuios keybinds unbind close_window w

  # Leave the action with no key at all
  tuios keybinds unbind close_window`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			key := ""
			if len(args) > 1 {
				key = args[1]
			}
			return keybindsUnbind(args[0], key)
		},
	}

	keybindsFreeCmd := &cobra.Command{
		Use:   "free <key>",
		Short: "Hand a key back to the program in the pane",
		Long: `Take one key off every action in every scope and write the change to
config.toml.

Every scope at once is the point. A key tuios still claims anywhere is a key the
program in your pane never sees, so freeing one table at a time does not free
the key. Each action that runs out of keys is written as an empty list, so the
default does not come back at the next load.

Two things this cannot take. The leader key is keybindings.leader_key rather
than an entry in a table, so it is moved rather than unbound. A few keys are
read by the input path itself and have no config entry. Either way the command
says so instead of reporting a success.`,
		Example: `  # Give alt+left back to your shell
  tuios keybinds free alt+left

  # Check first: this says every scope the key acts in
  tuios keybinds explain alt+left`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return keybindsFree(args[0])
		},
	}

	keybindsCmd.AddCommand(keybindsListCmd, keybindsCustomCmd, keybindsDoctorCmd,
		keybindsExplainCmd, keybindsUnbindCmd, keybindsFreeCmd)

	var createIfMissing bool
	var attachHost string
	var attachHold bool
	var attachSSH bool

	attachCmd := &cobra.Command{
		Use:   "attach [session-name]",
		Short: "Attach to a TUIOS session",
		Long: `Attach to an existing TUIOS session.

If no session name is provided, attaches to the most recent session.

If the daemon is not running, it is started and restores every session
saved on disk: the layout and working directories, with new shells. Attach
then opens one of those. With nothing saved and no
name given, a new session is opened instead. A name that matches no session
is an error unless -c is given.

With --host the session is on another machine. tuios runs ssh to the host
named in the [hosts] table and attaches with the tuios on that machine. The
client you see is the remote one. Press the prefix key twice to send it to
the remote client. See 'tuios hosts --help'.`,
		Example: `  # Attach to the most recent session
  tuios attach

  # Attach to a named session
  tuios attach mysession

  # Attach and create if session doesn't exist
  tuios attach mysession -c

  # Attach to a session on the machine named build
  tuios attach --host build mysession`,
		Aliases: []string{"a"},
		RunE: func(_ *cobra.Command, args []string) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			if attachHost != "" {
				return runAttachOnHost(attachHost, name, createIfMissing, attachHold, attachSSH)
			}
			// A failure is held on the screen with --hold for the reason a
			// host attach holds it: a pane that runs this command closes when
			// it exits, and a popup that runs tuios attach is such a pane.
			return holdAfter(runAttach(name, createIfMissing), attachHold)
		},
	}
	attachCmd.Flags().BoolVarP(&createIfMissing, "create", "c", false, "Create session if it doesn't exist")
	attachCmd.Flags().StringVar(&attachHost, "host", "", "Attach to a session on this host from the [hosts] table")
	attachCmd.Flags().BoolVar(&attachSSH, "ssh", false, "With --host, run ssh to the host and its own tuios instead of attaching here")
	attachCmd.Flags().BoolVar(&attachHold, "hold", false, "After a failure, wait for enter before the command exits")
	attachCmd.Flags().BoolVar(&attachForce, "force", false, "Attach even from a pane of the same session")
	attachCmd.Flags().BoolVar(&attachTerminalMode, "terminal-mode", false, "Start in terminal mode, whatever startup.start_in_terminal_mode says")
	registerHostNameCompletion(attachCmd, "host")

	var newDetach bool
	var newHost string
	var newHold bool
	var newGlobal bool
	var newSSH bool
	newCmd := &cobra.Command{
		Use:   "new [session-name]",
		Short: "Create a new TUIOS session",
		Long: `Create a new persistent TUIOS session and attach to it.

This starts a new session in the daemon (starting the daemon if needed)
and immediately attaches you to it.

With --detach the session is created headless (no client attached): it
gets an initial window, is immediately usable by control commands
(send-keys, run-command, capture-pane), and can be attached later.

Sessions persist even when you detach, allowing you to reconnect later
with 'tuios attach'.

With --host the session is created on another machine. tuios runs ssh to the
host named in the [hosts] table and runs 'tuios new' there. The client you
see is the remote one. With --detach the far side creates the session and
returns. See 'tuios hosts --help'.`,
		Example: `  # Create a new session with auto-generated name
  tuios new

  # Create a named session
  tuios new mysession

  # Create a headless session without attaching
  tuios new mysession --detach

  # Create a session on the machine named build and attach to it
  tuios new --host build

  # Create a named session on that machine without attaching
  tuios new --host build mysession --detach`,
		Aliases: []string{"n"},
		RunE: func(_ *cobra.Command, args []string) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			if newHost != "" {
				return runNewOnHost(newHost, name, newDetach, newHold, newSSH)
			}
			if newGlobal {
				// A global session is created with no windows whether or not
				// --detach was asked for: its first window names a machine,
				// and there is nothing here to ask.
				return runNewGlobalSessionDetached(name)
			}
			if newDetach {
				return runNewSessionDetached(name)
			}
			return runNewSession(name)
		},
	}
	newCmd.Flags().BoolVarP(&newDetach, "detach", "d", false, "Create the session headless without attaching a client")
	newCmd.Flags().StringVar(&newHost, "host", "", "Create the session on this host from the [hosts] table")
	newCmd.Flags().BoolVar(&newSSH, "ssh", false, "With --host, run ssh to the host and its own tuios instead of attaching here")
	newCmd.Flags().BoolVar(&newGlobal, "global", false, "Create a global session, which holds panes from more than one machine")
	newCmd.Flags().BoolVar(&newHold, "hold", false, "After a failure, wait for enter before the command exits")
	registerHostNameCompletion(newCmd, "host")

	var lsJSON bool
	var lsAllHosts bool
	var lsHost string
	lsCmd := &cobra.Command{
		Use:   "ls",
		Short: "List TUIOS sessions",
		Long: `List all active TUIOS sessions.

Shows session names, window counts, and whether clients are attached.

With no daemon running, the sessions saved on disk are listed instead,
marked "saved", and the command exits 3. Exit 3 lets a script tell a
stopped daemon from a running daemon with no sessions, which exits 0
with an empty list.

Use --json for machine-readable output; saved rows carry "saved": true.

With --all-hosts the listing also covers every machine in the [hosts] config
table. Local comes first. A host that does not answer gets a row saying so,
and it never fails the command.`,
		Example: `  tuios ls
  tuios ls --json
  tuios ls --all-hosts`,
		Aliases: []string{"list-sessions"},
		RunE: func(_ *cobra.Command, _ []string) error {
			if lsAllHosts || lsHost != "" {
				return runListSessionsAllHosts(lsHost, lsJSON)
			}
			return runListSessions(lsJSON)
		},
	}
	lsCmd.Flags().BoolVar(&lsJSON, "json", false, "Output as JSON")
	lsCmd.Flags().BoolVar(&lsAllHosts, "all-hosts", false, "List sessions on this machine and on every host in the [hosts] config table")
	lsCmd.Flags().StringVar(&lsHost, "host", "", "List sessions on one host by name (\"local\" means this machine)")
	hideHostFlags(attachCmd, newCmd, lsCmd)

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

	resurrectCmd := &cobra.Command{
		Use:   "resurrect [session-name]",
		Short: "Restore a previously saved session",
		Long: `Restore a session that was saved before a daemon restart, crash, or reboot.

With no arguments, lists the sessions that can be resurrected (from saved
state on disk). With a session name, restores that session in the daemon
(respawning fresh shells in each window's saved working directory) and
attaches to it.

Sessions are normally auto-restored when the daemon starts; this command is
useful when the daemon was started with --no-restore, or to bring back a
specific session on demand.`,
		Example: `  # List resurrectable sessions
  tuios resurrect

  # Restore and attach to a saved session
  tuios resurrect mysession`,
		Aliases: []string{"restore"},
		Args:    cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			return runResurrect(name)
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
			return runDaemon(false, false)
		},
	}

	var daemonLogLevel string
	var daemonNoRestore bool
	daemonCmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run the TUIOS daemon in the foreground",
		Long: `Run the TUIOS daemon in the foreground.

This is useful for debugging. Normally the daemon runs in the background.

Debug log levels:
  off:      No debug output (default)
  errors:   Only error messages
  basic:    Connection events and errors
  messages: All protocol messages except PTY I/O
  verbose:  All messages including PTY I/O
  trace:    Full payload hex dumps`,
		Example: `  tuios daemon
  tuios daemon --log-level=messages
  tuios daemon --log-level=verbose`,
		RunE: func(_ *cobra.Command, _ []string) error {
			if daemonLogLevel != "" {
				session.SetDebugLevel(session.ParseDebugLevel(daemonLogLevel))
			}
			// The daemon owns every emulator and scrollback ring, so it is
			// the process a memory question is about. --pprof is a
			// persistent flag, so it has to be honoured here too.
			startPprofServer()
			return runDaemon(true, daemonNoRestore)
		},
	}
	daemonCmd.Flags().StringVar(&daemonLogLevel, "log-level", "", "Debug log level: off, errors, basic, messages, verbose, trace")
	daemonCmd.Flags().BoolVar(&daemonNoRestore, "no-restore", false, "Do not auto-restore saved sessions on start (use 'tuios resurrect' to restore on demand)")

	killDaemonCmd := &cobra.Command{
		Use:   "kill-server",
		Short: "Stop the TUIOS daemon",
		Long: `Stop the TUIOS daemon.

This will stop all sessions and disconnect all clients.

The command is synchronous: it returns only once the daemon has saved every
session's state and removed its socket, so a new daemon can be started as soon
as it returns. It fails if the daemon has not finished within 10 seconds.`,
		Example: `  tuios kill-server`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runKillDaemon()
		},
	}

	// Remote control commands
	var sendKeysSession string
	var sendKeysLiteral bool
	var sendKeysRaw bool
	var sendKeysWindow string
	var sendKeysRepeat int
	var sendKeysJSON bool
	sendKeysCmd := &cobra.Command{
		Use:   "send-keys <keys>",
		Short: "Send keys (arrows, page keys, ctrl+c) to a window",
		Long: `Send keys to the program in a window: arrows, page keys, Enter, ctrl+c.

With -w the keys go to that window's terminal, whether or not a client is
attached and whichever window has the focus. Without -w they go to the attached
client as if the person pressed them, which drives the window manager or the
focused window; with no client attached they go to the focused window.

To type text, use send-text. send-keys splits its argument on spaces and commas,
so 'echo hello' types "echohello".

Keys (case-insensitive; the argument is split on spaces and commas):
  Enter Tab BTab Space Comma Escape Backspace
  Up Down Left Right Home End PageUp PageDown Insert Delete F1-F12
  a single character: q, j, /, G
  ctrl+X, alt+X, shift+X on a character or a key: ctrl+c, alt+b, shift+Up
  PREFIX: the leader key (only without -w, with a client attached)

Other spellings of the same keys work too: up, UP, arrow-up, ArrowUp, KEY_UP,
<Up>, PgDn, Page_Down, Esc, Return, BSpace, tmux's C-c and M-x, and ^C. An
escape sequence can be written as \e[A, \x1b[A or \033[A. A word that looks
like a misspelled key (Dwon, KEY_FOO, F13) is refused with the list of names,
and nothing is sent.

The list is closed and stable. The list-keys verb returns it, with every
spelling of every key.

--repeat sends the whole sequence that many times. The command prints where the
keys went: "sent 5 keys to window docs (d6b97fe4)".

Window targeting (-w), tried in this order: the full id, the index
list-windows prints, the exact window name, then a unique id prefix. A name set
with --name or new-window wins over a program's title. An ambiguous target is an error that lists the windows
it matched.`,
		Example: `  # Scroll the pager in the window named docs
  tuios send-keys -w docs Down
  tuios send-keys -w docs Down --repeat 10
  tuios send-keys -w docs PageDown
  tuios send-keys -w docs 'Up Up Up'

  # Interrupt what runs in a window
  tuios send-keys -w build ctrl+c

  # Quit a pager, then Enter
  tuios send-keys -w docs q
  tuios send-keys -w docs Enter

  # A key as an escape sequence
  tuios send-keys -w docs '\e[B'

  # Keys for the window manager: the leader key and then n, no -w
  tuios send-keys "PREFIX n"
  tuios send-keys "ctrl+b,n"`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runSendKeys(sendKeysSession, args[0], sendKeysLiteral, sendKeysRaw, sendKeysWindow, sendKeysRepeat, sendKeysJSON)
		},
	}
	sendKeysCmd.Flags().StringVarP(&sendKeysSession, "session", "s", "", "Target session (default: most recently active)")
	sendKeysCmd.Flags().BoolVarP(&sendKeysLiteral, "literal", "l", false, "Write the argument to the window's terminal unchanged, with no key names")
	sendKeysCmd.Flags().BoolVarP(&sendKeysRaw, "raw", "r", false, "Treat each character as a separate key (no splitting on space/comma)")
	sendKeysCmd.Flags().StringVarP(&sendKeysWindow, "window", "w", "", "Target window: id, index, name or id prefix (default: the attached client, else the focused window)")
	sendKeysCmd.Flags().IntVarP(&sendKeysRepeat, "repeat", "N", 1, "Send the whole sequence this many times (1 to 1000)")
	sendKeysCmd.Flags().BoolVar(&sendKeysJSON, "json", false, "Output result as JSON")
	_ = sendKeysCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	// Add completion for send-keys
	sendKeysCmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return getSendKeysCompletions(toComplete), cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// capture-pane command
	var capturePaneSession string
	var capturePaneWindow string
	var capturePaneScrollback bool
	var capturePaneANSI bool
	var capturePaneResolved bool
	var capturePanePalette []string
	var capturePaneLines int
	var capturePaneLastCommand bool
	var capturePaneJSON bool
	capturePaneCmd := &cobra.Command{
		Use:   "capture-pane",
		Short: "Capture the content of a pane",
		Long: `Capture the visible content (or scrollback history) of a terminal pane.

Output is written to stdout. By default captures the focused window's visible screen.
Use --scrollback to include the full scrollback history.
Use --lines to keep only the last N lines, which is how you read the tail of a
long scrollback without pulling all of it.
Use --ansi to preserve ANSI escape codes (colors, styles).
Use --resolved to rewrite ANSI index colours to 24-bit RGB, optionally against
--palette (16 hex colours of your theme, xterm defaults otherwise).
Use --last-command to read only what the last finished command printed. It
needs a shell that marks its commands with OSC 133, and it is plain text.

A capture from a session on another machine (-s host:session) is fenced as
untrusted content. With --ansi or --resolved, only colour and style codes are
kept from it. With --json the result carries host and "untrusted": true.`,
		Example: `  # Capture focused window
  tuios capture-pane

  # Capture specific window with scrollback
  tuios capture-pane -w mywindow --scrollback

  # Read the last 40 lines a build printed
  tuios capture-pane -w build --scrollback --lines 40

  # Capture with ANSI colors preserved
  tuios capture-pane --ansi

  # Capture with colours resolved to RGB against your theme palette
  tuios capture-pane --ansi --resolved --palette "#45475a,#f38ba8,#a6e3a1,#f9e2af,#89b4fa,#f5c2e7,#94e2d5,#bac2de,#585b70,#f38ba8,#a6e3a1,#f9e2af,#89b4fa,#f5c2e7,#94e2d5,#a6adc8"

  # Pipe to a file
  tuios capture-pane -w editor --scrollback > pane.txt

  # What the last command in the build pane printed, and nothing else
  tuios capture-pane -w build --last-command

  # Read pane 0 of session api on host build, as JSON
  tuios capture-pane -w build:api:0 --json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCapturePane(capturePaneSession, capturePaneWindow, capturePaneScrollback, capturePaneANSI, capturePaneResolved, capturePanePalette, capturePaneLines, capturePaneLastCommand, capturePaneJSON)
		},
	}
	capturePaneCmd.Flags().BoolVar(&capturePaneLastCommand, "last-command", false, "Capture only what the last finished command printed (needs OSC 133 shell integration)")
	capturePaneCmd.Flags().StringVarP(&capturePaneSession, "session", "s", "", "Target session")
	capturePaneCmd.Flags().StringVarP(&capturePaneWindow, "window", "w", "", "Target window by name or ID")
	capturePaneCmd.Flags().BoolVarP(&capturePaneScrollback, "scrollback", "S", false, "Include full scrollback history")
	capturePaneCmd.Flags().BoolVar(&capturePaneANSI, "ansi", false, "Preserve ANSI escape codes")
	capturePaneCmd.Flags().BoolVar(&capturePaneResolved, "resolved", false, "Rewrite ANSI index colours to 24-bit RGB")
	capturePaneCmd.Flags().StringSliceVar(&capturePanePalette, "palette", nil, "16 hex colours (#rrggbb) to resolve against (default: xterm)")
	capturePaneCmd.Flags().IntVar(&capturePaneLines, "lines", 0, "Keep only the last N lines (0 keeps all)")
	capturePaneCmd.Flags().BoolVar(&capturePaneJSON, "json", false, "Output result as JSON")
	_ = capturePaneCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	var runCommandSession string
	var runCommandList bool
	var runCommandJSON bool
	runCommandCmd := &cobra.Command{
		Use:   "run-command <command> [args...]",
		Short: "Execute a tape command in a running TUIOS session",
		Long: `Execute a tape command in a running TUIOS session.

This allows you to control TUIOS remotely by executing tape commands.
Use --list to see all available commands.
Use --json to get machine-readable output for scripting.

From inside a pane this needs the admin grant (see 'tuios pane-grants'),
which every pane holds under the default mode open. Prefer a verb where one
exists: get-window and list-windows read windows with the read grant.`,
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

Run 'tuios list-options' for every path, with its type, default and accepted
values. An [appearance] option also answers to its bare name, so border_style
and appearance.border_style are the same path.

  tuios set-config appearance.border_style rounded
  tuios set-config appearance.dockbar_position top`,
		Example: `  # Change dockbar position
  tuios set-config dockbar_position top

  # Change border style
  tuios set-config border_style rounded

  # Turn animations off
  tuios set-config motion none

  # Hide window buttons
  tuios set-config hide_window_buttons true`,
		Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			return runSetConfig(setConfigSession, args[0], args[1])
		},
	}
	setConfigCmd.Flags().StringVarP(&setConfigSession, "session", "s", "", "Target session (default: most recently active)")
	_ = setConfigCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	var getConfigSession string
	var getConfigJSON bool
	getConfigCmd := &cobra.Command{
		Use:   "get-config <path>",
		Short: "Read a configuration option from a running TUIOS session",
		Long: `Read a configuration option from a running TUIOS session. Options are
recorded in daemon-owned state, so this works whether or not a TUI client is
attached.

An option with no session override reads as its default, so a path that exists
always reads. --json also reports where the value came from: "session" for an
override set here, "default" for the built-in.

Run 'tuios list-options' to see every path.`,
		Example: `  # Read the border style
  tuios get-config border_style

  # Read it with its source and default
  tuios get-config appearance.sidebar.position --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runGetConfig(getConfigSession, args[0], getConfigJSON)
		},
	}
	getConfigCmd.Flags().BoolVar(&getConfigJSON, "json", false, "Output as JSON, with the value's source and default")
	var sendTextSession string
	var sendTextWindow string
	sendTextCmd := &cobra.Command{
		Use:   "send-text <text>",
		Short: "Write text verbatim to a pane",
		Long: `Write text straight to a pane's PTY with no key parsing at all.

Nothing in the argument is interpreted: spaces, quotes and punctuation arrive
as typed. End the text with a newline to run it as a command.`,
		Example: `  # Run a command in the focused pane
  tuios send-text 'go build ./...
'

  # The same thing without an embedded newline
  printf 'go build ./...\n' | xargs -0 tuios send-text -w build

  # Type without submitting
  tuios send-text -w build 'partial input'`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runSendText(sendTextSession, sendTextWindow, args[0])
		},
	}
	sendTextCmd.Flags().StringVarP(&sendTextSession, "session", "s", "", "Target session (default: most recently active)")
	sendTextCmd.Flags().StringVarP(&sendTextWindow, "window", "w", "", "Target window by name or ID (default: focused)")
	_ = sendTextCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	var newWindowSession string
	var newWindowWorkspace int
	var newWindowCwd string
	var newWindowNoFocus bool
	var newWindowHost string
	var newWindowGrants []string
	var newWindowJSON bool
	var newWindowPrintID bool
	newWindowCmd := &cobra.Command{
		Use:   "new-window [name] [command...]",
		Short: "Open a new window in a session",
		Long: `Open a new window in a running TUIOS session and print its id.

The window is created by the daemon whether or not a client is attached, so this
works on a detached session. Give it a name to address it later without holding
on to the id.

Arguments after the name are an argv the window runs as its own process instead
of a shell. Nothing re-parses them, so nothing needs quoting. The window closes
when the program exits. Put -- before a command that has flags of its own, or
tuios reads them as its own flags: tuios new-window log -- git log --oneline.

--workspace picks the workspace, --cwd sets the starting directory, and
--no-focus leaves the focus where it is.

The output is the short id and the name, "d6b97fe4  build". Either one is a
window target for -w. --print-id prints the full id alone, for a script:
id=$(tuios new-window build --print-id).

--host runs the window's process on another machine from the [hosts] table. The
window still belongs to this session and is drawn and sized here; only the
process is over there. There is no special mode to turn on: a session holding
one is an ordinary session with a window that happens to be elsewhere, so it
lists, scripts and restores like any other.

--grants says what the window's process may do through tuios: read, write,
fan, respond, admin, or none. Without it the window holds the default of
[agents.permissions]. See 'tuios pane-grants'.`,
		Example: `  # Open an unnamed window
  tuios new-window

  # Open a named window and run something in it
  tuios new-window build
  tuios send-text -w build 'go build ./...
'

  # Open a window whose process is the program itself, no shell in between
  tuios new-window htop /usr/bin/htop

  # A command with flags of its own goes after --
  tuios new-window log -- git log --oneline -20

  # Open a pane on workspace 2, in a directory, without taking the focus
  tuios new-window tests --workspace 2 --cwd /src/api --no-focus

  # Capture the new window's id for scripting
  id=$(tuios new-window docs --cwd ~/src/docs --no-focus --print-id)
  tuios new-window --json | jq -r .window_id

  # Open a window whose shell runs on another machine
  tuios new-window deploy --host build`,
		Args: cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			name := ""
			var command []string
			if len(args) > 0 {
				name = args[0]
				command = args[1:]
			}
			return runNewWindow(newWindowSession, name, newWindowWorkspace, newWindowCwd,
				!newWindowNoFocus, command, newWindowHost, newWindowGrants, newWindowJSON, newWindowPrintID)
		},
	}
	newWindowCmd.Flags().StringVarP(&newWindowSession, "session", "s", "", "Target session (default: most recently active)")
	newWindowCmd.Flags().IntVar(&newWindowWorkspace, "workspace", 0, "Workspace to open the window on (default: the current one)")
	newWindowCmd.Flags().StringVar(&newWindowCwd, "cwd", "", "Directory to start the shell in (default: the daemon's)")
	newWindowCmd.Flags().BoolVar(&newWindowNoFocus, "no-focus", false, "Leave the focus where it is")
	newWindowCmd.Flags().StringVar(&newWindowHost, "host", "", "Run the window's process on this machine from the [hosts] table (default: this machine)")
	newWindowCmd.Flags().StringSliceVar(&newWindowGrants, "grants", nil, "What the window's process may do through tuios, comma separated: read, write, fan, respond, admin, or none (default: [agents.permissions])")
	newWindowCmd.Flags().BoolVar(&newWindowJSON, "json", false, "Output result as JSON")
	newWindowCmd.Flags().BoolVar(&newWindowPrintID, "print-id", false, "Print only the new window's full id, for id=$(...)")
	newWindowCmd.MarkFlagsMutuallyExclusive("json", "print-id")
	_ = newWindowCmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	_ = newWindowCmd.RegisterFlagCompletionFunc("host", completeHostNames)
	_ = newWindowCmd.RegisterFlagCompletionFunc("grants", completeGrantNames)

	var popupSession string
	var popupWidth string
	var popupHeight string
	var popupName string
	var popupCwd string
	var popupWorkspace int
	var popupJSON bool
	var popupWait, popupCapture bool
	var popupTimeout int
	popupCmd := &cobra.Command{
		Use:   "popup -- <command> [args...]",
		Short: "Run a command in a floating pane that closes when it exits",
		Long: `Run a command in a floating pane centred over the layout, and print its id.

The pane closes when the command exits. Nothing re-parses the arguments after
--, so nothing needs quoting. This is how a picker becomes an overlay: run fzf,
gum or any other full-screen program in it.

Needs an attached client, because a popup is a thing on a screen. It is not
tiled, it is not in the window cycle, and it cannot be minimized.

The popup writes to its own screen, not to this command's output. With --wait
this command stays open until the popup's command exits, and exits with its
status. --capture-stdout (which implies --wait) also sends the command's
standard output here instead of into the popup: a picker such as fzf or gum
draws on the terminal and prints only the choice, so the choice is what this
command prints. Not on Windows.

--width and --height take cells or a percentage of the pane region. A size
larger than the region is cut down to the region. Neither has a short form: -w
selects a window everywhere else, and -h is help.`,
		Example: `  # Pick a file in a centred popup and use the answer
  file=$(tuios popup --capture-stdout -- fzf)

  # Wait for a popup and branch on its status
  tuios popup --wait -- gum confirm "Deploy?" && ./deploy.sh

  # Keep the answer in a file instead
  tuios popup -- sh -c 'ls | fzf > /tmp/pick'

  # Send the selection straight to the pane you came from
  tuios popup -- sh -c 'tuios send-text -w main "$(ls | fzf)"'

  # A small popup, in cells
  tuios popup --width 60 --height 20 -- gum choose one two three

  # Watch something, then press q to close it
  tuios popup --width 90% --height 80% -- htop

  # Capture the popup's id for scripting
  tuios popup --json -- fzf | jq -r .window_id`,
		Args: cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			return runPopup(popupOptions{
				session:   popupSession,
				name:      popupName,
				cwd:       popupCwd,
				width:     popupWidth,
				height:    popupHeight,
				workspace: popupWorkspace,
				command:   args,
				jsonOut:   popupJSON,
				wait:      popupWait || popupCapture,
				capture:   popupCapture,
				timeout:   popupTimeout,
			})
		},
	}
	popupCmd.Flags().BoolVar(&popupWait, "wait", false, "Stay open until the command exits, and exit with its status")
	popupCmd.Flags().BoolVar(&popupCapture, "capture-stdout", false, "Print the command's standard output here instead of in the popup (implies --wait)")
	popupCmd.Flags().IntVar(&popupTimeout, "timeout", 0, "With --wait: milliseconds to wait (default: as long as the popup is open)")
	popupCmd.Flags().StringVarP(&popupSession, "session", "s", "", "Target session (default: most recently active)")
	// Spelled out, with no shorthands. -w is the window selector in every other
	// tuios command and -h is cobra's help, so both of the short forms a reader
	// would reach for already mean something else here.
	popupCmd.Flags().StringVar(&popupWidth, "width", "", "Popup width in cells or percent (default: 80%)")
	popupCmd.Flags().StringVar(&popupHeight, "height", "", "Popup height in cells or percent (default: 60%)")
	popupCmd.Flags().StringVar(&popupName, "name", "", "Name for the popup")
	popupCmd.Flags().StringVar(&popupCwd, "cwd", "", "Directory to run the command in (default: the daemon's)")
	popupCmd.Flags().IntVar(&popupWorkspace, "workspace", 0, "Workspace to open the popup on (default: the current one)")
	popupCmd.Flags().BoolVar(&popupJSON, "json", false, "Output result as JSON")
	_ = popupCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	var splitWindowSession string
	var splitWindowWindow string
	var splitWindowName string
	var splitWindowJSON bool
	splitWindowCmd := &cobra.Command{
		Use:   "split-window <horizontal|vertical>",
		Short: "Divide a pane and open a new one beside it",
		Long: `Split a pane along an axis and print the id of the new pane.

Needs an attached client and tiling on. The split goes through the renderer's
own path, so the new pane lands in the layout exactly as one opened from the
keyboard does.`,
		Example: `  # Split the focused pane left/right
  tuios split-window vertical

  # Split a named pane and name what comes out of it
  tuios split-window horizontal -w build --name logs

  # Capture the new pane's id for scripting
  tuios split-window vertical --json | jq -r .window_id`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"horizontal", "vertical"},
		RunE: func(_ *cobra.Command, args []string) error {
			return runSplitWindow(splitWindowSession, splitWindowWindow, args[0],
				splitWindowName, splitWindowJSON)
		},
	}
	splitWindowCmd.Flags().StringVarP(&splitWindowSession, "session", "s", "", "Target session (default: most recently active)")
	splitWindowCmd.Flags().StringVarP(&splitWindowWindow, "window", "w", "", "Pane to split by name or ID (default: focused)")
	splitWindowCmd.Flags().StringVar(&splitWindowName, "name", "", "Name for the new pane")
	splitWindowCmd.Flags().BoolVar(&splitWindowJSON, "json", false, "Output result as JSON")
	_ = splitWindowCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	var focusWindowSession string
	var focusWindowRelative string
	var focusWindowDirection string
	var focusWindowJSON bool
	focusWindowCmd := &cobra.Command{
		Use:   "focus-window [window]",
		Short: "Move the focus to a pane",
		Long: `Move the focus to a pane, naming it by id or name, by position, or by
direction, and print the pane that ended up with it.

Pass exactly one of the window argument, --relative or --direction. Naming a
window switches to its workspace. --direction needs an attached client. The
other two forms work on a detached session.`,
		Example: `  # Focus a pane by name
  tuios focus-window build

  # Cycle through the panes on this workspace
  tuios focus-window --relative next

  # Focus the pane to the left
  tuios focus-window --direction left`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			window := ""
			if len(args) > 0 {
				window = args[0]
			}
			return runFocusWindow(focusWindowSession, window, focusWindowRelative,
				focusWindowDirection, focusWindowJSON)
		},
	}
	focusWindowCmd.Flags().StringVarP(&focusWindowSession, "session", "s", "", "Target session (default: most recently active)")
	focusWindowCmd.Flags().StringVar(&focusWindowRelative, "relative", "", "Focus the next or prev window on this workspace")
	focusWindowCmd.Flags().StringVar(&focusWindowDirection, "direction", "", "Focus the neighbouring pane: left, right, up or down")
	focusWindowCmd.Flags().BoolVar(&focusWindowJSON, "json", false, "Output result as JSON")
	_ = focusWindowCmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	_ = focusWindowCmd.RegisterFlagCompletionFunc("relative",
		fixedCompletions("next", "prev"))
	_ = focusWindowCmd.RegisterFlagCompletionFunc("direction",
		fixedCompletions("left", "right", "up", "down"))

	var moveWindowSession string
	var moveWindowWindow string
	var moveWindowFollow bool
	var moveWindowJSON bool
	moveWindowCmd := &cobra.Command{
		Use:   "move-window <workspace>",
		Short: "Move a window to another workspace",
		Long: `Move a window to another workspace and report where it came from.

Works on a detached session. Pass --follow to switch to that workspace after
the move instead of staying put.`,
		Example: `  # Move the focused window to workspace 3
  tuios move-window 3

  # Move a named window and go with it
  tuios move-window 2 -w build --follow`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			workspace, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("workspace must be a number, got %q", args[0])
			}
			return runMoveWindow(moveWindowSession, moveWindowWindow, workspace,
				moveWindowFollow, moveWindowJSON)
		},
	}
	moveWindowCmd.Flags().StringVarP(&moveWindowSession, "session", "s", "", "Target session (default: most recently active)")
	moveWindowCmd.Flags().StringVarP(&moveWindowWindow, "window", "w", "", "Window to move by name or ID (default: focused)")
	moveWindowCmd.Flags().BoolVar(&moveWindowFollow, "follow", false, "Switch to that workspace after moving")
	moveWindowCmd.Flags().BoolVar(&moveWindowJSON, "json", false, "Output result as JSON")
	_ = moveWindowCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	var setWindowSession string
	var setWindowWindow string
	var setWindowName string
	var setWindowMinimize bool
	var setWindowRestore bool
	var setWindowJSON bool
	setWindowCmd := &cobra.Command{
		Use:   "set-window",
		Short: "Rename a window or minimize it",
		Long: `Rename a window, or minimize and restore it. Pass only the flags to
change. Anything left out is untouched.

--name "" clears the custom name, so the window falls back to whatever its shell
sets as the title.`,
		Example: `  # Rename the focused window
  tuios set-window --name "api tests"

  # Clear a name and go back to the shell's title
  tuios set-window -w build --name ""

  # Minimize a window, then bring it back
  tuios set-window -w build --minimize
  tuios set-window -w build --restore`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if setWindowMinimize && setWindowRestore {
				return fmt.Errorf("--minimize and --restore ask for opposite things. Pass one")
			}
			// An unset flag has to stay unset rather than send its zero value:
			// --name "" is a request to clear the name, which is not the same as
			// not mentioning the name at all.
			var name *string
			if cmd.Flags().Changed("name") {
				name = &setWindowName
			}
			var minimized *bool
			if setWindowMinimize || setWindowRestore {
				minimized = &setWindowMinimize
			}
			return runSetWindow(setWindowSession, setWindowWindow, name, minimized, setWindowJSON)
		},
	}
	setWindowCmd.Flags().StringVarP(&setWindowSession, "session", "s", "", "Target session (default: most recently active)")
	setWindowCmd.Flags().StringVarP(&setWindowWindow, "window", "w", "", "Window to change by name or ID (default: focused)")
	setWindowCmd.Flags().StringVar(&setWindowName, "name", "", "New name, or \"\" to clear it")
	setWindowCmd.Flags().BoolVar(&setWindowMinimize, "minimize", false, "Minimize the window")
	setWindowCmd.Flags().BoolVar(&setWindowRestore, "restore", false, "Restore the window")
	setWindowCmd.Flags().BoolVar(&setWindowJSON, "json", false, "Output result as JSON")
	_ = setWindowCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	var selectWorkspaceSession string
	var selectWorkspaceJSON bool
	selectWorkspaceCmd := &cobra.Command{
		Use:   "select-workspace <workspace>",
		Short: "Show a workspace",
		Long: `Show a workspace, the way the workspace keybindings do.

This changes which workspace is displayed. To label one use
'tuios set-workspace-name', and to move a window onto one use
'tuios move-window'.`,
		Example: `  # Show workspace 2
  tuios select-workspace 2

  # Show it in a named session
  tuios select-workspace 2 -s work`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			workspace, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("workspace must be a number, got %q", args[0])
			}
			return runSelectWorkspace(selectWorkspaceSession, workspace, selectWorkspaceJSON)
		},
	}
	selectWorkspaceCmd.Flags().StringVarP(&selectWorkspaceSession, "session", "s", "", "Target session (default: most recently active)")
	selectWorkspaceCmd.Flags().BoolVar(&selectWorkspaceJSON, "json", false, "Output result as JSON")
	_ = selectWorkspaceCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	var listWorkspacesSession string
	var listWorkspacesJSON bool
	listWorkspacesCmd := &cobra.Command{
		Use:   "list-workspaces",
		Short: "List the workspaces in a session",
		Long: `List every workspace with its name, how many windows it holds, and which one
is showing.`,
		Example: `  # List the workspaces
  tuios list-workspaces

  # Find the empty ones
  tuios list-workspaces --json | jq '.workspaces[] | select(.window_count == 0)'`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runListWorkspaces(listWorkspacesSession, listWorkspacesJSON)
		},
	}
	listWorkspacesCmd.Flags().StringVarP(&listWorkspacesSession, "session", "s", "", "Target session (default: most recently active)")
	listWorkspacesCmd.Flags().BoolVar(&listWorkspacesJSON, "json", false, "Output as JSON")
	_ = listWorkspacesCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	var setLayoutSession string
	var setLayoutTiling string
	var setLayoutEqualize bool
	var setLayoutRotate bool
	var setLayoutMasterPosition string
	var setLayoutMasters int
	var setLayoutJSON bool
	setLayoutCmd := &cobra.Command{
		Use:   "set-layout",
		Short: "Turn tiling on or off, tidy the splits, and shape the master-stack layout",
		Long: `Turn tiling on or off, even out the split ratios, and flip the axis of the
split holding the focused pane.

--master-position and --masters shape the master-stack layout of the current
workspace. The workspace keeps them until you change them again.

Needs an attached client. Tiling is applied first, because equalize and rotate
only mean something while the panes are tiled.`,
		Example: `  # Tile the panes
  tuios set-layout --tiling true

  # Give every pane the same share of the screen
  tuios set-layout --equalize

  # Flip the split holding the focused pane
  tuios set-layout --rotate

  # Put the master pane in the center, with the stack on both sides
  tuios set-layout --master-position center

  # Use two master panes
  tuios set-layout --masters 2`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var tiling *bool
			if cmd.Flags().Changed("tiling") {
				parsed, err := strconv.ParseBool(setLayoutTiling)
				if err != nil {
					return fmt.Errorf("--tiling takes true or false, got %q", setLayoutTiling)
				}
				tiling = &parsed
			}
			if err := checkSetLayoutMaster(setLayoutMasterPosition, setLayoutMasters, cmd.Flags().Changed("masters")); err != nil {
				return err
			}
			return runSetLayout(setLayoutSession, tiling, setLayoutEqualize,
				setLayoutRotate, setLayoutMasterPosition, setLayoutMasters, setLayoutJSON)
		},
	}
	setLayoutCmd.Flags().StringVarP(&setLayoutSession, "session", "s", "", "Target session (default: most recently active)")
	setLayoutCmd.Flags().StringVar(&setLayoutTiling, "tiling", "", "Tile the panes automatically: true or false")
	setLayoutCmd.Flags().BoolVar(&setLayoutEqualize, "equalize", false, "Reset every split ratio so the panes share the space evenly")
	setLayoutCmd.Flags().BoolVar(&setLayoutRotate, "rotate", false, "Flip the axis of the split holding the focused pane")
	setLayoutCmd.Flags().StringVar(&setLayoutMasterPosition, "master-position", "", "Side the master panes take: left, right, top, bottom or center")
	setLayoutCmd.Flags().IntVar(&setLayoutMasters, "masters", 0, "How many panes are master panes, 1 to 9")
	setLayoutCmd.Flags().BoolVar(&setLayoutJSON, "json", false, "Output result as JSON")
	_ = setLayoutCmd.RegisterFlagCompletionFunc("master-position", fixedCompletions(config.MasterPositions...))
	_ = setLayoutCmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	_ = setLayoutCmd.RegisterFlagCompletionFunc("tiling", fixedCompletions("true", "false"))

	var listOptionsSession string
	var listOptionsSection string
	var listOptionsJSON bool
	var listOptionsSearch string
	listOptionsCmd := &cobra.Command{
		Use:   "list-options [prefix]",
		Short: "List every settable configuration option",
		Long: `List every configuration path 'tuios set-config' accepts, with its type,
default, accepted values and description, grouped by section.

Use it to find an option path instead of guessing one: a path that does not
exist is refused, never silently recorded. Pass a path prefix to narrow the
list, or --section to keep one group. Where this session carries an override,
the override is shown beside the default.`,
		Example: `  # Everything that can be set
  tuios list-options

  # One group
  tuios list-options --section sidebar

  # Everything under a path
  tuios list-options appearance.sidebar.

  # Search by name, value or description, best match first
  tuios list-options --search "pane bg"

  # Machine-readable, for an agent or a script
  tuios list-options --json | jq -r '.options[].path'`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			prefix := ""
			if len(args) > 0 {
				prefix = args[0]
			}
			return runListOptions(listOptionsSession, listOptionsSection, prefix, listOptionsSearch, listOptionsJSON)
		},
	}
	listOptionsCmd.Flags().StringVar(&listOptionsSearch, "search", "", "Fuzzy search the paths, values and descriptions, best match first")
	listOptionsCmd.Flags().StringVarP(&listOptionsSession, "session", "s", "", "Target session (default: most recently active)")
	listOptionsCmd.Flags().StringVar(&listOptionsSection, "section", "", "Only options in this group, e.g. sidebar or dock")
	listOptionsCmd.Flags().BoolVar(&listOptionsJSON, "json", false, "Output as JSON")
	_ = listOptionsCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	var listThemesSession string
	var listThemesFilter string
	var listThemesJSON bool
	listThemesCmd := &cobra.Command{
		Use:   "list-themes [theme]",
		Short: "List the themes, and describe one",
		Long: `List every registered theme and, given a name, print its colours with the
contrast each one measures against that theme's own background. The contrast
says whether the palette is legible before anyone has to look at it.

Writing <id>.json in the themes directory registers that theme. The directory
is re-read on every call, so a theme written a moment ago can be selected
without a restart.`,
		Example: `  # List the themes matching a filter
  tuios list-themes --filter catppuccin

  # Show a theme's colours and contrast
  tuios list-themes catppuccin_mocha

  # Show the active theme
  tuios list-themes --json | jq -r .active

  # List the colours that fail contrast on their own background
  tuios list-themes catppuccin_latte --json | jq -r '.palette.illegible[]'`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			return runListThemes(listThemesSession, name, listThemesFilter, listThemesJSON)
		},
	}
	listThemesCmd.Flags().StringVarP(&listThemesSession, "session", "s", "", "Target session (default: most recently active)")
	listThemesCmd.Flags().StringVar(&listThemesFilter, "filter", "", "Only ids containing this, e.g. gruvbox")
	listThemesCmd.Flags().BoolVar(&listThemesJSON, "json", false, "Output as JSON")
	_ = listThemesCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	var listGlyphsSession string
	var listGlyphsJSON bool
	listGlyphsCmd := &cobra.Command{
		Use:   "list-glyphs [set]",
		Short: "List the glyph sets, and describe one",
		Long: `List every glyph set and, given a name, print role by role what the set says
and what would actually be drawn.

A glyph set is the shape half of a rice, the way a theme is the colour half: it
says which corner the border turns, what the window controls are pictures of,
what a rule is drawn with and which mark the rail wears. Like a theme its value
is a name from an open set rather than a setting with a closed list, so this is
how to find one rather than guess it.

The two columns are different on purpose. A set states only the roles it
changes, and a role whose glyph is the wrong width for the slot it lands in is
dropped back to the default with nothing on screen to say so, because the
alternative is a window control the pointer no longer lands on. The second
column is what draws.

Writing <id>.json in the glyphs directory registers that set; the directory is
re-read on every call, so a set authored a moment ago can be selected without a
restart. Give it "inherits" to start from a built-in and change one mark.`,
		Example: `  # What sets are there, and what roles can a set name
  tuios list-glyphs

  # What does this set actually draw
  tuios list-glyphs heavy

  # Select one
  tuios set-config appearance.glyphs heavy

  # The roles a set asked for and did not get
  tuios list-glyphs mine --json | jq -r '.problems[]?'`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			return runListGlyphs(listGlyphsSession, name, listGlyphsJSON)
		},
	}
	listGlyphsCmd.Flags().StringVarP(&listGlyphsSession, "session", "s", "", "Target session (default: most recently active)")
	listGlyphsCmd.Flags().BoolVar(&listGlyphsJSON, "json", false, "Output as JSON")
	_ = listGlyphsCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	var listDockComponentsSession string
	var listDockComponentsJSON bool
	listDockComponentsCmd := &cobra.Command{
		Use:   "list-dock-components",
		Short: "List the dock's components and what each one last did",
		Long: `List every component the dock has placed, in draw order: its name, which side
it is on, whether it is a built-in or one of yours, how it refreshes, what its
cell currently reads, and what its command last did.

The last three are the whole debugging story for a component that is not
drawing. A component whose command fails is hidden rather than left showing a
value it can no longer produce, so an absent cell here carries the exit code and
the error that produced it.

The dock is composed by the attached client, so this needs one attached.`,
		Example: `  # What is the bar made of
  tuios list-dock-components

  # Why is my cell not showing
  tuios list-dock-components --json | jq '.components[] | select(.source=="custom")'`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return queryDockComponents(listDockComponentsSession, listDockComponentsJSON)
		},
	}
	listDockComponentsCmd.Flags().StringVarP(&listDockComponentsSession, "session", "s", "", "Target session (default: most recently active)")
	listDockComponentsCmd.Flags().BoolVar(&listDockComponentsJSON, "json", false, "Output as JSON")
	_ = listDockComponentsCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	var listHooksSession string
	var listHooksEvent string
	var listHooksJSON bool
	listHooksCmd := &cobra.Command{
		Use:   "list-hooks",
		Short: "List the hooks and what each one last did",
		Long: `List every hook command in your config, and what each one last did: how many
times it ran, its last exit code, when it last ran and its last error.

A hook that never fires is the commonest complaint and it used to have no
answer, because a hook ran with its output discarded and its error dropped.
Zero runs means the event never happened, so check the event name. A non-zero
exit means the command ran and failed, and the error says why.

The SIDE column says which process runs the hook. The daemon runs the hooks for
the facts it owns, so they fire with nobody attached. A client runs the ones
that need its terminal, so they are only listed while a client is attached.`,
		Example: `  # What is registered, and did it run
  tuios list-hooks

  # Only one event
  tuios list-hooks --event after-agent-state

  # Every hook that failed
  tuios list-hooks --json | jq '.hooks[] | select(.last_error != "")'`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runListHooks(listHooksSession, listHooksEvent, listHooksJSON)
		},
	}
	listHooksCmd.Flags().StringVarP(&listHooksSession, "session", "s", "", "Target session (default: most recently active)")
	listHooksCmd.Flags().StringVar(&listHooksEvent, "event", "", "Only the hooks on this event")
	listHooksCmd.Flags().BoolVar(&listHooksJSON, "json", false, "Output as JSON")
	_ = listHooksCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	var refreshDockSession string
	var refreshDockJSON bool
	refreshDockCmd := &cobra.Command{
		Use:   "refresh-dock [component]",
		Short: "Run a dock component again now",
		Long: `Re-run a dock component immediately, whatever its refresh mode says, and clear
a give-up so a component whose script has just been fixed starts working again
without restarting the session.

With no argument every component is re-run. This is what makes a component
scriptable: a hook, a cron entry or an agent can push a new value the moment the
thing it reports has changed, instead of the dock polling for it.`,
		Example: `  # After the script it reads has changed
  tuios refresh-dock agents

  # From a hook
  #   [hooks]
  #   after-agent-state = "tuios refresh-dock agents"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			return runRefreshDock(refreshDockSession, name, refreshDockJSON)
		},
	}
	refreshDockCmd.Flags().StringVarP(&refreshDockSession, "session", "s", "", "Target session (default: most recently active)")
	refreshDockCmd.Flags().BoolVar(&refreshDockJSON, "json", false, "Output as JSON")
	_ = refreshDockCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	var importThemeName string
	var importThemeJSON bool
	importThemeCmd := &cobra.Command{
		Use:   "import-theme <file>",
		Short: "Convert a terminal colour scheme into a tuios theme",
		Long: `Read a kitty, ghostty, alacritty or wezterm colour scheme and write it into
the tuios themes directory as a theme you can select.

The format is read from the file's content, not its name. A scheme that sets
only some of the 20 colours imports those. The rest fall back to the xterm
defaults.

The theme is registered as it is written, so the name it prints can be selected
straight away without a restart.`,
		Example: `  # A kitty theme
  tuios import-theme ~/.config/kitty/current-theme.conf

  # Name it something other than the file
  tuios import-theme ~/.config/ghostty/config --name mine

  # Import it and select it
  tuios import-theme ~/gruvbox.toml --name gruvbox
  tuios set-config appearance.theme gruvbox`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runImportTheme(args[0], importThemeName, importThemeJSON)
		},
	}
	importThemeCmd.Flags().StringVar(&importThemeName, "name", "", "Theme id to write it under (default: the file's name)")
	importThemeCmd.Flags().BoolVar(&importThemeJSON, "json", false, "Output as JSON")

	var waitForSession string
	var waitForWindow string
	var waitForPattern string
	var waitForUntil string
	var waitForIdle int
	var waitForThread uint64
	var waitForTimeout int
	var waitForAnySession bool
	var waitForSelect string
	var waitForEvery bool
	var waitForJSON bool
	var waitForCommandSeq uint64
	waitForCmd := &cobra.Command{
		Use:   "wait-for <condition>",
		Short: "Block until a condition matches",
		Long: `Block until the daemon reports that a condition matched, then exit 0.

Conditions:
  session-exists  the named session is present
  window-output   the window printed something matching --pattern
  window-exit     the window's shell exited
  window-idle     the window printed nothing for --idle milliseconds
  agent-state     an agent reached one of the --until states; without --window,
                  any agent pane in the session matches, with --any-session,
                  any agent pane in any session, and with --select, any pane
                  the selector matches (every one of them with --every)
  agent-message   mail arrived. With --window it matches unread mail for that
                  inbox, including mail queued before the wait started; without
                  one, anything said in the session after it started. --thread
                  narrows either shape to one conversation
  command-finished  a shell that marks its commands with OSC 133 finished
                  one. With --window, that pane's next command, or with
                  --command-seq N, the first after N finished commands, which
                  matches at once when it already happened. Without a window,
                  any pane in the session. Prints the exit code

The daemon watches its own events, so there is no need to poll with
capture-pane and sleep. A condition that does not match before --timeout exits
non-zero with the timeout error.`,
		Example: `  # Wait for a build to print its marker
  tuios wait-for window-output -w build --pattern 'BUILD OK'

  # Wait for a pane to go quiet for two seconds
  tuios wait-for window-idle -w build --idle 2000

  # Wait for a command's shell to exit
  tuios wait-for window-exit -w build --timeout 600000

  # Wait until any agent in the session is waiting on a human
  tuios wait-for agent-state -s work --until needs_input

  # Wait until an agent in any session is waiting on a human
  tuios wait-for agent-state --any-session --until needs_input

  # Wait until every agent of a fan-out has finished its turn
  tuios wait-for agent-state --select 'group:fan/add-retry' --until idle,done --every --timeout 3600000

  # Block until another agent leaves me a message
  tuios wait-for agent-message -s work -w "$TUIOS_PANE_ID" --timeout 600000

  # Block until someone answers the message I just sent
  tuios wait-for agent-message -s work -w "$TUIOS_PANE_ID" --thread 12

  # Wait for the command after the 4th in the build pane to finish
  tuios wait-for command-finished -w build --command-seq 4 --timeout 600000`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: session.WaitConditionNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			var commandSeq *uint64
			if cmd.Flags().Changed("command-seq") {
				commandSeq = &waitForCommandSeq
			}
			return runWaitFor(waitForSession, waitForWindow, args[0], waitForPattern,
				waitForUntil, waitForIdle, waitForThread, waitForTimeout, waitForAnySession, waitForSelect, waitForEvery, waitForJSON, commandSeq)
		},
	}
	waitForCmd.Flags().Uint64Var(&waitForCommandSeq, "command-seq", 0, "For command-finished: match once the pane has finished more than this many commands")
	waitForCmd.Flags().StringVar(&waitForSelect, "select", "", "For agent-state: watch the agent panes a selector matches, in every session. Takes no --session, --window or --any-session")
	waitForCmd.Flags().BoolVar(&waitForEvery, "every", false, "With --select: wait until every matched pane is in one of the --until states, not only the first")
	waitForCmd.Flags().StringVarP(&waitForSession, "session", "s", "", "Target session (default: most recently active)")
	waitForCmd.Flags().StringVarP(&waitForWindow, "window", "w", "", "Target window by name or ID (default: focused; agent-state: any window)")
	waitForCmd.Flags().StringVar(&waitForPattern, "pattern", "", "Regular expression to match, required by window-output")
	waitForCmd.Flags().StringVar(&waitForUntil, "until", "", "Agent state(s) to wait for, comma-separated, required by agent-state")
	waitForCmd.Flags().IntVar(&waitForIdle, "idle", 0, "Milliseconds of silence that count as idle, for window-idle (default: 500)")
	waitForCmd.Flags().Uint64Var(&waitForThread, "thread", 0, "Only match a message in this thread, for agent-message. Pass any message id in it")
	waitForCmd.Flags().IntVar(&waitForTimeout, "timeout", 30000, "Milliseconds to wait before giving up")
	waitForCmd.Flags().BoolVar(&waitForAnySession, "any-session", false, "For agent-state: watch every session on the daemon. Takes no --session or --window")
	waitForCmd.Flags().BoolVar(&waitForJSON, "json", false, "Output result as JSON")
	_ = waitForCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	var setSessionNameSession string
	setSessionNameCmd := &cobra.Command{
		Use:   "set-session-name [name]",
		Short: "Set a session's display name",
		Long: `Set the label a session shows in the sidebar and the dock.

The session keeps its own name for addressing, persistence and TUIOS_SESSION, so
a script that targets it by name keeps working. Pass no name to clear the label.
To change the name itself, use 'tuios rename-session'.`,
		Example: `  # Label the current session
  tuios set-session-name "Payments API"

  # Label a specific session
  tuios set-session-name -s work "Payments API"

  # Clear the label
  tuios set-session-name`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			return runSetSessionName(setSessionNameSession, name)
		},
	}
	setSessionNameCmd.Flags().StringVarP(&setSessionNameSession, "session", "s", "", "Target session (default: most recently active)")
	_ = setSessionNameCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	var renameSessionSession string
	renameSessionCmd := &cobra.Command{
		Use:   "rename-session [session] <new-name>",
		Short: "Rename a session",
		Long: `Rename a session.

The new name is what 'tuios ls' shows and what attach and -s take. New panes
get it as TUIOS_SESSION. Panes that already run keep the old name, and tuios
commands from them still reach the session. Attach and kill-session by the old
name fail and name the new one. The name of a running or saved session is
refused.

With one argument, the session is the one given by -s, or else the session of
the pane you run it in.`,
		Example: `  # Rename the session "test" to "work"
  tuios rename-session test work

  # Rename the session of this pane
  tuios rename-session work`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			target, name := renameSessionSession, args[len(args)-1]
			if len(args) == 2 {
				target = args[0]
			}
			return runRenameSession(target, name)
		},
	}
	renameSessionCmd.Flags().StringVarP(&renameSessionSession, "session", "s", "", "Session to rename (default: the session of this pane)")
	_ = renameSessionCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	var setSessionAccentSession string
	setSessionAccentCmd := &cobra.Command{
		Use:   "set-session-accent [accent]",
		Short: "Set a session's accent",
		Long: `Set a session's accent colour. Every attached client shares it, and it
survives a reattach. Pass no accent to clear it.`,
		Example: `  # Accent the current session
  tuios set-session-accent cyan

  # Clear the accent
  tuios set-session-accent`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			accent := ""
			if len(args) > 0 {
				accent = args[0]
			}
			return runSetSessionAccent(setSessionAccentSession, accent)
		},
	}
	setSessionAccentCmd.Flags().StringVarP(&setSessionAccentSession, "session", "s", "", "Target session (default: most recently active)")
	_ = setSessionAccentCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	var setWorkspaceNameSession string
	setWorkspaceNameCmd := &cobra.Command{
		Use:   "set-workspace-name <workspace> [name]",
		Short: "Name a workspace",
		Long: `Name a workspace so the dock and the sidebar show the label instead of the
number. The number stays the workspace's identity. Pass no name to clear it.`,
		Example: `  # Name workspace 2
  tuios set-workspace-name 2 review

  # Clear the name
  tuios set-workspace-name 2`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			workspace, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("workspace must be a number, got %q", args[0])
			}
			name := ""
			if len(args) > 1 {
				name = args[1]
			}
			return runSetWorkspaceName(setWorkspaceNameSession, workspace, name)
		},
	}
	setWorkspaceNameCmd.Flags().StringVarP(&setWorkspaceNameSession, "session", "s", "", "Target session (default: most recently active)")
	_ = setWorkspaceNameCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	getConfigCmd.Flags().StringVarP(&getConfigSession, "session", "s", "", "Target session (default: most recently active)")
	_ = getConfigCmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	getConfigCmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return getConfigPathCompletions(toComplete), cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

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

	// Logs command for debugging daemon
	var logsCount int
	var logsClear bool
	var logsFollow bool
	logsCmd := &cobra.Command{
		Use:   "logs",
		Short: "View daemon logs",
		Long: `View recent log entries from the TUIOS daemon.

This is useful for debugging issues with remote commands, sessions, and PTY handling.
Logs are stored in a ring buffer (1000 entries by default).

The ring buffer stops at the daemon. The daemon also appends errors and basic
events to $XDG_STATE_HOME/tuios/daemon.log, so a crash leaves a record.

Raise the detail with 'tuios set-config daemon.log_level messages'. The daemon
applies it at once. Levels verbose and trace also record pane content, window
titles and paths.`,
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
		RunE: func(cmd *cobra.Command, _ []string) error {
			if all, _ := cmd.Flags().GetBool("all"); all {
				logsCount = 0
			}
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
Use --json for machine-readable output.

It is a read: from inside a pane it needs only the read grant, on the pane's
own session and its fan group.`,
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
			return queryWindow(getWindowSession, args, getWindowJSON)
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

Shows mode, workspace, tiling state, size and window count.
Use --json for machine-readable output.

The theme is not listed here. It is a session option. Read it with
'tuios list-themes'.`,
		Example: `  # Get session info (table format)
  tuios session-info

  # Get as JSON for scripting
  tuios session-info --json

  # Use with jq to check if tiling is enabled ("tiling" or "floating")
  tuios session-info --json | jq -r '.tiling_mode'`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return querySession(sessionInfoSession, sessionInfoJSON)
		},
	}
	sessionInfoCmd.Flags().StringVarP(&sessionInfoSession, "session", "s", "", "Target session (default: most recently active)")
	sessionInfoCmd.Flags().BoolVar(&sessionInfoJSON, "json", false, "Output as JSON")
	_ = sessionInfoCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	var listVerbsJSON bool
	listVerbsCmd := &cobra.Command{
		Use:   "list-verbs [verb]",
		Short: "List the control-protocol verbs the daemon supports",
		Long: `List every verb the daemon's JSON control protocol supports, with its
parameter schema and example requests.

This is the discovery entry point for scripting and for agents driving TUIOS:
it reports the protocol version, every verb and parameter, the stable error
codes, and the request/response envelope shape, so no documentation is needed
to drive the control plane.

Name a verb to describe only that verb.`,
		Example: `  # Every verb with its parameters
  tuios list-verbs

  # Just one verb
  tuios list-verbs capture-pane

  # Machine-readable, for an agent or a script
  tuios list-verbs --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			verb := ""
			if len(args) > 0 {
				verb = args[0]
			}
			return runListVerbs(verb, listVerbsJSON)
		},
	}
	listVerbsCmd.Flags().BoolVar(&listVerbsJSON, "json", false, "Output as JSON")

	// Layout template commands
	layoutCmd := &cobra.Command{
		Use:   "layout",
		Short: "Manage layout templates",
		Long:  `Save, load, list, and delete window layout templates`,
	}
	layoutListCmd := &cobra.Command{
		Use:   "list",
		Short: "List saved layout templates",
		RunE: func(_ *cobra.Command, _ []string) error {
			templates, err := app.LoadLayoutTemplates()
			if err != nil {
				return err
			}
			if len(templates) == 0 {
				fmt.Println(layoutSaveHint(loadKeybindConfig()))
				return nil
			}
			for _, t := range templates {
				windows := len(t.Windows)
				tiling := "free-float"
				if t.AutoTiling {
					tiling = "tiled"
				}
				fmt.Printf("  %-20s  %d windows  %s  %s\n", t.Name, windows, tiling, t.CreatedAt.Format("2006-01-02 15:04"))
			}
			return nil
		},
	}
	layoutDeleteCmd := &cobra.Command{
		Use:   "delete [name]",
		Short: "Delete a layout template",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := app.DeleteLayoutTemplate(args[0]); err != nil {
				return err
			}
			fmt.Printf("Deleted layout '%s'\n", args[0])
			return nil
		},
	}
	layoutDirCmd := &cobra.Command{
		Use:   "dir",
		Short: "Print layout templates directory path",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Println(app.GetTemplatesDir())
		},
	}
	layoutExportCmd := &cobra.Command{
		Use:   "export [name]",
		Short: "Export a layout template as a tape script",
		Long: `Print a saved layout template as a tape script on stdout. Run the script
with 'tuios tape play', or with 'tuios tape exec' against a running session.

The template itself is a JSON file in the directory 'tuios layout dir' prints.`,
		Example: `  tuios layout export dev-layout > dev-layout.tape
  tuios tape play dev-layout.tape`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			templates, err := app.LoadLayoutTemplates()
			if err != nil {
				return err
			}
			for _, t := range templates {
				if t.Name == args[0] {
					fmt.Print(app.GenerateTapeScript(t))
					return nil
				}
			}
			return fmt.Errorf("layout '%s' not found", args[0])
		},
	}
	layoutCmd.AddCommand(layoutListCmd, layoutDeleteCmd, layoutDirCmd, layoutExportCmd)

	// The interface flags ride only the commands that draw the interface. They
	// were persistent on the root once, which buried a read command's few real
	// flags under twenty appearance ones in its help.
	registerInterfaceFlags(rootCmd, attachCmd, newCmd)

	rootCmd.AddCommand(configCmd, keybindsCmd, layoutCmd)
	rootCmd.AddCommand(attachCmd, newCmd, lsCmd, killSessionCmd, resurrectCmd)
	rootCmd.AddCommand(startDaemonCmd, daemonCmd, killDaemonCmd)
	rootCmd.AddCommand(sendKeysCmd, runCommandCmd, setConfigCmd, getConfigCmd, logsCmd, capturePaneCmd)
	rootCmd.AddCommand(sendTextCmd, newWindowCmd, waitForCmd)
	rootCmd.AddCommand(renameSessionCmd, setSessionNameCmd, setSessionAccentCmd, setWorkspaceNameCmd)
	rootCmd.AddCommand(splitWindowCmd, popupCmd, focusWindowCmd, moveWindowCmd, setWindowCmd, newPiPCommand())
	rootCmd.AddCommand(selectWorkspaceCmd, listWorkspacesCmd, setLayoutCmd)
	rootCmd.AddCommand(listWindowsCmd, getWindowCmd, sessionInfoCmd, listVerbsCmd, listOptionsCmd, listThemesCmd, listGlyphsCmd, importThemeCmd)
	rootCmd.AddCommand(listDockComponentsCmd, refreshDockCmd, listHooksCmd)
	rootCmd.AddCommand(newPaneGrantsCommand(), newSetPaneGrantsCommand())
	rootCmd.AddCommand(newXpanesCommand(), newCloseWorkspaceCommand())

	// The commands tuios-slim leaves out. The full build adds them here; the
	// slim build adds a stub for each name that says it is not in this build.
	addFeatureCommands(rootCmd)

	return rootCmd
}

// registerInterfaceFlags registers the appearance and interface flags on each
// command that renders the TUI: the bare root, attach, new, ssh, and tape
// playback. Every registration binds the same interfaceFlags, so the run paths
// keep reading one set of values while commands that only talk to the daemon
// stop inheriting flags that mean nothing to them. tuios-web registers the same
// set through the same cliflags package.
func registerInterfaceFlags(cmds ...*cobra.Command) {
	for _, cmd := range cmds {
		interfaceFlags.Register(cmd.Flags())
	}
}
