package app

import (
	"testing"

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
	prev := accentProfile.Load()
	SetAccentColorProfile(colorprofile.TrueColor)
	t.Cleanup(func() { accentProfile.Store(prev) })
}

// TestAccentPreviewFoldStaysAllocationFree: the rail's cache key is folded on
// every frame, so it has to cost nothing. An open picker adds its preview to the
// fold, and the picker gaining a continuous model and five more controls must
// not have turned that into an allocation per frame.
func TestAccentPreviewFoldStaysAllocationFree(t *testing.T) {
	m := accentTestOS(t, 120, 30)
	m.OpenAccentPicker("aaaaaaaa1111")
	m.AccentPickerSetSlider(accentChanS, 61)
	m.sidebarSignature() // warm anything one-off

	if got := testing.AllocsPerRun(200, func() { m.sidebarSignature() }); got != 0 {
		t.Errorf("folding the signature with the picker open allocates %.1f times a frame", got)
	}
}
