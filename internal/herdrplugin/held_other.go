//go:build !unix && !windows

package herdrplugin

import "os"

// tryLock always succeeds where there is no file lock: the browser build
// installs nothing.
func tryLock(*os.File) (bool, error) { return true, nil }

// processAlive reports every process as running, so nothing is removed.
func processAlive(int) bool { return true }
