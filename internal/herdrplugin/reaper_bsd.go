//go:build unix && !linux && !darwin

package herdrplugin

const waitsForGroup = false

func becomeSubreaper() {}
func waitForGroup()    {}

func waitExited(int) {}
