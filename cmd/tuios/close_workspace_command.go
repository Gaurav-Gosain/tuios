package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
)

// newCloseWorkspaceCommand builds `tuios close-workspace`: close every pane on
// a workspace in one call, like tmux kill-window.
func newCloseWorkspaceCommand() *cobra.Command {
	var sessionName string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "close-workspace [workspace]",
		Short: "Close every pane on a workspace",
		Long: `Close every pane on a workspace, like tmux kill-window. Without a number,
tuios closes the panes of the current workspace.

Inside a tuios pane, the session is the session of the pane. Outside, it is the
most recently active session. -s names a different one.

Scratch panes stay open. To close a scratch group, name its scratch workspace.
A pane that runs this command needs the admin grant.`,
		Example: `  # Close the panes that tuios xpanes opened on workspace 2
  tuios close-workspace 2

  # Close the panes of the current workspace in the session work
  tuios close-workspace -s work`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ws := 0
			if len(args) == 1 {
				n, err := strconv.Atoi(args[0])
				if err != nil || n < 1 {
					return fmt.Errorf("the workspace must be a number from 1, not %q", args[0])
				}
				ws = n
			}
			return runCloseWorkspace(sessionName, ws, jsonOutput)
		},
	}
	cmd.Flags().StringVarP(&sessionName, "session", "s", "", "Target session (default: this pane's session, else the most recently active)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}

func runCloseWorkspace(sessionName string, ws int, jsonOutput bool) error {
	if sessionName == "" {
		sessionName = os.Getenv("TUIOS_SESSION")
	}
	t, err := dialSessionTarget(sessionName)
	if err != nil {
		return err
	}
	defer t.Close()
	params := map[string]any{}
	if ws != 0 {
		params["workspace"] = ws
	}
	raw, err := t.client.Call("close-workspace", t.params(params))
	if err != nil {
		return reportVerbError(t.explain("close-workspace", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, true)
	}
	var res struct {
		Workspace int      `json:"workspace"`
		Closed    []string `json:"closed"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	fmt.Println(closeWorkspaceSummary(res.Workspace, len(res.Closed)))
	return nil
}

// closeWorkspaceSummary is the line close-workspace prints.
func closeWorkspaceSummary(ws, n int) string {
	switch n {
	case 0:
		return fmt.Sprintf("Workspace %d has no panes to close.", ws)
	case 1:
		return fmt.Sprintf("Closed 1 pane on workspace %d.", ws)
	}
	return fmt.Sprintf("Closed %d panes on workspace %d.", n, ws)
}
