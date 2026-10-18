package main

import (
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// The way back after a restart from the app.
//
// The app stops on ExitRestarted once the new daemon answers. This process
// then runs the newest tuios as `tuios attach <session> --after-restart`, so
// the person lands back in the session on the new build of the client too.
// On Unix it is an exec: the same process, the same terminal, nothing left
// behind. Windows has no exec, so the new client runs as a child and this
// process waits for it and exits with its status.
//
// The interface flags the person started with (--theme, --no-animations and
// the rest) and the root's --debug and --server go along. The flags of the
// command that started this client do not: the session is named now, so a
// --create or a --host would mean something else.

// attachAfterRestart is the hidden attach --after-restart flag.
var attachAfterRestart bool

// ranCommand is the command this process runs, for the flags it carries to
// the client a restart starts, and attachCommand is tuios attach.
var ranCommand, attachCommand *cobra.Command

// reattachAfterRestart starts the client again on the session.
func reattachAfterRestart(e *app.RestartExec) error {
	fmt.Printf("The daemon restarted. tuios attaches to session '%s' again.\n", e.Session)
	return execClient(e.Binary, restartAttachArgs(e.Session))
}

// restartAttachArgs is the command line of the client after a restart,
// without the program name.
func restartAttachArgs(session string) []string {
	args := carriedFlags()
	return append(args, "attach", "--after-restart", "--", session)
}

// attachOwnFlags are the attach flags that are not carried: they say which
// session to attach to and how, and the restart names the session itself.
var attachOwnFlags = map[string]bool{"create": true, "host": true, "ssh": true, "hold": true, "force": true, "detach-others": true, "terminal-mode": true, "after-restart": true}

// carriedFlags is the flags this process was given that attach takes too, as
// --name=value: the interface flags, and the root's --debug and --server.
func carriedFlags() []string {
	if ranCommand == nil || attachCommand == nil {
		return nil
	}
	var out []string
	persistent := ranCommand.Root().PersistentFlags()
	ranCommand.Flags().Visit(func(f *pflag.Flag) {
		// The root's flags (--debug, --server, --pprof) are not in attach's
		// own set until attach runs, so they are looked up on the root. A CPU
		// profile is this process's, and the next one must not write over it.
		root := persistent.Lookup(f.Name) != nil && f.Name != "cpuprofile"
		if root || (!attachOwnFlags[f.Name] && attachCommand.Flags().Lookup(f.Name) != nil) {
			out = append(out, "--"+f.Name+"="+f.Value.String())
		}
	})
	return out
}

// restartFailure is what a restart that left no daemon said, as the error the
// client exits with.
func appRestartFailure(f *app.RestartFailure) error {
	return &diagnosticError{What: f.What, Fix: strings.TrimSuffix(strings.TrimSpace(f.Fix), ".") + "."}
}
