//go:build !windows

package app

import "os"

// openProbeTTY opens the controlling terminal for the capability probe. One
// file carries both directions.
func openProbeTTY() (probeTTY, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return probeTTY{}, err
	}
	return probeTTY{in: tty, out: tty, close: func() { _ = tty.Close() }}, nil
}
