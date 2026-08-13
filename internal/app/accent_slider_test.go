package app

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

// sliderRow is what one slider drew: the rect its track was recorded at, the
// thumb's column in the frame, and the number printed beside it.
type sliderRow struct {
	hit   accentHit
	thumb int
	value int
}

// readSliders renders the picker and reads every slider back off the frame, so
// the assertions below are against what the user is looking at rather than
// against the state that produced it.
func readSliders(t *testing.T, m *OS) map[int]sliderRow {
	t.Helper()
	lines := pickerLines(t, m)
	out := map[int]sliderRow{}
	for _, h := range m.accentHits {
		if h.Kind != accentHitSlider {
			continue
		}
		if h.Rect.Y0 >= len(lines) {
			t.Fatalf("slider %d was recorded on row %d of a %d-row dialog", h.Col, h.Rect.Y0, len(lines))
		}
		row := []rune(lines[h.Rect.Y0])
		thumb := -1
		for x := h.Rect.X0; x < h.Rect.X1 && x < len(row); x++ {
			if row[x] == '◆' || row[x] == '+' {
				thumb = x - h.Rect.X0
			}
		}
		if thumb < 0 {
			t.Fatalf("slider %d drew no thumb in its track: %q", h.Col, string(row))
		}
		text := strings.TrimSpace(strings.TrimSuffix(
			string(row[min(h.Rect.X1+1, len(row)):min(h.Rect.X1+1+accentSliderValueWidth, len(row))]), "%"))
		v, err := strconv.Atoi(text)
		if err != nil {
			t.Fatalf("slider %d printed %q, which is not a number: %q", h.Col, text, string(row))
		}
		out[h.Col] = sliderRow{hit: h, thumb: thumb, value: v}
	}
	return out
}

// TestAccentSliderPrintsWhatItHolds is the honesty claim. A 24-cell bar carries
// about eleven units of red per cell, so the thumb rounds and the number must
// not: the printed value has to be the value the picker holds, at every value
// including the ones the thumb cannot distinguish.
func TestAccentSliderPrintsWhatItHolds(t *testing.T) {
	m := accentTestOS(t, 120, 30)
	m.OpenAccentPicker("aaaaaaaa1111")

	for ch := accentChannel(0); ch < accentChanCount; ch++ {
		for v := 0; v <= ch.max(); v++ {
			m.AccentPickerSetSlider(ch, v)
			rows := readSliders(t, m)
			row, ok := rows[int(ch)]
			if !ok {
				t.Fatalf("slider %s was not drawn", ch.label())
			}
			if row.value != v {
				t.Fatalf("%s set to %d prints %d", ch.label(), v, row.value)
			}
			if got := m.AccentPicker.sliderValue(ch); got != v {
				t.Fatalf("%s set to %d holds %d", ch.label(), v, got)
			}
			barW := row.hit.Rect.X1 - row.hit.Rect.X0
			if want := accentSliderCol(v, barW, ch.max()); row.thumb != want {
				t.Fatalf("%s at %d drew its thumb in cell %d, want %d", ch.label(), v, row.thumb, want)
			}
		}
	}
}

// TestAccentSLStepsOffACellAndBackOntoIt is what the continuous model buys. The
// grid quantises saturation to about nine percent a column; a slider stepping
// the printed percent would leave the cell on the way out and land beside it on
// the way back. Stepping the model returns the cell's exact colour.
func TestAccentSLStepsOffACellAndBackOntoIt(t *testing.T) {
	m := accentTestOS(t, 120, 30)
	m.OpenAccentPicker("aaaaaaaa1111")
	cols, rows := m.accentGridSize()

	for _, hue := range []int{0, 5, 11} {
		m.AccentPickerHueCell(hue % cols)
		for col := range cols {
			for row := range rows {
				m.AccentPickerCell(col, row)
				want := m.AccentPicker.Cur
				for _, ch := range []accentChannel{accentChanS, accentChanL} {
					m.AccentPicker.Focus = ch.focus()
					// Step away from whichever end the cell sits at: a step into the
					// end of a range clamps, and clamping is the point of a range.
					dir := 1
					if m.AccentPicker.sliderValue(ch) >= ch.max() {
						dir = -1
					}
					m.AccentPickerSliderStep(ch, dir)
					m.AccentPickerSliderStep(ch, -dir)
					if got := m.AccentPicker.Cur; got != want {
						t.Fatalf("cell (%d,%d): %s out and back gave %s, want %s",
							col, row, ch.label(), hexString(got), hexString(want))
					}
					// And the grid cursor is back on the cell it started on.
					if m.AccentPicker.Col != col || m.AccentPicker.Row != row {
						t.Fatalf("cell (%d,%d): %s out and back left the cursor on (%d,%d)",
							col, row, ch.label(), m.AccentPicker.Col, m.AccentPicker.Row)
					}
				}
			}
		}
	}
}

