//go:build freebsd || netbsd || openbsd || dragonfly

package dirwatch

import "golang.org/x/sys/unix"

// openMode is a plain read-only open. These systems have no O_EVTONLY.
const openMode = unix.O_RDONLY
