package app

import (
	"math"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/charmbracelet/colorprofile"
)

// TestAccentHarmonyChipZeroIsTheComplement pins the wheel's origin. Every other
// chip is a turn from it, so if chip zero is not the complement the whole set
// means nothing in particular.
func TestAccentHarmonyChipZeroIsTheComplement(t *testing.T) {
	for _, w := range []int{120, 60, 38} {
		m := accentTestOS(t, w, 30)
		m.OpenAccentPicker("aaaaaaaa1111")
		m.AccentPickerHueCell(3)
		m.AccentPickerCell(m.AccentPicker.Col, m.AccentPicker.Row)

		s := &m.AccentPicker
		count := m.accentPlan().HarmonyCount()
		want := hslToRGB(s.baseHue()+180, s.Sat, s.Light)
		if got := s.harmonyColor(0, count); got != want {
			t.Errorf("w=%d: chip 0 is %s, want the complement %s", w, hexString(got), hexString(want))
		}

		// And the rest are even turns around the circle from it, bar the compact
		// row, which names three relationships instead of drawing a wheel.
		if count == accentHarmonyCompactCount {
			continue
		}
		for i := 1; i < count; i++ {
			wantHue := math.Mod(s.baseHue()+180+float64(i)*360/float64(count), 360)
			gotHue, sat, _ := rgbToHSL(s.harmonyColor(i, count))
			// The hue is a circle, so the two ends of it are next to each other.
			off := math.Abs(gotHue - wantHue)
			if off > 180 {
				off = 360 - off
			}
			if sat > 0 && off > 1 {
				t.Errorf("w=%d: chip %d of %d is at hue %.1f, want %.1f", w, i, count, gotHue, wantHue)
			}
		}
	}
}

// TestAccentHarmonyKeepsTheHeldSaturationAndLightness: a chip is this colour at
// another hue. Picking one that reset the saturation and lightness to the seed's
// would throw away whatever the sliders had just been used for.
func TestAccentHarmonyKeepsTheHeldSaturationAndLightness(t *testing.T) {
	m := accentTestOS(t, 120, 30)
	m.OpenAccentPicker("aaaaaaaa1111")
	m.AccentPickerSetSlider(accentChanS, 41)
	m.AccentPickerSetSlider(accentChanL, 73)

	count := m.accentPlan().HarmonyCount()
	for i := range count {
		m.AccentPickerHarmonyAt(i)
		if got := m.AccentPicker.sliderValue(accentChanS); got != 41 {
			t.Errorf("chip %d left saturation at %d%%, want 41%%", i, got)
		}
		if got := m.AccentPicker.sliderValue(accentChanL); got != 73 {
			t.Errorf("chip %d left lightness at %d%%, want 73%%", i, got)
		}
		// A chip is a literal colour, never a theme slot.
		if m.AccentPicker.Slot != -1 {
			t.Errorf("chip %d was taken as slot %d", i, m.AccentPicker.Slot)
		}
	}

	// Walking the chips must not move the chips.
	before := make([]string, count)
	for i := range count {
		before[i] = hexString(m.AccentPicker.harmonyColor(i, count))
	}
	for i := range count {
		m.AccentPickerHarmonyAt(i)
		for j := range count {
			if got := hexString(m.AccentPicker.harmonyColor(j, count)); got != before[j] {
				t.Fatalf("landing on chip %d moved chip %d from %s to %s", i, j, before[j], got)
			}
		}
	}
}

// TestAccentHarmonyChipsAreClickableAtEveryLayout: every chip the layout draws
// has a rect, both its edge columns select it, and no two chips share a cell.
func TestAccentHarmonyChipsAreClickableAtEveryLayout(t *testing.T) {
	for _, w := range []int{120, 73, 60, 40, 38, 30} {
		m := accentTestOS(t, w, 30)
		m.OpenAccentPicker("aaaaaaaa1111")
		p := m.accentPlan()
		m.renderAccentPicker()

		seen := map[[2]int]int{}
		var chips []accentHit
		for _, h := range m.accentHits {
			if h.Kind != accentHitHarmony {
				continue
			}
			chips = append(chips, h)
			for x := h.Rect.X0; x < h.Rect.X1; x++ {
				if other, dup := seen[[2]int{x, h.Rect.Y0}]; dup {
					t.Errorf("w=%d: chips %d and %d both claim cell (%d,%d)", w, other, h.Col, x, h.Rect.Y0)
				}
				seen[[2]int{x, h.Rect.Y0}] = h.Col
			}
		}
		if len(chips) != p.HarmonyCount() {
			t.Fatalf("w=%d: %d chip rects for %d chips", w, len(chips), p.HarmonyCount())
		}
		for _, h := range chips {
			for _, x := range []int{h.Rect.X0, h.Rect.X1 - 1} {
				if ok, _ := m.accentPickerPress(x, h.Rect.Y0); !ok {
					t.Fatalf("w=%d: a press at column %d of chip %d was not routed", w, x, h.Col)
				}
				if m.AccentPicker.Harmony != h.Col {
					t.Errorf("w=%d: pressing column %d of chip %d selected %d",
						w, x, h.Col, m.AccentPicker.Harmony)
				}
			}
		}
		m.OverlayMouseRelease()
	}
}

// TestAccentChipsSurviveAMonochromeTerminal is the honest floor. Without colour
// every background-painted swatch renders as blank space; the chips fall back to
// a foreground glyph so the row is still a row of somethings the user can aim
// at, and the cursor is still findable on it.
func TestAccentChipsSurviveAMonochromeTerminal(t *testing.T) {
	m := accentTestOS(t, 120, 30)
	m.OpenAccentPicker("aaaaaaaa1111")
	m.AccentPickerHarmonyAt(2)

	SetAccentColorProfile(colorprofile.ASCII)
	lines := pickerLines(t, m)

	var chipRow overlay.Rect
	for _, h := range m.accentHits {
		if h.Kind == accentHitHarmony && h.Col == 0 {
			chipRow = h.Rect
		}
	}
	row := lines[chipRow.Y0]
	if got := strings.Count(row, "●") + strings.Count(row, "◆"); got != accentWideChipCols {
		t.Errorf("a colourless terminal drew %d chip marks on the first row, want %d: %q",
			got, accentWideChipCols, row)
	}
	if !strings.Contains(row, "◆") {
		t.Errorf("the chip cursor is not findable without colour: %q", row)
	}
	// And the hex line still says what the chip under the cursor holds, which is
	// what makes it pickable rather than merely visible.
	if want := hexString(m.AccentPicker.Cur); !strings.Contains(strings.Join(lines, "\n"), want) {
		t.Errorf("the colour under the chip cursor (%s) is not printed anywhere", want)
	}
}
