package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Window IDs are UUIDs when tuios makes them, but a restored session or the
// daemon wire can hand over anything, and the log lines on the close path used
// to slice them at 8 unconditionally.
func TestDeleteWindowSurvivesAShortID(t *testing.T) {
	m := splitOS(t)
	m.Windows[0].ID = "w1"
	m.Windows = append(m.Windows, &terminal.Window{ID: "", Workspace: 1, Width: 200, Height: 60})
	m.AddWindowToBSPTree(m.Windows[1])

	m.DeleteWindow(1)
	m.DeleteWindow(0)

	if len(m.Windows) != 0 {
		t.Fatalf("wanted every window closed, got %d left", len(m.Windows))
	}
}

func TestShortIDTruncatesOnlyWhatItCan(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"w1", "w1"},
		{"8bf1c038", "8bf1c038"},
		{"8bf1c038-1e4b-4d6a-9c0f-2b1a5d7e3f90", "8bf1c038"},
	} {
		if got := shortID(tc.in); got != tc.want {
			t.Errorf("shortID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
