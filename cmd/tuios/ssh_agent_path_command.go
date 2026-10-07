package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// newSSHAgentPathCmd is tuios ssh-agent-path: print the session's ssh agent
// link, for a shell rc. See internal/session/ssh_agent_follow.go.
func newSSHAgentPathCmd() *cobra.Command {
	var (
		sessionName string
		jsonOut     bool
	)
	cmd := &cobra.Command{
		Use:   "ssh-agent-path",
		Short: "Print the ssh agent link of a session",
		Long: `Print the ssh agent link of a session.

With ssh_agent = "follow" in [daemon], each session has a link to the ssh
agent socket of the client that attached or used the session last. New panes
get SSH_AUTH_SOCK set to the link. A shell that started before you set the
option keeps its old value. Put this in your shell rc to use the link:

  p=$(tuios ssh-agent-path 2>/dev/null) && export SSH_AUTH_SOCK="$p"

In a pane, the session is the session of the pane. Outside a pane, give it
with -s. The command fails when ssh_agent is not "follow".`,
		Example: `  tuios ssh-agent-path
  tuios ssh-agent-path -s work
  tuios ssh-agent-path -s work --json`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			name := sessionName
			if name == "" {
				name = os.Getenv("TUIOS_SESSION")
			}
			if name == "" {
				return fmt.Errorf("name the session with -s. Outside a pane there is no session to use")
			}
			return runSSHAgentPath(name, jsonOut)
		},
	}
	cmd.Flags().StringVarP(&sessionName, "session", "s", "", "Session whose link to print (default: the pane's session)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON, with the socket the link points at")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}

func runSSHAgentPath(name string, jsonOut bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call("ssh-agent-path", map[string]any{"session": name})
	if err != nil {
		return explainVerbError("ssh-agent-path", err)
	}
	var res struct {
		Session string `json:"session"`
		Path    string `json:"path"`
		Follow  bool   `json:"follow"`
		Target  string `json:"target,omitempty"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if jsonOut {
		return printJSON(res)
	}
	if !res.Follow {
		return &diagnosticError{
			What:  "The daemon does not keep an ssh agent link.",
			Cause: `ssh_agent is not "follow" in [daemon].`,
			Fix:   `set ssh_agent = "follow" in [daemon] of config.toml.`,
		}
	}
	fmt.Println(res.Path)
	return nil
}
