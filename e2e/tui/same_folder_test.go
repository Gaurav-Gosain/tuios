package tuie2e

import "path/filepath"

// sameFolder reports whether a and b name one folder, compared with their
// symbolic links resolved. The kernel gives a process's folder resolved, and
// on macOS a test's temporary folder is under /var, a link to /private/var,
// so a folder the test made and the folder a pane reports for it are spelled
// differently there and the same on Linux.
func sameFolder(a, b string) bool {
	if a == b {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}
