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
// started. RIS (ESC c), which this used to start with, erased that screen
// again, and kitty and ghostty also drop the whole scrollback on it
// (discussion #588). DECSTR, the soft reset, puts the modes, margins, pen and
// character sets back without touching a cell.
const ResetSequence = "\033[!p" + // DECSTR: soft reset, which clears nothing
	"\033[?1000l" + // Disable normal mouse tracking
	"\033[?1002l" + // Disable button event tracking
	"\033[?1003l" + // Disable all motion tracking
	"\033[?1004l" + // Disable focus tracking
	"\033[?1006l" + // Disable SGR extended mouse mode
	"\033[?1016l" + // Disable SGR-pixel mouse reports
	"\033[?2031l" + // Stop colour scheme (light and dark) reports
	"\033[?25h" + // Show cursor
	"\033]22;default\033\\" + // Pointer shape back to default (OSC 22)
	"\033[?47l" + // Exit alternate screen buffer
	"\033[0m" + // Reset all text attributes
	"\r\n" // Clean line ending

// ResetTerminal sends escape sequences to reset the terminal to a clean state.
// This should be called when exiting the application to restore the terminal.
func ResetTerminal() {
	fmt.Print(ResetSequence)
	_ = os.Stdout.Sync()
}
