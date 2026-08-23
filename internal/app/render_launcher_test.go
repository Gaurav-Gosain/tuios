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

// TestLauncherRowsReachTheScreen follows the row all the way to the drawn panel
// rather than stopping at the filter, because the filter agreeing and the
// screen agreeing are two different claims.
func TestLauncherRowsReachTheScreen(t *testing.T) {
	out, rows := renderLauncherWith(t, "ripgrep", "ripgrep", "htop")
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "ripgrep") {
		t.Fatalf("the program never reached the panel:\n%s", plain)
	}
	if !strings.Contains(plain, "Run a program") {
		t.Errorf("the panel does not say what it is:\n%s", plain)
	}
	if len(rows) == 0 {
		t.Error("a clickable row recorded no hit rectangle")
	}
}

// TestLauncherFooterOffersBothVerbs is the discoverability of the second one.
// Type-it-out has no other place to announce itself, and a choice nobody can
// find is the same as a setting nobody remembers.
func TestLauncherFooterOffersBothVerbs(t *testing.T) {
	out, _ := renderLauncherWith(t, "", "htop")
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "run") {
		t.Errorf("the footer does not offer run:\n%s", plain)
	}
	if !strings.Contains(plain, "type it out") {
		t.Errorf("the footer does not offer type it out:\n%s", plain)
	}
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

// TestLauncherSaysWhyItIsEmpty separates the two empty states. Before the first
// scan lands there is nothing to match against yet, and saying "no program
// matches" then is simply wrong.
func TestLauncherSaysWhyItIsEmpty(t *testing.T) {
	m := runTestOS(t)
	m.LauncherItems = []LauncherItem{}
	out, _, _ := m.renderLauncher()
	if !strings.Contains(ansi.Strip(out), "Scanning") {
		t.Errorf("an unscanned launcher does not say so:\n%s", ansi.Strip(out))
	}

	out, _ = renderLauncherWith(t, "zzzzz", "htop")
	if !strings.Contains(ansi.Strip(out), "No program matches") {
		t.Errorf("a launcher with no match does not say so:\n%s", ansi.Strip(out))
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
