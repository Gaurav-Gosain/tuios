package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// railFrame renders the rail and returns its rows with the styling stripped, so
// an assertion is against what the user sees rather than against a fragment a
// helper built.
func railFrame(t *testing.T, m *OS) []string {
	t.Helper()
	return railText(t, m)
}

// The rail's widths, asserted as whole frames: the resting state is the point
// of the pass, so it is what the test reads. Two of them are the user's own
// states; the middle one is only ever the responsive clamp's doing.
func TestRailRendersTheThreeWidths(t *testing.T) {
	for _, tc := range []struct {
		name      string
		width     int
		collapsed bool
		want      []string
	}{
		// The add control is a "+" on the sessions and terminals headers now, so
		// the header lines are what carry it; the footer is the toggle alone.
		{"full", config.SidebarDefaultWidth, false, []string{" agents", " sessions", "+", "«"}},
		{"narrow", config.SidebarNarrowWidth, false, []string{" agents", " sessions", "+", "«"}},
		{"glyph", config.SidebarDefaultWidth, true, []string{"»"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := daemonRailOS(t, 120, 14)
			prev := config.Global.SidebarWidth
			config.Global.SidebarWidth = tc.width
			m.SidebarCollapsed = tc.collapsed
			t.Cleanup(func() { config.Global.SidebarWidth = prev })

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

// The footer's toggle is offered only where it can move: a control that
// provably cannot do anything is noise, which is the same rule the new-session
// control follows in a standalone session.
func TestRailToggleIsOfferedOnlyWhereItCanMove(t *testing.T) {
	m := daemonRailOS(t, 120, 14)
	prev := config.Global.SidebarWidth
	t.Cleanup(func() { config.Global.SidebarWidth = prev })

	config.Global.SidebarWidth = config.SidebarDefaultWidth
	if _, ok := m.sidebarCollapseGlyph(sidebarVariantFull); !ok {
		t.Error("a full rail on a wide screen is not offered a collapse")
	}

	// A strip on a screen too narrow for anything wider cannot expand.
	narrow := daemonRailOS(t, config.SidebarBreakpointNarrow-1, 14)
	m.Settings = config.Global
	if _, ok := narrow.sidebarCollapseGlyph(sidebarVariantGlyph); ok {
		t.Error("a strip is offered an expand on a screen with no room for one")
	}
	// On a wide screen the same rail can open again.
	if _, ok := m.sidebarCollapseGlyph(sidebarVariantGlyph); !ok {
		t.Error("a strip on a wide screen is not offered an expand")
	}
}

// The rail has two user states and the toggle walks between them, idempotently:
// pressing the same directed key twice is not a flicker.
func TestRailCollapseIsBinaryAndIdempotent(t *testing.T) {
	// No daemon client: the toggle re-lays the panes, and this is about the two
	// states rather than about syncing them anywhere.
	m := sidebarTestOS(t, 120, 14, "left")
	prev := m.Settings.SidebarWidth
	m.Settings.SidebarWidth = config.SidebarDefaultWidth
	t.Cleanup(func() { m.Settings.SidebarWidth = prev })

	if got := sidebarVariant(m.GetSidebarWidth()); got != sidebarVariantFull {
		t.Fatalf("the rail starts at variant %d, want full", got)
	}
	m.SidebarSetCollapsed(true)
	if got := sidebarVariant(m.GetSidebarWidth()); got != sidebarVariantGlyph {
		t.Fatalf("collapsing landed on variant %d, want glyph", got)
	}
	m.SidebarSetCollapsed(true) // already collapsed
	if got := sidebarVariant(m.GetSidebarWidth()); got != sidebarVariantGlyph {
		t.Fatalf("collapsing twice landed on variant %d, want glyph", got)
	}
	m.SidebarSetCollapsed(false)
	if got := sidebarVariant(m.GetSidebarWidth()); got != sidebarVariantFull {
		t.Fatalf("expanding landed on variant %d, want full", got)
	}
	// There is no middle stop left to land on.
	m.SidebarToggleCollapsed()
	m.SidebarToggleCollapsed()
	if got := sidebarVariant(m.GetSidebarWidth()); got != sidebarVariantFull {
		t.Fatalf("a round trip landed on variant %d, want full", got)
	}
	if m.Settings.SidebarWidth != config.SidebarDefaultWidth {
		t.Errorf("the round trip moved the stored width to %d", m.Settings.SidebarWidth)
	}
}

// Mouse and keyboard reach the rail's controls the same way: the hit rect the
// renderer recorded and the nav row it published point at the same thing, in
// the same order.
func TestRailControlHitsAndNavStayParallel(t *testing.T) {
	m := daemonRailOS(t, 120, 14)
	m.SidebarFocused = true
	railFrame(t, m)

	var controls []sidebarRowHit
	for _, h := range m.SidebarHits {
		switch h.Kind {
		case sidebarRowNewSession, sidebarRowNewWindow, sidebarRowCollapse:
			controls = append(controls, h)
		}
	}
	// Two add controls in the headers, and the footer's toggle.
	if len(controls) != 3 {
		t.Fatalf("the rail recorded %d controls, want the two adds and the toggle", len(controls))
	}
	if controls[len(controls)-1].Kind != sidebarRowCollapse {
		t.Errorf("the last control drawn is %v, want the footer's toggle", controls[len(controls)-1].Kind)
	}

	// Each control's own columns hit-test back to it, and its nav row exists.
	for _, h := range controls {
		row, ok := m.sidebarRowAt(h.X0, h.Y0)
		if !ok || row.Kind != h.Kind {
			t.Errorf("%v's own columns hit-test to %+v (ok=%v)", h.Kind, row, ok)
		}
		found := false
		for _, n := range m.SidebarNav {
			if sidebarNavRowsEqual(n, navRowOf(h)) {
				found = true
			}
		}
		if !found {
			t.Errorf("%v has a hit rect but no nav row: the keyboard cannot reach it", h.Kind)
		}
	}

	before := config.Global.SidebarWidth
	t.Cleanup(func() { config.Global.SidebarWidth = before })
}

// Every glyph the footer adds needs an ASCII answer.
func TestRailFooterDegradesToASCII(t *testing.T) {
	prev := config.Global.UseASCIIOnly
	config.Global.UseASCIIOnly = true
	overlay.SetASCII(true)
	t.Cleanup(func() {
		config.Global.UseASCIIOnly = prev
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
	m := sidebarTestOS(t, 120, 14, "left")
	prev := m.Settings.SidebarWidth
	m.Settings.SidebarWidth = config.SidebarDefaultWidth
	t.Cleanup(func() { m.Settings.SidebarWidth = prev })

	before := m.sidebarSignature()
	m.SidebarSetCollapsed(true)
	if after := m.sidebarSignature(); after == before {
		t.Error("collapsing the rail did not change its signature; the cache would serve the old width")
	}
}

// A click on the toggle has to move the rail. A hit rect that resolves to the
// control is not the control doing anything, which is how the footer shipped a
// stepper only the keyboard could move.
func TestRailToggleClickCollapsesTheRail(t *testing.T) {
	m := sidebarTestOS(t, 120, 14, "left")
	prev := m.Settings.SidebarWidth
	m.Settings.SidebarWidth = config.SidebarDefaultWidth
	t.Cleanup(func() { m.Settings.SidebarWidth = prev })

	railFrame(t, m)
	var step sidebarRowHit
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowCollapse {
			step = h
		}
	}
	if step.X1 == 0 {
		t.Fatal("the footer drew no toggle to click")
	}

	if !m.SidebarClick(step.X0, step.Y0, false) {
		t.Fatal("the toggle did not consume its own click")
	}
	if got := sidebarVariant(m.GetSidebarWidth()); got != sidebarVariantGlyph {
		t.Errorf("a click on the toggle landed on variant %d, want glyph", got)
	}
}
