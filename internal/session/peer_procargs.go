//go:build darwin || !slim

package session

import (
	"bytes"
	"encoding/binary"
)

// procargsEnvVar reads one environment variable out of a kern.procargs2
// buffer: int32 argc, the executable path, NUL padding, argc arguments, then
// the environment, each NUL-terminated. It is kept apart from the sysctl so
// the layout can be tested on every platform.
func procargsEnvVar(buf []byte, name string) (string, bool) {
	if len(buf) < 4 {
		return "", false
	}
	argc := int(int32(binary.LittleEndian.Uint32(buf[:4])))
	rest := buf[4:]
	end := bytes.IndexByte(rest, 0)
	if end < 0 || argc < 0 {
		return "", false
	}
	rest = rest[end:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
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
	return environVar(rest, name)
}
