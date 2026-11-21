package main

import (
	"os"

	"github.com/spf13/cobra"
)

const sessionFlagUsage = "Target session (default: this pane's session, else the most recently active)"

const windowFlagUsage = "Target window by name or ID (default: focused)"

const jsonFlagUsage = "Output result as JSON"

func addSessionFlag(cmd *cobra.Command, v *string, usage ...string) {
	cmd.Flags().StringVarP(v, "session", "s", "", flagUsage(sessionFlagUsage, usage))
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
}

func addWindowFlag(cmd *cobra.Command, v *string, usage ...string) {
	cmd.Flags().StringVarP(v, "window", "w", "", flagUsage(windowFlagUsage, usage))
}

func addJSONFlag(cmd *cobra.Command, v *bool, usage ...string) {
	cmd.Flags().BoolVar(v, "json", false, flagUsage(jsonFlagUsage, usage))
}

func flagUsage(standard string, usage []string) string {
	if len(usage) > 0 {
		return usage[0]
	}
	return standard
}

func paneSessionOr(session string) string {
	if session != "" {
		return session
	}
	return os.Getenv("TUIOS_SESSION")
}
