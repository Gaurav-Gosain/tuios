package vt

import "testing"

// TestPerFrameModeSetsAllocateNothing pins the cost of the mode changes an
// application sends around every redraw. nvim hides and shows the cursor
// around each one, and Claude Code also pushes and pops kitty keyboard flags.
// These used to pass through a debug logf whose arguments Go boxes before
// logf can see that no logger is set: on a replay of a Claude Code session
// that was more than half of every byte the emulator allocated.
func TestPerFrameModeSetsAllocateNothing(t *testing.T) {
	e := NewEmulator(80, 24)
	e.WriteString("\x1b[?25l\x1b[?25h\x1b[?1h\x1b[?1l\x1b[>1u\x1b[<u")

	for _, seq := range []string{
		"\x1b[?25l\x1b[?25h", // DECTCEM, around every nvim redraw
		"\x1b[?1h\x1b[?1l",   // DECCKM
		"\x1b[>1u\x1b[<u",    // kitty keyboard push and pop
		"\x1b[=1;1u",         // kitty keyboard set
	} {
		if got := testing.AllocsPerRun(200, func() { e.WriteString(seq) }); got > 0 {
			t.Errorf("%q allocates %.1f times per write, want 0", seq, got)
		}
	}
}
