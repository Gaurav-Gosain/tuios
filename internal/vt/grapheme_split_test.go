package vt_test

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestEmulator_GraphemeSplitAcrossWrites checks that a grapheme cluster split
// across Write calls renders the same as the identical bytes delivered at once.
//
// A PTY read boundary can fall anywhere, so this is not a theoretical split: it
// happens whenever a program's output straddles a read. The emulator used to
// close the pending cluster at the end of every Write, so the continuation
// runes formed a second cluster that overwrote the first, silently dropping
// combining marks and emoji modifiers from the screen.
func TestEmulator_GraphemeSplitAcrossWrites(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"combining marks", "é̂̃"},
		{"single combining mark", "á"},
		{"emoji skin tone", "\U0001F44D\U0001F3FD"},
		{"zwj family", "\U0001F468\u200d\U0001F469\u200d\U0001F467"},
		{"variation selector", "❤️"},
		{"devanagari cluster", "क्ष"},
		{"flag sequence", "\U0001F1EF\U0001F1F5"},
		{"keycap", "1️⃣"},
		{"text then cluster", "hi é̂"},
		{"cluster then text", "é̂ hi"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			whole := vt.NewEmulator(80, 24)
			defer whole.Close()
			if _, err := whole.Write([]byte(tc.input)); err != nil {
				t.Fatalf("Write: %v", err)
			}
			want := whole.String()

			for _, size := range []int{1, 2, 3, 5} {
				split := vt.NewEmulator(80, 24)
				data := []byte(tc.input)
				for off := 0; off < len(data); off += size {
					end := min(off+size, len(data))
					if _, err := split.Write(data[off:end]); err != nil {
						split.Close()
						t.Fatalf("Write: %v", err)
					}
				}
				got := split.String()
				split.Close()
				if got != want {
					t.Errorf("chunk size %d: got %q, want %q", size, got, want)
				}
			}
		})
	}
}

// TestEmulator_GraphemeSplitKeepsWidth checks that a cluster whose width
// changes with its last rune gets the same cells, widths included, and leaves
// the cursor in the same place wherever a Write boundary splits it.
//
// TestEmulator_GraphemeSplitAcrossWrites compares String, which cannot see a
// width: a heart one column wide and a heart two columns wide read the same.
// tuitest's copy of this emulator had exactly that bug. A read ending between
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

// TestEmulator_SplitGraphemeClosedBySequence checks that an escape sequence
// arriving after a Write ended mid-cluster closes the cluster rather than
// redrawing it, which would double-print the character.
func TestEmulator_SplitGraphemeClosedBySequence(t *testing.T) {
	emu := vt.NewEmulator(80, 24)
	defer emu.Close()

	if _, err := emu.WriteString("❯"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := emu.WriteString("\x1b[31mX"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	line, _, _ := strings.Cut(emu.String(), "\n")
	if want := "❯X"; !strings.HasPrefix(line, want) {
		t.Fatalf("first line = %q, want prefix %q", line, want)
	}
}
