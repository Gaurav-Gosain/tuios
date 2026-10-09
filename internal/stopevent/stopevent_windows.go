//go:build windows

package stopevent

import (
	"errors"

	"golang.org/x/sys/windows"
)

// Watch makes the daemon's stop event and returns a channel that closes
// when kill-server sets it. The event closes when done closes.
//
// A process that still holds an event of this name, such as a kill-server
// that stopped the last daemon, can leave it set, so an event that already
// exists is reset first.
func Watch(socketPath string, done <-chan struct{}) (<-chan struct{}, error) {
	ev, err := create(Name(socketPath))
	if err != nil {
		return nil, err
	}
	stop := make(chan struct{})
	go wait(done, ev, stop)
	return stop, nil
}

func create(name string) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	ev, err := windows.CreateEvent(nil, 1, 0, p)
	if ev == 0 {
		return 0, err
	}
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		_ = windows.ResetEvent(ev)
	}
	return ev, nil
}

// wait closes stop when the event is set. It polls, since a wait on the
// event alone could not be ended when the daemon stops for another reason.
func wait(done <-chan struct{}, ev windows.Handle, stop chan<- struct{}) {
	defer func() { _ = windows.CloseHandle(ev) }()
	for {
		select {
		case <-done:
			return
		default:
		}
		r, err := windows.WaitForSingleObject(ev, 200)
		if err != nil {
			return
		}
		if r == windows.WAIT_OBJECT_0 {
			close(stop)
			return
		}
	}
}

// Request sets the stop event of the daemon on socketPath. It looks in this
// Windows session first, then in session 0, where a daemon started over
// OpenSSH runs. ErrNotWaiting means neither has the event: the daemon is
// from a tuios older than the event, or it runs in another session that
// this one cannot see. Any other error is a daemon that has the event and
// could not be told.
func Request(socketPath string) error {
	for _, name := range []string{Name(socketPath), GlobalName(socketPath)} {
		p, err := windows.UTF16PtrFromString(name)
		if err != nil {
			return err
		}
		ev, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, p)
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			continue
		}
		if err != nil {
			return err
		}
		err = windows.SetEvent(ev)
		_ = windows.CloseHandle(ev)
		return err
	}
	return ErrNotWaiting
}
