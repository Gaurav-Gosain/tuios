package session

import (
	"bytes"
)

// environVar finds name in a NUL-separated environment block, the layout of
// /proc/<pid>/environ and of the tail of darwin's kern.procargs2. It returns
// the value and whether the variable was present.
func environVar(block []byte, name string) (string, bool) {
	prefix := []byte(name + "=")
	for entry := range bytes.SplitSeq(block, []byte{0}) {
		if v, ok := bytes.CutPrefix(entry, prefix); ok {
			return string(v), true
		}
	}
	return "", false
}
