package procinfo

import (
	"bytes"
	"encoding/binary"
)

// EnvironVar finds name in a NUL-separated environment block, the layout of
// /proc/<pid>/environ and of the tail of darwin's kern.procargs2. It returns
// the value and whether the variable was present.
func EnvironVar(block []byte, name string) (string, bool) {
	prefix := []byte(name + "=")
	for entry := range bytes.SplitSeq(block, []byte{0}) {
		if v, ok := bytes.CutPrefix(entry, prefix); ok {
			return string(v), true
		}
	}
	return "", false
}

// ProcargsEnvVar reads one environment variable out of a kern.procargs2
// buffer: int32 argc, the executable path, NUL padding, argc arguments, then
// the environment, each NUL-terminated. It is kept apart from the sysctl so
// the layout can be tested on every platform.
func ProcargsEnvVar(buf []byte, name string) (string, bool) {
	if len(buf) < 4 {
		return "", false
	}
	argc := int(int32(binary.LittleEndian.Uint32(buf[:4])))
	rest := buf[4:]
	end := bytes.IndexByte(rest, 0)
	if end < 0 || argc < 0 {
		return "", false
	}
	rest = bytes.TrimLeft(rest[end:], "\x00")
	for range argc {
		end := bytes.IndexByte(rest, 0)
		if end < 0 {
			return "", false
		}
		rest = rest[end+1:]
	}
	// The environment ends at the first empty string. What follows is the
	// kernel's own apple strings and padding, which are not the environment.
	if stop := bytes.Index(rest, []byte{0, 0}); stop >= 0 {
		rest = rest[:stop]
	}
	return EnvironVar(rest, name)
}

// Env is the environment a process was started with, read once, so several
// variables cost one read.
type Env struct {
	block    []byte
	procargs bool
}

// Get returns one variable and whether it was present.
func (e Env) Get(name string) (string, bool) {
	if e.procargs {
		return ProcargsEnvVar(e.block, name)
	}
	return EnvironVar(e.block, name)
}
