package main

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestSetConfigHelpExamplesAreAccepted checks every `tuios set-config <path>
// <value>` line in the command's help against the option registry. The help
// once showed `set-config animations toggle`, which the daemon refuses: there
// is no option called animations, and a bool does not take toggle.
func TestSetConfigHelpExamplesAreAccepted(t *testing.T) {
	cmd, _, err := newRootCommand().Find([]string{"set-config"})
	if err != nil {
		t.Fatalf("Find(set-config): %v", err)
	}

	checked := 0
	for _, line := range strings.Split(cmd.Long+"\n"+cmd.Example, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 4 || fields[0] != "tuios" || fields[1] != "set-config" {
			continue
		}
		path, value := fields[2], fields[3]
		// The daemon also accepts a bare [appearance] option name.
		if _, ok := config.LookupOption(path); !ok && !strings.Contains(path, ".") {
			path = "appearance." + path
		}
		if _, ok := config.LookupOption(path); !ok {
			t.Errorf("help example %q: no such option %q", strings.TrimSpace(line), fields[2])
			continue
		}
		if err := config.SetOptionValue(config.DefaultConfig(), path, value); err != nil {
			t.Errorf("help example %q: %v", strings.TrimSpace(line), err)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("found no set-config examples in the help to check")
	}
}
