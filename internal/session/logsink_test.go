package session

import (
	"testing"
)

// restoreLevel sets the debug level for one test and puts the old one back.
func restoreLevel(t *testing.T, level DebugLevel) {
	t.Helper()
	previous := GetDebugLevel()
	SetDebugLevel(level)
	t.Cleanup(func() { SetDebugLevel(previous) })
}
