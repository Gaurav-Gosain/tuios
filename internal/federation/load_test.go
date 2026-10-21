package federation

import (
	"math"
	"testing"
)

// A far daemon is untrusted: the load its hello carries is held to what a
// machine can report before anything ranks or draws it. Security boundary
// test.
func TestLoadIsBounded(t *testing.T) {
	for _, tc := range []struct {
		in   Load
		want Load
	}{
		{Load{Cores: -4, Load1: -1, MemPct: -3, Working: -2}, Load{}},
		{Load{Cores: 1 << 30, Load1: 1e300, MemPct: 1e9, Working: 1 << 30}, Load{Cores: 1 << 16, Load1: 1 << 20, MemPct: 100, Working: 1 << 16}},
		{Load{Cores: 8, Load1: math.NaN(), MemPct: math.NaN()}, Load{Cores: 8}},
		{Load{Cores: 8, Load1: 1.5, MemPct: 40, Working: 3}, Load{Cores: 8, Load1: 1.5, MemPct: 40, Working: 3}},
	} {
		if got := tc.in.bounded(); got != tc.want {
			t.Errorf("%+v bounded to %+v, want %+v", tc.in, got, tc.want)
		}
	}
}
