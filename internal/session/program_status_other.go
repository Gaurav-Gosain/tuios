//go:build !unix

package session

// processGroupAlive cannot be answered on a platform without process
// groups, where no report is ever given a group, so a group is never ended.
func processGroupAlive(int) bool { return true }
