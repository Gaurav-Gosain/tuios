package config

import "testing"

// TestTheTwoClockFormatsResolveInOneOrder pins the precedence between the two
// settings that arrived from opposite directions for the same question.
// [dock.clock] format is the specific one and wins; appearance.clock_format is
// the general one and is what an untouched [dock.clock] falls through to. The
// third case is the one the merge of the two nearly lost: before the fall
// through, a session that set only the appearance option watched the dock
// ignore it.
func TestTheTwoClockFormatsResolveInOneOrder(t *testing.T) {
	appearance := DefaultConfig()
	appearance.Appearance.ClockFormat = "3:04 PM"
	ApplyAppearanceConfig(appearance, &Global)
	t.Cleanup(func() { ApplyAppearanceConfig(DefaultConfig(), &Global) })

	specific := DockConfig{Clock: DockClockConfig{Format: "15:04"}}
	if got := specific.DockClockFormat(&Global); got != "15:04" {
		t.Errorf("with both set, format = %q, want the [dock.clock] one, 15:04", got)
	}

	var general DockConfig
	if got := general.DockClockFormat(&Global); got != "3:04 PM" {
		t.Errorf("with only appearance set, format = %q, want it honoured: 3:04 PM", got)
	}

	ApplyAppearanceConfig(DefaultConfig(), &Global)
	if got := general.DockClockFormat(&Global); got != DefaultClockFormat {
		t.Errorf("with neither set, format = %q, want the default %q", got, DefaultClockFormat)
	}
}
