//go:build !slim

package config

import (
	"slices"
	"testing"

	tfx "github.com/Gaurav-Gosain/tuiffects"
)

// TestSlimScreensaverEffectNames holds tuios-slim's written-out list of
// effect names equal to the engine's, so a config file that loads in one
// build loads in the other.
func TestSlimScreensaverEffectNames(t *testing.T) {
	if got, want := slimScreensaverEffectNames, tfx.Names(); !slices.Equal(got, want) {
		t.Fatalf("slimScreensaverEffectNames = %q, the engine has %q", got, want)
	}
}
