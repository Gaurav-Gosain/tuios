package main

import (
	"testing"

	"github.com/spf13/pflag"

	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"
)

// TestTalkFlagsMatchTheCommands holds the flags the agents view reads a
// message command line with (transcriptview.TalkFlags) to the commands
// themselves. A flag the table does not know makes the line unreadable, so a
// command that gains a flag would turn every message card that uses it back
// into a plain command card. A flag the table reads with or without a value
// the wrong way would read the message as a flag's value.
func TestTalkFlagsMatchTheCommands(t *testing.T) {
	short := transcriptview.TalkShorthands()
	for name, flags := range transcriptview.TalkFlags() {
		root := newRootCommand()
		if name == "" {
			root.PersistentFlags().VisitAll(func(f *pflag.Flag) {
				takes, ok := flags[f.Name]
				if !ok {
					t.Errorf("tuios --%s is not in the talk flags", f.Name)
				} else if takes != (f.NoOptDefVal == "") {
					t.Errorf("tuios --%s: the talk flags say value=%v", f.Name, takes)
				}
			})
			continue
		}
		cmd, _, err := root.Find([]string{name})
		if err != nil || cmd.Name() != name {
			t.Errorf("tuios %s is no command: %v", name, err)
			continue
		}
		seen := map[string]bool{}
		check := func(f *pflag.Flag) {
			seen[f.Name] = true
			takes, ok := flags[f.Name]
			switch {
			case !ok:
				t.Errorf("tuios %s --%s is not in the talk flags", name, f.Name)
			case takes != (f.NoOptDefVal == ""):
				t.Errorf("tuios %s --%s: the talk flags say value=%v", name, f.Name, takes)
			}
			if f.Shorthand != "" && short[name][f.Shorthand[0]] != f.Name {
				t.Errorf("tuios %s -%s is %s, the talk flags say %q", name, f.Shorthand, f.Name, short[name][f.Shorthand[0]])
			}
		}
		cmd.InitDefaultHelpFlag()
		cmd.LocalFlags().VisitAll(check)
		for flag := range flags {
			if !seen[flag] {
				t.Errorf("the talk flags give tuios %s a --%s it does not have", name, flag)
			}
		}
	}
}
