package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/tmuxcompat"
	"github.com/spf13/cobra"
)

// The tmux compatibility shim. See internal/tmuxcompat for the mapping and
// docs/TMUX_SHIM.md for the person's view of it.
//
// Three entry points:
//
//   - tuios tmux-shim [-- command]: the opt-in. It runs one command with a
//     `tmux` link to this binary first on PATH and TMUX naming the shim.
//   - tmux ...: this binary run through that link. A call for the shim is
//     answered; any other (TMUX unset or naming a real server, or -L or -S
//     naming one) is handed to the next tmux on PATH, so the link never
//     breaks a real tmux.
//   - tuios tmux ...: the shim, asked for by name.

// tmuxShimDir is the shim's runtime directory, beside the daemon socket, so it
// is private to the user in the same way.
func tmuxShimDir() (string, error) {
	sock, err := session.GetSocketPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(sock), "tmux"), nil
}

// isTmuxName reports whether the binary was run by the name tmux.
func isTmuxName(arg0 string) bool {
	base := strings.ToLower(filepath.Base(arg0))
	return base == "tmux" || base == "tmux.exe"
}

// selfExe is this binary with its links resolved.
func selfExe() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		return r
	}
	return exe
}

// runAsTmux answers a call made through the `tmux` link.
func runAsTmux(args []string) int {
	dir, err := tmuxShimDir()
	if err != nil {
		dir = ""
	}
	g, _, perr := tmuxcompat.ParseGlobal(args)
	ours := false
	if perr == nil {
		ours = tmuxcompat.ForShim(g, os.Getenv("TMUX"), dir)
	} else {
		ours = dir != "" && tmuxcompat.SocketFromTmux(os.Getenv("TMUX")) == tmuxcompat.SocketPath(dir)
	}
	if ours {
		return runTmuxShim(args, dir)
	}
	real, err := tmuxcompat.FindRealTmux(os.Getenv("PATH"), dir, selfExe())
	if err != nil {
		fmt.Fprintln(os.Stderr, "tmux: this is the tuios tmux shim, and the call is not for it (TMUX does not name it); "+err.Error())
		return 1
	}
	c := exec.Command(real, args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			return ee.ExitCode()
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// lazyCaller dials the daemon on the first verb call, so -V and has-session
// answer without one.
type lazyCaller struct {
	client *session.VerbClient
}

func (l *lazyCaller) Call(verb string, params any) (json.RawMessage, error) {
	if l.client == nil {
		c, err := dialVerb()
		if err != nil {
			return nil, err
		}
		l.client = c
	}
	raw, err := l.client.Call(verb, params)
	if err != nil {
		return nil, explainVerbError(verb, err)
	}
	return raw, nil
}

func (l *lazyCaller) close() {
	if l.client != nil {
		_ = l.client.Close()
	}
}

// runTmuxShim runs one tmux invocation through the shim.
func runTmuxShim(args []string, dir string) int {
	caller := &lazyCaller{}
	defer caller.close()
	cwd, _ := os.Getwd()
	pid := 0
	if parts := strings.Split(os.Getenv("TMUX"), ","); len(parts) > 1 {
		pid, _ = strconv.Atoi(parts[1])
	}
	var holderEnv []string
	for _, k := range []string{tmuxcompat.EnvLog, tmuxcompat.EnvLogAll} {
		if v := os.Getenv(k); v != "" {
			holderEnv = append(holderEnv, k+"="+v)
		}
	}
	shim := &tmuxcompat.Shim{
		Caller:    caller,
		Session:   os.Getenv("TUIOS_SESSION"),
		Window:    os.Getenv("TUIOS_PANE_ID"),
		TmuxPane:  os.Getenv("TMUX_PANE"),
		Cwd:       cwd,
		Exe:       selfExe(),
		Dir:       dir,
		ServerPID: pid,
		HolderEnv: holderEnv,
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		Log:       tmuxcompat.LoggerFromEnv(os.Getenv),
	}
	if dir != "" {
		if err := tmuxcompat.EnsureDir(dir); err != nil {
			fmt.Fprintln(os.Stderr, "tmux: "+err.Error())
			return 1
		}
	}
	return shim.Run(args)
}

// newTmuxCommand is `tuios tmux`: the shim, asked for by name.
func newTmuxCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "tmux [tmux arguments]",
		Short: "Answer a tmux command in the caller's tuios session (the tmux shim)",
		Long: `Answer one tmux command line in the tuios session of the pane it runs in,
the same way the tmux link that 'tuios tmux-shim' installs does.

The tmux session is the caller's tuios session, a tmux window is a workspace
(@N), and a tmux pane is a tuios window (%N, a number derived from its id).
Nothing reaches another session.

Supported: split-window, new-window, send-keys, capture-pane -p,
display-message, list-panes, list-windows, list-sessions, has-session,
kill-pane, kill-window, select-pane, select-window, rename-window,
respawn-pane -k and -V. set-option, set-window-option, set-hook,
refresh-client, select-layout, resize-pane and start-server succeed and do
nothing. Anything else fails and is recorded in the shim log.`,
		Example: `  tuios tmux display-message -p '#{pane_id} #{window_id}'
  tuios tmux split-window -d -P -F '#{pane_id}'
  tuios tmux list-panes -F '#{pane_id} #{pane_title}'`,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
				return cmd.Help()
			}
			dir, err := tmuxShimDir()
			if err != nil {
				return err
			}
			if code := runTmuxShim(args, dir); code != 0 {
				os.Exit(code)
			}
			return nil
		},
	}
}

