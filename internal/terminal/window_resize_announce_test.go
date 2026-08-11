package terminal

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestResizeDoesNotReannounceSeededSize pins N9 at the window level: after the
// reattach path seeds the size the daemon PTY already carries, a retile to that
// same size must not call the daemon resize callback, because that resize
// SIGWINCHes the shell into repainting its prompt. A genuine size change still
// announces.
func TestResizeDoesNotReannounceSeededSize(t *testing.T) {
	w := &Window{
		Tiled:      true,
		DaemonMode: true,
		Width:      60,
		Height:     40,
		Terminal:   vt.NewEmulator(60, 40),
		announcedW: 0,
		announcedH: 0,
	}
	var calls int
	w.DaemonResizeFunc = func(int, int) error { calls++; return nil }

	// The daemon PTY is already 60x40; seed it as announced.
	w.SeedAnnouncedSize(60, 40)

	// Same size: zero announcements.
	w.Resize(60, 40)
	if calls != 0 {
		t.Fatalf("same-size resize announced %d times, want 0", calls)
	}

	// A real change: exactly one announcement.
	w.Resize(80, 40)
	if calls != 1 {
		t.Fatalf("changed resize announced %d times, want 1", calls)
	}

	// Repeating that same new size announces nothing more.
	w.Resize(80, 40)
	if calls != 1 {
		t.Fatalf("re-resize to the same new size announced again (%d), want 1", calls)
	}
}
