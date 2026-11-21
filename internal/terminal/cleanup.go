package terminal

import (
	"fmt"
	"os"
)

// ResetSequence puts the terminal back in a clean state: it undoes the modes a
// tuios client turns on and leaves the alternate screen.
//
// It must not clear anything. Bubble Tea has already left the alternate
// screen when this runs, so the host shows what it showed before tuios
// started. RIS (ESC c) would erase that screen again, and kitty and ghostty
// also drop the whole scrollback on it. DECSTR, the soft reset, puts the modes, margins, pen and
// character sets back without touching a cell.
const ResetSequence = "\033[!p" +
	"\033[?1000l" +
	"\033[?1002l" +
	"\033[?1003l" +
	"\033[?1004l" +
	"\033[?1006l" +
	"\033[?1016l" +
	"\033[?2004l" +
	"\033[?2031l" +
	"\033[?25h" +
	"\033[?7h" +
	"\033]22;default\033\\" +
	"\033[?47l" +
	"\033[0m" +
	"\r\n" // Clean line ending

// ResetTerminal sends escape sequences to reset the terminal to a clean state.
// This should be called when exiting the application to restore the terminal.
func ResetTerminal() {
	fmt.Print(ResetSequence)
	_ = os.Stdout.Sync()
}
