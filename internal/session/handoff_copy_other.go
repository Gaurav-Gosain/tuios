//go:build !darwin

package session

import (
	"errors"
	"os"
)

func cloneFrom(*os.File, string) error { return errors.ErrUnsupported }

func copyQuarantine(_, _ *os.File) error { return nil }
