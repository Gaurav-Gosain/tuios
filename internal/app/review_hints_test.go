package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/charmbracelet/x/ansi"
)

// reviewHintStripAtBase is reviewHintStrip as it was before the footer stopped
// re-rendering every prefix, kept so a change to the strip's own output shows.
func reviewHintStripAtBase(hints []overlay.Hint, pal overlay.Palette) string {
	key := lipgloss.NewStyle().Foreground(pal.AccentBright).Background(pal.Surface).Bold(true)
	label := lipgloss.NewStyle().Foreground(pal.FgDim).Background(pal.Surface)
	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		k := h.Key
		if k == overlay.EnterGlyph {
			k = overlay.EnterKey()
		}
		parts = append(parts, key.Render(k)+label.Render(" "+h.Label))
	}
	return strings.Join(parts, reviewSpaces(2, pal.Surface))
}

// reviewHintsByPrefix is reviewHints as it was: it fits hints by rendering
// the whole strip for each candidate prefix and measuring it.
func reviewHintsByPrefix(hints []overlay.Hint, width int, pal overlay.Palette) string {
	if len(hints) == 0 {
		return reviewSpaces(width, pal.Surface)
	}
	last := hints[len(hints)-1]
	room := width - 2 - ansi.StringWidth(reviewHintStrip([]overlay.Hint{last}, pal))
	var fit []overlay.Hint
	for _, h := range hints[:len(hints)-1] {
		try := append(append([]overlay.Hint{}, fit...), h)
		if ansi.StringWidth(reviewHintStrip(try, pal))+2 > room {
			continue
		}
		fit = try
	}
	strip := reviewSpaces(1, pal.Surface) + reviewHintStrip(append(fit, last), pal)
	return strip + reviewSpaces(width-ansi.StringWidth(strip), pal.Surface)
}

// reviewHintLists are the footers the fit is compared on: none, one, the
// Enter pair the editor draws, eight with wide runes, an empty key, an empty
// label and a tab, and one whose long middle hint is skipped while the short
// ones after it still fit.
func reviewHintLists() []struct {
	name  string
	hints []overlay.Hint
} {
	return []struct {
		name  string
		hints []overlay.Hint
	}{
		{"empty", nil},
		{"one hint", []overlay.Hint{{Key: "esc", Label: "close"}}},
		{"enter pair", []overlay.Hint{{Key: overlay.EnterGlyph, Label: "save"}, {Key: "esc", Label: "drop"}}},
		{"eight", reviewHintsEight()},
		{"long middle", []overlay.Hint{
			{Key: "a", Label: "one"},
			{Key: "b", Label: "a hint long enough that it is the one given up first"},
			{Key: "c", Label: "two"},
			{Key: "d", Label: "three"},
			{Key: "q", Label: "last"},
		}},
	}
}

// reviewHintsEight is eight hints with CJK and emoji in keys and labels, an
// empty key, an empty label, and a tab in a label.
func reviewHintsEight() []overlay.Hint {
	return []overlay.Hint{
		{Key: "j", Label: "下一个"},
		{Key: "🙂", Label: "smile"},
		{Key: "", Label: "no key"},
		{Key: "x", Label: ""},
		{Key: "t", Label: "tab\there"},
		{Key: "文", Label: "wide key"},
		{Key: "esc", Label: "close 🎉"},
		{Key: "q", Label: "quit"},
	}
}

// TestReviewHintsMatchTheStripsTheyReplace holds the review footer's hints to
// the algorithm they replace: at every width, in both glyph modes, the same
// bytes the prefix-by-prefix fit drew, and the strip itself unchanged for its
// other callers.
//
// How it could pass wrongly: a width taken from the raw text instead of the
// render would agree everywhere a render adds no cells (a label with a tab,
// which lipgloss expands, is in the eight-hint list);
// the Enter glyph maps only in ASCII mode, so one glyph mode alone would miss
// it (both run); a fixed set of widths could miss the one where a hint just
// fits (every width from -2 to 160 runs); and a list with nothing skipped
// would pass a fit that stops at the first hint that does not fit (the long
// middle list skips one and keeps the short ones after it).
func TestReviewHintsMatchTheStripsTheyReplace(t *testing.T) {
	pal := shotOS(t).reviewLook().pal
	for _, ascii := range []bool{false, true} {
		inGlyphMode(t, ascii, func() {
			for _, l := range reviewHintLists() {
				if got, want := reviewHintStrip(l.hints, pal), reviewHintStripAtBase(l.hints, pal); got != want {
					t.Errorf("ascii=%v, %s: reviewHintStrip changed:\n got %q\nwant %q", ascii, l.name, got, want)
				}
				for width := -2; width <= 160; width++ {
					got := reviewHints(l.hints, width, pal)
					want := reviewHintsByPrefix(l.hints, width, pal)
					if got != want {
						t.Errorf("ascii=%v, %s, width %d: reviewHints differs from the prefix fit:\n got %q\nwant %q",
							ascii, l.name, width, got, want)
						break
					}
				}
			}
		})
	}
}

// TestReviewHintsRendersEachHintOnce is a perf budget: the footer is drawn on
// every review frame, and fitting it by rendering the whole strip for each
// prefix is quadratic in the hints. It renders each hint once, so a footer
// of eight hints allocates at most twice what one strip of all eight does.
//
// How it could pass wrongly: a width where nothing fits but the last hint
// would skip the prefix renders (at 80 most of the eight fit); and two
// numbers from different conditions would not compare (both are measured
// here, in the same run, on the same palette).
func TestReviewHintsRendersEachHintOnce(t *testing.T) {
	skipAllocCountsUnderRace(t)
	pal := shotOS(t).reviewLook().pal
	hints := reviewHintsEight()
	footer := func() { _ = reviewHints(hints, 80, pal) }
	strip := func() { _ = reviewHintStrip(hints, pal) }
	footer()
	strip()
	footerAllocs := testing.AllocsPerRun(50, footer)
	stripAllocs := testing.AllocsPerRun(50, strip)
	t.Logf("reviewHints at width 80: %.0f allocs/op; one reviewHintStrip of all eight: %.0f allocs/op", footerAllocs, stripAllocs)
	if footerAllocs > 2*stripAllocs {
		t.Errorf("reviewHints allocates %.0f times, %.1fx one strip's %.0f, want at most 2x",
			footerAllocs, footerAllocs/stripAllocs, stripAllocs)
	}
}
