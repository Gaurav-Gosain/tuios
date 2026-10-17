//go:build !darwin && !linux

package session

import "errors"

// canPeekExit is false here: there is no waitid with WNOWAIT this build can
// call, so a child is reaped the moment it exits and a live handoff is
// refused (see pty_reap.go).
const canPeekExit = false

func peekExit(int) error { return errors.ErrUnsupported }
