package main

import (
	"strings"

	"github.com/spf13/cobra"
)

// skillArgs rewrites "--skill TOPIC" on the root command line to
// "--skill=TOPIC", so `tuios --skill fleet` prints the fleet topic.
//
// --skill has an optional value, and pflag binds an optional value only when it
// is written with "=". Without the rewrite the topic would be read as a
// positional argument, and a topic that shares its name with a subcommand
// (mcp, hosts, tmux) would run that subcommand instead of printing the topic.
//
// Only the root command's own flags are rewritten: the walk stops at "--" and
// at the first word that names a subcommand, so an argument to a subcommand
// that happens to read --skill is passed through as written.
func skillArgs(root *cobra.Command, args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" || isSubcommand(root, arg) {
			return append(out, args[i:]...)
		}
		if arg == "--skill" && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			out = append(out, "--skill="+args[i+1])
			i++
			continue
		}
		out = append(out, arg)
	}
	return out
}

// isSubcommand reports whether word names one of root's subcommands or their
// aliases.
func isSubcommand(root *cobra.Command, word string) bool {
	for _, c := range root.Commands() {
		if c.Name() == word || c.HasAlias(word) {
			return true
		}
	}
	return false
}
