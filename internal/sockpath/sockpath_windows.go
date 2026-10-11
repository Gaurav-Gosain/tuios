//go:build windows

package sockpath

// RuntimeDir is "" on Windows: the daemon's socket lives under LOCALAPPDATA,
// and nothing there uses a runtime folder.
func RuntimeDir() string { return "" }

// SetRootsForTest does nothing on Windows, which has neither root.
func SetRootsForTest(runUser, tmp string) (restore func()) { return func() {} }
