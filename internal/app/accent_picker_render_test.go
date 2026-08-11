package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// accentTestOS is sidebarTestOS with a pane that has no agent state, so the
// accent mark has a glyph column to occupy: state outranks identity, and a
// preview drawn over a state glyph would be testing the wrong rule.
func accentTestOS(t *testing.T, w, h int) *OS {
	t.Helper()
	m := sidebarTestOS(t, w, h, "left")
	m.Windows[0].AgentState = ""
	return m
}

// pickerLines renders the accent dialog and returns its rows with styling
// stripped.
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

// TestAccentPickerOffersFifteenSlots: the slot model went from eight to
// fifteen, and every slot is one the theme author chose, so the set survives a
// theme switch without a stored hex to re-resolve.
func TestAccentPickerOffersFifteenSlots(t *testing.T) {
	m := accentTestOS(t, 120, 30)
	m.OpenAccentPicker("aaaaaaaa1111")

	text := strings.Join(pickerLines(t, m), "\n")
	for _, name := range accentNames {
		if !strings.Contains(text, name) {
			t.Errorf("the picker does not list %q:\n%s", name, text)
		}
	}
	if accentSwatchCount != 15 {
		t.Errorf("accentSwatchCount = %d, want 15", accentSwatchCount)
	}
	// Every slot resolves to a colour of its own; a duplicate would mean two
	// rows that look identical and mean different things.
	seen := map[string]int{}
	for i := range accentSwatchCount {
		r, g, b, _ := accentColor(i).RGBA()
		key := string(rune(r>>8)) + string(rune(g>>8)) + string(rune(b>>8))
		if prev, dup := seen[key]; dup {
			t.Errorf("slots %d (%s) and %d (%s) resolve to the same colour",
				prev, accentNames[prev], i, accentNames[i])
		}
		seen[key] = i
	}
}

// The cursor row and the row holding the current accent are different marks, so
// they read correctly when they land on different rows.
func TestAccentPickerSeparatesCursorFromCurrent(t *testing.T) {
	m := accentTestOS(t, 120, 30)
	m.SetWindowAccent("aaaaaaaa1111", 4) // blue
	m.OpenAccentPicker("aaaaaaaa1111")
	m.AccentPickerSelected = 1 // red

	content, _, _ := m.renderAccentPicker()
	pal := theme.UI()
	// Matched on the row's first word, so "dark red" is not mistaken for "red".
	var cursorRow, currentRow string
	for _, r := range strings.Split(content, "\n") {
		f := strings.Fields(stripANSIForTrace(strings.TrimPrefix(stripANSIForTrace(r), "│")))
		if len(f) == 0 {
			continue
		}
		name := f[0]
		if name == overlay.SigilMark() && len(f) > 1 {
			name = f[1]
		}
		switch name {
		case "red":
			cursorRow = r
		case "blue":
			currentRow = r
		}
	}
	if cursorRow == "" || currentRow == "" {
		t.Fatalf("the picker drew neither row:\n%s", content)
	}
	if !strings.Contains(cursorRow, bgParams(pal.Surface)) {
		t.Errorf("the cursor row has no band: %q", cursorRow)
	}
	if !strings.Contains(stripANSIForTrace(cursorRow), listRowMarker(true)) {
		t.Errorf("the cursor row has no marker: %q", cursorRow)
	}
	if !strings.Contains(stripANSIForTrace(currentRow), accentCurrentGlyph()) {
		t.Errorf("the current accent is not marked: %q", currentRow)
	}
	if strings.Contains(currentRow, bgParams(pal.Surface)) {
		t.Errorf("the current row is wearing the cursor's band: %q", currentRow)
	}
	// The name is drawn in the colour it names.
	if !strings.Contains(currentRow, fgParams(accentColor(4))) {
		t.Errorf("the slot name is not in its own colour: %q", currentRow)
	}
	// Old-vs-new: what it wears now, and what the cursor would give it.
	now := stripANSIForTrace(strings.Split(content, "\n")[1])
	if !strings.Contains(now, "now") || !strings.Contains(now, "blue") || !strings.Contains(now, "red") {
		t.Errorf("the old-to-new line does not name both slots: %q", now)
	}
}

// The picker previews its cursor slot on the rail row it targets, driven purely
// by signature keys: no tick, and no rebuild on anything but the keystrokes
// that move the cursor.
func TestAccentPickerPreviewsOnTheRail(t *testing.T) {
	m := accentTestOS(t, 120, 30)
	mark := accentMark()

	resting := m.sidebarSignature()
	m.OpenAccentPicker("aaaaaaaa1111") // "editor", no agent state
	m.AccentPickerSelected = 1         // red
	if m.sidebarSignature() == resting {
		t.Fatal("opening the picker left the rail signature unchanged, so the preview would not draw")
	}
	withRed := m.sidebarSignature()

	row := ""
	for _, l := range railText(t, m) {
		if strings.Contains(l, "editor") {
			row = l
		}
	}
	if !strings.Contains(row, mark) {
		t.Errorf("the rail row is not previewing the cursor slot: %q", row)
	}

	m.AccentPickerMove(1)
	if m.sidebarSignature() == withRed {
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

// The dialog fits a short screen by scrolling its slots rather than drawing off
// the bottom of it, and every row it draws is exactly its own width.
func TestAccentPickerFitsShortScreens(t *testing.T) {
	for _, h := range []int{40, 24, 14, 10} {
		m := accentTestOS(t, 120, h)
		m.OpenAccentPicker("aaaaaaaa1111")
		m.AccentPickerSelected = accentSwatchCount - 1 // the last slot

		content, geo, hits := m.renderAccentPicker()
		lines := strings.Split(content, "\n")
		if geo.Height > h {
			t.Errorf("h=%d: the dialog is %d rows tall", h, geo.Height)
		}
		for i, l := range lines {
			if w := lipgloss.Width(l); w != geo.Width {
				t.Errorf("h=%d row %d is %d cells, want %d: %q", h, i, w, geo.Width, l)
			}
		}
		// The selected slot is on screen, and the recorded hits line up with the
		// rows as drawn.
		want := accentNames[accentSwatchCount-1]
		if !strings.Contains(stripANSIForTrace(content), want) {
			t.Errorf("h=%d: the selected slot %q scrolled off:\n%s", h, want, content)
		}
		for _, hit := range hits {
			if hit.Rect.Y0 < 0 || hit.Rect.Y1 > geo.Height {
				t.Errorf("h=%d: hit for row %d is outside the dialog: %+v", h, hit.Idx, hit.Rect)
			}
			plain := stripANSIForTrace(lines[hit.Rect.Y0])
			name := "none"
			if hit.Idx < accentSwatchCount {
				name = accentNames[hit.Idx]
			}
			if !strings.Contains(plain, name) {
				t.Errorf("h=%d: the hit for %q points at %q", h, name, plain)
			}
		}
	}
}

// ASCII mode keeps every mark one cell and drops the box glyphs.
func TestAccentPickerDegradesToASCII(t *testing.T) {
	overlay.SetASCII(true)
	t.Cleanup(func() { overlay.SetASCII(false) })

	m := accentTestOS(t, 120, 30)
	m.SetWindowAccent("aaaaaaaa1111", 4)
	m.OpenAccentPicker("aaaaaaaa1111")

	for i, l := range pickerLines(t, m) {
		if strings.ContainsAny(l, "╭╮╰╯│─╌✕●›→") {
			t.Errorf("row %d still draws non-ASCII marks: %q", i, l)
		}
	}
}
