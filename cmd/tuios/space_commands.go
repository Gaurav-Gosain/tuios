package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/spf13/cobra"
)

// The space commands: a space is a label on sessions (see set-session-space),
// so each of these acts on every session filed under one.

// callSpaceVerb calls verb with params and decodes the result into out, or
// prints it as indented JSON when jsonOut is set and out is then left as is.
func callSpaceVerb(verb string, params map[string]any, jsonOut bool, out any) (printed bool, err error) {
	client, err := dialVerb()
	if err != nil {
		return false, err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call(verb, params)
	if err != nil {
		return false, explainVerbError(verb, err)
	}
	if jsonOut {
		var pretty any
		if err := json.Unmarshal(raw, &pretty); err != nil {
			return false, fmt.Errorf("failed to parse response: %w", err)
		}
		return true, printJSON(pretty)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}
	return false, nil
}

func newRenameSpaceCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "rename-space <space> <new-name>",
		Short: "Rename a space",
		Long: `Rename a space: every session filed under it is filed under the new name.
If the new name is a space already, the two become one.

The rename is made one session at a time. If it stops part way, some sessions
are left under each name, and running the same command again moves the rest.`,
		Example: `  tuios rename-space payments billing`,
		Args:    cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			var res struct {
				Space    string   `json:"space"`
				OldSpace string   `json:"old_space"`
				Sessions []string `json:"sessions"`
				Already  bool     `json:"already"`
			}
			printed, err := callSpaceVerb("rename-space", map[string]any{"space": args[0], "name": args[1]}, jsonOut, &res)
			if err != nil || printed {
				return err
			}
			if res.Already {
				fmt.Printf("No session is left in space '%s'. Space '%s' already holds its sessions.\n", res.OldSpace, res.Space)
				return nil
			}
			fmt.Printf("Renamed space '%s' to '%s': %s (%s).\n", res.OldSpace, res.Space,
				plural.Count(len(res.Sessions), "session"), strings.Join(res.Sessions, ", "))
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func newKillSpaceCmd() *cobra.Command {
	var force, jsonOut bool
	cmd := &cobra.Command{
		Use:   "kill-space <space> --force",
		Short: "Kill every session in a space",
		Long: `Terminate every session filed under a space, and every window in them,
as kill-session does one session.

It ends every process in every one of those sessions, so it needs --force.
Without it nothing is killed, and the command names the sessions it would kill.`,
		Example: `  # See what would be killed
  tuios kill-space payments

  # Kill them
  tuios kill-space payments --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			params := map[string]any{"space": args[0]}
			if force {
				params["force"] = true
			}
			var res struct {
				Space    string   `json:"space"`
				Sessions []string `json:"sessions"`
			}
			printed, err := callSpaceVerb("kill-space", params, jsonOut, &res)
			if err != nil || printed {
				return err
			}
			fmt.Printf("Killed space '%s': %s (%s).\n", res.Space,
				plural.Count(len(res.Sessions), "session"), strings.Join(res.Sessions, ", "))
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Kill the sessions. Without it nothing is killed")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func newSwitchSpaceCmd() *cobra.Command {
	var fromSession, clientID string
	var jsonOut, none bool
	cmd := &cobra.Command{
		Use:   "switch-space <space> | --none",
		Short: "Switch an attached client to a space",
		Long: `Switch an attached client to the session of a space the person used last,
in place, as switch-session does. A session nobody has used ranks by its
latest activity.

--none switches among the sessions in no space instead, the rail's none row. A
global session counts as in no space, as the rail draws it.

The client to switch is chosen as switch-session chooses it:
  - the client given with --client, from tuios list-clients
  - else the client that shows the session given with -s
  - else, from a pane, the client that shows the pane's session
  - else the only client attached`,
		Example: `  # From a pane: show the space payments
  tuios switch-space payments

  # From outside tuios: switch the client that shows work
  tuios switch-space -s work payments

  # The session in no space used last
  tuios switch-space --none`,
		Args: func(_ *cobra.Command, args []string) error {
			switch {
			case none && len(args) > 0:
				return fmt.Errorf("--none switches to the sessions in no space, so it takes no space name (got %q)", args[0])
			case !none && len(args) != 1:
				return fmt.Errorf("name one space, or pass --none for the sessions in no space")
			}
			return nil
		},
		RunE: func(_ *cobra.Command, args []string) error {
			params := map[string]any{}
			if none {
				params["none"] = true
			} else {
				params["space"] = args[0]
			}
			if fromSession != "" {
				params["session"] = fromSession
			}
			if clientID != "" {
				params["client"] = clientID
			}
			var res struct {
				Space   string `json:"space"`
				Session string `json:"session"`
				Already bool   `json:"already"`
			}
			printed, err := callSpaceVerb("switch-space", params, jsonOut, &res)
			if err != nil || printed {
				return err
			}
			where := "of space " + res.Space
			if none {
				where = "in no space"
			}
			if res.Already {
				fmt.Printf("The client already shows session %s %s.\n", res.Session, where)
				return nil
			}
			fmt.Printf("Switched to session %s %s.\n", res.Session, where)
			return nil
		},
	}
	cmd.Flags().BoolVar(&none, "none", false, "Switch among the sessions in no space")
	cmd.Flags().StringVarP(&fromSession, "session", "s", "", "Switch the client that shows this session")
	cmd.Flags().StringVar(&clientID, "client", "", "Switch the client with this id, from tuios list-clients")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}
