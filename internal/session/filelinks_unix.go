//go:build unix

package session

import (
	"os"
	"syscall"
)

func fileLinks(f *os.File) (uint64, error) {
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 1, nil
	}
	return uint64(st.Nlink), nil //nolint:unconvert // Nlink is uint16, uint32 or uint64 by system
}
