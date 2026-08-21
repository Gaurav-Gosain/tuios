package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// renderRunPalette opens a palette holding the given programs and draws it with
// query typed, returning the drawn panel and the hit rows the draw recorded.
func renderRunPalette(t *testing.T, query string, programs ...string) (string, []overlayRowHit) {
	t.Helper()
	m := runTestOS(t)
	m.applyPathApps(fakeEntries(programs...))
	m.ShowCommandPalette = true
	m.PaletteAppItems = m.runItems(fakeEntries(programs...))
	m.rebuildPaletteItems()
	m.CommandPaletteQuery = query
	// No row selected, so a comparison between two renders is not confounded by
	// the selected row's own bold and background.
	m.CommandPaletteSelected = -1

	out, geo, rows := m.renderCommandPalette()
	assertPanelOwnsEveryCell(t, "run palette", out, geo)
	assertRowHitsMatchPanel(t, "run palette", geo, rows)
	return out, rows
}

// TestRunRowsReachTheScreen follows the row all the way to the drawn panel
// rather than stopping at the filter, because the filter agreeing and the
// screen agreeing are two different claims.
func TestRunRowsReachTheScreen(t *testing.T) {
	out, rows := renderRunPalette(t, "ripgrep", "ripgrep", "htop")
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "ripgrep") {
		t.Fatalf("the program never reached the panel:\n%s", plain)
	}
	if !strings.Contains(plain, "["+PaletteCategoryRun+"]") {
		t.Errorf("the row does not say what it is:\n%s", plain)
	}
	if len(rows) == 0 {
		t.Error("a clickable row recorded no hit rectangle")
	}
}

// TestRunRowHighlightsTheMatch is the visual half of the matcher returning
// positions: the characters the query matched have to come out styled
// differently from the ones it did not, in the real render path.
// The baseline is a query that reaches the row through its category rather than
// its name ("run" has no u in ripgrep), because such a row carries no match
// positions and so is the same row drawn with nothing lit.
func TestRunRowHighlightsTheMatch(t *testing.T) {
	lit, _ := renderRunPalette(t, "rip", "ripgrep")
	unlit, _ := renderRunPalette(t, "run", "ripgrep")

	litRow := panelRowContaining(t, lit, "ripgrep")
	unlitRow := panelRowContaining(t, unlit, "ripgrep")

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

// TestRunRowSurvivesAQueryWithNoHighlight covers the category fallback, whose
// rows carry no match positions at all and must still draw.
func TestRunRowSurvivesAQueryWithNoHighlight(t *testing.T) {
	out, _ := renderRunPalette(t, strings.ToLower(PaletteCategoryRun), "htop")
	if plain := ansi.Strip(out); !strings.Contains(plain, "htop") {
		t.Fatalf("a category-only match dropped its row:\n%s", plain)
	}
}

func panelRowContaining(t *testing.T, panel, want string) string {
	t.Helper()
	for _, ln := range strings.Split(panel, "\n") {
		if strings.Contains(ansi.Strip(ln), want) {
			return ln
		}
	}
	t.Fatalf("no row containing %q in:\n%s", want, ansi.Strip(panel))
	return ""
}
