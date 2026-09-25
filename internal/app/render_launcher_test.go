package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// renderLauncherWith opens a launcher holding the given programs and draws it
// with query typed, returning the drawn panel and the hit rows the draw
// recorded.
func renderLauncherWith(t *testing.T, query string, programs ...string) (string, []overlayRowHit) {
	t.Helper()
	m := runTestOS(t)
	seedLauncher(t, m, programs...)
	m.LauncherQuery = query
	// No row selected, so a comparison between two renders is not confounded by
	// the selected row's own bold and background.
	m.LauncherSelected = -1

	out, geo, rows := m.renderLauncher()
	assertPanelOwnsEveryCell(t, "launcher", out, geo)
	assertRowHitsMatchPanel(t, "launcher", geo, rows)
	return out, rows
}

// TestLauncherRowHighlightsTheMatch is the visual half of the matcher returning
// positions: the characters the query matched have to come out styled
// differently from the ones it did not, in the real render path.
func TestLauncherRowHighlightsTheMatch(t *testing.T) {
	lit, _ := renderLauncherWith(t, "rip", "ripgrep")
	unlit, _ := renderLauncherWith(t, "", "ripgrep")

	litRow := launcherRowContaining(t, lit, "ripgrep")
	unlitRow := launcherRowContaining(t, unlit, "ripgrep")

	if ansi.Strip(litRow) != ansi.Strip(unlitRow) {
		t.Fatalf("the rows print different text, so the comparison says nothing:\n%q\n%q",
			ansi.Strip(litRow), ansi.Strip(unlitRow))
	}
	if litRow == unlitRow {
		t.Fatalf("a matched row is styled identically to an unmatched one, so nothing is highlighted:\n%q", litRow)
	}
	// The highlight is spliced by byte offset, so a miscount shows up as extra
	// or missing cells rather than as wrong colour.
	if got, want := ansi.StringWidth(litRow), ansi.StringWidth(unlitRow); got != want {
		t.Errorf("highlighting changed the row width from %d to %d", want, got)
	}
}

func launcherRowContaining(t *testing.T, panel, want string) string {
	t.Helper()
	for _, ln := range strings.Split(panel, "\n") {
		if strings.Contains(ansi.Strip(ln), want) {
			return ln
		}
	}
	t.Fatalf("no row containing %q in:\n%s", want, ansi.Strip(panel))
	return ""
}
