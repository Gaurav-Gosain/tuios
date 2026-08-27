package app

import (
	"encoding/json"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// writeSidebarStateFile drops raw JSON where loadSidebarState will find it.
func writeSidebarStateFile(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(sidebarStateDir(), sidebarStateFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("make the state dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write the state file: %v", err)
	}
}

// TestLegacyAccentFileRendersIdentically is the migration's real obligation: a
// sidebar state file written before the colour picker existed has to put the
// same pixels on the rail afterwards.
//
// The proof is a byte comparison of the rendered rows. The "before" side is the
// rail rendered from an accent built the way the old code built one, an index
// resolved against the live theme; the "after" side is the same rail rendered
// from the file. Nothing about the row may differ, including the colour the
// chip is painted in.
func TestLegacyAccentFileRendersIdentically(t *testing.T) {
	const legacyID = "cccccccc3333" // "logs", the fixture pane with no agent state

	// After: a pre-migration file, loaded through the migration.
	after := accentTestOS(t, 120, 40)
	writeSidebarStateFile(t, `{"accents":{"`+legacyID+`":4}}`)
	after.SidebarAccents = nil
	after.loadSidebarState()

	// Before: the accent the old model would have held for index 4, which is an
	// ANSI slot resolved against whatever theme is loaded.
	before := accentTestOS(t, 120, 40)
	after.Settings = config.Global
	after.Settings = before.Settings
	before.SidebarAccents = map[string]Accent{legacyID: SlotAccent(4)}

	beforeRows, afterRows := railStyledFrame(t, before), railStyledFrame(t, after)

	// Guard against the comparison passing because neither side drew anything:
	// the chip has to be on the row for its colour to be worth comparing.
	chipRows := 0
	for _, r := range beforeRows {
		if strings.Contains(stripANSIForTrace(r), accentMark()) {
			chipRows++
		}
	}
	if chipRows == 0 {
		t.Fatalf("the pre-migration rail draws no accent chip, so this proves nothing:\n%s",
			strings.Join(beforeRows, "\n"))
	}

	if len(beforeRows) != len(afterRows) {
		t.Fatalf("the rail changed height: %d rows before, %d after", len(beforeRows), len(afterRows))
	}
	for i := range beforeRows {
		if beforeRows[i] != afterRows[i] {
			t.Fatalf("row %d of the rail changed across the migration:\n before %q\n  after %q",
				i, beforeRows[i], afterRows[i])
		}
	}

	// And the accent it loaded is still a slot, not a hex snapshot: it has to go
	// on re-resolving when the theme changes, which is what a slot is for.
	got, ok := after.WindowAccent(legacyID)
	if !ok {
		t.Fatal("the legacy accent did not survive the load")
	}
	if !got.IsSlot() || got.Slot != 4 {
		t.Errorf("the legacy index loaded as %+v, want slot 4", got)
	}
	if got.RGB() != toRGBA(accentColor(4)) {
		t.Errorf("slot 4 resolved to %s, want the theme's ANSI slot %s",
			got.Hex(), hexString(toRGBA(accentColor(4))))
	}
}

// TestLegacyAccentIndexZeroIsBrightBlack pins the one index whose meaning is
// easiest to lose in a migration: 0 is the first of the bright slots, and a
// scheme that made it "no accent" or shifted it by one would silently repaint
// every row that had it.
func TestLegacyAccentIndexZeroIsBrightBlack(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	writeSidebarStateFile(t, `{"accents":{"w1":0}}`)

	m := &OS{Settings: config.Global}
	m.loadSidebarState()

	got, ok := m.WindowAccent("w1")
	if !ok {
		t.Fatal("index 0 loaded as no accent at all")
	}
	if !got.IsSlot() || got.Slot != 0 {
		t.Fatalf("index 0 loaded as %+v, want slot 0", got)
	}
	// Slot 0 is ANSI 8, bright black, which is what it has always been.
	if want := toRGBA(theme8()); got.RGB() != want {
		t.Errorf("slot 0 resolved to %s, want bright black %s", got.Hex(), hexString(want))
	}
}

// TestAccentFileRoundTripsBothKinds: a slot stays an int on disk and a picked
// colour is written as a hex, so an older binary reading this file keeps the
// accents it understands instead of failing to parse and losing the order, the
// collapse state and the width with them.
func TestAccentFileRoundTripsBothKinds(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)

	m := &OS{Settings: config.Global}
	m.SidebarAccents = map[string]Accent{
		"slot":   SlotAccent(6),
		"picked": RGBAccent(color.RGBA{R: 0x3b, G: 0x82, B: 0xf6, A: 0xff}),
	}
	m.saveSidebarState()

	raw, err := os.ReadFile(filepath.Join(sidebarStateDir(), sidebarStateFileName))
	if err != nil {
		t.Fatalf("read back the state file: %v", err)
	}
	var st sidebarStateFile
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("the state file does not parse: %v", err)
	}
	if st.Accents["slot"] != 6 {
		t.Errorf("the slot accent was not written as an index: %v", st.Accents)
	}
	if _, dup := st.AccentColors["slot"]; dup {
		t.Errorf("the slot accent was written to both maps: %v", st.AccentColors)
	}
	if st.AccentColors["picked"] != "#3b82f6" {
		t.Errorf("the picked colour was written as %q, want #3b82f6", st.AccentColors["picked"])
	}
	if _, dup := st.Accents["picked"]; dup {
		t.Errorf("the picked colour was also written as an index: %v", st.Accents)
	}

	next := &OS{Settings: config.Global}
	next.loadSidebarState()
	for id, want := range m.SidebarAccents {
		if got, ok := next.WindowAccent(id); !ok || got != want {
			t.Errorf("%s came back as %+v, want %+v", id, got, want)
		}
	}
}

