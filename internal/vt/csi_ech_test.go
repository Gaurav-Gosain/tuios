package vt_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestEmulator_EraseCharacterHugeCount checks that a hostile ECH count returns
// promptly instead of walking a billion out-of-bounds cells.
//
// Any program can print ESC[999999999X. The erase runs under the window IO
// lock, so an unclamped count froze the whole pane rather than just wasting
// time. The budget here is generous: unclamped, the same input takes minutes.
func TestEmulator_EraseCharacterHugeCount(t *testing.T) {
	inputs := []string{
		"\x1b[999999999X",
		"\x1b[2147483647X",
		"hello\x1b[1;1H\x1b[4294967295X",
		"\x1b[1;80H\x1b[999999999X",
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
			case <-time.After(5 * time.Second):
				t.Fatalf("ECH with an unclamped count did not return within 5s")
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
