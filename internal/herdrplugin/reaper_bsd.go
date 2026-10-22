//go:build unix && !linux && !darwin

package herdrplugin

// The runner does not start native plugins here, and no reaper is set.
const waitsForGroup = false

func becomeSubreaper() {}
func waitForGroup()    {}

// waitExited returns at once, so the runner drops a command's owner entry
// as soon as it starts. That is safe only because kernelPlacesCallers is
// false here: Start refuses native plugins, and procinfo.PeerPID is 0, so
// the daemon never asks Owner about a caller. Give this a real wait before
// kernelPlacesCallers names this platform.
func waitExited(int) {}