// TestAccentSLOpenTellingTheTruth: the sliders open on the colour the target is
// wearing, not on a default. A picker that opened showing 50 % of something it
// was not holding would be lying on its first frame.
func TestAccentSLOpenTellingTheTruth(t *testing.T) {
	const id = "aaaaaaaa1111"
	for _, hex := range []string{"#3aa0ff", "#801020", "#ffffff", "#000000", "#7f7f7f"} {
		want, ok := parseHexColor(hex)
		if !ok {
			t.Fatalf("%q is not a colour", hex)
		}
		m := accentTestOS(t, 120, 30)
		m.SetWindowAccent(id, RGBAccent(want))
		m.OpenAccentPicker(id)

		_, sat, light := rgbToHSL(want)
		rows := readSliders(t, m)
		for ch, wantF := range map[accentChannel]float64{accentChanS: sat, accentChanL: light} {
			if got := rows[int(ch)].value; got != int(math.Round(wantF*100)) {
				t.Errorf("%s: %s opened at %d%%, want %d%%",
					hex, ch.label(), got, int(math.Round(wantF*100)))
			}
		}
		for ch, wantV := range map[accentChannel]int{
			accentChanR: int(want.R), accentChanG: int(want.G), accentChanB: int(want.B),
		} {
			if got := rows[int(ch)].value; got != wantV {
				t.Errorf("%s: %s opened at %d, want %d", hex, ch.label(), got, wantV)
			}
		}
	}
}

// TestAccentSLAndGridAreOneModel: the grid cursor is the coarse view of the
// saturation and lightness the sliders hold, so moving a slider moves the
// cursor to the cell nearest what it now holds and never anywhere else.
func TestAccentSLAndGridAreOneModel(t *testing.T) {
	m := accentTestOS(t, 120, 30)
	m.OpenAccentPicker("aaaaaaaa1111")
	cols, rows := m.accentGridSize()

	// The distance from a saturation and lightness to a cell, and the least of
	// them over the whole grid, found by looking at every cell rather than by
	// asking the function under test. A value exactly between two cells is
	// equally near both, so the claim is about the distance, not the cell.
	dist := func(sat, light float64, col, row int) float64 {
		cs, cl := accentCellSL(col, row, cols, rows)
		return math.Abs(cs-sat) + math.Abs(cl-light)
	}
	least := func(sat, light float64) float64 {
		best := math.MaxFloat64
		for col := range cols {
			for row := range rows {
				best = math.Min(best, dist(sat, light, col, row))
			}
		}
		return best
	}

	for v := 0; v <= 100; v += 3 {
		m.AccentPickerSetSlider(accentChanS, v)
		m.AccentPickerSetSlider(accentChanL, 100-v)
		sat, light := m.AccentPicker.Sat, m.AccentPicker.Light
		got := dist(sat, light, m.AccentPicker.Col, m.AccentPicker.Row)
		if want := least(sat, light); got > want+1e-12 {
			t.Errorf("S=%d%% L=%d%%: the cursor sits on (%d,%d), %.4f away, when a cell %.4f away exists",
				v, 100-v, m.AccentPicker.Col, m.AccentPicker.Row, got, want)
		}
		// The colour is built from the model, not from the cell under the cursor.
		if want := hslToRGB(m.AccentPicker.Hue, m.AccentPicker.Sat, m.AccentPicker.Light); m.AccentPicker.Cur != want {
			t.Errorf("S=%d%%: the colour is %s, want %s from the model it holds",
				v, hexString(m.AccentPicker.Cur), hexString(want))
		}
	}
}

// TestAccentHueTurnKeepsTheFineValue: turning the hue is a hue change, so the
// saturation and lightness the sliders were left holding carry across it rather
// than rounding to the grid cell the cursor happens to sit in.
func TestAccentHueTurnKeepsTheFineValue(t *testing.T) {
	m := accentTestOS(t, 120, 30)
	m.OpenAccentPicker("aaaaaaaa1111")
	cols, _ := m.accentGridSize()

	m.AccentPickerSetSlider(accentChanS, 82)
	m.AccentPickerSetSlider(accentChanL, 66)
	for i := range cols {
		m.AccentPickerHueCell(i)
		if got := m.AccentPicker.sliderValue(accentChanS); got != 82 {
			t.Fatalf("hue cell %d: saturation became %d%%, want 82%%", i, got)
		}
		if got := m.AccentPicker.sliderValue(accentChanL); got != 66 {
			t.Fatalf("hue cell %d: lightness became %d%%, want 66%%", i, got)
		}
	}
}

