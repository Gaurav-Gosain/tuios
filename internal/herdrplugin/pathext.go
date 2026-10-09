package herdrplugin

import (
	"os/exec"
	"runtime"
)

// withExtension finds the program Windows runs for a path named without its
// extension, such as bin/herdr-nvim for bin/herdr-nvim.exe. It asks
// exec.LookPath, which on Windows tries each PATHEXT extension on a path
// with a separator, the same lookup exec.Command makes when it runs the
// program. ok is false elsewhere, and when no file matches; the caller then
// checks the path as written.
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
