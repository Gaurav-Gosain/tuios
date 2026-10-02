//go:build !slim

package main

import (
	"slices"
	"testing"

	"github.com/spf13/cobra"
)

// TestSlimDroppedCommandsMatchFeatures holds slimDroppedCommands equal to the
// commands addFeatureCommands adds, so tuios-slim has a stub for every
// command it leaves out and for nothing else.
func TestSlimDroppedCommandsMatchFeatures(t *testing.T) {
	root := &cobra.Command{Use: "tuios"}
	addFeatureCommands(root)
	var got []string
	for _, c := range root.Commands() {
		got = append(got, c.Name())
		if len(c.Aliases) > 0 {
			t.Errorf("%s has aliases %q; give the slim stub the same aliases", c.Name(), c.Aliases)
		}
	}
	want := slices.Sorted(slices.Values(slimDroppedCommands))
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("addFeatureCommands adds %q, slimDroppedCommands names %q", got, want)
	}
}
