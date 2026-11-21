package herdrplugin

import (
	"os/exec"
	"runtime"
)

func withExtension(path string) (string, bool) {
	if runtime.GOOS != "windows" {
		return "", false
	}
	found, err := exec.LookPath(path)
	if err != nil {
		return "", false
	}
	return found, true
}
