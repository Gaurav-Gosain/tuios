package terminal

import (
	"fmt"
	"os"
)

// ResetTerminal sends escape sequences to reset the terminal to a clean state.
// This should be called when exiting the application to restore the terminal.
func ResetTerminal() {
	fmt.Print(
		"\033c" + // Reset terminal to initial state
			"\033[?1000l" + // Disable normal mouse tracking
			"\033[?1002l" + // Disable button event tracking
			"\033[?1003l" + // Disable all motion tracking
			"\033[?1004l" + // Disable focus tracking
			"\033[?1006l" + // Disable SGR extended mouse mode
			"\033[?25h" + // Show cursor
			"\033[?47l" + // Exit alternate screen buffer
			"\033[0m" + // Reset all text attributes
			"\r\n", // Clean line ending
	)
	_ = os.Stdout.Sync()
}
