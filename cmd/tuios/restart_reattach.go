package main

import (
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var attachAfterRestart bool

var ranCommand, attachCommand *cobra.Command

func reattachAfterRestart(e *app.RestartExec) error {
	fmt.Printf("The daemon restarted. tuios attaches to session '%s' again.\n", e.Session)
	return execClient(e.Binary, restartAttachArgs(e.Session))
}

func restartAttachArgs(session string) []string {
	args := carriedFlags()
	return append(args, "attach", "--after-restart", "--", session)
}

var attachAfterUpgrade string

func followUpgrade(e *app.UpgradeExec) error {
	fmt.Printf("The daemon was upgraded to %s. tuios starts it on session '%s'.\n", e.Version, e.Session)
	return execClient(e.Binary, upgradeAttachArgs(e.Session, e.Version))
}

func upgradeAttachArgs(session, version string) []string {
	args := carriedFlags()
	return append(args, "attach", "--after-upgrade="+version, "--", session)
}

var attachOwnFlags = map[string]bool{"create": true, "host": true, "ssh": true, "hold": true, "force": true, "detach-others": true, "terminal-mode": true, "after-restart": true, "after-upgrade": true}

func carriedFlags() []string {
	if ranCommand == nil || attachCommand == nil {
		return nil
	}
	var out []string
	persistent := ranCommand.Root().PersistentFlags()
	ranCommand.Flags().Visit(func(f *pflag.Flag) {
		root := persistent.Lookup(f.Name) != nil && f.Name != "cpuprofile"
		if root || (!attachOwnFlags[f.Name] && attachCommand.Flags().Lookup(f.Name) != nil) {
			out = append(out, "--"+f.Name+"="+f.Value.String())
		}
	})
	return out
}

func appRestartFailure(f *app.RestartFailure) error {
	return &diagnosticError{What: f.What, Fix: strings.TrimSuffix(strings.TrimSpace(f.Fix), ".") + "."}
}

func afterUpgradeFor(host string) string {
	if host != "" {
		return ""
	}
	return attachAfterUpgrade
}
