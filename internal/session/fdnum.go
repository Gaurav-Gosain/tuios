package session

import "os"

func fdNum(f *os.File) int {
	return int(f.Fd()) //nolint:gosec // a descriptor number fits an int
}
