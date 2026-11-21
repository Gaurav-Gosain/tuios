//go:build freebsd || netbsd || openbsd || dragonfly

package dirwatch

import "golang.org/x/sys/unix"

const openMode = unix.O_RDONLY
