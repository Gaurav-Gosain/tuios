package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Gaurav-Gosain/tuios/internal/tutor"
)

// groupGettingStarted is the help group a new person reads first.
const groupGettingStarted = "getting-started"

// newTutorCommand is tuios tutor: a guided tour in this terminal, on a
// practice copy, with nothing of the person's touched.
func newTutorCommand() *cobra.Command {
	var list, reset, ascii bool
	cmd := &cobra.Command{
		Use:     "tutor [track]",
		Aliases: []string{"learn"},
		GroupID: groupGettingStarted,
		Short:   "Learn tuios in a few minutes, by doing it",
		Long: `Learn tuios in a few minutes, by doing it.

The tutor runs a real tuios with a practice shell. The shell, the files and
the agents in it are pretend. Nothing on your computer changes: the tutor
starts no program, reads none of your config and does not touch a running
tuios. It keeps your progress in $XDG_STATE_HOME/tuios/tutor.json.

In a lesson, F2 opens the menu, F3 shows you the keys and F4 skips a step.
The quit keys of tuios open the menu too.`,
		Example: `  # Start, or continue where you left off
  tuios tutor

  # Open one track at once
  tuios tutor basics

  # List the tracks
  tuios tutor --list`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(_ *cobra.Command, args []string) error {
			switch {
			case list:
				return tutor.List(os.Stdout)
			case reset:
				if err := tutor.Reset(); err != nil {
					return err
				}
				fmt.Println("Tutor progress cleared.")
				return nil
			}
			opts := tutor.Options{ASCII: ascii}
			if len(args) == 1 {
				opts.Track = args[0]
			}
			return tutor.Run(opts)
		},
	}
	cmd.Flags().BoolVar(&list, "list", false, "List the tracks and exit")
	cmd.Flags().BoolVar(&reset, "reset", false, "Clear your tutor progress and exit")
	cmd.Flags().BoolVar(&ascii, "ascii-only", false, "Draw the tutor with ASCII characters only")
	return cmd
}
