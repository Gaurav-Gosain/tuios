package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/spf13/cobra"
)

// newSwitchSessionCmd is tuios switch-session: move an attached client to
// another session in place, as the session switcher does. From a pane it
// moves the client that shows the pane's session, which is what a popup
// command key needs for a sessionizer.
func newSwitchSessionCmd() *cobra.Command {
	var (
		fromSession string
		clientID    string
		create      bool
		cwd         string
		jsonOut     bool
	)
	cmd := &cobra.Command{
		Use:   "switch-session [HOST:]NAME",
		Short: "Switch an attached client to another session",
		Long: `Switch an attached client to another session in place, the way the
session switcher does. Nothing is nested.

The client to switch is:
  - the client given with --client, from tuios list-clients
  - else the client that shows the session given with -s
  - else, from a pane, the client that shows the pane's session
  - else the only client attached

HOST is a machine from the [hosts] table. Without HOST the session is on
this machine.

With --create a missing session is created first. --cwd sets the
directory its windows start in.`,
		Example: `  # From a pane: show the session api
  tuios switch-session api

  # Show api, and create it in ~/dev/api if it is missing
  tuios switch-session --create --cwd ~/dev/api api

  # Show the session api on the machine named build
  tuios switch-session build:api

  # From outside tuios: switch the client that shows work
  tuios switch-session -s work api`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeSessionNames,
		RunE: func(_ *cobra.Command, args []string) error {
			if cwd != "" && !create {
				return fmt.Errorf("--cwd sets the directory of a session --create makes. Add --create")
			}
			target := federation.ParseSessionTarget(args[0])
			if target.Session == "" {
				return fmt.Errorf("name the session to switch to")
			}
			host := target.Host
			if host == federation.LocalHostName {
				host = ""
			}
			if cwd != "" && host == "" {
				abs, err := checkSessionDir(cwd)
				if err != nil {
					return err
				}
				cwd = abs
			}
			return runSwitchSession(target.Session, host, fromSession, clientID, create, cwd, jsonOut)
		},
	}
	cmd.Flags().StringVarP(&fromSession, "session", "s", "", "Switch the client that shows this session")
	cmd.Flags().StringVar(&clientID, "client", "", "Switch the client with this id, from tuios list-clients")
	cmd.Flags().BoolVarP(&create, "create", "c", false, "Create the session if it does not exist")
	cmd.Flags().StringVar(&cwd, "cwd", "", "With --create, the directory the new session's windows start in")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}

func runSwitchSession(name, host, fromSession, clientID string, create bool, cwd string, jsonOut bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	params := map[string]any{"name": name}
	if host != "" {
		params["host"] = host
	}
	if create {
		params["create"] = true
	}
	if cwd != "" {
		params["cwd"] = cwd
	}
	if fromSession != "" {
		params["session"] = fromSession
	}
	if clientID != "" {
		params["client"] = clientID
	}
	raw, err := client.Call("switch-session", params)
	if err != nil {
		return explainVerbError("switch-session", err)
	}
	if jsonOut {
		var pretty any
		if err := json.Unmarshal(raw, &pretty); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}
		out, err := json.MarshalIndent(pretty, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		return nil
	}
	var res struct {
		Session string `json:"session"`
		Host    string `json:"host"`
		Created bool   `json:"created"`
		Already bool   `json:"already"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	where := res.Session
	if res.Host != "" {
		where = res.Session + " on " + res.Host
	}
	switch {
	case res.Already:
		fmt.Printf("The client already shows session %s.\n", where)
	case res.Created:
		fmt.Printf("Created session %s and switched to it.\n", where)
	default:
		fmt.Printf("Switched to session %s.\n", where)
	}
	return nil
}

// checkSessionDir makes dir absolute and checks that it is a directory. The
// daemon starts shells in it, and its own working directory is not the
// caller's, so a relative path would mean somewhere else there.
func checkSessionDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("--cwd %s: %w", dir, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("--cwd %s: the directory does not exist", dir)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("--cwd %s: this is not a directory", dir)
	}
	return abs, nil
}
