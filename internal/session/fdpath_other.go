//go:build !linux && !darwin && !windows

package session

import "os"

// fdPath cannot name an open file here. The read check then rests on the
// path it judged before the open.
func fdPath(*os.File) (string, bool) { return "", false }
