//go:build unix && !js

package session

import (
	"syscall"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

func stopOwnedPaneProcesses(sess *Session, callerPID int) []ownedPaneProcess {
	paneIDs := make(map[string]struct{})
	for _, window := range sess.GetState().Windows {
		if window.Host == "" && window.ID != "" {
			paneIDs[window.ID] = struct{}{}
		}
	}
	if len(paneIDs) == 0 {
		return nil
	}
	if callerPID > 1 {
		if paneID, ok := procinfo.EnvVar(callerPID, "TUIOS_PANE_ID"); ok {
			if _, owned := paneIDs[paneID]; owned {
				return []ownedPaneProcess{{pid: callerPID, paneID: paneID, detail: "close-out must be initiated outside the worker pane"}}
			}
		}
	}

	deadline := time.Now().Add(time.Second)
	for {
		processes, ok := ownedPaneProcesses(paneIDs)
		if !ok {
			if !hasAgentPane(sess) {
				return nil
			}
			return []ownedPaneProcess{{detail: "the process table could not be read"}}
		}
		if len(processes) == 0 {
			return nil
		}
		for _, process := range processes {
			if process.start == 0 || !stillOwnedPaneProcess(process) {
				continue
			}
			_ = syscall.Kill(process.pid, syscall.SIGTERM)
		}
		survivors := make([]ownedPaneProcess, 0, len(processes))
		for time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
			survivors = survivors[:0]
			for _, process := range processes {
				if stillOwnedPaneProcess(process) {
					survivors = append(survivors, process)
				}
			}
			if len(survivors) == 0 {
				break
			}
		}
		if len(survivors) > 0 {
			return survivors
		}
	}
}

func ownedPaneProcesses(paneIDs map[string]struct{}) ([]ownedPaneProcess, bool) {
	pids, ok := procinfo.ProcessIDs()
	if !ok {
		return nil, false
	}
	var out []ownedPaneProcess
	for _, pid := range pids {
		paneID, ok := procinfo.EnvVar(pid, "TUIOS_PANE_ID")
		if !ok {
			continue
		}
		if _, owned := paneIDs[paneID]; !owned {
			continue
		}
		start, _ := procinfo.StartTime(pid)
		out = append(out, ownedPaneProcess{pid: pid, paneID: paneID, start: start})
	}
	return out, true
}

func stillOwnedPaneProcess(process ownedPaneProcess) bool {
	if process.start == 0 {
		return true
	}
	start, ok := procinfo.StartTime(process.pid)
	if !ok || start != process.start {
		return false
	}
	paneID, ok := procinfo.EnvVar(process.pid, "TUIOS_PANE_ID")
	return ok && paneID == process.paneID
}
