//go:build !unix

package vt

import "os"

// kittyOwnedByMe has no owner to compare off unix. The objects it guards are
// in /dev/shm and temporary directories, which other users do not share there.
func kittyOwnedByMe(os.FileInfo) bool { return true }
