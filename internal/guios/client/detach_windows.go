package client

import (
	"os"

	"golang.org/x/sys/windows"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
)

// Interrupt asks the daemon of a pid to save and stop, through its event
// (proto.StopEvent): Windows has no signal for a process without a console.
func Interrupt(p *os.Process) error {
	name, err := windows.UTF16PtrFromString(proto.StopEvent(p.Pid))
	if err != nil {
		return err
	}
	ev, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(ev)
	return windows.SetEvent(ev)
}
