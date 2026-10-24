package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// newClearHistoryCommand builds `tuios clear-history`: drop a pane's
// scrollback through the clear-history verb, as tmux clear-history does. With
// --screen it also removes the rows above the prompt, which is what the
// clear_scrollback key does.
func newClearHistoryCommand() *cobra.Command {
	var sessionName, window string
	var screen, jsonOutput bool
	cmd := &cobra.Command{
		Use:   "clear-history",
		Short: "Clear the scrollback of a pane",
		Long: `Clear the scrollback of a pane, as tmux clear-history does. The screen stays.
With --screen, tuios also removes the rows above the prompt, as cmd+k does in
Ghostty and iTerm2. The prompt row then moves to the top of the pane.

The daemon clears the pane for every client of the session. An attach later
does not bring the old rows back.

Without -w, tuios clears the focused pane. tuios clears nothing while a
full-screen program, such as an editor, uses the pane.`,
		Example: `  # Clear the scrollback of the focused pane
  tuios clear-history

  # Clear the scrollback and the screen of the pane named build
  tuios clear-history -w build --screen`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runClearHistory(sessionName, window, screen, jsonOutput)
		},
	}
	cmd.Flags().StringVarP(&sessionName, "session", "s", "", "Target session (default: this pane's session, else the most recently active)")
	cmd.Flags().StringVarP(&window, "window", "w", "", "Target window by name or ID (default: the focused pane)")
	cmd.Flags().BoolVar(&screen, "screen", false, "Also remove the rows above the prompt")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}

func runClearHistory(sessionName, window string, screen, jsonOutput bool) error {
	if sessionName == "" {
		sessionName = os.Getenv("TUIOS_SESSION")
	}
	t, err := dialTarget(sessionName, window)
	if err != nil {
		return err
	}
	defer t.Close()
	raw, err := t.client.Call("clear-history", t.params(map[string]any{"window": nil, "screen": screen}))
	if err != nil {
		return reportVerbError(t.explain("clear-history", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, true)
	}
	fmt.Println("Cleared the scrollback.")
	return nil
}
