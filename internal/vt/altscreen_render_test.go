package vt

import (
	"strings"
	"testing"
)

// The renderer has two paths for a terminal pane. The focused pane is rendered
// cell by cell via CellAt, and the unfocused pane takes a fast path that calls
// Emulator.Render. Render reads e.scr, the currently active screen pointer, so
// the two paths agree only as long as e.scr tracks the alternate screen
// correctly. A blank unfocused pane running a full-screen application would be
// explained by e.scr pointing at the main screen while the application draws on
// the alternate one, so these tests pin that pointer down across every
// operation that can move it.

// cellText reads the emulator the way the focused (slow) render path does.
func cellText(e *Emulator, width, height int) string {
	var b strings.Builder
	for y := range height {
		for x := range width {
			c := e.scr.CellAt(x, y)
			if c == nil || c.Content == "" {
				b.WriteString(" ")
				continue
			}
			b.WriteString(c.Content)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func assertBothPathsSee(t *testing.T, e *Emulator, width, height int, want string) {
	t.Helper()
	if got := e.Render(); !strings.Contains(got, want) {
		t.Errorf("fast path (Emulator.Render) lost %q, got %q", want, got)
	}
	if got := cellText(e, width, height); !strings.Contains(got, want) {
		t.Errorf("slow path (CellAt) lost %q, got %q", want, got)
	}
}

// TestRenderFollowsAlternateScreen covers the ordinary DEC 1049 entry and exit
// an application performs, and asserts both render paths agree at each step.
func TestRenderFollowsAlternateScreen(t *testing.T) {
	const width, height = 40, 10
	e := NewEmulator(width, height)

	_, _ = e.Write([]byte("MAINTEXT"))
	if e.IsAltScreen() {
		t.Fatal("emulator reports alternate screen before any DEC 1049")
	}
	assertBothPathsSee(t, e, width, height, "MAINTEXT")

	// Enter the alternate screen and paint, as vim or htop would.
	_, _ = e.Write([]byte("\x1b[?1049h\x1b[1;1HALTTEXT"))
	if !e.IsAltScreen() {
		t.Fatal("emulator does not report alternate screen after DEC 1049 set")
	}
	if e.scr != &e.scrs[1] {
		t.Fatal("active screen pointer is not the alternate screen after DEC 1049 set")
	}
	assertBothPathsSee(t, e, width, height, "ALTTEXT")

	// Leaving the alternate screen must restore the main screen contents.
	_, _ = e.Write([]byte("\x1b[?1049l"))
	if e.IsAltScreen() {
		t.Fatal("emulator still reports alternate screen after DEC 1049 reset")
	}
	if e.scr != &e.scrs[0] {
		t.Fatal("active screen pointer is not the main screen after DEC 1049 reset")
	}
	assertBothPathsSee(t, e, width, height, "MAINTEXT")
}

// TestRenderFollowsAlternateScreenAcrossResize pins the behaviour that matters
// for a daemon state sync: the sync resizes the emulator underneath an idle
// full-screen application. An idle application emits nothing afterwards, so if
// a resize dropped the alternate buffer the pane could never repair itself.
func TestRenderFollowsAlternateScreenAcrossResize(t *testing.T) {
	const width, height = 40, 10
	e := NewEmulator(width, height)
	_, _ = e.Write([]byte("\x1b[?1049h\x1b[1;1HALTTEXT"))

	// Shrink, as a retile onto a smaller tile would.
	e.Resize(width-2, height-1)
	if e.scr != &e.scrs[1] {
		t.Fatal("resize moved the active screen off the alternate screen")
	}
	assertBothPathsSee(t, e, width-2, height-1, "ALTTEXT")

	// Grow back. Both paths must still agree, and the fast path must not read
	// the main screen.
	e.Resize(width, height)
	if e.scr != &e.scrs[1] {
		t.Fatal("resize moved the active screen off the alternate screen")
	}
	assertBothPathsSee(t, e, width, height, "ALTTEXT")
}

// TestRestoreAltScreenModeMovesActiveScreen covers the daemon reattach path,
// which switches the buffer pointer directly instead of replaying DEC 1049 so
// that it does not clear the restored contents.
func TestRestoreAltScreenModeMovesActiveScreen(t *testing.T) {
	const width, height = 40, 10
	e := NewEmulator(width, height)
	_, _ = e.Write([]byte("\x1b[?1049h\x1b[1;1HALTTEXT"))

	// Restoring to the alternate screen must be idempotent and must not clear.
	e.RestoreAltScreenMode(true)
	if e.scr != &e.scrs[1] {
		t.Fatal("RestoreAltScreenMode(true) did not select the alternate screen")
	}
	assertBothPathsSee(t, e, width, height, "ALTTEXT")

	e.RestoreAltScreenMode(false)
	if e.scr != &e.scrs[0] {
		t.Fatal("RestoreAltScreenMode(false) did not select the main screen")
	}

	e.RestoreAltScreenMode(true)
	if e.scr != &e.scrs[1] {
		t.Fatal("RestoreAltScreenMode(true) did not reselect the alternate screen")
	}
	assertBothPathsSee(t, e, width, height, "ALTTEXT")
}
