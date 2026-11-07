package session

import "os"

// fdNum is f's descriptor number as an int, the form the handoff passes on
// the command line and the unix calls take. A descriptor is a small
// non-negative number, so it always fits an int.
func fdNum(f *os.File) int {
	return int(f.Fd()) //nolint:gosec // a descriptor number fits an int
}
