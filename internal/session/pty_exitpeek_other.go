//go:build !darwin && !linux

package session

import "errors"

const canPeekExit = false

func peekExit(int) error { return errors.ErrUnsupported }

func waitForeignExit(int) (code, sig int) { return -1, 0 }
