package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// withTheme pins the active theme for one test and puts the old one back. The
// theme is global, which is exactly why a slot accent is worth keeping as a
// slot: it is the thing that moves under a stored colour.
func withTheme(t *testing.T, id string) {
	t.Helper()
	prev := theme.CurrentThemeID()
	if err := theme.Initialize(id); err != nil {
		t.Fatalf("theme %q: %v", id, err)
	}
	t.Cleanup(func() { _ = theme.Initialize(prev) })
}

// TestAnsiQuickPickStoresASlotNotAHex is the whole point of the row. The slot
// and the colour it resolves to are indistinguishable on the day and diverge on
// the next theme switch, and freezing the hex is the kind of bug nobody traces
// back.
func TestAnsiQuickPickStoresASlotNotAHex(t *testing.T) {
	withTheme(t, "nord")
	m := accentTestOS(t, 120, 30)

	m.OpenAccentPicker("aaaaaaaa1111")
	const cyan = 6 // bright cyan
	m.AccentPickerSlot(cyan)
	if got := m.AccentPicker.Cur; got != SlotAccent(cyan).RGB() {
		t.Errorf("picking a slot left the working colour at %s", hexString(got))
	}
	m.AccentPickerApply()

	got, ok := m.WindowAccent("aaaaaaaa1111")
	if !ok || !got.IsSlot() || got.Slot != cyan {
		t.Fatalf("the pane stored %+v, want the slot itself", got)
	}

	// The proof: the colour it renders as follows the theme.
	before := got.RGB()
	withTheme(t, "atom_one_light")
	if after := got.RGB(); after == before {
		t.Errorf("the slot resolves to %s under both themes, so it was stored as a literal", hexString(after))
	}
}

// TestAnsiRowSeedsOnTheSlotTheTargetWears: opening on an inheriting pane, whose
// session colour is one of these very slots, must put the ANSI cursor on that
// swatch and name it, rather than leaving the user to find it.
func TestAnsiRowSeedsOnTheSlotTheTargetWears(t *testing.T) {
	withSessionColors(t, true)
	m, _ := accentInheritOS(t)

	want, _ := m.SessionColor("main")
	if !want.IsSlot() {
		t.Fatalf("the fixture's session colour is %+v, which this test needs to be a slot", want)
	}
	m.OpenAccentPicker("aaaaaaaa1111")
	s := m.AccentPicker
	if s.Slot != want.Slot {
		t.Errorf("the ANSI cursor is on slot %d, want the session's slot %d", s.Slot, want.Slot)
	}
	if s.Focus != accentFocusANSI {
		t.Errorf("the keyboard landed on %v, want the row holding the colour the pane wears", s.Focus)
	}
	if text := strings.Join(pickerLines(t, m), "\n"); !strings.Contains(text, accentSlotNames[want.Slot]) {
		t.Errorf("the picker does not name the slot it opened on (%s):\n%s", accentSlotNames[want.Slot], text)
	}

	// Opening on a colour no slot names leaves the grid holding the keyboard, so
	// the picker still opens where the colour actually is.
	m.CloseAccentPicker()
	m.SetWindowAccent("aaaaaaaa1111", RGBAccent(hslToRGB(31, 0.63, 0.42)))
	m.OpenAccentPicker("aaaaaaaa1111")
	if m.AccentPicker.Slot >= 0 || m.AccentPicker.Focus != accentFocusGrid {
		t.Errorf("a literal colour opened on slot %d with focus %v", m.AccentPicker.Slot, m.AccentPicker.Focus)
	}
}

// TestAnsiRowIsReachableByMouseAndKeys: every swatch has a rect the renderer
// recorded as it drew, and the arrows walk the row and step between the bright
// eight and the seven under them.
func TestAnsiRowIsReachableByMouseAndKeys(t *testing.T) {
	m := accentTestOS(t, 120, 30)
	m.OpenAccentPicker("aaaaaaaa1111")
	m.renderAccentPicker()

	seen := map[int]bool{}
	for _, h := range m.accentHits {
		if h.Kind != accentHitANSI {
			continue
		}
		if seen[h.Col] {
			t.Errorf("slot %d was recorded twice", h.Col)
		}
		seen[h.Col] = true
		if ok, _ := m.accentPickerPress(h.Rect.X0, h.Rect.Y0); !ok {
			t.Fatalf("a press on slot %d's own rect was not routed", h.Col)
		}
		if m.AccentPicker.Slot != h.Col {
			t.Errorf("pressing slot %d's rect selected %d", h.Col, m.AccentPicker.Slot)
		}
	}
	if len(seen) != accentSwatchCount {
		t.Fatalf("%d of %d swatches are clickable", len(seen), accentSwatchCount)
	}

	// Right walks the bright row and stops at its end rather than spilling into
	// the normal one, which is a different row on screen.
	m.AccentPickerSlot(0)
	for range accentBrightCount + 2 {
		m.AccentPickerMove(1, 0)
	}
	if got := m.AccentPicker.Slot; got != accentBrightCount-1 {
		t.Errorf("walking right off the bright row landed on %d, want %d", got, accentBrightCount-1)
	}

	// Down steps to the normal colour under the bright one, and up comes back.
	m.AccentPickerSlot(6) // bright cyan
	m.AccentPickerMove(0, 1)
	if got, want := m.AccentPicker.Slot, 13; got != want { // cyan
		t.Errorf("down from bright cyan landed on %d (%s), want %d", got, accentSlotNames[got], want)
	}
	m.AccentPickerMove(0, -1)
	if got := m.AccentPicker.Slot; got != 6 {
		t.Errorf("up from cyan landed on %d, want bright cyan", got)
	}
}

