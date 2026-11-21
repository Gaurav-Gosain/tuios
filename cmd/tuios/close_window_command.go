package main

import (
	"fmt"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/spf13/cobra"
)

func newCloseWindowCommand() *cobra.Command {
	var sessionName string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "close-window <window>",
		Short: "Close one pane",
		Long: `Close one pane and stop its program. Name the pane by its name, its id or
the number list-windows prints.

Inside a tuios pane, the session is the session of the pane. Outside, it is the
most recently active session. -s names a different one.

It works with no client attached. A pane whose program has exited stays in
list-windows until something closes it.`,
		Example: `  # Close the pane named build
  tuios close-window build

  # Close a pane by id in the session work
  tuios close-window -s work 86e5e19f`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runCloseWindow(sessionName, args[0], jsonOutput)
		},
	}
	addSessionFlag(cmd, &sessionName)
	addJSONFlag(cmd, &jsonOutput)
	return cmd
}

func runCloseWindow(sessionName, window string, jsonOutput bool) error {
	sessionName = paneSessionOr(sessionName)
	t, err := dialTarget(sessionName, window)
	if err != nil {
		return err
	}
	defer t.Close()
	if t.window == "" {
		return errfmt.Errorf("name the pane to close")
	}
	raw, err := t.client.Call("close-window", t.params(map[string]any{"window": nil}))
	if err != nil {
		return reportVerbError(t.explain("close-window", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, true)
	}
	fmt.Printf("Closed %s.\n", window)
	return nil
}
