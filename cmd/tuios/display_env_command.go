package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/spf13/cobra"
)

// newDisplayEnvCmd is tuios display-env: print the display a new pane of the
// session gets, as export lines for eval. See
// internal/session/display_follow.go.
func newDisplayEnvCmd() *cobra.Command {
	var (
		sessionName string
		jsonOut     bool
	)
	cmd := &cobra.Command{
		Use:   "display-env",
		Short: "Print the display of a session as export lines",
		Long: `Print the display of a session as export lines.

A daemon that started without a display, for example over ssh, gives new
panes the display of the local client that attached or used the session last.
This is display = "follow" in [daemon], the default. A shell that started
before that client attached has no display. Run this line in the shell, or
put it in your shell rc:

  eval "$(tuios display-env 2>/dev/null)"

The command prints export lines for WAYLAND_DISPLAY, DISPLAY and XAUTHORITY.
It prints nothing when the session has no display to give.

In a pane, the session is the session of the pane. Outside a pane, give it
with -s.`,
		Example: `  eval "$(tuios display-env)"
  tuios display-env -s work
  tuios display-env -s work --json`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			name := sessionName
			if name == "" {
				name = os.Getenv("TUIOS_SESSION")
			}
			if name == "" {
				return fmt.Errorf("name the session with -s. Outside a pane there is no session to use")
			}
			return runDisplayEnv(name, jsonOut)
		},
	}
	cmd.Flags().StringVarP(&sessionName, "session", "s", "", "Session whose display to print (default: the pane's session)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON, with the link and the socket it points at")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}

func runDisplayEnv(name string, jsonOut bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call("display-env", map[string]any{"session": name})
	if err != nil {
		return explainVerbError("display-env", err)
	}
	var res struct {
		Session string            `json:"session"`
		Follow  bool              `json:"follow"`
		Own     bool              `json:"own"`
		Link    string            `json:"link"`
		Target  string            `json:"target,omitempty"`
		Env     map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if jsonOut {
		return printJSON(res)
	}
	for _, k := range slices.Sorted(maps.Keys(res.Env)) {
		fmt.Printf("export %s=%s\n", k, shellQuote(res.Env[k]))
	}
	return nil
}
