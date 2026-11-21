package session

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
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

func readAgentHintEnv(pid int) (string, bool) { return procinfo.EnvVar(pid, AgentHintEnv) }

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
