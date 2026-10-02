//go:build slim

package main

import (
	"github.com/Gaurav-Gosain/tuios/internal/edition"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
)

// tuios-slim has no links to other machines. These stubs stand in for the
// host files of the full build, which are tagged !slim. A host flag stays on
// its command, hidden, so a script that passes it reads why it fails.

// registerHostNameCompletion has no host names to offer in tuios-slim.
func registerHostNameCompletion(*cobra.Command, string) {}

// hideHostFlags hides the flags that reach other machines. They stay on
// their commands, so a script that passes one reads why it fails, and not
// "unknown flag".
func hideHostFlags(cmds ...*cobra.Command) {
	for _, cmd := range cmds {
		for _, name := range []string{"host", "ssh", "all-hosts", "global"} {
			if cmd.Flags().Lookup(name) != nil {
				_ = cmd.Flags().MarkHidden(name)
			}
		}
	}
}

// The capture-pane help has no part about other machines in tuios-slim.
const (
	capturePaneHostHelp    = ""
	capturePaneHostExample = ""
)

func runAttachOnHost(string, string, bool, bool, bool) error {
	return edition.Missing("attach --host")
}

func runNewOnHost(string, string, bool, bool, bool) error {
	return edition.Missing("new --host")
}

func runListSessionsAllHosts(host string, _ bool) error {
	if host != "" {
		return edition.Missing("ls --host")
	}
	return edition.Missing("ls --all-hosts")
}

func connectThroughHost(*session.TUIClient, string, int, int, *session.ClientCapabilities) error {
	return edition.Missing("A session on another machine")
}

func dialVerbThroughHost(host, _, _ string) (*session.VerbClient, error) {
	return nil, edition.Missing("The host target " + host + ":")
}

func explainMissingHostSession(_, _ string, _ []string, err error) error { return err }

func runNewGlobalSessionDetached(string) error {
	return edition.Missing("new --global")
}
