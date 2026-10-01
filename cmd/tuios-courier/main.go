// Command tuios-courier carries agent mail between people's machines where
// tuios links cannot reach, through a relay over HTTPS.
//
// It is a separate binary from tuios on purpose, the way tuios-web is: it adds
// no way into the daemon. It does not link the daemon's packages (a test keeps
// it so), never talks to the daemon's socket, and only carries text. See
// docs/COURIER.md.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/fang"
	"github.com/Gaurav-Gosain/tuios/skills"
	"github.com/spf13/cobra"
)

// Version information (set by goreleaser)
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
	builtBy = "unknown"
)

// exitError ends the program with a status other than 1, after fang has
// printed the error.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

// agentEnv labels the agent running in this pane, for --as and --agent.
const agentEnv = "TUIOS_COURIER_AGENT"

func newRootCmd() *cobra.Command {
	var printSkill bool
	root := &cobra.Command{
		Use:   "tuios-courier",
		Short: "Agent mail between machines, through a relay over HTTPS",
		Long: `tuios-courier carries mail between people's agents on different machines,
where tuios links cannot reach. Mail is signed and sealed to the recipient,
goes through a relay over HTTPS, and is held for the recipient's person until
they release it.

Agents: run tuios-courier --skill to learn how to use it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if printSkill {
				_, err := fmt.Fprint(cmd.OutOrStdout(), skills.Courier)
				return err
			}
			return cmd.Help()
		},
	}
	root.Flags().BoolVar(&printSkill, "skill", false, "Print the agent skill for tuios-courier")
	root.AddCommand(
		newInitCmd(), newWhoamiCmd(), newPeersCmd(),
		newSendCmd(), newReplyCmd(), newReadCmd(), newWaitCmd(),
		newInboxCmd(), newShowCmd(), newReleaseCmd(), newDropCmd(), newWatchCmd(),
		newHookCmd(), newIntegrationCmd(), newRelayCmd(),
	)
	return root
}

func main() {
	err := fang.Execute(
		context.Background(),
		newRootCmd(),
		fang.WithVersion(fmt.Sprintf("%s\nCommit: %s\nBuilt: %s\nBy: %s", version, commit, date, builtBy)),
		fang.WithNotifySignal(os.Interrupt, syscall.SIGTERM),
	)
	if err != nil {
		var ee *exitError
		if errors.As(err, &ee) {
			os.Exit(ee.code)
		}
		os.Exit(1)
	}
}
