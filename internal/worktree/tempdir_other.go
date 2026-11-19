//go:build !unix

package worktree

// processAlive cannot tell here, so it answers that the process runs, and
// SweepTemp removes only this process's own folders.
func processAlive(int) bool { return true }

// ownedByMe is true: the temp dir here is the user's own.
func ownedByMe(string) bool { return true }
