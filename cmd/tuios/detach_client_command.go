package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// newDetachClientCmd is tuios detach-client: take attached clients off their
// sessions, as tmux detach-client does. The sessions keep running.
func newDetachClientCmd() *cobra.Command {
	var (
		clientID    string
		fromSession string
		allOther    bool
		jsonOut     bool
	)
	cmd := &cobra.Command{
		Use:   "detach-client",
		Short: "Detach clients from their sessions",
		Long: `Detach attached clients from their sessions, as tmux detach-client does.
The sessions continue to run. Each detached client exits with a message.

The clients to detach are:
  - the client given with --client, from tuios list-clients
  - else every client of the session given with -s
  - else, from a pane, the client used last in the pane's session
  - else the client used last in the only session with a client

--all-other keeps the client given with --client, or the client used last
in the session, and detaches every other client of that session.

From a pane, this command needs the admin grant.`,
		Example: `  # Detach one client
  tuios detach-client --client c3

  # Detach every client of the session work
  tuios detach-client -s work

  # Keep the client used last in work, and detach the others
  tuios detach-client -s work --all-other`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if clientID != "" && fromSession != "" {
				return fmt.Errorf("give --client or --session, not both")
			}
			return runDetachClient(clientID, fromSession, allOther, jsonOut)
		},
	}
	cmd.Flags().StringVar(&clientID, "client", "", "Detach the client with this id, from tuios list-clients")
	cmd.Flags().StringVarP(&fromSession, "session", "s", "", "Detach every client of this session")
	cmd.Flags().BoolVarP(&allOther, "all-other", "a", false, "Keep one client and detach every other client of its session")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}

func runDetachClient(clientID, fromSession string, allOther, jsonOut bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	params := map[string]any{}
	if clientID != "" {
		params["client"] = clientID
	}
	if fromSession != "" {
		params["session"] = fromSession
	}
	if allOther {
		params["all_other"] = true
	}
	raw, err := client.Call("detach-client", params)
	if err != nil {
		return explainVerbError("detach-client", err)
	}
	var res struct {
		Detached []string `json:"detached"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if jsonOut {
		return printJSON(res)
	}
	switch len(res.Detached) {
	case 0:
		fmt.Println("No client was detached.")
	case 1:
		fmt.Printf("Detached client %s.\n", res.Detached[0])
	default:
		fmt.Printf("Detached %d clients: %s.\n", len(res.Detached), strings.Join(res.Detached, ", "))
	}
	return nil
}
