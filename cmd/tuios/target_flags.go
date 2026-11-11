package main

import (
	"os"

	"github.com/spf13/cobra"
)

// The flags that name what a command acts on, and how --json reads, declared
// once. A command with a session, window or json flag of its own meaning
// passes its own usage line.

// sessionFlagUsage is the help line of a --session flag that takes the
// daemon's default.
const sessionFlagUsage = "Target session (default: this pane's session, else the most recently active)"

// windowFlagUsage is the help line of a --window flag that takes the focused
// window by default.
const windowFlagUsage = "Target window by name or ID (default: focused)"

// jsonFlagUsage is the help line of a --json flag that prints the result.
const jsonFlagUsage = "Output result as JSON"

// addSessionFlag adds -s/--session to cmd, bound to v, with session name
// completion. usage replaces sessionFlagUsage when given.
func addSessionFlag(cmd *cobra.Command, v *string, usage ...string) {
	cmd.Flags().StringVarP(v, "session", "s", "", flagUsage(sessionFlagUsage, usage))
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
}

// addWindowFlag adds -w/--window to cmd, bound to v. usage replaces
// windowFlagUsage when given.
func addWindowFlag(cmd *cobra.Command, v *string, usage ...string) {
	cmd.Flags().StringVarP(v, "window", "w", "", flagUsage(windowFlagUsage, usage))
}

// addJSONFlag adds --json to cmd, bound to v. usage replaces jsonFlagUsage
// when given.
func addJSONFlag(cmd *cobra.Command, v *bool, usage ...string) {
	cmd.Flags().BoolVar(v, "json", false, flagUsage(jsonFlagUsage, usage))
}

func flagUsage(standard string, usage []string) string {
	if len(usage) > 0 {
		return usage[0]
	}
	return standard
}

// paneSessionOr is session, or the session of the pane this runs in when
// session is empty: TUIOS_SESSION. Outside a pane it stays empty, and the
// daemon takes its own default.
func paneSessionOr(session string) string {
	if session != "" {
		return session
	}
	return os.Getenv("TUIOS_SESSION")
}
