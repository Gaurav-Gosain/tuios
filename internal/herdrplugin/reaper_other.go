//go:build !unix

package herdrplugin

// RunReaper is not used on Windows, where no caller is placed by its
// session and no reaper is set.
func RunReaper([]string) int { return 127 }

// waitExited returns at once, so the runner drops a command's owner entry
// as soon as it starts. That is safe only because kernelPlacesCallers is
// false here: Start refuses native plugins, and procinfo.PeerPID is 0, so
// the daemon never asks Owner about a caller. Give this a real wait before
// kernelPlacesCallers names this platform.
func waitExited(int) {}
