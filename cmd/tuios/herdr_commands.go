//go:build !slim

package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/herdrcli"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
)

// herdr's command line, answered by tuios. See internal/herdrcli.
//
// Two entry points reach it:
//
//   - herdr ...: this binary run through the herdr link the daemon makes
//     (session.Manager.HerdrEnv sets HERDR_BIN_PATH to it). It is herdr's
//     whole CLI, as plugins and tools built for herdr call it.
//   - tuios pane ... and tuios notification ...: the same front, for a pane
//     whose HERDR_BIN_PATH names the tuios binary itself. A daemon from an
//     older tuios set it that way, and so does a daemon that could not make
//     the link. Only these two groups are reached this way: tuios has
//     commands of its own named worktree, workspace and agent.

// isHerdrName reports whether the binary was run by the name herdr.
func isHerdrName(arg0 string) bool {
	base := strings.ToLower(filepath.Base(arg0))
	return base == "herdr" || base == "herdr.exe"
}

// runAsHerdr answers a herdr command line.
func runAsHerdr(args []string) int {
	return herdrcli.Main(args, herdrcli.Options{Socket: herdrSocketPath})
}

// herdrSocketPath is the herdr socket beside this user's daemon socket, for a
// call made where HERDR_SOCKET_PATH is not set.
func herdrSocketPath() (string, error) {
	sock, err := session.GetSocketPath()
	if err != nil {
		return "", err
	}
	return session.HerdrSocketPath(sock), nil
}

// newHerdrGroupCommand is a hidden tuios command that hands its arguments to
// the herdr front as herdr's group name.
func newHerdrGroupCommand(group string) *cobra.Command {
	return &cobra.Command{
		Use:                group + " <herdr arguments>",
		Short:              "herdr's " + group + " commands, for tools built for herdr",
		Long:               "herdr's " + group + " commands, for tools built for herdr. Run herdr " + group + " help, through $HERDR_BIN_PATH, for the subcommands.",
		Hidden:             true,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
				return cmd.Help()
			}
			code := herdrcli.Main(append([]string{group}, args...), herdrcli.Options{
				Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(), Socket: herdrSocketPath,
			})
			if code != 0 {
				os.Exit(code)
			}
			return nil
		},
	}
}
