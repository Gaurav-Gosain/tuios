package progresume_test

import (
	"errors"
	"log"

	"tuios.dev/progresume"
)

// A program reports how to start it again when it opens a session file, and
// clears the report when the person quits it on purpose.
func Example() {
	err := progresume.Report([]string{"nvim", "-S", "/home/user/.local/state/nvim/sessions/8f2c1d.vim"}, "")
	if err != nil && !errors.Is(err, progresume.ErrNoToken) {
		log.Printf("cannot report: %v", err)
	}
	// ... the program runs ...
	_ = progresume.Clear()
}
