//go:build windows

package session

import (
	"errors"
	"runtime"

	"golang.org/x/sys/windows"
)

// awakeHolder holds Windows awake with SetThreadExecutionState. The state
// belongs to a thread, so one goroutine locked to its own thread sets it and
// clears it. The state ends with the process.
//
// Adapted from Kero (github.com/egoist/kero, awake_windows.go, MIT). See
// NOTICE.
type awakeHolder struct {
	asks chan bool
	errs chan error
}

var procSetThreadExecutionState = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadExecutionState")

const (
	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001
)

func awakeMethod() string { return "SetThreadExecutionState" }

func (h *awakeHolder) ask(on bool) error {
	if h.asks == nil {
		h.asks = make(chan bool)
		h.errs = make(chan error)
		go func() {
			runtime.LockOSThread()
			for on := range h.asks {
				state := uintptr(esContinuous)
				if on {
					state |= esSystemRequired
				}
				r, _, err := procSetThreadExecutionState.Call(state)
				if r == 0 {
					h.errs <- err
					continue
				}
				h.errs <- nil
			}
		}()
	}
	h.asks <- on
	if err := <-h.errs; err != nil {
		return errors.New("SetThreadExecutionState failed: " + err.Error())
	}
	return nil
}

func (h *awakeHolder) take(_ string, _ chan<- error) error { return h.ask(true) }

func (h *awakeHolder) release() {
	if h.asks != nil {
		_ = h.ask(false)
	}
}
