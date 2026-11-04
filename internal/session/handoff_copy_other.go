//go:build !darwin

package session

import (
	"errors"
	"os"
)

// cloneFrom clones nothing: only darwin's APFS clones are used.
func cloneFrom(*os.File, string) error { return errors.ErrUnsupported }

// copyQuarantine does nothing: only darwin has a quarantine mark.
func copyQuarantine(_, _ *os.File) error { return nil }
