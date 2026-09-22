package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestRunCommandListOffersOnlyAcceptedValues. 'run-command --list' offered
// SetDockbarPosition top|bottom|left|right, and left and right are refused.
func TestRunCommandListOffersOnlyAcceptedValues(t *testing.T) {
	accepted := map[string][]string{
		"SetDockbarPosition": config.DockbarPositions,
	}
	for _, entry := range runCommandCatalog() {
		fields := strings.Fields(entry.name)
		want, ok := accepted[fields[0]]
		if !ok {
			continue
		}
		delete(accepted, fields[0])
		if len(fields) != 2 {
			t.Errorf("%q: want one argument spelled as a|b|c", entry.name)
			continue
		}
		if got := strings.Split(fields[1], "|"); !slices.Equal(got, want) {
			t.Errorf("%s lists %v, the executor accepts %v", fields[0], got, want)
		}
	}
	for name := range accepted {
		t.Errorf("%s is missing from the run-command list", name)
	}
}

// TestRunCommandCompletionsMatchAccepted checks the shell completions for the
// same commands offer what the executor accepts.
func TestRunCommandCompletionsMatchAccepted(t *testing.T) {
	for command, want := range map[string][]string{
		"SetDockbarPosition": config.DockbarPositions,
		"SetBorderStyle":     config.BorderStyles,
	} {
		if got := getRunCommandArgCompletions(command, 1, ""); !slices.Equal(got, want) {
			t.Errorf("%s completes %v, the executor accepts %v", command, got, want)
		}
	}
}
