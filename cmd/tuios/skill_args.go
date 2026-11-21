package main

import (
	"strings"

	"github.com/spf13/cobra"
)

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

func isSubcommand(root *cobra.Command, word string) bool {
	for _, c := range root.Commands() {
		if c.Name() == word || c.HasAlias(word) {
			return true
		}
	}
	return false
}
