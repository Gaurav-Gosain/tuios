//go:build !unix

package session

// ForegroundGroup is 0 on a platform without process groups.
func ForegroundGroup(int) int { return 0 }

// ProcessGroupAlive reports a group alive on a platform without process
// groups, where a registration is not bound to one: it lasts until the
// program takes it back or its pane closes.
func ProcessGroupAlive(int) bool { return true }
