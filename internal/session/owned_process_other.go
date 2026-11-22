//go:build !unix || js

package session

import "runtime"

func stopOwnedPaneProcesses(sess *Session, _ int) []ownedPaneProcess {
	if runtime.GOOS == "js" {
		return nil
	}
	for _, window := range sess.GetState().Windows {
		if isAgentWindow(window) || window.AgentSessionID != "" {
			return []ownedPaneProcess{{paneID: window.ID, detail: "worker-owned processes cannot be verified for pane " + window.ID + " on this platform"}}
		}
	}
	return nil
}
