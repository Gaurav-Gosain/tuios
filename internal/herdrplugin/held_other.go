//go:build !unix && !windows

package herdrplugin

import "os"

func tryLock(*os.File) (bool, error) { return true, nil }

func lockShared(*os.File) error { return nil }

func lockExclusive(*os.File) error { return nil }
