package config

import "testing"

func TestOuterGapFit(t *testing.T) {
	cases := []struct {
		gap, free, floor, want int
	}{
		{0, 100, 20, 0},  // off
		{-3, 100, 20, 0}, // a negative gap is no gap
		{3, 100, 20, 3},  // room to spare
		{OuterGapMax + 5, 1000, 20, OuterGapMax},
		{8, 30, 20, 5}, // (30-20)/2: the panes keep the floor
		{8, 21, 20, 0}, // one spare cell cannot be split over two sides
		{8, 10, 20, 0}, // already under the floor
	}
	for _, c := range cases {
		if got := OuterGapFit(c.gap, c.free, c.floor); got != c.want {
			t.Errorf("OuterGapFit(%d, %d, %d) = %d, want %d", c.gap, c.free, c.floor, got, c.want)
		}
	}
}

func TestOuterGapOptionIsRegistered(t *testing.T) {
	o, ok := LookupOption("appearance.outer_gap")
	if !ok {
		t.Fatal("appearance.outer_gap is not in the option registry, so set-config and the settings page cannot reach it")
	}
	if o.Type != OptionInt || o.Default != "0" || o.Min != 0 || o.Max != OuterGapMax {
		t.Fatalf("appearance.outer_gap is registered as %+v", o)
	}
}
