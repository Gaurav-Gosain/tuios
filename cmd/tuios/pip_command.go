package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// newPiPCommand is tuios pip: the CLI half of the pip verb.
func newPiPCommand() *cobra.Command {
	var session string
	var off, jsonOutput bool
	cmd := &cobra.Command{
		Use:   "pip [window]",
		Short: "Pin a pane as the picture-in-picture view",
		Long: `Pin a pane as the picture-in-picture view of the attached client, or unpin it.

The view is a small live copy of the pane in a corner of the screen. It shows
while another pane has the focus. The pane stays where it is in the layout.
A click on the view goes to the pane.

Name the pane by id or name. Omit it to pin the focused pane. Name the pinned
pane again to unpin it. --off unpins whatever is pinned.

The view belongs to the client, not to the session. It needs an attached
client, and a detach forgets it. Set its size and corner in the [pip] table.`,
		Example: `  # Watch the agent pane while you work in another pane
  tuios pip agent

  # Take the view away
  tuios pip --off`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			window := ""
			if len(args) > 0 {
				window = args[0]
			}
			if off && window != "" {
				return fmt.Errorf("--off unpins whatever is pinned, so it takes no window")
			}
			return runPiP(session, window, off, jsonOutput)
		},
	}
	cmd.Flags().StringVarP(&session, "session", "s", "", "Target session (default: most recently active)")
	cmd.Flags().BoolVar(&off, "off", false, "Unpin whatever is pinned")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}

// runPiP calls the pip verb and says what is pinned now.
func runPiP(sessionName, window string, off, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	params := map[string]any{"session": sessionName}
	if window != "" {
		params["window"] = window
	}
	if off {
		params["off"] = true
	}
	raw, err := client.Call("pip", params)
	if err != nil {
		return reportVerbError(explainVerbError("pip", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	var res struct {
		Pinned   bool   `json:"pinned"`
		WindowID string `json:"window_id"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if !res.Pinned {
		fmt.Println("No pane is pinned.")
		return nil
	}
	fmt.Printf("Pinned %s.\n", shortWindowID(res.WindowID))
	return nil
}
