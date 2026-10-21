//go:build !unix

package herdrplugin

// RunReaper is not used on Windows, where no caller is placed by its
// session and no reaper is set.
func RunReaper([]string) int { return 127 }

func waitExited(int) {}
