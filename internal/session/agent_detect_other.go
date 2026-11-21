//go:build !linux && !darwin

package session

func readForegroundPGID(int) (int, bool) { return 0, false }

func readProcessInfo(int) foregroundInfo { return foregroundInfo{} }

func foregroundGroup(int, int, int) func(yield func(foregroundInfo) bool) { return nil }
