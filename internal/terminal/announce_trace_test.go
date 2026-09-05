package terminal

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestAnnounceTraceSeesEverySizeHandedToTheGuest pins the diagnostic hook the
// render trace uses to say what resized a pane: it fires once per size the
// guest is told, with that size, and not for a resize that tells the guest
// nothing.
func TestAnnounceTraceSeesEverySizeHandedToTheGuest(t *testing.T) {
	w := &Window{
		Tiled:      true,
		DaemonMode: true,
		Width:      60,
		Height:     40,
		Terminal:   vt.NewEmulator(60, 40),
	}
	w.DaemonResizeFunc = func(int, int) error { return nil }
	w.SeedAnnouncedSize(60, 40)

	var seen [][2]int
	AnnounceTrace = func(win *Window, cols, rows int) {
		if win != w {
			t.Errorf("the trace named another window")
		}
		seen = append(seen, [2]int{cols, rows})
	}
	t.Cleanup(func() { AnnounceTrace = nil })

	w.Resize(60, 40)
	if len(seen) != 0 {
		t.Fatalf("a same-size resize traced %v, want nothing", seen)
	}
	w.Resize(80, 40)
	if len(seen) != 1 || seen[0] != [2]int{80, 40} {
		t.Fatalf("a resize to 80x40 traced %v, want [[80 40]]", seen)
	}
	w.Resize(80, 40)
	if len(seen) != 1 {
		t.Fatalf("repeating the size traced %v, want the one entry", seen)
	}
}
