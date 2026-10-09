package webshell

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestFastfetchFitsThePane checks that no line of fastfetch is wider than the
// pane, at every width a pane can have, and that each width gets the largest
// layout that fits. A line wider than the pane wraps, which broke the logo
// and split the info values mid-word in the narrow hero pane on /learn.
func TestFastfetchFitsThePane(t *testing.T) {
	for cols := 1; cols <= 160; cols++ {
		for i, line := range fastfetch(cols, "1h2m3s") {
			if w := ansi.StringWidth(line); w > cols {
				t.Fatalf("cols %d: line %d is %d cells wide: %q", cols, i, w, ansi.Strip(line))
			}
		}
	}
	for _, tc := range []struct {
		cols int
		want string // a line that only this layout draws
		info string // an info line drawn in full
	}{
		{120, logo[0] + "  guest@tuios", "OS      your browser"},
		{80, logo[0] + "  guest@tuios", "OS      your browser"},
		{57, smallLogo[0] + "  guest@tuios", "OS      your browser"},
		{40, smallLogo[0] + "  guest@tuios", "OS      your browser"},
		{30, smallLogo[0], "OS      your browser"},
		{12, "guest@tuios", "OS      you…"},
	} {
		got := ansi.Strip(strings.Join(fastfetch(tc.cols, "1h2m3s"), "\n"))
		if !strings.Contains(got, tc.want) || !strings.Contains(got, tc.info) {
			t.Errorf("cols %d: want %q and %q in\n%s", tc.cols, tc.want, tc.info, got)
		}
	}
}
