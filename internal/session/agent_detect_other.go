//go:build !linux && !darwin

package session

// Platforms with neither procfs nor the darwin sysctls report no foreground
// process at all. That is the honest answer: with no way to see what a pane is
// running, the detector must say nothing rather than assume the shell is idle or
// that anything is an agent. Everything above this layer already treats
// not-running as "auto-detection has no opinion", so a pane on such a platform
// keeps whatever state a harness reports for itself or the user sets by hand.

func readForegroundPGID(int) (int, bool) { return 0, false }

func readProcessInfo(int) foregroundInfo { return foregroundInfo{} }
