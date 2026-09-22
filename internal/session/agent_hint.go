package session

import (
	"bytes"
	"encoding/binary"
	"strings"
)

// AgentHintEnv is the environment variable a wrapper sets to name the harness
// it runs. It is for the agents process detection cannot see: one in a
// container, in a VM, over ssh, or under a build tool such as go run. Nothing
// in the pane's process tree is the agent there, so the one who started the
// wrapper says what it is:
//
//	TUIOS_AGENT=claude-code docker run -it sandbox claude
//
// Only the foreground process group leader's own environment is read, and only
// this one variable of it. A value that names no manifest is ignored.
const AgentHintEnv = "TUIOS_AGENT"

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

// agentHint returns the harness a process's TUIOS_AGENT names, trimmed and
// lowercased, or "" when it names none.
func agentHint(read func(pid int) (string, bool), pid int) string {
	if read == nil || pid <= 1 {
		return ""
	}
	v, ok := read(pid)
	if !ok {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(v))
}
