package vt

import (
	"strings"
	"testing"
)

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

// TestRenderFollowsAlternateScreenAcrossResize pins the behaviour that matters
// for a daemon state sync: the sync resizes the emulator underneath an idle
// full-screen application. An idle application emits nothing afterwards, so if
// a resize dropped the alternate buffer the pane could never repair itself.
func TestRenderFollowsAlternateScreenAcrossResize(t *testing.T) {
	const width, height = 40, 10
	e := NewEmulator(width, height)
	_, _ = e.Write([]byte("\x1b[?1049h\x1b[1;1HALTTEXT"))

	e.Resize(width-2, height-1)
	if e.scr != &e.scrs[1] {
		t.Fatal("resize moved the active screen off the alternate screen")
	}
	assertBothPathsSee(t, e, width-2, height-1, "ALTTEXT")

	e.Resize(width, height)
	if e.scr != &e.scrs[1] {
		t.Fatal("resize moved the active screen off the alternate screen")
	}
	assertBothPathsSee(t, e, width, height, "ALTTEXT")
}
