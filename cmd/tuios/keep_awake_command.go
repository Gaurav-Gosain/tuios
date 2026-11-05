package main

import (
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
)

// keepAwakeStatus is what get-keep-awake and set-keep-awake answer.
type keepAwakeStatus struct {
	Mode      string `json:"mode"`
	Held      bool   `json:"held"`
	Working   int    `json:"working"`
	Method    string `json:"method"`
	Error     string `json:"error,omitempty"`
	Saved     *bool  `json:"saved,omitempty"`
	SaveError string `json:"save_error,omitempty"`
}

// newKeepAwakeCmd is tuios keep-awake: show or set [daemon] keep_awake. See
// internal/session/keep_awake.go.
func newKeepAwakeCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "keep-awake [off|on|agents]",
		Short: "Keep the machine awake while agents work",
		Long: `Keep the machine awake while agents work.

With no argument, the command shows the mode and the hold. With a mode, it
sets daemon.keep_awake, applies it at once and saves it to config.toml.

  agents  The machine stays awake while an agent pane on this machine works.
  on      The machine stays awake while the daemon runs.
  off     The machine sleeps as its own settings say. This is the default.

The daemon holds the machine awake, so it works with no client attached and
on a host. Only sleep is held off. The display can still dim and lock.
On Linux the daemon uses systemd-inhibit, on macOS caffeinate, and on Windows
SetThreadExecutionState.`,
		Example: `  tuios keep-awake
  tuios keep-awake agents
  tuios keep-awake off
  tuios keep-awake --json`,
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: config.KeepAwakeModes,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && !slices.Contains(config.KeepAwakeModes, args[0]) {
				return errfmt.Errorf("%q is not a mode. Use off, on or agents", args[0])
			}
			return runKeepAwake(cmd.OutOrStdout(), args, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func runKeepAwake(w io.Writer, args []string, jsonOut bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	verb, params := "get-keep-awake", map[string]any{}
	if len(args) == 1 {
		verb, params = "set-keep-awake", map[string]any{"mode": args[0]}
	}
	raw, err := client.Call(verb, params)
	if err != nil {
		return explainVerbError(verb, err)
	}
	var st keepAwakeStatus
	if err := jsonx.Unmarshal(raw, &st); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	if jsonOut {
		return printJSON(st)
	}
	writeKeepAwake(w, st)
	if st.SaveError != "" {
		fmt.Fprintf(os.Stderr, "Warning: config.toml was not written: %s. The mode applies until the daemon stops.\n", st.SaveError)
	}
	return nil
}

// writeKeepAwake prints the status in a few plain lines.
func writeKeepAwake(w io.Writer, st keepAwakeStatus) {
	fmt.Fprintf(w, "Mode:     %s\n", st.Mode)
	agents := fmt.Sprintf("%d agents work", st.Working)
	if st.Working == 1 {
		agents = "1 agent works"
	}
	switch {
	case st.Error != "":
		fmt.Fprintf(w, "Hold:     none. The machine cannot be kept awake: %s\n", st.Error)
	case st.Held:
		fmt.Fprintf(w, "Hold:     the machine stays awake (%s)\n", st.Method)
	default:
		fmt.Fprintln(w, "Hold:     none. The machine can sleep")
	}
	fmt.Fprintf(w, "Agents:   %s on this machine\n", agents)
}

// newDoctorKeepAwakeCommand is tuios doctor keep-awake: check that this
// machine can be held awake, and show what the daemon does now.
func newDoctorKeepAwakeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "keep-awake",
		Short: "Check that tuios can keep this machine awake",
		Long: `Check that tuios can keep this machine awake.

The command checks the program the daemon uses to hold the machine awake.
When a daemon runs, it also shows daemon.keep_awake and the hold.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDoctorKeepAwake(cmd.OutOrStdout())
		},
	}
}

func runDoctorKeepAwake(w io.Writer) error {
	method, ok, detail := keepAwakeMethodCheck()
	if ok {
		fmt.Fprintf(w, "Method:   %s, ready\n", method)
	} else {
		fmt.Fprintf(w, "Method:   %s, not ready: %s\n", method, detail)
	}
	if !session.IsDaemonRunning() {
		fmt.Fprintln(w, "Daemon:   not running. Start tuios to see the hold")
		return nil
	}
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	raw, err := client.Call("get-keep-awake", map[string]any{})
	if err != nil {
		return explainVerbError("get-keep-awake", err)
	}
	var st keepAwakeStatus
	if err := jsonx.Unmarshal(raw, &st); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	writeKeepAwake(w, st)
	if st.Mode == config.KeepAwakeOff {
		fmt.Fprintln(w, "Turn it on with 'tuios keep-awake agents'.")
	}
	return nil
}
