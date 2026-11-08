package main

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// refuseUnreadArgs gives every leaf command that declares no argument rule
// and names no argument in its Use line a rule that refuses arguments.
//
// cobra accepts any positional argument on a leaf with no Args rule, and a
// command that does not read them drops them in silence. So `tuios
// capture-pane -s work 3f2a` captured the focused pane, not pane 3f2a, and
// said nothing. A command that reads arguments names them in its Use line
// (`attach [session-name]`) or sets Args itself, and keeps them.
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

// noArgsWithHint is cobra.NoArgs with words that say what to do instead.
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
