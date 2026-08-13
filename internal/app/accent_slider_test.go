package app

import (
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
		m.AccentPicker.Focus = ch.focus()
		m.AccentPickerSetSlider(ch, 128)
		m.AccentPickerMove(1, 0)
		if got := m.AccentPicker.sliderValue(ch); got != 129 {
			t.Errorf("%s: right moved 128 to %d", ch.label(), got)
		}
		m.AccentPickerMove(0, 1)
		if got := m.AccentPicker.sliderValue(ch); got != 128 {
			t.Errorf("%s: down moved 129 to %d", ch.label(), got)
		}
		m.AccentPickerMoveShift(1, 0)
		if got := m.AccentPicker.sliderValue(ch); got != 138 {
			t.Errorf("%s: shift+right moved 128 to %d", ch.label(), got)
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