// TestAccentSliderColumnRoundTrips: a column maps to a value that maps back to
// the same column, at both bar widths the layout produces. Without this a drag
// would fight the thumb, moving it a cell and putting it back.
func TestAccentSliderColumnRoundTrips(t *testing.T) {
	for _, barW := range []int{24, 26, 30} {
		for _, maxV := range []int{100, 255} {
			for col := range barW {
				v := accentSliderValueAt(col, barW, maxV)
				if got := accentSliderCol(v, barW, maxV); got != col {
					t.Errorf("barW=%d max=%d: column %d means %d, which draws in column %d",
						barW, maxV, col, v, got)
				}
			}
			// The ends are the ends, exactly.
			if got := accentSliderValueAt(0, barW, maxV); got != 0 {
				t.Errorf("barW=%d max=%d: the first column means %d", barW, maxV, got)
			}
			if got := accentSliderValueAt(barW-1, barW, maxV); got != maxV {
				t.Errorf("barW=%d max=%d: the last column means %d", barW, maxV, got)
			}
		}
	}
}

// TestAccentSliderHitsMatchTheDrawnTrack: pressing either edge column of a
// recorded track is the end of that channel's range, at every width the dialog
// lays out at.
func TestAccentSliderHitsMatchTheDrawnTrack(t *testing.T) {
	for _, w := range []int{120, 74, 60, 42} {
		m := accentTestOS(t, w, 30)
		m.OpenAccentPicker("aaaaaaaa1111")
		rows := readSliders(t, m)
		if len(rows) != int(accentChanCount) {
			t.Fatalf("w=%d: %d sliders drawn, want %d", w, len(rows), accentChanCount)
		}
		for ch := accentChannel(0); ch < accentChanCount; ch++ {
			r := rows[int(ch)].hit.Rect
			if ok, _ := m.accentPickerPress(r.X0, r.Y0); !ok {
				t.Fatalf("w=%d: a press on %s's left edge was not routed", w, ch.label())
			}
			if got := m.AccentPicker.sliderValue(ch); got != 0 {
				t.Errorf("w=%d: %s's left edge set %d, want 0", w, ch.label(), got)
			}
			if ok, _ := m.accentPickerPress(r.X1-1, r.Y0); !ok {
				t.Fatalf("w=%d: a press on %s's right edge was not routed", w, ch.label())
			}
			if got := m.AccentPicker.sliderValue(ch); got != ch.max() {
				t.Errorf("w=%d: %s's right edge set %d, want %d", w, ch.label(), got, ch.max())
			}
			m.OverlayMouseRelease()
		}
	}
}

// TestAccentSliderDragRidesThePointer: press, drag past the end of the track,
// release. The value clamps to the end rather than stopping where the track
// stops, the drag stays on the channel it was started on, and nothing moves on
// motion with no button held.
func TestAccentSliderDragRidesThePointer(t *testing.T) {
	m := accentTestOS(t, 120, 30)
	m.OpenAccentPicker("aaaaaaaa1111")
	rows := readSliders(t, m)
	red, green := rows[int(accentChanR)].hit.Rect, rows[int(accentChanG)].hit.Rect

	// Button-free motion over a track changes nothing.
	before := m.AccentPicker.Cur
	m.accentPickerDragTo(red.X0+5, red.Y0)
	if m.AccentPicker.Cur != before {
		t.Errorf("motion with no button held moved the slider: %s -> %s",
			hexString(before), hexString(m.AccentPicker.Cur))
	}

	m.pointerDown = true
	if ok, _ := m.accentPickerPress(red.X0+3, red.Y0); !ok {
		t.Fatal("the press on the red track was not routed")
	}
	barW := red.X1 - red.X0
	if want := accentSliderValueAt(3, barW, 255); m.AccentPicker.sliderValue(accentChanR) != want {
		t.Errorf("the press set R to %d, want %d", m.AccentPicker.sliderValue(accentChanR), want)
	}

	// Well past the right-hand end of the track, and off its row entirely.
	g := m.AccentPicker.sliderValue(accentChanG)
	m.accentPickerDragTo(red.X1+40, green.Y0)
	if got := m.AccentPicker.sliderValue(accentChanR); got != 255 {
		t.Errorf("dragging past the end left R at %d, want 255", got)
	}
	if got := m.AccentPicker.sliderValue(accentChanG); got != g {
		t.Errorf("the drag wandered onto green: %d -> %d", g, got)
	}

	// And back inside it.
	m.accentPickerDragTo(red.X0, red.Y0)
	if got := m.AccentPicker.sliderValue(accentChanR); got != 0 {
		t.Errorf("dragging back to the left edge left R at %d, want 0", got)
	}

	m.OverlayMouseRelease()
	if m.accentDragging {
		t.Error("the release did not end the drag")
	}
	m.accentPickerDragTo(red.X1-1, red.Y0)
	if got := m.AccentPicker.sliderValue(accentChanR); got != 0 {
		t.Errorf("motion after the release moved R to %d", got)
	}
}

