//go:build unix && !js

package session

import (
	"os"
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

func killPaneTree(proc *os.Process) {
	if proc == nil {
		return
	}
	for _, pgid := range paneGroups(proc.Pid) {
		_ = syscall.Kill(-pgid, syscall.SIGHUP)
		_ = syscall.Kill(-pgid, syscall.SIGCONT)
	}
	_ = proc.Kill()
}

func paneGroups(leader int) []int {
	if leader <= 1 {
		return nil
	}
	if sid, ok := procinfo.SessionID(leader); !ok || sid != leader {
		return nil
	}
	own := syscall.Getpgrp()
	seen := map[int]bool{}
	var out []int
	for _, pid := range append([]int{leader}, procinfo.SessionMembers(leader)...) {
		pgid, err := syscall.Getpgid(pid)
		if err != nil || pgid <= 1 || pgid == own || seen[pgid] {
			continue
		}
		seen[pgid] = true
		out = append(out, pgid)
	}
	return out
}
