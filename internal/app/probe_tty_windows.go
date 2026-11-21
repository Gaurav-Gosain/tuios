//go:build windows

package app

import (
	"os"

	"golang.org/x/sys/windows"
)

func openProbeTTY() (probeTTY, error) {
	in, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		return probeTTY{}, err
	}
	out, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		_ = in.Close()
		return probeTTY{}, err
	}
	outHandle := windows.Handle(out.Fd())
	var mode uint32
	err = windows.GetConsoleMode(outHandle, &mode)
	if err == nil {
		err = windows.SetConsoleMode(outHandle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	}
	if err != nil {
		_ = in.Close()
		_ = out.Close()
		return probeTTY{}, err
	}
	return probeTTY{in: in, out: out, close: func() {
		_ = windows.SetConsoleMode(outHandle, mode)
		_ = in.Close()
		_ = out.Close()
	}}, nil
}