// TestAccentSliderKeysStepAndJump: the arrows nudge by one, shifted arrows by
// ten, home and end reach the range's ends. A one-unit step often leaves the
// thumb in the cell it was in; the value still has to move.
func TestAccentSliderKeysStepAndJump(t *testing.T) {
	m := accentTestOS(t, 120, 30)
	m.OpenAccentPicker("aaaaaaaa1111")

	for ch := accentChannel(0); ch < accentChanCount; ch++ {
		mid := ch.max() / 2
		m.AccentPicker.Focus = ch.focus()
		m.AccentPickerSetSlider(ch, mid)
		m.AccentPickerMove(1, 0)
		if got := m.AccentPicker.sliderValue(ch); got != mid+1 {
			t.Errorf("%s: right moved %d to %d", ch.label(), mid, got)
		}
		m.AccentPickerMove(0, 1)
		if got := m.AccentPicker.sliderValue(ch); got != mid {
			t.Errorf("%s: down moved %d to %d", ch.label(), mid+1, got)
		}
		m.AccentPickerMoveShift(1, 0)
		if got := m.AccentPicker.sliderValue(ch); got != mid+ch.coarse() {
			t.Errorf("%s: shift+right moved %d to %d", ch.label(), mid, got)
		}
		m.AccentPickerSliderEnd(true)
		if got := m.AccentPicker.sliderValue(ch); got != ch.max() {
			t.Errorf("%s: end left it at %d", ch.label(), got)
		}
		m.AccentPickerSliderEnd(false)
		if got := m.AccentPicker.sliderValue(ch); got != 0 {
			t.Errorf("%s: home left it at %d", ch.label(), got)
		}
		// Clamped, not wrapped: a channel has no other side to come out on.
		m.AccentPickerMove(-1, 0)
		if got := m.AccentPicker.sliderValue(ch); got != 0 {
			t.Errorf("%s: stepping below zero reached %d", ch.label(), got)
		}
		// The keyboard stays where it was put.
		if m.AccentPicker.Focus != ch.focus() {
			t.Errorf("%s: driving the slider moved the focus to %d", ch.label(), m.AccentPicker.Focus)
		}
	}
}

// TestAccentSliderReturningToTheSeedWritesNothing: the unchanged guard is what
// stops an opened-and-closed picker pinning an inheriting pane. A slider walked
// away and walked back is the same colour, so it is still unchanged.
func TestAccentSliderReturningToTheSeedWritesNothing(t *testing.T) {
	const id = "aaaaaaaa1111"
	seedRGB, ok := parseHexColor("#3aa0ff")
	if !ok {
		t.Fatal("the fixture hex does not parse")
	}
	seed := RGBAccent(seedRGB)

	m := accentTestOS(t, 120, 30)
	m.SetWindowAccent(id, seed)
	m.OpenAccentPicker(id)

	m.AccentPicker.Focus = accentFocusR
	was := m.AccentPicker.sliderValue(accentChanR)
	m.AccentPickerSetSlider(accentChanR, 0)
	m.AccentPickerSetSlider(accentChanR, was)
	if m.AccentPicker.Cur != seedRGB {
		t.Fatalf("the slider did not come back to the seed: %s vs %s",
			hexString(m.AccentPicker.Cur), seed.Hex())
	}

	// Take the stored accent away behind the picker's back. An apply that writes
	// puts it back, and an apply that holds the guard does not.
	m.ClearWindowAccent(id)
	m.AccentPickerApply()
	if got, ok := m.WindowAccent(id); ok {
		t.Errorf("applying a colour walked away from and back to the seed wrote %v", got)
	}

	// Moving somewhere and staying there does write, so the guard is not simply
	// swallowing every apply.
	m.SetWindowAccent(id, seed)
	m.OpenAccentPicker(id)
	m.AccentPicker.Focus = accentFocusG
	m.AccentPickerSetSlider(accentChanG, 7)
	want := m.AccentPicker.Cur
	m.AccentPickerApply()
	if got, _ := m.WindowAccent(id); got.RGB() != want {
		t.Errorf("a moved slider stored %s, want %s", got.Hex(), hexString(want))
	}
}
