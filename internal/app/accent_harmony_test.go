package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/charmbracelet/colorprofile"
)

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
		t.Errorf("the apply hint stored %s, want %s", got.Hex(), overlay.Hex(want))
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
				if !strings.Contains(text, overlay.Hex(m.AccentPicker.Cur)) {
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
