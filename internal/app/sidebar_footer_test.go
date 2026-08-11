package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// railFrame renders the rail and returns its rows with the styling stripped, so
// an assertion is against what the user sees rather than against a fragment a
// helper built.
func railFrame(t *testing.T, m *OS) []string {
	t.Helper()
	return railText(t, m)
}

// The rail's three widths, asserted as whole frames: the resting state is the
// point of the pass, so it is what the test reads.
func TestRailRendersTheThreeWidths(t *testing.T) {
	for _, tc := range []struct {
		name  string
		width int
		want  []string
	}{
		{"full", config.SidebarDefaultWidth, []string{" agents", " sessions", " + new", "«"}},
		{"narrow", config.SidebarNarrowWidth, []string{" agents", " sessions", " + new", "«"}},
		{"glyph", config.SidebarGlyphWidth, []string{" +", " »"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := daemonRailOS(t, 120, 14)
			prev := config.SidebarWidth
			config.SidebarWidth = tc.width
			t.Cleanup(func() { config.SidebarWidth = prev })

			lines := railFrame(t, m)
			joined := strings.Join(lines, "\n")
			for _, want := range tc.want {
				if !strings.Contains(joined, want) {
					t.Errorf("the %s rail does not show %q:\n%s", tc.name, want, joined)
				}
			}
			// Headers are lowercase, always: bold Title case ranked a label
			// above the rows that want a human.
			for _, shout := range []string{"Agents", "Sessions"} {
				if strings.Contains(joined, shout) {
					t.Errorf("the %s rail still shouts %q", tc.name, shout)
				}
			}
		})
	}
}

// The glyph rail spends both its cells: herdr's count-and-state sliver, where
// the digit is the only other thing worth saying at three columns.
func TestGlyphRailShowsCountAndGlyph(t *testing.T) {
	m := daemonRailOS(t, 120, 14)
	prev := config.SidebarWidth
	config.SidebarWidth = config.SidebarGlyphWidth
	t.Cleanup(func() { config.SidebarWidth = prev })

	lines := railFrame(t, m)
	if len(lines) == 0 {
		t.Fatal("the glyph rail drew nothing")
	}
	row := lines[0]
	if len(row) < 2 || row[0] < '2' || row[0] > '9' {
		t.Errorf("the session row leads with %q, want its window count: %q", row[:1], row)
	}

	// A single-window session has no count to give, so the cell stays blank
	// rather than saying "1" on every row forever.
	single := m.sidebarSessionRow(
		sessiontree.Node{ID: "solo", Title: "solo", WindowCount: 1},
		sidebarVariantGlyph, false, config.SidebarGlyphWidth-1, theme.UI(), false, false)
	if got := stripANSIForTrace(single); !strings.HasPrefix(got, " ") {
		t.Errorf("a one-window session leads with %q, want a blank cell", got)
	}
}

// The footer's stepper is offered only where it can move: a control that
// provably cannot do anything is noise, which is the same rule the new-session
// control follows in a standalone session.
func TestRailStepperIsOfferedOnlyWhereItCanMove(t *testing.T) {
	m := daemonRailOS(t, 120, 14)
	prev := config.SidebarWidth
	t.Cleanup(func() { config.SidebarWidth = prev })

	config.SidebarWidth = config.SidebarDefaultWidth
	if _, _, ok := m.sidebarCollapseGlyph(sidebarVariantFull); !ok {
		t.Error("a full rail on a wide screen is not offered a step down")
	}

	// A glyph rail on a screen too narrow for anything wider cannot widen.
	narrow := daemonRailOS(t, config.SidebarBreakpointNarrow-1, 14)
	config.SidebarWidth = config.SidebarGlyphWidth
	if _, _, ok := narrow.sidebarCollapseGlyph(sidebarVariantGlyph); ok {
		t.Error("a glyph rail is offered a step up on a screen with no room for one")
	}
	// On a wide screen the same rail can go back.
	if _, _, ok := m.sidebarCollapseGlyph(sidebarVariantGlyph); !ok {
		t.Error("a glyph rail on a wide screen is not offered a step back up")
	}
}

