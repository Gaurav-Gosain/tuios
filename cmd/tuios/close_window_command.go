package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// newCloseWindowCommand builds `tuios close-window`: close one pane by name or
// id, through the close-window verb. Scripts used to reach it through
// run-command CloseWindow, which needs the binary protocol and the admin
// grant for every command it can run.
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
	cmd.Flags().StringVarP(&sessionName, "session", "s", "", "Target session (default: this pane's session, else the most recently active)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}

func runCloseWindow(sessionName, window string, jsonOutput bool) error {
	if sessionName == "" {
		sessionName = os.Getenv("TUIOS_SESSION")
	}
	// The window goes through dialTarget like -w does everywhere, so a host
	// prefix resolves. params fills the window key from the target: an empty
	// one would close the focused pane.
	t, err := dialTarget(sessionName, window)
	if err != nil {
		return err
	}
	defer t.Close()
	if t.window == "" {
		return fmt.Errorf("name the pane to close")
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
