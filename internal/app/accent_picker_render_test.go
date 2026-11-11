package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/charmbracelet/colorprofile"
)

// accentTestOS is sidebarTestOS with a pane that has no agent state, so the
// accent mark has a glyph column to occupy: state outranks identity, and a
// preview drawn over a state glyph would be testing the wrong rule.
func accentTestOS(t *testing.T, w, h int) *OS {
	t.Helper()
	m := sidebarTestOS(t, w, h, "left")
	m.Windows[0].AgentState = ""
	truecolorForTest(t)
	return m
}

// truecolorForTest pins the colour profile so a swatch is painted with the exact
// colour asked for. Without this the tests would assert against whatever
// terminal happened to run them.
func truecolorForTest(t *testing.T) {
	t.Helper()
	prev := theme.ColorProfile()
	SetAccentColorProfile(colorprofile.TrueColor)
	t.Cleanup(func() { SetAccentColorProfile(prev) })
}

// pickerLines renders the picker and returns its rows with styling stripped.
func pickerLines(t *testing.T, m *OS) []string {
	t.Helper()
	content, _, _ := m.renderAccentPicker()
	rows := strings.Split(content, "\n")
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, stripANSIForTrace(r))
	}
	return out
}

// TestAccentPickerHueDragStaysOnTheStrip: a drag along the hue strip wanders
// into the grid row below it, and a drag that changed meaning halfway would
// repaint the colour with whatever the pointer brushed past.
func TestAccentPickerHueDragStaysOnTheStrip(t *testing.T) {
	m := accentTestOS(t, 120, 30)
	m.OpenAccentPicker("aaaaaaaa1111")
	m.renderAccentPicker()

	var strip, cell accentHit
	for _, h := range m.accentHits {
		if h.Kind == accentHitHue && h.Col == 3 {
			strip = h
		}
		if h.Kind == accentHitGrid && cell.Kind == accentHitNone {
			cell = h
		}
	}
	// Update tracks the button for the real path; this drives the routing
	// directly, so it has to say the button is down itself.
	m.pointerDown = true
	if ok, _ := m.accentPickerPress(strip.Rect.X0, strip.Rect.Y0); !ok {
		t.Fatal("the press on the hue strip was not routed")
	}
	hue := m.AccentPicker.Hue

	// Slip onto the grid mid-drag: nothing moves.
	m.accentPickerDragTo(cell.Rect.X0, cell.Rect.Y0)
	if m.AccentPicker.Hue != hue {
		t.Errorf("the drag followed the pointer off the strip: hue %v -> %v", hue, m.AccentPicker.Hue)
	}
	if m.AccentPicker.Focus != accentFocusHue {
		t.Error("the drag handed focus to the grid it slipped over")
	}

	// Back on the strip it keeps working.
	for _, h := range m.accentHits {
		if h.Kind == accentHitHue && h.Col == 9 {
			m.accentPickerDragTo(h.Rect.X0, h.Rect.Y0)
		}
	}
	if m.AccentPicker.Hue == hue {
		t.Error("the drag stopped tracking the strip it started on")
	}
	m.OverlayMouseRelease()
	if m.accentDragging {
		t.Error("the release did not end the drag")
	}
}

// TestAccentPreviewFoldStaysAllocationFree: the rail's cache key is folded on
// every frame, so it has to cost nothing. An open picker adds its preview to the
// fold, and the picker gaining a continuous model and five more controls must
// not have turned that into an allocation per frame.
func TestAccentPreviewFoldStaysAllocationFree(t *testing.T) {
	skipAllocCountsUnderRace(t)
	m := accentTestOS(t, 120, 30)
	m.OpenAccentPicker("aaaaaaaa1111")
	m.AccentPickerSetSlider(accentChanS, 61)
	m.sidebarSignature() // warm anything one-off

	if got := testing.AllocsPerRun(200, func() { m.sidebarSignature() }); got != 0 {
		t.Errorf("folding the signature with the picker open allocates %.1f times a frame", got)
	}
}

// TestAccentPickerMovesTheRailSignatureOnlyWhenTheColourMoves: the preview is
// the picker's whole contribution to the rail, so the rail must rebuild on the
// keystrokes that change the colour and on nothing else. A slider step too fine
// to change the colour is a step the rail has no reason to hear about.
func TestAccentPickerMovesTheRailSignatureOnlyWhenTheColourMoves(t *testing.T) {
	m := accentTestOS(t, 120, 30)
	m.OpenAccentPicker("aaaaaaaa1111")

	for _, step := range []struct {
		name string
		do   func()
	}{
		{"a grid move", func() { m.AccentPickerMoveCell(1, 0) }},
		{"a hue turn", func() { m.AccentPickerMoveHue(1) }},
		{"a hue nudge", func() { m.AccentPickerNudgeHue(5) }},
		{"a red step", func() { m.AccentPickerSliderStep(accentChanR, 10) }},
		{"a saturation step", func() { m.AccentPickerSliderStep(accentChanS, 10) }},
		{"a harmony step", func() { m.AccentPickerHarmonyAt(1) }},
	} {
		before, wasColour := m.sidebarSignature(), m.AccentPicker.Cur
		step.do()
		moved := m.AccentPicker.Cur != wasColour
		changed := m.sidebarSignature() != before
		if moved != changed {
			t.Errorf("%s moved the colour=%v but moved the rail signature=%v", step.name, moved, changed)
		}
	}
}
