package integration

import (
	"path/filepath"
	"strings"
)

// maxAncestors bounds the parent walk. A hook is rarely more than a handful of
// processes below the pane's shell; the bound only matters for a cycle a racy
// read could make.
const maxAncestors = 32

// SelfProcess reports what the current process can say about where it runs:
// its session id, which is the pane shell's pid when its controlling terminal
// is a tuios pane, and its ancestors, nearest first, for when it is not. The
// daemon's resolve-pane verb matches either against the shells it started.
// A platform with no way to read them reports zero and nil.
func SelfProcess() (sid int, ancestors []int) {
	sid = selfSID()
	seen := map[int]bool{}
	for pid := parentPID(0); pid > 1 && len(ancestors) < maxAncestors && !seen[pid]; pid = parentPID(pid) {
		seen[pid] = true
		ancestors = append(ancestors, pid)
	}
	return sid, ancestors
}

// launcherNames are the programs a harness may start a hook command through:
// the shell that runs the command line, and env. None of them is the harness.
var launcherNames = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "mksh": true,
	"fish": true, "tcsh": true, "csh": true, "ash": true, "busybox": true, "env": true,
	"cmd.exe": true, "powershell.exe": true, "pwsh": true, "pwsh.exe": true,
}

// HarnessPID names the harness process that ran this hook: the nearest
// ancestor that is not a shell or env. Claude Code and Gemini CLI run a hook
// command through a shell, which may or may not exec it, so the parent alone
// is sometimes the harness and sometimes a shell. ancestors is nearest first,
// as SelfProcess reports it. An ancestor whose name cannot be read is taken as
// the harness, and no ancestors gives 0, which the daemon reads as unstated.
//
// The daemon uses it to tell a new conversation in the same harness process
// from a nested run: a claude -p a tool call started has its own pid.
func HarnessPID(ancestors []int) int {
	return harnessPID(ancestors, processName)
}

func harnessPID(ancestors []int, name func(int) string) int {
	for _, pid := range ancestors {
		if pid <= 1 {
			return 0
		}
		n := strings.TrimPrefix(strings.ToLower(filepath.Base(name(pid))), "-")
		if n == "" || !launcherNames[n] {
			return pid
		}
	}
	return 0
}
