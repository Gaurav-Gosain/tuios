// Package main implements TUIOS, the Terminal UI Operating System.
// TUIOS is a terminal-based window manager that provides a modern interface
// for managing multiple terminal sessions with workspace support, tiling modes,
// and comprehensive keyboard/mouse interactions.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/cliflags"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/fang"
	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/Gaurav-Gosain/tuios/internal/serverscope"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
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
	serverName     string
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
	// A plugin command's reaper does nothing else (internal/herdrplugin,
	// reaper.go).
	if len(os.Args) > 1 && os.Args[1] == herdrplugin.ReaperArg {
		os.Exit(herdrplugin.RunReaper(os.Args[2:]))
	}
	// Run as the executable of the TUIOS Daemon app, this binary only starts
	// the daemon (daemon_app.go).
	if isDaemonAppLauncher() {
		os.Exit(runDaemonAppLauncher())
	}
	session.PluginReaper = pluginReaper()
	// The build identity, handed to every package that reports it before
	// anything can crash or open a pane. A crash report that cannot say which
	// build produced it cannot be placed against a commit.
	applyBuildIdentity()
	theme.ExtraFiles = pluginThemeFiles
	app.PluginSavers = pluginSaverFiles
	// The add sheet installs the release of this version with the helpers
	// tuios update and hosts sync use.
	app.MachineReleases = updateReleases{}
	app.SketchProgram = func() string {
		prog, err := sketchPluginProgram()
		if err != nil {
			return ""
		}
		return prog
	}

	// The server a pane's daemon was started for reaches every tuios run in
	// that pane through TUIOS_SERVER, the tmux and herdr links included, so it
	// is applied before either can take over. --server replaces it later.
	if err := serverscope.ApplyEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "tuios: %v.\n", err)
		os.Exit(1)
	}

	// Run through the `tmux` link that tuios tmux-shim installs, this binary
	// is tmux: the shim answers, or hands the call to the real tmux.
	if isTmuxName(os.Args[0]) {
		os.Exit(runAsTmux(os.Args[1:]))
	}
	// Run through the herdr link the daemon makes, this binary is herdr's
	// command line, as tools built for herdr call it. See herdr_commands.go.
	if isHerdrName(os.Args[0]) {
		os.Exit(runAsHerdr(os.Args[1:]))
	}

	// This machine's agent switch governs an agent call to another machine.
	session.HostCallGuard = refuseAgentCallHere

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
		// A command may say what a wrong flag exits with (tuios cp exits 2).
		var coded interface{ ExitStatus() int }
		if errors.As(err, &coded) {
			os.Exit(coded.ExitStatus())
		}
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
	rootCmd := newRootCommandBase()

	sshCmd := newSSHCommand()

	configCmd := newConfigCommand()

	keybindsCmd := newKeybindsCommand()

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
			// Playback runs the interface for as long as the tape lasts;
			// --pprof is a persistent flag that tape play used to accept and
			// ignore.
			startPprofServer()
			if err := startCPUProfile(); err != nil {
				return err
			}
			defer stopCPUProfile()
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

	var tapeListJSON bool
	tapeListCmd := &cobra.Command{
		Use:   "list",
		Short: "List all saved tape recordings",
		Long: `List the tape files in the tape recordings directory, with the size and the
time each one last changed. 'tuios tape dir' prints the directory.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return listTapeFiles(tapeListJSON)
		},
	}
	tapeListCmd.Flags().BoolVar(&tapeListJSON, "json", false, "Output as JSON")

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

	tapeCmd.AddCommand(tapePlayCmd, tapeValidateCmd, tapeListCmd, tapeDirCmd, tapeDeleteCmd, tapeShowCmd)

	var createIfMissing bool
	var attachHost string
	var attachHold bool
	var attachSSH bool

	attachCmd := &cobra.Command{
		Use:   "attach [session-name]",
		Args:  cobra.MaximumNArgs(1),
		Short: "Attach to a TUIOS session",
		Long: `Attach to an existing TUIOS session.

If no session name is provided, attaches to the most recent session.

If the daemon is not running, it is started and restores every session
saved on disk: the layout and working directories, with new shells. Attach
then opens one of those. With nothing saved and no
name given, a new session is opened instead. A name that matches no session
is an error unless -c is given.

With -d every other client of the session detaches as this one attaches,
as tmux attach -d does. Each of them exits with a message. To do this on
every attach, set single_client = true in [daemon].

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

  # Attach and detach every other client of the session
  tuios attach -d mysession

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
	attachCmd.Flags().BoolVarP(&attachDetachOthers, "detach-others", "d", false, "Detach every other client of the session, as tmux attach -d does")
	attachCmd.Flags().BoolVar(&attachTerminalMode, "terminal-mode", false, "Start in terminal mode, whatever startup.start_in_terminal_mode says")
	attachCommand = attachCmd
	// Set by the client a restart from the app starts. See restart_reattach.go.
	attachCmd.Flags().BoolVar(&attachAfterRestart, "after-restart", false, "Say that the daemon restarted when the client starts")
	_ = attachCmd.Flags().MarkHidden("after-restart")
	// Set by the client that follows a live upgrade. See restart_reattach.go.
	attachCmd.Flags().StringVar(&attachAfterUpgrade, "after-upgrade", "", "Say that this client follows a live upgrade to this version")
	_ = attachCmd.Flags().MarkHidden("after-upgrade")
	registerHostNameCompletion(attachCmd, "host")

	var newDetach bool
	var newHost string
	var newHold bool
	var newGlobal bool
	var newSSH bool
	var newCwd string
	var newSpace string
	newCmd := &cobra.Command{
		Use:   "new [session-name]",
		Args:  cobra.MaximumNArgs(1),
		Short: "Create a new TUIOS session",
		Long: `Create a new persistent TUIOS session and attach to it.

This starts a new session in the daemon (starting the daemon if needed)
and immediately attaches you to it.

With --detach the session is created headless (no client attached): it
gets an initial window, is immediately usable by control commands
(send-keys, run-command, capture-pane), and can be attached later.

Sessions persist even when you detach, allowing you to reconnect later
with 'tuios attach'.

The session's windows start in the directory you run the command from.
--cwd names another directory. A window opened later with no pane to take
a directory from starts there too.

With --host the session is created on another machine. tuios runs ssh to the
host named in the [hosts] table and runs 'tuios new' there. The client you
see is the remote one. With --detach the far side creates the session and
returns. See 'tuios hosts --help'.

With --space the session is filed under a space, a named group of related
sessions that 'tuios ls' and the sidebar list together.`,
		Example: `  # Create a new session with auto-generated name
  tuios new

  # Create a named session
  tuios new mysession

  # Create a headless session without attaching
  tuios new mysession --detach

  # Create a session whose windows start in ~/dev/api
  tuios new api --cwd ~/dev/api

  # Create a session in the space payments
  tuios new api --space payments --detach

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
				if newCwd != "" {
					return errfmt.Errorf("--cwd is a directory on this machine, so it cannot be used with --host")
				}
				if newSpace != "" {
					return errfmt.Errorf("--space cannot be used with --host. Run 'tuios set-session-space' on the host after you create the session")
				}
				return runNewOnHost(newHost, name, newDetach, newHold, newSSH)
			}
			dir, err := newSessionStartDir(newCwd)
			if err != nil {
				return err
			}
			newSessionDir = dir
			if newSpace != "" {
				if newGlobal {
					return errors.New(session.GlobalSpaceRefusal)
				}
				return runNewSessionInSpace(name, newSpace, newDetach)
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
	newCmd.Flags().StringVar(&newCwd, "cwd", "", "Directory the session's windows start in (default: the current directory)")
	newCmd.Flags().StringVar(&newSpace, "space", "", "File the session under this space, a named group of related sessions")
	registerHostNameCompletion(newCmd, "host")

	lsCmd := newLsCommand()

	listClientsCmd := newListClientsCommand()

	killSessionCmd := newKillSessionCommand()

	resurrectCmd := newResurrectCommand()

	startDaemonCmd := newStartDaemonCommand()

	var daemonLogLevel string
	var daemonNoRestore bool
	var daemonAdoptFD int
	var daemonService bool
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
			if err := startCPUProfile(); err != nil {
				return err
			}
			defer stopCPUProfile()
			if daemonAdoptFD > 0 {
				return runAdoptingDaemon(daemonAdoptFD)
			}
			if daemonService {
				return runServiceDaemon(daemonNoRestore)
			}
			return runDaemon(true, daemonNoRestore)
		},
	}
	daemonCmd.Flags().StringVar(&daemonLogLevel, "log-level", "", "Debug log level: off, errors, basic, messages, verbose, trace")
	daemonCmd.Flags().BoolVar(&daemonNoRestore, "no-restore", false, "Do not auto-restore saved sessions on start (use 'tuios resurrect' to restore on demand)")
	// Set only by a live upgrade, which execs the next daemon image with the
	// descriptor of the manifest its predecessor wrote. See daemon_upgrade.go.
	daemonCmd.Flags().IntVar(&daemonAdoptFD, adoptFDFlag, 0, "Adopt the panes a live upgrade handed over on this descriptor")
	_ = daemonCmd.Flags().MarkHidden(adoptFDFlag)
	// Set only by the service that install-service writes. A live upgrade
	// keeps it, since handoffArgs keeps the daemon's own arguments.
	daemonCmd.Flags().BoolVar(&daemonService, "service", false, "A service manager started this daemon")
	_ = daemonCmd.Flags().MarkHidden("service")
	daemonCmd.AddCommand(handoffAcceptsCommand(), handoffKeeperCommand(), daemonUpgradeCommand(),
		newDaemonInstallServiceCommand(), newDaemonUninstallServiceCommand(), newDaemonServiceStatusCommand())

	killDaemonCmd := newKillDaemonCommand()

	// Remote control commands
	var sendKeysSession string
	var sendKeysLiteral bool
	var sendKeysRaw bool
	var sendKeysWindow string
	var sendKeysRepeat int
	var sendKeysJSON bool
	var sendKeysAllowBlocked bool
	sendKeysCmd := &cobra.Command{
		Use:   "send-keys <keys>",
		Short: "Send keys (arrows, page keys, ctrl+c) to a window",
		Long: `Send keys to the program in a window: arrows, page keys, Enter, ctrl+c.

With -w the keys go to that window's terminal, whether or not a client is
attached and whichever window has the focus. Without -w they go to the attached
client as if the person pressed them, which drives the window manager or the
focused window. With no client attached they go to the focused window.

From inside a pane, -w is required. A pane does not send keys into a pane
that waits on a prompt (needs_input). To answer that prompt, pass
--allow-blocked. It also needs the respond grant.

To type text, use send-text. send-keys splits its argument on spaces and commas,
so 'echo hello' types "echohello".

Keys (case-insensitive, and the argument is split on spaces and commas):
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
			return runSendKeys(sendKeysSession, args[0], sendKeysLiteral, sendKeysRaw, sendKeysWindow, sendKeysRepeat, sendKeysAllowBlocked, sendKeysJSON)
		},
	}
	sendKeysCmd.Flags().StringVarP(&sendKeysSession, "session", "s", "", "Target session (default: this pane's session, else the most recently active)")
	sendKeysCmd.Flags().BoolVarP(&sendKeysLiteral, "literal", "l", false, "Write the argument to the window's terminal unchanged, with no key names")
	sendKeysCmd.Flags().BoolVarP(&sendKeysRaw, "raw", "r", false, "Treat each character as a separate key (no splitting on space/comma)")
	sendKeysCmd.Flags().StringVarP(&sendKeysWindow, "window", "w", "", "Target window: id, index, name or id prefix (default: the attached client, else the focused window)")
	sendKeysCmd.Flags().IntVarP(&sendKeysRepeat, "repeat", "N", 1, "Send the whole sequence this many times (1 to 1000)")
	sendKeysCmd.Flags().BoolVar(&sendKeysJSON, "json", false, "Output result as JSON")
	sendKeysCmd.Flags().BoolVar(&sendKeysAllowBlocked, "allow-blocked", false, "From a pane, send keys into a pane that waits on a prompt. Read the prompt first")
	_ = sendKeysCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	// Add completion for send-keys
	sendKeysCmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return getSendKeysCompletions(toComplete), cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	capturePaneCmd := newCapturePaneCommand()

	screenshotCmd := newScreenshotCommand()

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
				return errfmt.Errorf("command name required (use --list to see available commands)")
			}
			return runCommand(runCommandSession, args[0], args[1:], runCommandJSON)
		},
	}
	runCommandCmd.Flags().StringVarP(&runCommandSession, "session", "s", "", "Target session (default: this pane's session, else the most recently active)")
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
	var setConfigJSON bool
	var setConfigNoSave bool
	setConfigCmd := &cobra.Command{
		Use:   "set-config <path> <value>",
		Short: "Set a configuration option in a running TUIOS session",
		Long: `Set a configuration option in a running TUIOS session at runtime.

Run 'tuios list-options' for every path, with its type, default and accepted
values. An [appearance] option also answers to its bare name, so border_style
and appearance.border_style are the same path. A path or a value that the
option does not take is refused, and nothing changes.

With a client attached, the client applies the value and writes it to
config.toml. With no client attached, the daemon keeps the value for this
session. The next client that attaches applies it and writes it to config.toml.
The command then says so on stderr, and --json reports "applied": false with
the reason.

--no-save applies the value and does not write it to a config file. Later
saves of other settings do not write it either. The value stays until the
client exits, or until you set the option again without --no-save. Use it in
a hook that changes the display for a moment.

agents.enabled is the person's switch. A process in a pane cannot set it.
--no-save does not apply to it.

  tuios set-config appearance.border_style rounded
  tuios set-config appearance.dockbar_position top`,
		Example: `  # Change dockbar position
  tuios set-config dockbar_position top

  # Change border style
  tuios set-config border_style rounded

  # Turn animations off
  tuios set-config motion none

  # Hide window buttons
  tuios set-config hide_window_buttons true

  # Hide the sidebar for now and keep config.toml as it is
  tuios set-config --no-save appearance.sidebar.enabled false`,
		Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			return runSetConfig(setConfigSession, args[0], args[1], setConfigJSON, setConfigNoSave)
		},
	}
	setConfigCmd.Flags().StringVarP(&setConfigSession, "session", "s", "", "Target session (default: this pane's session, else the most recently active)")
	setConfigCmd.Flags().BoolVar(&setConfigJSON, "json", false, "Output result as JSON, with applied and the reason when it is false")
	setConfigCmd.Flags().BoolVar(&setConfigNoSave, "no-save", false, "Apply the value and keep it out of the config files until the client exits")
	_ = setConfigCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	var getConfigSession string
	var getConfigJSON bool
	getConfigCmd := &cobra.Command{
		Use:   "get-config <path>",
		Short: "Read a configuration option from a running TUIOS session",
		Long: `Read a configuration option from a running TUIOS session. Options are
recorded in daemon-owned state, so this works whether or not a TUI client is
attached.

An option with no session override reads as the value in config.toml. An
option that config.toml does not set reads as its default. A path that exists
always reads. --json also reports where the value came from: "session" for an
override set here, "config" for a value the daemon read from config.toml, and
"default" for the built-in. agents.enabled is the daemon's switch, so it reads
as the value in effect for every session.

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
	setAgentStateCmd := newSetAgentStateCommand()

	setAgentMetaCmd := newSetAgentMetaCommand()

	setAgentSessionCmd := newSetAgentSessionCommand()

	getAgentStateCmd := newGetAgentStateCommand()

	explainAgentDetectCmd := newExplainAgentDetectCommand()

	explainAgentScreenCmd := newExplainAgentScreenCommand()

	sendTextCmd := newSendTextCommand()

	newWindowCmd := newNewWindowCommand()

	var popupSession string
	var popupWidth string
	var popupHeight string
	var popupDim int
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
selects a window everywhere else, and -h is help.

--dim takes a percent from 0 to 90 and overrides appearance.popup_dim for this
popup. Omit it to follow the config (default: 0); --dim 0 disables dimming.`,
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

  # Dim the background while choosing
  tuios popup --dim 30 -- gum choose one two three

  # Capture the popup's id for scripting
  tuios popup --json -- fzf | jq -r .window_id`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var dim *config.PopupDim
			if cmd.Flags().Changed("dim") {
				dim = &config.PopupDim{Percent: popupDim}
			}
			return runPopup(popupOptions{
				dim:       dim,
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
	popupCmd.Flags().StringVarP(&popupSession, "session", "s", "", "Target session (default: this pane's session, else the most recently active)")
	// Spelled out, with no shorthands. -w is the window selector in every other
	// tuios command and -h is cobra's help, so both of the short forms a reader
	// would reach for already mean something else here.
	popupCmd.Flags().StringVar(&popupWidth, "width", "", "Popup width in cells or percent (default: 80%)")
	popupCmd.Flags().StringVar(&popupHeight, "height", "", "Popup height in cells or percent (default: 60%)")
	popupCmd.Flags().IntVar(&popupDim, "dim", 0, "Dim the background by 0 to 90 percent (default: appearance.popup_dim; 0 disables)")
	popupCmd.Flags().StringVar(&popupName, "name", "", "Name for the popup")
	popupCmd.Flags().StringVar(&popupCwd, "cwd", "", "Directory to run the command in (default: the daemon's)")
	popupCmd.Flags().IntVar(&popupWorkspace, "workspace", 0, "Workspace to open the popup on (default: the current one)")
	popupCmd.Flags().BoolVar(&popupJSON, "json", false, "Output result as JSON")
	_ = popupCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	focusWindowCmd := newFocusWindowCommand()

	moveWindowCmd := newMoveWindowCommand()

	setWindowCmd := newSetWindowCommand()

	selectWorkspaceCmd := newSelectWorkspaceCommand()

	listWorkspacesCmd := newListWorkspacesCommand()

	setLayoutCmd := newSetLayoutCommand()

	listOptionsCmd := newListOptionsCommand()

	listThemesCmd := newListThemesCommand()

	listGlyphsCmd := newListGlyphsCommand()

	listDockComponentsCmd := newListDockComponentsCommand()

	listHooksCmd := newListHooksCommand()

	refreshDockCmd := newRefreshDockCommand()

	importThemeCmd := newImportThemeCommand()

	themeCmd := &cobra.Command{
		Use:   "theme",
		Short: "Work with theme files",
	}
	var themeReloadJSON bool
	themeReloadCmd := &cobra.Command{
		Use:   "reload",
		Short: "Read the theme and glyph files again in every client",
		Long: `Read the theme and glyph files again in every tuios client on this machine,
ask each terminal for its colours again, and repaint. A client also does this
by itself when a file in the themes or glyphs directory changes.

Run it from a wallpaper tool that writes a theme file: pywal with wal -o, or
matugen with a post_hook.`,
		Example: `  tuios theme reload
  wal -i ~/wall.png -o "tuios theme reload"`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runThemeReload(themeReloadJSON)
		},
	}
	themeReloadCmd.Flags().BoolVar(&themeReloadJSON, "json", false, "Output as JSON")
	themeCmd.AddCommand(themeReloadCmd)

	completeThemeID := func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		theme.ReloadCustomThemes()
		return theme.AvailableThemes(), cobra.ShellCompDirectiveNoFileComp
	}
	var themeExportFormat string
	var themeExportFull bool
	themeExportCmd := &cobra.Command{
		Use:   "export <theme>",
		Short: "Print a theme as a theme file",
		Long: `Print a theme as a theme file on standard output.

A theme from your themes directory is written against the built-in theme it
starts from, with only the colours that are different. A built-in theme is
written with all its colours. --full writes all the colours of any theme, with
no base.

--format base16 writes a base16 scheme in YAML for other programs.`,
		Example: `  tuios theme export mine > mine.json
  tuios theme export dracula --format base16`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeThemeID,
		RunE: func(_ *cobra.Command, args []string) error {
			return runThemeExport(args[0], themeExportFormat, themeExportFull)
		},
	}
	themeExportCmd.Flags().StringVar(&themeExportFormat, "format", "tuios", "Output format: tuios or base16")
	themeExportCmd.Flags().BoolVar(&themeExportFull, "full", false, "Write every colour, with no base")
	_ = themeExportCmd.RegisterFlagCompletionFunc("format", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"tuios", "base16"}, cobra.ShellCompDirectiveNoFileComp
	})
	themeCmd.AddCommand(themeExportCmd)

	themeShareCmd := &cobra.Command{
		Use:   "share <theme>",
		Short: "Print a theme as one line to paste in a message",
		Long: `Print a theme as one line that starts with tuios-theme:v1:. The line holds
the same file that tuios theme export writes, so a theme from your themes
directory holds only the colours that are different from its base.`,
		Example:           `  tuios theme share mine`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeThemeID,
		RunE: func(_ *cobra.Command, args []string) error {
			line, err := theme.ShareString(args[0])
			if err != nil {
				return err
			}
			fmt.Println(line)
			return nil
		},
	}
	themeCmd.AddCommand(themeShareCmd)

	waitForCmd := newWaitForCommand()

	setSessionNameCmd := newSetSessionNameCommand()

	renameSessionCmd := newRenameSessionCommand()

	setSessionAccentCmd := newSetSessionAccentCommand()

	setSessionSpaceCmd := newSetSessionSpaceCommand()

	setSessionOrderCmd := newSetSessionOrderCommand()

	setWorkspaceNameCmd := newSetWorkspaceNameCommand()

	getConfigCmd.Flags().StringVarP(&getConfigSession, "session", "s", "", "Target session (default: this pane's session, else the most recently active)")
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

	var tapeExecSession string
	var tapeExecTimeout time.Duration
	tapeExecCmd := &cobra.Command{
		Use:   "exec <file.tape>",
		Short: "Execute a tape file in a running session",
		Long: `Execute a tape file in a running TUIOS session.

The session needs an attached client, which plays the tape. The command
returns when the tape ends. It exits non-zero when the tape has an error or
a command fails, such as a WaitFor that times out or an Expect that does not
hold. The message gives the file and line.

For single tape commands, use: tuios run-command <Command> [args...]`,
		Example: `  # Execute a tape file
  tuios tape exec demo.tape
  tuios tape exec ./examples/advanced_demo.tape

  # Execute in a specific session
  tuios tape exec --session mysession demo.tape`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runTapeExec(tapeExecSession, args[0], tapeExecTimeout)
		},
	}
	tapeExecCmd.Flags().StringVarP(&tapeExecSession, "session", "s", "", "Target session (default: this pane's session, else the most recently active)")
	tapeExecCmd.Flags().DurationVar(&tapeExecTimeout, "timeout", 30*time.Minute, "How long to wait for the tape to end")
	_ = tapeExecCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	// Add exec to tape command group
	tapeCmd.AddCommand(tapeExecCmd)

	logsCmd := newLogsCommand()

	listWindowsCmd := newListWindowsCommand()

	getWindowCmd := newGetWindowCommand()

	sessionInfoCmd := newSessionInfoCommand()

	listVerbsCmd := newListVerbsCommand()

	// Layout template commands
	layoutCmd := &cobra.Command{
		Use:   "layout",
		Short: "Manage layout templates",
		Long: `Save, load, list, delete and export the window layout templates.

Save and load a layout in a running session with these commands, the layout
prefix keys or the command palette. With no layout saved, 'tuios layout list'
says which keys save one.`,
	}
	var layoutListJSON bool
	layoutListCmd := &cobra.Command{
		Use:   "list",
		Short: "List saved layout templates",
		Long: `List the saved layout templates: the name, how many windows each holds,
whether it is tiled, and when it was saved. 'tuios layout dir' prints the
directory that holds them.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			templates, err := app.LoadLayoutTemplates()
			if err != nil {
				return err
			}
			if layoutListJSON {
				type layoutRow struct {
					Name      string    `json:"name"`
					Windows   int       `json:"windows"`
					Tiled     bool      `json:"tiled"`
					CreatedAt time.Time `json:"created_at"`
				}
				rows := make([]layoutRow, 0, len(templates))
				for _, t := range templates {
					rows = append(rows, layoutRow{Name: t.Name, Windows: len(t.Windows), Tiled: t.AutoTiling, CreatedAt: t.CreatedAt})
				}
				return printJSON(rows)
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
		Use:   "delete <name>",
		Short: "Delete a layout template",
		Long:  `Delete a saved layout template by its name. 'tuios layout list' shows the names.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if _, err := findLayoutTemplate(args[0]); err != nil {
				return err
			}
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
		Long:  `Print the path of the directory that holds the layout templates. Each template is a JSON file there.`,
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Println(app.GetTemplatesDir())
		},
	}
	layoutExportCmd := &cobra.Command{
		Use:   "export <name>",
		Short: "Export a layout template as a tape script",
		Long: `Print a saved layout template as a tape script on stdout. Run the script
with 'tuios tape play', or with 'tuios tape exec' against a running session.

The template itself is a JSON file in the directory 'tuios layout dir' prints.`,
		Example: `  tuios layout export dev-layout > dev-layout.tape
  tuios tape play dev-layout.tape`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			t, err := findLayoutTemplate(args[0])
			if err != nil {
				return err
			}
			fmt.Print(app.GenerateTapeScript(t))
			return nil
		},
	}
	layoutListCmd.Flags().BoolVar(&layoutListJSON, "json", false, "Output as JSON")
	layoutCmd.AddCommand(layoutListCmd, newLayoutSaveCommand(), newLayoutLoadCommand(), layoutDeleteCmd, layoutDirCmd, layoutExportCmd)

	// The interface flags ride only the commands that draw the interface. They
	// were persistent on the root once, which buried a read command's few real
	// flags under twenty appearance ones in its help.
	registerInterfaceFlags(rootCmd, attachCmd, newCmd, sshCmd, tapePlayCmd)

	listAgentsCmd := newListAgentsCommand()

	sendAgentMessageCmd := newSendAgentMessageCommand()

	readAgentMessagesCmd := newReadAgentMessagesCommand()

	askAgentCmd := newAskAgentCommand()

	updateCmd := newUpdateCommand()

	hostsCmd := newHostsCommand()

	stdioProxyCmd := newStdioProxyCommand()

	rootCmd.AddCommand(sshCmd, configCmd, newLookCommand(), keybindsCmd, tapeCmd, layoutCmd, updateCmd)
	rootCmd.AddCommand(attachCmd, newCmd, lsCmd, listClientsCmd, killSessionCmd, resurrectCmd)
	rootCmd.AddCommand(newSwitchSessionCmd())
	rootCmd.AddCommand(newDetachClientCmd())
	rootCmd.AddCommand(newSSHAgentPathCmd())
	rootCmd.AddCommand(newDisplayEnvCmd())
	rootCmd.AddCommand(newKeepAwakeCmd())
	rootCmd.AddCommand(startDaemonCmd, daemonCmd, killDaemonCmd, newRestartServerCommand(), newDaemonAppCommand())
	rootCmd.AddCommand(sendKeysCmd, runCommandCmd, setConfigCmd, getConfigCmd, logsCmd, capturePaneCmd, screenshotCmd)
	rootCmd.AddCommand(setAgentStateCmd, setAgentMetaCmd, setAgentSessionCmd, newResumeAgentCommand(), getAgentStateCmd, explainAgentDetectCmd, explainAgentScreenCmd)
	rootCmd.AddCommand(listAgentsCmd, sendAgentMessageCmd, readAgentMessagesCmd, askAgentCmd, newListAttentionCommand(),
		newPeekPromptCommand(), newRespondCommand(), newQueueCommand(), newTaskCommand(), newReviewCommand(), newCheckpointCommand(), newShipCommand())
	rootCmd.AddCommand(sendTextCmd, newWindowCmd, waitForCmd, newSubscribeCommand(), newRunCommand(), newAskHumanCommand())
	rootCmd.AddCommand(renameSessionCmd, setSessionNameCmd, setSessionAccentCmd, setSessionSpaceCmd, setSessionOrderCmd, setWorkspaceNameCmd, newNoteCommand())
	rootCmd.AddCommand(newLayoutCommands()...)
	rootCmd.AddCommand(newSplitWindowCommand(), popupCmd, focusWindowCmd, moveWindowCmd, setWindowCmd, newSetAgentQuietCommand(), newPiPCommand(), newCallCommand(), newPaneViewCommand())
	rootCmd.AddCommand(selectWorkspaceCmd, listWorkspacesCmd, setLayoutCmd)
	rootCmd.AddCommand(listWindowsCmd, getWindowCmd, sessionInfoCmd, listVerbsCmd, listOptionsCmd, listThemesCmd, listGlyphsCmd, importThemeCmd, themeCmd)
	rootCmd.AddCommand(listDockComponentsCmd, refreshDockCmd, listHooksCmd)
	rootCmd.AddCommand(hostsCmd, stdioProxyCmd, newGUIBridgeCommand())
	rootCmd.AddCommand(newStashCommand(), newPaneGrantsCommand(), newSetPaneGrantsCommand())
	rootCmd.AddCommand(newBufferCommands()...)
	rootCmd.AddCommand(newWorktreeCommand(), newFanCommand(), newStartAgentCommand(), newDelegateCommand(), newXpanesCommand(), newCloseWorkspaceCommand(), newCloseWindowCommand(), newClearHistoryCommand())
	rootCmd.AddCommand(newAgentHookCommand(), newAgentStatusLineCommand(), newIntegrationCommand(), newDoctorCommand(), newMCPCommand())
	rootCmd.AddCommand(newTmuxCommand(), newTmuxShimCommand(), newTmuxPaneCommand())
	rootCmd.AddCommand(newAgentProtoCommand(), newAgentLogCommand(), newNotifyCommand(), newStatusCommand(), newPairCommand())
	rootCmd.AddCommand(newHerdrGroupCommand("pane"), newHerdrGroupCommand("notification"))
	rootCmd.AddCommand(newPluginsCommand(), newPluginCellCommand(), newPluginNotifyCommand())
	rootCmd.AddCommand(newCpCommand(), newTransfersCommand())
	rootCmd.AddCommand(newMermaidCommand())
	rootCmd.AddCommand(newTMLCommand())

	// The tutor heads the help: it is the first thing a new person needs.
	rootCmd.AddGroup(&cobra.Group{ID: groupGettingStarted, Title: "getting started"})
	rootCmd.AddCommand(newTutorCommand())

	addExplorers(rootCmd)
	rejectUnknownSubcommands(rootCmd)
	refuseUnreadArgs(rootCmd)
	return rootCmd
}

// rejectUnknownSubcommands makes every group command below the root (a
// command with subcommands and nothing of its own to run) fail on a word
// that names none of its subcommands. Cobra checks that only for the root.
// Below it, a command with no Run returns flag.ErrHelp before it looks at
// the args, so "tuios plugins uninstal" printed the help and exited 0. A
// bare group, or one given only "help", still prints its help.
func rejectUnknownSubcommands(cmd *cobra.Command) {
	for _, sub := range cmd.Commands() {
		rejectUnknownSubcommands(sub)
	}
	if !cmd.HasParent() || !cmd.HasSubCommands() || cmd.Run != nil || cmd.RunE != nil {
		return
	}
	cmd.Args = func(c *cobra.Command, args []string) error {
		if len(args) == 1 && args[0] == "help" {
			return nil
		}
		return cobra.NoArgs(c, args)
	}
	cmd.RunE = func(c *cobra.Command, _ []string) error {
		return c.Help()
	}
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

// findLayoutTemplate returns the saved layout template called name.
func findLayoutTemplate(name string) (app.LayoutTemplate, error) {
	templates, err := app.LoadLayoutTemplates()
	if err != nil {
		return app.LayoutTemplate{}, err
	}
	for _, t := range templates {
		if t.Name == name {
			return t, nil
		}
	}
	return app.LayoutTemplate{}, errfmt.Errorf("no layout named %q. Run 'tuios layout list' to see the saved layouts", name)
}
