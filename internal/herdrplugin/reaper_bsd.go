//go:build unix && !linux && !darwin

package herdrplugin

// The runner does not start native plugins here, and no reaper is set.
const waitsForGroup = false

func becomeSubreaper() {}
func waitForGroup()    {}
func waitExited(int)   {}