// The stepper walks full, narrow, glyph and back, and the keyboard and the
// click reach the same mutation.
func TestRailStepperWalksTheVariants(t *testing.T) {
	// No daemon client: the step re-lays the panes, and this is about the width
	// ladder rather than about syncing it anywhere.
	m := bandTestOS(t, 120, 14, "left")
	prev := config.SidebarWidth
	config.SidebarWidth = config.SidebarDefaultWidth
	t.Cleanup(func() { config.SidebarWidth = prev })

	if got := sidebarVariant(m.GetSidebarWidth()); got != sidebarVariantFull {
		t.Fatalf("the rail starts at variant %d, want full", got)
	}
	m.SidebarStepWidth(-1)
	if got := sidebarVariant(m.GetSidebarWidth()); got != sidebarVariantNarrow {
		t.Fatalf("one step down landed on variant %d, want narrow", got)
	}
	m.SidebarStepWidth(-1)
	if got := sidebarVariant(m.GetSidebarWidth()); got != sidebarVariantGlyph {
		t.Fatalf("two steps down landed on variant %d, want glyph", got)
	}
	m.SidebarStepWidth(-1) // already the narrowest
	if got := sidebarVariant(m.GetSidebarWidth()); got != sidebarVariantGlyph {
		t.Fatalf("a third step down landed on variant %d, want glyph", got)
	}
	m.SidebarStepWidth(1)
	if got := sidebarVariant(m.GetSidebarWidth()); got != sidebarVariantFull {
		t.Fatalf("a step up landed on variant %d, want full", got)
	}
}

// Mouse and keyboard reach the footer's controls the same way: the hit rect the
// renderer recorded and the nav row it published point at the same thing.
func TestRailFooterHitsAndNavStayParallel(t *testing.T) {
	m := daemonRailOS(t, 120, 14)
	m.SidebarFocused = true
	railFrame(t, m)

	var footerHits []sidebarRowHit
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowNewSession || h.Kind == sidebarRowCollapse {
			footerHits = append(footerHits, h)
		}
	}
	if len(footerHits) != 2 {
		t.Fatalf("the footer recorded %d hits, want the new-session control and the stepper", len(footerHits))
	}

	navTail := m.SidebarNav[len(m.SidebarNav)-2:]
	for i, h := range footerHits {
		if navTail[i].Kind != h.Kind {
			t.Errorf("footer hit %d is %v but nav row %d is %v: the two are not parallel",
				i, h.Kind, i, navTail[i].Kind)
		}
	}

	// A click inside the stepper's columns narrows the rail, exactly as the
	// cursor on its nav row does.
	step := footerHits[1]
	before := config.SidebarWidth
	t.Cleanup(func() { config.SidebarWidth = before })
	if row, ok := m.sidebarRowAt(step.X0, step.Y0); !ok || row.Kind != sidebarRowCollapse {
		t.Fatalf("the stepper's own columns do not hit-test to it: %+v ok=%v", row, ok)
	}
}

// Every glyph the footer adds needs an ASCII answer.
func TestRailFooterDegradesToASCII(t *testing.T) {
	prev := config.UseASCIIOnly
	config.UseASCIIOnly = true
	overlay.SetASCII(true)
	t.Cleanup(func() {
		config.UseASCIIOnly = prev
		overlay.SetASCII(prev)
	})

	m := daemonRailOS(t, 120, 14)
	joined := strings.Join(railFrame(t, m), "\n")
	if !strings.Contains(joined, "<<") {
		t.Errorf("the ASCII footer has no stepper:\n%s", joined)
	}
	for _, glyph := range []string{"«", "»"} {
		if strings.Contains(joined, glyph) {
			t.Errorf("the ASCII footer still draws %q:\n%s", glyph, joined)
		}
	}
}

// The rail is cached by signature, so a width step that the cache cannot see
// would leave yesterday's rail on screen.
func TestSidebarSignatureCoversTheWidthStep(t *testing.T) {
	m := bandTestOS(t, 120, 14, "left")
	prev := config.SidebarWidth
	config.SidebarWidth = config.SidebarDefaultWidth
	t.Cleanup(func() { config.SidebarWidth = prev })

	before := m.sidebarSignature()
	m.SidebarStepWidth(-1)
	if after := m.sidebarSignature(); after == before {
		t.Error("narrowing the rail did not change its signature; the cache would serve the old width")
	}
}

// A click on the stepper has to step the rail. A hit rect that resolves to the
// control is not the control doing anything, which is how the footer shipped a
// stepper only the keyboard could move.
func TestRailStepperClickNarrowsTheRail(t *testing.T) {
	m := bandTestOS(t, 120, 14, "left")
	prev := config.SidebarWidth
	config.SidebarWidth = config.SidebarDefaultWidth
	t.Cleanup(func() { config.SidebarWidth = prev })

	railFrame(t, m)
	var step sidebarRowHit
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowCollapse {
			step = h
		}
	}
	if step.X1 == 0 {
		t.Fatal("the footer drew no stepper to click")
	}

	if !m.SidebarClick(step.X0, step.Y0, false) {
		t.Fatal("the stepper did not consume its own click")
	}
	if got := sidebarVariant(m.GetSidebarWidth()); got != sidebarVariantNarrow {
		t.Errorf("a click on the stepper landed on variant %d, want narrow", got)
	}
}
