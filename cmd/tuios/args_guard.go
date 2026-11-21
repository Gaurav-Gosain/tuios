package main

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

func refuseUnreadArgs(root *cobra.Command) {
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}
		if c.HasSubCommands() || c.Args != nil || (c.RunE == nil && c.Run == nil) {
			return
		}
		if len(strings.Fields(c.Use)) > 1 {
			return
		}
		c.Args = noArgsWithHint
	}
	walk(root)
}

func noArgsWithHint(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	msg := cmd.CommandPath() + " does not take arguments, and it got " + quoteArgs(args) + "."
	if cmd.Flags().Lookup("window") != nil {
		msg += " To name a window, use -w " + args[0] + "."
	}
	return errfmt.Errorf("%s", msg)
}

func quoteArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = `"` + a + `"`
	}
	return strings.Join(quoted, " ")
}
