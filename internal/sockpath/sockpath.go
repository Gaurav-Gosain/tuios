// Package sockpath names the folder the daemon keeps its socket in, and the
// longest path a Unix socket may have. The daemon and the link layer both
// need these, and they must agree on them.
package sockpath

import "runtime"

// MaxLen is the longest Unix socket path the platform takes, without the
// terminating zero: sun_path is 108 bytes on Linux and 104 on macOS and the
// BSDs.
func MaxLen() int {
	if runtime.GOOS == "linux" {
		return 107
	}
	return 103
}
