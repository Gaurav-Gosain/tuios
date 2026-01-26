package terminal

import (
	"fmt"
	"os"
)

// ResetTerminal sends escape sequences to reset the terminal to a clean state.
// This should be called when exiting the application to restore the terminal.
func ResetTerminal() {
	// Reset terminal to initial state
	fmt.Print("\033c")
	// Disable mouse tracking modes
	fmt.Print("\033[?1000l") // Disable normal tracking mode
	fmt.Print("\033[?1002l") // Disable button event tracking
	fmt.Print("\033[?1003l") // Disable all motion tracking
	fmt.Print("\033[?1004l") // Disable focus tracking
	fmt.Print("\033[?1006l") // Disable SGR extended mouse mode
	// Show cursor
	fmt.Print("\033[?25h")
	// Exit alternate screen buffer
	fmt.Print("\033[?47l")
	// Reset all text attributes
	fmt.Print("\033[0m")
	// Ensure clean line ending
	fmt.Print("\r\n")
	// Flush stdout
	_ = os.Stdout.Sync()
}