// newTmuxShimCommand is the opt-in launcher.
func newTmuxShimCommand() *cobra.Command {
	var logPath string
	var logAll bool
	cmd := &cobra.Command{
		Use:   "tmux-shim [-- command [args...]]",
		Short: "Run a command that drives tmux against this tuios session instead",
		Long: `Run a command with the tuios tmux shim as its tmux.

The command (your shell when none is given) runs with a 'tmux' link to this
binary first on PATH, TMUX naming the shim, and TMUX_PANE naming this pane. A
tool that drives tmux, such as Claude Code agent teams, then opens its panes,
types into them and reads them in this tuios session. See 'tuios tmux --help'
for what the shim answers.

The shim is off until you run this. It changes nothing outside the command it
starts. A 'tmux' call whose TMUX or -S names another server still goes to the
real tmux on PATH.

Calls the shim cannot fully answer are recorded, as JSON lines, in
$XDG_STATE_HOME/tuios/tmux-shim.log, or the file --log names. --log-all
records every call.

Run it in a tuios pane: it needs TUIOS_SESSION and TUIOS_PANE_ID. It is not
available on Windows.`,
		Example: `  # Claude Code agent teams, with teammates in tuios panes
  tuios tmux-shim -- env CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1 claude

  # A shell whose tmux is the shim, recording every call
  tuios tmux-shim --log-all`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS == "windows" {
				return errors.New("tuios tmux-shim is not available on Windows")
			}
			window := os.Getenv("TUIOS_PANE_ID")
			if os.Getenv("TUIOS_SESSION") == "" || window == "" {
				return errors.New("tuios tmux-shim runs in a tuios pane: TUIOS_SESSION and TUIOS_PANE_ID are not set here")
			}
			dir, err := tmuxShimDir()
			if err != nil {
				return err
			}
			if err := tmuxcompat.EnsureDir(dir); err != nil {
				return err
			}
			exe := selfExe()
			if exe == "" {
				return errors.New("cannot find the tuios binary to link as tmux")
			}
			if err := tmuxcompat.InstallLink(dir, exe); err != nil {
				return fmt.Errorf("install the tmux link: %w", err)
			}
			if logPath != "" {
				if abs, err := filepath.Abs(logPath); err == nil {
					logPath = abs
				}
			}
			env := tmuxcompat.LauncherEnv(os.Environ(), dir, window, logPath, logAll)
			if len(args) == 0 {
				shell := os.Getenv("SHELL")
				if shell == "" {
					shell = "/bin/sh"
				}
				args = []string{shell}
			}
			return tmuxcompat.ExecCommand(args, env)
		},
	}
	// Flags after the command are the command's: tuios tmux-shim claude
	// --resume passes --resume to claude.
	cmd.Flags().SetInterspersed(false)
	cmd.Flags().StringVar(&logPath, "log", "", "record shim calls in this file instead of $XDG_STATE_HOME/tuios/tmux-shim.log")
	cmd.Flags().BoolVar(&logAll, "log-all", false, "record every tmux call, not only the ones the shim could not fully answer")
	return cmd
}

// newTmuxPaneCommand is the pane holder the shim runs in every pane it opens.
// It is internal, so it is hidden.
func newTmuxPaneCommand() *cobra.Command {
	var dir string
	var env []string
	cmd := &cobra.Command{
		Use:    "tmux-pane --dir DIR [--env KEY=VALUE]... -- [command...]",
		Short:  "Hold a pane the tmux shim opened (internal)",
		Hidden: true,
		Args:   cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dir == "" {
				return errors.New("--dir is required")
			}
			os.Exit(tmuxcompat.RunPane(tmuxcompat.PaneOptions{
				Dir:     dir,
				Window:  os.Getenv("TUIOS_PANE_ID"),
				Command: args,
				Env:     env,
				Shell:   os.Getenv("SHELL"),
			}))
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "the shim's runtime directory")
	cmd.Flags().StringArrayVar(&env, "env", nil, "KEY=VALUE for the pane's processes")
	return cmd
}
