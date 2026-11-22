package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/spf13/cobra"
)

// remote-desktop runs tuios-rd, a separate program, so the tuios binary does
// not carry the viewer. tuios-rd sets up the screen engine on the host and
// shows its desktop in the pane.

func newRemoteDesktopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote-desktop HOST [flags]",
		Short: "Show the desktop of a linked machine in this pane",
		Long: `Show the desktop of HOST, a machine in the [hosts] table, in this pane.

The first time, tuios-rd installs the screen engine on HOST and starts it
as a user service. On macOS it shows which permissions to turn on in System
Settings and goes on by itself when they are on. The picture comes through
tuios forward, so no ssh -L is needed.

Press Ctrl+] to take control of the desktop and to give it back. Press
Ctrl+\ to quit.

This command runs the tuios-rd program, which comes with tuios as a
separate binary. Run tuios remote-desktop HOST --help for its flags.`,
		Example: `  tuios remote-desktop build
  tuios remote-desktop mac --display DP-2
  tuios remote-desktop mac --all
  tuios remote-desktop build --check`,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
				return cmd.Help()
			}
			return runRemoteDesktop(args)
		},
	}
	// The flags go to tuios-rd as they are. They are declared here for the
	// help and the docs.
	f := cmd.Flags()
	f.String("display", "", "show this display of HOST")
	f.Bool("all", false, "open one pane for each display of HOST")
	f.Bool("check", false, "do every step, read one frame, print the result and quit")
	f.String("engine-bin", "", "install this engine binary on HOST (built for HOST)")
	f.String("engine-src", "", "build the engine on HOST from this screen-relay source folder")
	f.String("engine-path", "", "run this engine binary that is already on HOST, at its own path")
	f.String("service", "auto", "the service on HOST: auto, launchd, systemd or process")
	return cmd
}

// findTuiosRD finds the tuios-rd program: TUIOS_RD_BIN, then next to this
// tuios, then PATH.
func findTuiosRD() (string, error) {
	if p := os.Getenv("TUIOS_RD_BIN"); p != "" {
		return p, nil
	}
	name := "tuios-rd"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if self, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(self), name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", errors.New("tuios remote-desktop needs the tuios-rd program, and it is not installed.\n" +
		"Install tuios again from a release, which has tuios-rd next to tuios,\n" +
		"or build it from the tuios source: go build -o ~/.local/bin/tuios-rd ./cmd/tuios-rd")
}

func runRemoteDesktop(args []string) error {
	rd, err := findTuiosRD()
	if err != nil {
		return err
	}
	argv := append([]string{rd, "connect"}, args...)
	env := os.Environ()
	if self, err := os.Executable(); err == nil {
		env = append(env, "TUIOS_BIN="+self)
	}
	if runtime.GOOS != "windows" {
		return syscall.Exec(rd, argv, env)
	}
	cmd := exec.Command(rd, argv[1:]...)
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			return &statusError{code: ee.ExitCode()}
		}
		return err
	}
	return nil
}
