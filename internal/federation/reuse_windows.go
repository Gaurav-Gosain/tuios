//go:build windows

package federation

// SharedMasterDir is "" on Windows: Win32-OpenSSH has no ControlMaster, so
// there is no master to share.
func SharedMasterDir() string { return "" }

// reuseOptions is nil on Windows. See SharedMasterDir.
func reuseOptions(Host) []string { return nil }
