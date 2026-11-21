package integration

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

const maxAncestors = 32

// SelfProcess reports what the current process can say about where it runs:
// its session id, which is the pane shell's pid when its controlling terminal
// is a tuios pane, and its ancestors, nearest first, for when it is not. The
// daemon's resolve-pane verb matches either against the shells it started.
// A platform with no way to read them reports zero and nil.
func SelfProcess() (sid int, ancestors []int) {
	return selfSID(), ancestorsFrom(parentPID(0))
}

// ProcessOf is SelfProcess for another process, such as a daemon's view of
// the process at the other end of its socket: its session id and its
// ancestors, nearest first.
func ProcessOf(pid int) (sid int, ancestors []int) {
	if pid <= 1 {
		return 0, nil
	}
	sid, _ = procinfo.SessionID(pid)
	return sid, ancestorsFrom(parentPID(pid))
}

// ProcessAt is ProcessOf for a hook that runs inside the harness: a plugin
// that reports over the daemon's socket. The harness is pid itself, so the
// ancestors start at pid, and HarnessPID names it, as it names the parent of
// a tuios agent-hook process. Starting at the parent named the process above
// the pane's shell instead (#2160).
func ProcessAt(pid int) (sid int, ancestors []int) {
	if pid <= 1 {
		return 0, nil
	}
	sid, _ = procinfo.SessionID(pid)
	return sid, ancestorsFrom(pid)
}

func ancestorsFrom(pid int) []int {
	var ancestors []int
	seen := map[int]bool{}
	for ; pid > 1 && len(ancestors) < maxAncestors && !seen[pid]; pid = parentPID(pid) {
		seen[pid] = true
		ancestors = append(ancestors, pid)
	}
	return ancestors
}

func parentPID(pid int) int {
	if pid == 0 {
		return os.Getppid()
	}
	ppid, _ := procinfo.ParentPID(pid)
	return ppid
}

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
