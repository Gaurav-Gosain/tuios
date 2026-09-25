package app

import (
	"testing"
)

// TestContextPercentReads: the forms a context value comes in.
func TestContextPercentReads(t *testing.T) {
	for v, want := range map[string]float64{"42%": 42, " 81.5% ": 81.5, "90": 90, "84% ctx": 84} {
		if got, ok := sidebarContextPercent(v); !ok || got != want {
			t.Errorf("sidebarContextPercent(%q) = %v, %v; want %v", v, got, ok, want)
		}
	}
	for _, v := range []string{"", "12000 tokens", "140%", "-3%"} {
		if _, ok := sidebarContextPercent(v); ok {
			t.Errorf("sidebarContextPercent(%q) read as a percent", v)
		}
	}
}
