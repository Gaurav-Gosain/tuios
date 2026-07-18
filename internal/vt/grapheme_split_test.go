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
		{"zwj family", "\U0001F468‍\U0001F469‍\U0001F467"},
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
