package pidwait

import (
	"context"
	"errors"

	"golang.org/x/sys/windows"
)

// Polls is false: the process handle is signalled when the process exits.
const Polls = false

func wait(ctx context.Context, pid int) error {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid)) //nolint:gosec // a pid
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return ErrNotRunning
		}
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	cancel, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(cancel) }()
	stop := context.AfterFunc(ctx, func() { _ = windows.SetEvent(cancel) })
	defer stop()
	ev, err := windows.WaitForMultipleObjects([]windows.Handle{h, cancel}, false, windows.INFINITE)
	switch {
	case err != nil:
		return err
	case ev == windows.WAIT_OBJECT_0:
		return nil
	default:
		return ctx.Err()
	}
}