// TestAccentSurvivesRestart is the real-user case: an accent set today is on the
// row after the client is restarted.
func TestAccentSurvivesRestart(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)

	want := RGBAccent(color.RGBA{R: 0xf5, G: 0x9e, B: 0x0b, A: 0xff})
	m := &OS{Settings: config.Global}
	m.SetWindowAccent("w1", want)

	next := &OS{Settings: config.Global}
	next.loadSidebarState()
	if got, ok := next.WindowAccent("w1"); !ok || got != want {
		t.Fatalf("accent after restart = %+v, want %+v", got, want)
	}
}

// railStyledFrame renders the rail and returns its rows with the styling left
// on, which is what a byte comparison across the migration has to be built on:
// the colour of the accent chip is exactly the thing that must not have moved.
func railStyledFrame(t *testing.T, m *OS) []string {
	t.Helper()
	lines, _ := m.sidebarPanelLines()
	return append([]string(nil), lines...)
}

// theme8 is the bright-black ANSI slot, which is what accent index 0 means.
func theme8() color.Color { return accentColor(0) }

// TestAccentPickerOpensOnALegacySlot: opening the picker on a window that still
// carries a stored index has to start from that colour, so the old-to-new line
// tells the truth and a stray keystroke cannot silently move the accent.
func TestAccentPickerOpensOnALegacySlot(t *testing.T) {
	m := accentTestOS(t, 120, 30)
	m.SidebarAccents = map[string]Accent{"aaaaaaaa1111": SlotAccent(4)}

	m.OpenAccentPicker("aaaaaaaa1111")
	if !m.AccentPicker.HadPrev || m.AccentPicker.Prev.Slot != 4 {
		t.Fatalf("the picker opened with prev %+v (had=%v), want slot 4",
			m.AccentPicker.Prev, m.AccentPicker.HadPrev)
	}
	if m.AccentPicker.Cur != toRGBA(accentColor(4)) {
		t.Errorf("the picker opened on %s, want the slot's colour %s",
			hexString(m.AccentPicker.Cur), hexString(toRGBA(accentColor(4))))
	}
	// The old colour is named on screen.
	if plain := stripANSIForTrace(mustRenderPicker(t, m)); !strings.Contains(plain, m.AccentPicker.Prev.Hex()) {
		t.Errorf("the old-to-new line does not show the accent the window has:\n%s", plain)
	}
}

// mustRenderPicker renders the picker and returns the frame.
func mustRenderPicker(t *testing.T, m *OS) string {
	t.Helper()
	content, _, _ := m.renderAccentPicker()
	return content
}
