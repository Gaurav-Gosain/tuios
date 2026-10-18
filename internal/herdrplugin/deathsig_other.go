//go:build !linux

package herdrplugin

import "os/exec"

// deathSignal does nothing here. An orphaned build writes only into a build
// folder that the lock file does not name, which the next install removes.
func deathSignal(*exec.Cmd) {}
