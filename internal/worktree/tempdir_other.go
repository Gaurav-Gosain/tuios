//go:build !unix

package worktree

func processAlive(int) bool { return true }

func ownedByMe(string) bool { return true }
