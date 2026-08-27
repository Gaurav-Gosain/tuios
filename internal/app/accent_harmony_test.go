package app

import (
	"math"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
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

// accentHintRect returns where the named hint was drawn, off the recorded
// geometry rather than off the layout arithmetic that produced it.
func accentHintRect(t *testing.T, m *OS, which int) overlay.Rect {
	t.Helper()
	for _, h := range m.accentHits {
		if h.Kind == accentHitHint && h.Col == which {
			return h.Rect
		}
	}
	t.Fatalf("hint %d was not recorded", which)
	return overlay.Rect{}
}

// TestAccentHintsArePressable: the border hints are the mouse's only way to
// apply or cancel, so each one has to do what its own words say, and the words
// have to be where the rect claims they are.
func TestAccentHintsArePressable(t *testing.T) {
	const id = "aaaaaaaa1111"
	seed, ok := parseHexColor("#3aa0ff")
	if !ok {
		t.Fatal("the fixture hex does not parse")
	}

	// The words are in the cells the rects claim.
	m := accentTestOS(t, 120, 30)
	m.OpenAccentPicker(id)
	lines, _ := accentFrame(t, m)
	for i, want := range []string{"tab field", "↵ apply", "x clear", "esc cancel"} {
		r := accentHintRect(t, m, i)
		row := []rune(lines[r.Y0])
		if got := string(row[r.X0:r.X1]); got != want {
			t.Errorf("hint %d spans %q, want %q", i, got, want)
		}
	}

	// Apply stores the colour under the cursor.
	m = accentTestOS(t, 120, 30)
	m.Settings = config.Global
	m.SetWindowAccent(id, RGBAccent(seed))
	m.OpenAccentPicker(id)
	m.renderAccentPicker()
	m.AccentPickerCell(4, 2)
	want := m.AccentPicker.Cur
	r := accentHintRect(t, m, accentHintApply)
	if handled, _ := m.accentPickerPress(r.X0, r.Y0); !handled {
		t.Fatal("a press on the apply hint was not routed")
	}
	if m.ShowAccentPicker {
		t.Error("the apply hint left the picker open")
	}
	if got, _ := m.WindowAccent(id); got.RGB() != want {
		t.Errorf("the apply hint stored %s, want %s", got.Hex(), hexString(want))
	}

	// Cancel closes and writes nothing, even after moving.
	m = accentTestOS(t, 120, 30)
	m.Settings = config.Global
	m.SetWindowAccent(id, RGBAccent(seed))
	m.OpenAccentPicker(id)
	m.renderAccentPicker()
	m.AccentPickerCell(6, 1)
	r = accentHintRect(t, m, accentHintCancel)
	if handled, _ := m.accentPickerPress(r.X1-1, r.Y0); !handled {
		t.Fatal("a press on the cancel hint was not routed")
	}
	if m.ShowAccentPicker {
		t.Error("the cancel hint left the picker open")
	}
	if got, _ := m.WindowAccent(id); got.RGB() != seed {
		t.Errorf("the cancel hint wrote %s over the colour that was there", got.Hex())
	}

	// Clear takes the accent away, which is a return to inheriting.
	m = accentTestOS(t, 120, 30)
	m.Settings = config.Global
	m.SetWindowAccent(id, RGBAccent(seed))
	m.OpenAccentPicker(id)
	m.renderAccentPicker()
	r = accentHintRect(t, m, accentHintClear)
	if handled, _ := m.accentPickerPress(r.X0, r.Y0); !handled {
		t.Fatal("a press on the clear hint was not routed")
	}
	if _, still := m.WindowAccent(id); still {
		t.Error("the clear hint left the accent in place")
	}

	// And the focus hint walks the controls, so no hint is a dead word.
	m = accentTestOS(t, 120, 30)
	m.Settings = config.Global
	m.OpenAccentPicker(id)
	m.renderAccentPicker()
	was := m.AccentPicker.Focus
	r = accentHintRect(t, m, accentHintFocus)
	if handled, _ := m.accentPickerPress(r.X0, r.Y0); !handled {
		t.Fatal("a press on the focus hint was not routed")
	}
	if m.AccentPicker.Focus == was {
		t.Error("the focus hint did not move the keyboard on")
	}
}

// TestAccentPickerStaysCoherentOnALesserTerminal walks the layouts through the
// profiles a real terminal might have. The claim is not that it looks the same,
// which it cannot, but that the frame stays a rectangle, the numbers stay
// readable, and every swatch shown agrees with the hex printed for it.
func TestAccentPickerStaysCoherentOnALesserTerminal(t *testing.T) {
	for _, prof := range []colorprofile.Profile{colorprofile.TrueColor, colorprofile.ANSI256, colorprofile.ANSI, colorprofile.ASCII} {
		for _, ascii := range []bool{false, true} {
			for _, w := range []int{120, 60, 38} {
				m := accentTestOS(t, w, 30)
				m.OpenAccentPicker("aaaaaaaa1111")
				m.AccentPickerSetSlider(accentChanR, 137)

				overlay.SetASCII(ascii)
				SetAccentColorProfile(prof)
				lines, geo := accentFrame(t, m)
				overlay.SetASCII(false)

				name := prof.String()
				for i, l := range lines {
					if got := lipgloss.Width(l); got != geo.Width {
						t.Fatalf("%s ascii=%v w=%d: row %d is %d cells, want %d",
							name, ascii, w, i, got, geo.Width)
					}
				}
				text := strings.Join(lines, "\n")
				// The numbers are the picker's floor: they survive every profile,
				// and on the ones that cannot show the colour they are all there is.
				if !strings.Contains(text, hexString(m.AccentPicker.Cur)) {
					t.Errorf("%s ascii=%v w=%d: the frame lost the hex:\n%s", name, ascii, w, text)
				}
				// Where the sliders are drawn at all, their numbers are the last
				// thing standing on a terminal that can paint no colour.
				if m.accentSlidersShown() && !strings.Contains(text, "137") {
					t.Errorf("%s ascii=%v w=%d: the frame lost the red channel's value:\n%s", name, ascii, w, text)
				}
				// A terminal that cannot show the colour exactly says so, beside it.
				if fb := accentFallbackLabel(m.AccentPicker.Cur); fb != "" && !strings.Contains(text, fb) {
					t.Errorf("%s w=%d: the frame does not admit the fallback %q:\n%s", name, w, fb, text)
				}
				if ascii {
					for i, l := range lines {
						if strings.ContainsAny(l, "╭╮╰╯│─╌┆✕●›→◆━") {
							t.Errorf("%s w=%d: ASCII row %d still draws a box glyph: %q", name, w, i, l)
						}
					}
				}
			}
		}
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
