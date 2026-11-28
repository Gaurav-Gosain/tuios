package session

import (
	"fmt"
	"strings"
)

type ownedPaneProcess struct {
	pid    int
	paneID string
	start  uint64
	detail string
}

func hasAgentPane(sess *Session) bool {
	for _, window := range sess.GetState().Windows {
		if isAgentWindow(window) || window.AgentSessionID != "" {
			return true
		}
	}
	return false
}

func ownedPaneProcessDetail(session string, processes []ownedPaneProcess) string {
	if len(processes) == 1 && processes[0].detail != "" {
		return fmt.Sprintf("Worktree session %s was not removed: %s.", session, processes[0].detail)
	}
	items := make([]string, 0, len(processes))
	for _, process := range processes {
		if process.pid > 0 {
			items = append(items, fmt.Sprintf("PID %d (pane %s)", process.pid, process.paneID))
		} else {
			items = append(items, process.detail)
		}
	}
	return fmt.Sprintf("Worktree session %s still owns processes: %s. Stop them and retry; the worktree was not removed.", session, strings.Join(items, ", "))
}
