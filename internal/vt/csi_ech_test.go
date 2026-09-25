package vt_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestEmulator_HugeCountsReturnPromptly checks that a hostile repeat count
// returns promptly instead of looping on the raw parameter.
//
// Any program can print ESC[999999999X. The loop runs under the window IO
// lock, so an unclamped count froze the whole pane rather than just wasting
// time. REP (b) repeated the last character about 2.1 billion times, CHT and
// CBT (I, Z) stepped that many tab stops, and ECH (X) walked that many cells.
//
// The budget is sized from measurement, not guessed. Clamped, the slowest ECH
// input costs about 100us; unclamped it costs about 2.3s, and REP far more. A
// 1s budget therefore sits roughly four orders of magnitude above correct
// behaviour and below broken behaviour. An earlier 5s budget sat above both
// ECH costs and passed against the unclamped code, which is why it is stated
// here.
//
// The last two ECH inputs carry counts at or past the int32 boundary, which
// the parameter parser discards before the erase runs, so they cost nothing
// either way and are kept as parser-robustness cases.
func TestEmulator_HugeCountsReturnPromptly(t *testing.T) {
	inputs := []string{
		"X\x1b[2000000000b",
		"\x1b[2000000000I\x1b[2000000000Z",
		"\x1b[999999999X",
		"\x1b[1;80H\x1b[999999999X",
		"\x1b[2147483647X",
		"hello\x1b[1;1H\x1b[4294967295X",
	}

	for _, in := range inputs {
		t.Run(strings.ReplaceAll(in, "\x1b", "ESC"), func(t *testing.T) {
			emu := vt.NewEmulator(80, 24)
			defer emu.Close()

			done := make(chan struct{})
			go func() {
				defer close(done)
				_, _ = emu.WriteString(in)
			}()

			select {
			case <-done:
			case <-time.After(1 * time.Second):
				t.Fatalf("a count that is not clamped did not return within 1s")
			}
		})
	}
}

// TestEmulator_EraseCharacterClamped checks the clamp did not change what ECH
// actually erases: up to the right margin, never past it, cursor unmoved.
func TestEmulator_EraseCharacterClamped(t *testing.T) {
	emu := vt.NewEmulator(10, 2)
	defer emu.Close()

	// Fill the first line, park the cursor at column 4, erase to end of line.
	if _, err := emu.WriteString("abcdefghij\x1b[1;5H\x1b[999X"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	line, _, _ := strings.Cut(emu.String(), "\n")
	if want := "abcd"; strings.TrimRight(line, " ") != want {
		t.Errorf("line = %q, want %q after trailing blanks", line, want)
	}
	if pos := emu.CursorPosition(); pos.X != 4 || pos.Y != 0 {
		t.Errorf("ECH moved the cursor to (%d,%d), want (4,0)", pos.X, pos.Y)
	}

	// A count that fits must still erase exactly that many cells.
	emu2 := vt.NewEmulator(10, 2)
	defer emu2.Close()
	if _, err := emu2.WriteString("abcdefghij\x1b[1;1H\x1b[3X"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	line2, _, _ := strings.Cut(emu2.String(), "\n")
	if want := "   defghij"; line2 != want {
		t.Errorf("line = %q, want %q", line2, want)
	}
}
