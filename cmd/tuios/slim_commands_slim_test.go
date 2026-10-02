//go:build slim

package main

import (
	"errors"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/edition"
)

// TestSlimStubsPrintOneLine runs each command tuios-slim leaves out, with no
// extension installed, and checks it fails with the one line that names it.
func TestSlimStubsPrintOneLine(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, name := range slimDroppedCommands {
		root := newRootCommand()
		root.SetArgs([]string{name, "--help"})
		err := root.Execute()
		var missing *edition.MissingCommandError
		if !errors.As(err, &missing) || missing.Name != name {
			t.Errorf("tuios-slim %s --help returned %v, want the not-in-slim line", name, err)
		}
	}
}
