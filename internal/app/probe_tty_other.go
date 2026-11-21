//go:build !windows

package app

import "os"

func openProbeTTY() (probeTTY, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return probeTTY{}, err
	}
	return probeTTY{in: tty, out: tty, close: func() { _ = tty.Close() }}, nil
}
