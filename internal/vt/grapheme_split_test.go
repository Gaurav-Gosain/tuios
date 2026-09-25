package vt_test

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestEmulator_GraphemeSplitKeepsWidth checks that a cluster whose width
// changes with its last rune gets the same cells, widths included, and leaves
// the cursor in the same place wherever a Write boundary splits it.
//
// A comparison of String cannot see a width: a heart one column wide and a
// heart two columns wide read the same. tuitest's copy of this emulator had exactly that bug. A read ending between
// U+2764 and U+FE0F drew the heart one column wide and shifted the rest of the
// row, and the wide rune differential in e2e/tui failed or passed depending on
// where the kernel ended a PTY read. This pins the behaviour the copy is synced
// from.
func TestEmulator_GraphemeSplitKeepsWidth(t *testing.T) {
	const cols, rows = 12, 3
	for _, tc := range []struct {
		name string
		in   string
	}{
		{"presentation selector", "ab❤️❤️❤️cd"},
		{"keycap", "1️⃣x"},
		{"selector at the right margin", "0123456789a❤️z"},
		{"selector at the last two columns", "0123456789❤️z"},
		{"selector then styled text", "❤️\x1b[1mb\x1b[0m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(tc.in)
			whole := vt.NewEmulator(cols, rows)
			defer whole.Close()
			if _, err := whole.Write(data); err != nil {
				t.Fatalf("Write: %v", err)
			}
			wantCursor := whole.CursorPosition()

			for at := 1; at < len(data); at++ {
				split := vt.NewEmulator(cols, rows)
				if _, err := split.Write(data[:at]); err != nil {
					t.Fatalf("Write: %v", err)
				}
				if _, err := split.Write(data[at:]); err != nil {
					t.Fatalf("Write: %v", err)
				}
				for y := range rows {
					for x := range cols {
						w, g := whole.CellAt(x, y), split.CellAt(x, y)
						if w.Content != g.Content || w.Width != g.Width {
							t.Errorf("split at byte %d: cell (%d,%d) is %q width %d, want %q width %d",
								at, x, y, g.Content, g.Width, w.Content, w.Width)
						}
					}
				}
				if got := split.CursorPosition(); got != wantCursor {
					t.Errorf("split at byte %d: cursor at %v, want %v", at, got, wantCursor)
				}
				split.Close()
			}
		})
	}
}

// TestEmulator_SplitGraphemeVisibleImmediately checks the other half of the
// contract: a cluster arriving at the end of a Write must be on screen right
// away. Buffering it until the next Write would make the last character of a
// shell prompt invisible until the next byte of output arrived.
func TestEmulator_SplitGraphemeVisibleImmediately(t *testing.T) {
	emu := vt.NewEmulator(80, 24)
	defer emu.Close()

	if _, err := emu.WriteString("prompt ❯"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := emu.String(); !strings.Contains(got, "prompt ❯") {
		t.Fatalf("trailing grapheme not rendered after Write: %q", got)
	}

	// Extending it must not leave a stale copy behind in the next cell.
	if _, err := emu.WriteString("́"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := emu.String()
	if strings.Contains(got, "❯❯") {
		t.Fatalf("continuation duplicated the open cluster: %q", got)
	}
	if !strings.Contains(got, "prompt ❯́") {
		t.Fatalf("continuation did not extend the open cluster: %q", got)
	}
}
