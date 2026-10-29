//go:build !unix && !windows

package herdrplugin

import "os"

// tryLock always succeeds where there is no file lock: the browser build
// installs nothing.
func tryLock(*os.File) (bool, error) { return true, nil }

// lockShared does nothing where there is no file lock.
func lockShared(*os.File) error { return nil }

// lockExclusive does nothing where there is no file lock.
func lockExclusive(*os.File) error { return nil }
