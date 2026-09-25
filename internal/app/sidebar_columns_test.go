package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/charmbracelet/x/ansi"
)

// nameColumn is where a row's own text starts, measured from the rail's first
// content column.
func nameColumn(t *testing.T, lines []string, name string) int {
	t.Helper()
	for _, l := range lines {
		if c := rowColumn(ansi.Strip(l), name); c >= 0 {
			return c
		}
	}
	t.Fatalf("no rail row carries %q:\n%s", name, strings.Join(lines, "\n"))
	return -1
}

// rowColumn is the display column name starts at in an already-stripped row, or
// -1. Glyphs are multi-byte, so the byte offset is not the column.
func rowColumn(row, name string) int {
	i := strings.Index(row, name)
	if i < 0 {
		return -1
	}
	return lipgloss.Width(row[:i])
}

// The footer carries the rail's own two controls: the file view on the outer
// corner and the collapse toggle on the pane-facing one. What each must keep is
// agreement between the columns it is drawn on and the columns its hit zone
// claims, since nothing recomputes either.
//
// Negative control, confirmed red: with the zones recorded in construction
// order rather than left to right, the ordering assertion below fails ("footer
// zones = [{Kind:7 ... X0:28} {Kind:8 ... X0:1}], want files then collapse"),
// and so does the fuzz oracle's rail-hit-band rule, which was seen failing
// exactly that way before the sort went in ("hit 6 at (1,37) overlaps or
// precedes hit 5 at (25,37)").
func TestSidebarFooterZonesMatchWhatIsDrawn(t *testing.T) {
	m := daemonRailOS(t, 120, 40)
	lines, zones := m.sidebarFooter(sidebarVariantFull, 30, theme.UI(), -1, -1,
		func(sidebarRowKind) bool { return false })
	if len(lines) != 1 {
		t.Fatalf("the footer took %d lines, want one", len(lines))
	}
	if len(zones) != 2 {
		t.Fatalf("footer zones = %+v, want the file view control and the collapse toggle", zones)
	}
	if zones[0].Kind != sidebarRowFiles || zones[1].Kind != sidebarRowCollapse {
		t.Fatalf("footer zones = %+v, want files then collapse", zones)
	}
	if zones[0].X1 > zones[1].X0 {
		t.Errorf("the two controls overlap: %+v", zones)
	}
	row := ansi.Strip(lines[0])
	// The expected columns are read off the drawn row, for each control's own
	// text, never taken from the zones the function under test returned.
	if got := rowColumn(row, "«"); got != zones[1].X0 {
		t.Errorf("the toggle is drawn at column %d but its hit zone starts at %d: %q", got, zones[1].X0, row)
	}
	if got := rowColumn(row, sidebarFilesLabel); got != zones[0].X0 {
		t.Errorf("the files control is drawn at column %d but its hit zone starts at %d: %q", got, zones[0].X0, row)
	}
}
