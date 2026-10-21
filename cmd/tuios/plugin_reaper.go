package main

import (
	"os"
	"runtime"

	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
)

// pluginReaper is the argv the daemon runs a plugin command under, or nil
// where no native plugin runs. On Linux it names /proc/self/exe, which
// still runs this build after an update replaced the file.
func pluginReaper() []string {
	switch runtime.GOOS {
	case "linux":
		return []string{"/proc/self/exe", herdrplugin.ReaperArg}
	case "darwin":
		if exe, err := os.Executable(); err == nil {
			return []string{exe, herdrplugin.ReaperArg}
		}
	}
	return nil
}
