package app

import (
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
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

// TestAccentPickerReachesTheWholeColourSpace: the picker is no longer fifteen
// fixed slots. Walking the grid and the hue strip has to produce colours the
// old slot set never could, and the dialog has to say which one is selected.
func TestAccentPickerReachesTheWholeColourSpace(t *testing.T) {
	m := accentTestOS(t, 120, 30)
	m.OpenAccentPicker("aaaaaaaa1111")
	cols, rows := m.accentGridSize()

	seen := map[color.RGBA]bool{}
	for hue := range m.accentHueCells() {
		m.AccentPickerHueCell(hue)
		for col := range cols {
			for row := range rows {
				m.AccentPickerCell(col, row)
				seen[m.AccentPicker.Cur] = true
			}
		}
	}
	if len(seen) < 500 {
		t.Errorf("the grid and strip together only reach %d colours; this is meant to be a true-colour picker", len(seen))
	}

	// The hex of the selected colour is on screen, so the user can read off what
	// they landed on.
	m.AccentPickerCell(cols-1, rows/2)
	text := strings.Join(pickerLines(t, m), "\n")
	if want := hexString(m.AccentPicker.Cur); !strings.Contains(text, want) {
		t.Errorf("the dialog does not show the selected colour %s:\n%s", want, text)
	}
}

// TestAccentGridAndHexConverge is the core consistency claim: the two ways in
// name the same colour. Reading a cell's hex and typing it back must land on
// that exact colour and put the cursor back on that exact cell.
func TestAccentGridAndHexConverge(t *testing.T) {
	m := accentTestOS(t, 120, 30)
	m.OpenAccentPicker("aaaaaaaa1111")
	cols, rows := m.accentGridSize()

	for _, cell := range [][2]int{{0, 0}, {cols / 3, 1}, {cols - 1, rows - 1}, {cols / 2, rows / 2}} {
		m.AccentPickerHueCell(9)
		m.AccentPickerCell(cell[0], cell[1])
		want := m.AccentPicker.Cur
		wantHex := hexString(want)

		// Type the same hex in, digit by digit, the way a user would.
		m.AccentPickerCell(0, 0) // walk away first, so converging means something
		for _, r := range wantHex {
			m.AccentPickerHexKey(r)
		}
		if got := m.AccentPicker.Cur; got != want {
			t.Errorf("cell %v: typing %s gave %s", cell, wantHex, hexString(got))
		}
		if m.AccentPicker.Col != cell[0] || m.AccentPicker.Row != cell[1] {
			t.Errorf("cell %v: typing %s put the cursor on (%d,%d)",
				cell, wantHex, m.AccentPicker.Col, m.AccentPicker.Row)
		}
	}
}

// TestAccentPickerCancelRestores: the picker previews live on the rail, so
// cancelling has to put back exactly what was there, and applying has to store
// exactly what was under the cursor.
func TestAccentPickerCancelRestores(t *testing.T) {
	m := accentTestOS(t, 120, 30)
	before := RGBAccent(color.RGBA{R: 0x33, G: 0x99, B: 0x66, A: 0xff})
	m.SetWindowAccent("aaaaaaaa1111", before)

	m.OpenAccentPicker("aaaaaaaa1111")
	m.AccentPickerHueCell(4)
	m.AccentPickerCell(7, 2)
	previewed, ok := m.accentPreview(AccentTargetWindow, "aaaaaaaa1111")
	if !ok || previewed.RGB() == before.RGB() {
		t.Fatalf("the picker is not previewing a new colour (%v, ok=%v)", previewed, ok)
	}
	// Nothing is stored while previewing.
	if got, _ := m.WindowAccent("aaaaaaaa1111"); got != before {
		t.Errorf("the preview wrote through to the stored accent: %v", got)
	}

	m.CloseAccentPicker()
	got, ok := m.WindowAccent("aaaaaaaa1111")
	if !ok || got != before {
		t.Errorf("cancel left the accent as %v, want the exact colour it had (%v)", got, before)
	}

	// Applying stores what the cursor was on.
	m.OpenAccentPicker("aaaaaaaa1111")
	m.AccentPickerCell(5, 1)
	want := m.AccentPicker.Cur
	m.AccentPickerApply()
	if m.ShowAccentPicker {
		t.Error("applying left the picker open")
	}
	if got, _ := m.WindowAccent("aaaaaaaa1111"); got.RGB() != want {
		t.Errorf("applied accent = %s, want %s", got.Hex(), hexString(want))
	}
}

// TestAccentPickerHitsMatchTheDrawnCells is the mouse contract, checked at three
// widths. The assertion is built off the rendered frame: every recorded rect
// must land inside the dialog on the row it claims, and clicking the rect must
// select the cell that was drawn there.
func TestAccentPickerHitsMatchTheDrawnCells(t *testing.T) {
	for _, w := range []int{120, 60, 30} {
		m := accentTestOS(t, w, 30)
		m.OpenAccentPicker("aaaaaaaa1111")
		content, geo, _ := m.renderAccentPicker()
		lines := strings.Split(content, "\n")
		cols, rows := m.accentGridSize()

		if len(lines) != geo.Height {
			t.Fatalf("w=%d: the dialog drew %d rows but reports %d", w, len(lines), geo.Height)
		}
		for i, l := range lines {
			if got := lipgloss.Width(l); got != geo.Width {
				t.Fatalf("w=%d: row %d is %d cells, want %d", w, i, got, geo.Width)
			}
		}

		var grid, hue, harmony int
		for _, h := range m.accentHits {
			if h.Rect.Y0 < 0 || h.Rect.Y1 > geo.Height || h.Rect.X0 < 0 || h.Rect.X1 > geo.Width {
				t.Errorf("w=%d: %v is outside the %dx%d dialog", w, h.Rect, geo.Width, geo.Height)
				continue
			}
			switch h.Kind {
			case accentHitGrid:
				grid++
			case accentHitHue:
				hue++
			case accentHitHarmony:
				harmony++
			}
		}
		if grid != cols*rows {
			t.Errorf("w=%d: %d grid rects recorded for a %dx%d grid", w, grid, cols, rows)
		}
		if cells := m.accentHueCells(); hue != cells {
			t.Errorf("w=%d: %d hue rects recorded for a %d-cell strip", w, hue, cells)
		}
		if want := m.accentPlan().HarmonyCount(); harmony != want {
			t.Errorf("w=%d: %d harmony rects recorded, want %d", w, harmony, want)
		}

		// Press the middle of every recorded grid rect and check the picker lands
		// on the cell that rect was drawn for, and that the swatch under the
		// pointer is the colour the cell claims.
		for _, h := range m.accentHits {
			if h.Kind != accentHitGrid {
				continue
			}
			if ok, _ := m.accentPickerPress(h.Rect.X0, h.Rect.Y0); !ok {
				t.Fatalf("w=%d: a press on the recorded rect %v was not routed", w, h.Rect)
			}
			if m.AccentPicker.Col != h.Col || m.AccentPicker.Row != h.Row {
				t.Fatalf("w=%d: pressing the rect for cell (%d,%d) selected (%d,%d)",
					w, h.Col, h.Row, m.AccentPicker.Col, m.AccentPicker.Row)
			}
			want := accentCellColor(m.AccentPicker.Hue, h.Col, h.Row, cols, rows)
			if m.AccentPicker.Cur != want {
				t.Fatalf("w=%d: cell (%d,%d) selected %s, want %s",
					w, h.Col, h.Row, hexString(m.AccentPicker.Cur), hexString(want))
			}
		}
		m.OverlayMouseRelease()
	}
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

// TestAccentPickerKeyboardReachesEveryControl: tab walks all four controls and
// each one moves under the arrows, so nothing is mouse-only.
func TestAccentPickerKeyboardReachesEveryControl(t *testing.T) {
	m := accentTestOS(t, 120, 30)
	m.OpenAccentPicker("aaaaaaaa1111")

	seen := map[accentFocus]bool{}
	for range int(accentFocusCount) {
		seen[m.AccentPicker.Focus] = true
		m.AccentPickerFocus(1)
	}
	for f := accentFocus(0); f < accentFocusCount; f++ {
		// A stop the picker is not drawing is a stop tab must skip, which is the
		// rule for the slot rows and the sliders on a short screen and for the
		// keyword chips on a target that has no keywords.
		if !m.accentFocusShown(f) {
			if seen[f] {
				t.Errorf("tab landed on focus %d, which this picker does not draw", f)
			}
			continue
		}
		if !seen[f] {
			t.Errorf("tab never reached focus %d", f)
		}
	}

	// The hue strip turns.
	for m.AccentPicker.Focus != accentFocusHue {
		m.AccentPickerFocus(1)
	}
	hue := m.AccentPicker.Hue
	m.AccentPickerMove(1, 0)
	if m.AccentPicker.Hue == hue {
		t.Error("an arrow on the hue strip did not turn the hue")
	}

	// The grid moves on both axes.
	for m.AccentPicker.Focus != accentFocusGrid {
		m.AccentPickerFocus(1)
	}
	m.AccentPickerCell(4, 2)
	m.AccentPickerMove(1, 0)
	m.AccentPickerMove(0, 1)
	if m.AccentPicker.Col != 5 || m.AccentPicker.Row != 3 {
		t.Errorf("arrows moved the grid cursor to (%d,%d), want (5,3)", m.AccentPicker.Col, m.AccentPicker.Row)
	}

	// The harmony chips step, and each is a different colour from the base.
	for m.AccentPicker.Focus != accentFocusHarmony {
		m.AccentPickerFocus(1)
	}
	first := m.AccentPicker.Cur
	m.AccentPickerMove(1, 0)
	if m.AccentPicker.Cur == first {
		t.Error("an arrow on the harmony row did not move between chips")
	}
	// Walking the chips must not move the chips themselves.
	base := m.AccentPicker.Base
	m.AccentPickerMove(1, 0)
	if m.AccentPicker.Base != base {
		t.Error("walking the harmony chips moved the base they are computed from")
	}
}

// TestAccentPickerFallsBackHonestly: on a terminal that cannot show the colour,
// the swatch is painted with the nearest one the terminal has and the dialog
// says so, rather than printing a hex it is not showing.
func TestAccentPickerFallsBackHonestly(t *testing.T) {
	m := accentTestOS(t, 120, 30)
	m.OpenAccentPicker("aaaaaaaa1111")
	for _, r := range "#3b82f6" {
		m.AccentPickerHexKey(r)
	}
	want := m.AccentPicker.Cur

	// Truecolour: the colour is exact and nothing is claimed about a fallback.
	if label := accentFallbackLabel(want); label != "" {
		t.Errorf("truecolour reported a fallback of %q", label)
	}
	if got := toRGBA(accentShown(want)); got != want {
		t.Errorf("truecolour changed the colour: %s -> %s", hexString(want), hexString(got))
	}

	for _, p := range []colorprofile.Profile{colorprofile.ANSI256, colorprofile.ANSI} {
		SetAccentColorProfile(p)
		shown := accentShown(want)
		got := toRGBA(shown)
		if got == want {
			t.Errorf("%v: the colour was not stepped down at all", p)
		}
		label := accentFallbackLabel(want)
		if label == "" {
			t.Errorf("%v: the picker claims the exact colour it cannot show", p)
		}
		// The fallback is the nearest colour the profile has, not an arbitrary one.
		if d := colorDistance(got, want); d > colorDistance(toRGBA(p.Convert(want)), want) {
			t.Errorf("%v: %s fell back to %s, which is not what the profile converts to",
				p, hexString(want), hexString(got))
		}
		// And the user is told, in the frame.
		text := strings.Join(pickerLines(t, m), "\n")
		if !strings.Contains(text, label) {
			t.Errorf("%v: the fallback %q is not on screen:\n%s", p, label, text)
		}
	}
}

// colorDistance is the squared channel distance between two colours.
func colorDistance(a, b color.RGBA) int {
	dr, dg, db := int(a.R)-int(b.R), int(a.G)-int(b.G), int(a.B)-int(b.B)
	return dr*dr + dg*dg + db*db
}

// The picker previews the colour under its cursor on the rail row it targets,
// driven purely by signature keys: no tick, and no rebuild on anything but the
// keystrokes that move the cursor.
func TestAccentPickerPreviewsOnTheRail(t *testing.T) {
	m := accentTestOS(t, 120, 30)

	resting := m.sidebarSignature()
	m.OpenAccentPicker("aaaaaaaa1111") // "editor", no agent state, and the focused window
	m.AccentPickerCell(4, 1)
	if m.sidebarSignature() == resting {
		t.Fatal("opening the picker left the rail signature unchanged, so the preview would not draw")
	}
	withFirst := m.sidebarSignature()

	// "editor" is the focused window, so the preview reaches the rail through
	// the gutter mark's own colour rather than a separate accent chip: a
	// focused row wears exactly one identity bar.
	preview, ok := m.accentPreview(AccentTargetWindow, "aaaaaaaa1111")
	if !ok {
		t.Fatal("no preview colour under the picker cursor")
	}
	lines, _ := m.sidebarPanelLines()
	row := ""
	for _, l := range lines {
		if strings.Contains(stripANSIForTrace(l), "editor") {
			row = l
			break
		}
	}
	if row == "" {
		t.Fatalf("no rail row for editor")
	}
	if !strings.Contains(row, fgSeq(preview.Color())) {
		t.Errorf("the rail row is not previewing the colour under the cursor: %q", row)
	}

	m.AccentPickerMoveCell(1, 0)
	if m.sidebarSignature() == withFirst {
		t.Error("moving the picker cursor left the rail signature unchanged")
	}

	// Nothing is stored until it is applied.
	if _, ok := m.WindowAccent("aaaaaaaa1111"); ok {
		t.Error("the preview stored an accent before it was applied")
	}
	m.CloseAccentPicker()
	if m.sidebarSignature() != resting {
		t.Error("closing the picker did not put the rail signature back")
	}
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

// The dialog fits a short screen by shrinking the grid rather than drawing off
// the bottom of it, and every row it draws is exactly its own width.
func TestAccentPickerFitsShortScreens(t *testing.T) {
	for _, h := range []int{40, 24, 14, 10, 8} {
		m := accentTestOS(t, 120, h)
		m.OpenAccentPicker("aaaaaaaa1111")

		content, geo, _ := m.renderAccentPicker()
		lines := strings.Split(content, "\n")
		if geo.Height > h {
			t.Errorf("h=%d: the dialog is %d rows tall", h, geo.Height)
		}
		for i, l := range lines {
			if w := lipgloss.Width(l); w != geo.Width {
				t.Errorf("h=%d row %d is %d cells, want %d: %q", h, i, w, geo.Width, l)
			}
		}
		// The furniture the picker cannot do without is still there.
		plain := stripANSIForTrace(content)
		for _, want := range []string{"accent", "now", "hex"} {
			if !strings.Contains(plain, want) {
				t.Errorf("h=%d: the dialog lost %q:\n%s", h, want, plain)
			}
		}
		// The harmony chips have no words on them in every layout, so they are
		// counted rather than read.
		var chips int
		for _, hit := range m.accentHits {
			if hit.Kind == accentHitHarmony {
				chips++
			}
		}
		if chips == 0 {
			t.Errorf("h=%d: the dialog lost the harmony chips:\n%s", h, plain)
		}
	}
}

// ASCII mode keeps every mark one cell and drops the box glyphs.
func TestAccentPickerDegradesToASCII(t *testing.T) {
	overlay.SetASCII(true)
	t.Cleanup(func() { overlay.SetASCII(false) })

	m := accentTestOS(t, 120, 30)
	m.SetWindowAccent("aaaaaaaa1111", SlotAccent(4))
	m.OpenAccentPicker("aaaaaaaa1111")

	for i, l := range pickerLines(t, m) {
		if strings.ContainsAny(l, "╭╮╰╯│─╌┆✕●›→◆━") {
			t.Errorf("row %d still draws non-ASCII marks: %q", i, l)
		}
	}
}
