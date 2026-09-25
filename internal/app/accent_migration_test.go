package app

import (
	"encoding/json"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
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
		t.Errorf("slot 0 resolved to %s, want bright black %s", got.Hex(), overlay.Hex(want))
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

// theme8 is the bright-black ANSI slot, which is what accent index 0 means.
func theme8() color.Color { return accentColor(0) }