// TestAnsiRowLeavesTheSlotWhenTheColourIsPickedElsewhere: the grid, the hue
// strip, the hex field and the harmony chips all produce literals, so a slot
// selected before them must not survive and be stored instead of what the user
// then picked.
func TestAnsiRowLeavesTheSlotWhenTheColourIsPickedElsewhere(t *testing.T) {
	m := accentTestOS(t, 120, 30)

	for _, step := range []struct {
		name string
		do   func()
	}{
		{"grid", func() { m.AccentPickerCell(4, 2) }},
		{"hue", func() { m.AccentPickerHueCell(9) }},
		{"hue nudge", func() { m.AccentPickerNudgeHue(3) }},
		{"hex", func() { m.AccentPickerHexKey('a') }},
		{"harmony", func() { m.AccentPickerHarmonyAt(1) }},
		{"R slider", func() { m.AccentPickerSetSlider(accentChanR, 40) }},
		{"G slider", func() { m.AccentPickerSetSlider(accentChanG, 40) }},
		{"B slider", func() { m.AccentPickerSetSlider(accentChanB, 40) }},
		{"S slider", func() { m.AccentPickerSetSlider(accentChanS, 40) }},
		{"L slider", func() { m.AccentPickerSetSlider(accentChanL, 40) }},
	} {
		m.OpenAccentPicker("aaaaaaaa1111")
		m.AccentPickerSlot(2)
		step.do()
		if m.AccentPicker.Slot >= 0 {
			t.Errorf("after the %s the picker still holds slot %d", step.name, m.AccentPicker.Slot)
		}
		m.AccentPickerApply()
		if a, _ := m.WindowAccent("aaaaaaaa1111"); a.IsSlot() {
			t.Errorf("after the %s the pane stored slot %d rather than the colour picked", step.name, a.Slot)
		}
		m.ClearWindowAccent("aaaaaaaa1111")
	}
}

// TestAnsiRowIsTheFirstThingTabReaches: the theme's own colours are the easy
// answer and the whole colour space below them is the expert one, so the row
// stays drawn first and reached first however many controls arrive beside it.
func TestAnsiRowIsTheFirstThingTabReaches(t *testing.T) {
	for _, w := range []int{120, 60, 38} {
		m := accentTestOS(t, w, 30)
		m.OpenAccentPicker("aaaaaaaa1111")
		if !m.accentSlotsShown() {
			t.Fatalf("w=%d: the fixture screen does not draw the slot rows", w)
		}

		// The row is drawn above every other control.
		m.renderAccentPicker()
		top := map[accentHitKind]int{}
		for _, h := range m.accentHits {
			if y, seen := top[h.Kind]; !seen || h.Rect.Y0 < y {
				top[h.Kind] = h.Rect.Y0
			}
		}
		for kind, y := range top {
			if kind != accentHitANSI && kind != accentHitHint && y < top[accentHitANSI] {
				t.Errorf("w=%d: a control of kind %d is drawn on row %d, above the slot row at %d",
					w, kind, y, top[accentHitANSI])
			}
		}

		// And tab reaches it before anything else, from wherever it starts.
		m.AccentPicker.Focus = accentFocusHarmony
		order := make([]accentFocus, 0, int(accentFocusCount))
		for range int(accentFocusCount) {
			m.AccentPickerFocus(1)
			order = append(order, m.AccentPicker.Focus)
		}
		if order[0] != accentFocusANSI {
			t.Errorf("w=%d: tab wrapped onto focus %d, want the slot row", w, order[0])
		}
	}
}

// TestSessionSlotPickSendsTheName: a session's accent is stored by the daemon as
// text, so a slot goes over as the word for it. The word re-resolves against
// whatever theme each client is running, and it is what the automatic
// arbitration reads to keep that hue for the session that asked for it.
func TestSessionSlotPickSendsTheName(t *testing.T) {
	withSessionColors(t, true)
	m, _ := accentInheritOS(t)

	m.OpenSessionAccentPicker("main")
	m.AccentPickerSlot(13) // cyan
	sel := m.AccentPicker.selection()
	if cmd := m.AccentPickerApply(); cmd == nil {
		t.Fatal("applying a slot to a session reached nothing")
	}
	if got := accentPayload(sel); got != "cyan" {
		t.Errorf("the daemon is sent %q for %+v, want the slot's name", got, sel)
	}
	if a, ok := ParseAccent(accentPayload(sel)); !ok || a != SlotAccent(13) {
		t.Errorf("the name does not read back as the slot it was sent for: %+v", a)
	}
	// A colour no slot names still goes over as a literal.
	if got := accentPayload(RGBAccent(hslToRGB(31, 0.63, 0.42))); !strings.HasPrefix(got, "#") {
		t.Errorf("a literal colour is sent as %q", got)
	}
}
