//go:build windows

package app

import (
	"os"

	"golang.org/x/sys/windows"
)

// openProbeTTY opens the console for the capability probe: CONIN$ for the
// replies and CONOUT$ for the queries. These are the console's own input and
// screen buffer, whatever stdin and stdout were redirected to, as /dev/tty is
// on Unix. The probe used to open /dev/tty here too, which does not exist on
// Windows, so it never asked the host anything (#1107).
//
// A console that is not processing escape sequences would print the queries
// as text and answer none of them, so output VT processing is turned on for
// the probe and the old mode put back when it closes. A console that refuses
// it has no VT support to probe.
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
