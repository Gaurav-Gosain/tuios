package session

import (
	"os"
	"path/filepath"
	"slices"
)

// multiplexerServers are the executable names of the terminal multiplexers
// whose server leaves the pane it was started from: it runs in a session of its
// own, on terminals of its own, under init, while the programs in its panes
// keep the environment of the pane that started it, TUIOS_PANE_ID included. A
// harness whose ancestry reaches one of them is taken as running in the pane
// that shows the multiplexer's client. See harnessOutsidePane.
var multiplexerServers = []string{"tmux", "screen", "zellij", "dtach", "abduco"}

// harnessOutsidePane says whether a hook report about window target of sess
// comes from a harness that does not run in that pane, and so must not be
// attributed to it.
//
// A harness finds its pane through TUIOS_PANE_ID, and every process it starts
// inherits the variable, including one that leaves the pane. Claude Code's
// background sessions do: its own daemon starts them on its own terminal with
// the environment of the pane that asked for them. Their hooks named a pane
// that held only a shell, and the rail showed an agent there that the pane
// did not run.
//
// A report is refused when all of these hold:
//
//   - it names the harness process (harness_pid), and came from this machine.
//     A call forwarded from a hosted pane drops the pid, and a call over a
//     link names a process of another machine.
//   - the pane runs here and its process is alive.
//   - the harness's own environment names this pane in TUIOS_PANE_ID, so the
//     pane was inherited, not chosen. agent-hook --window run from outside the
//     pane is a choice, and it keeps working.
//   - the harness is not the pane's process, does not share its terminal and
//     does not descend from it.
//   - no ancestor of the harness is a multiplexer server (multiplexerServers).
//     A harness in tmux inside the pane has left the pane's process tree the
//     same way a background session has, and its reports belong on the pane
//     that shows the tmux client.
//
// The caller's pid, which the kernel gives, checks the harness_pid it claims:
// a hook runs under its harness, so the harness is the caller or one of its
// ancestors. A claim that is neither is not believed, and the caller is tested
// in its place.
//
// What cannot be read leaves the report alone: a harness that has exited, as
// one may by the time its SessionEnd arrives, a platform with no process
// reader, or another user's process. Only a positive reading refuses.
func (d *Daemon) harnessOutsidePane(cs *connState, sess *Session, target string, harnessPID int) bool {
	if harnessPID <= 1 || harnessPID == os.Getpid() {
		return false
	}
	if cs != nil && (cs.viaLink || cs.paneOnly) {
		return false
	}
	windowID, root := sess.paneProcess(target)
	if root <= 1 {
		return false
	}
	if cs != nil && cs.peerPID > 1 && !selfOrAncestor(harnessPID, cs.peerPID) {
		harnessPID = cs.peerPID
	}
	if id, ok := readEnvVarOf(harnessPID, "TUIOS_PANE_ID"); !ok || id != windowID {
		return false
	}
	if in, known := processInPane(harnessPID, root); !known || in {
		return false
	}
	reached, known := ancestryReachesMultiplexer(harnessPID)
	return known && !reached
}

// paneProcess returns the id of the window target names and the pid of the
// process its pane runs. The pid is 0 for a pane on another machine, one whose
// process has exited, and a target that names no window.
func (s *Session) paneProcess(target string) (string, int) {
	s.stateMu.RLock()
	idx, err := findWindowStateIndex(s.state.Windows, target)
	if err != nil {
		s.stateMu.RUnlock()
		return "", 0
	}
	w := &s.state.Windows[idx]
	id, host, ptyID := w.ID, w.Host, w.PTYID
	s.stateMu.RUnlock()
	if host != "" || ptyID == "" {
		return id, 0
	}
	pty := s.GetPTY(ptyID)
	if pty == nil || pty.IsExited() {
		return id, 0
	}
	return id, pty.ShellPID()
}

// selfOrAncestor says whether pid is child or one of child's ancestors. A
// process record on the way that cannot be read answers true: the claim
// stands when it cannot be checked.
func selfOrAncestor(pid, child int) bool {
	for depth := 0; depth <= paneOriginMaxDepth && child > 1; depth++ {
		if child == pid {
			return true
		}
		next, _, ok := readProcLineage(child)
		if !ok {
			return true
		}
		child = next
	}
	return false
}

// ancestryReachesMultiplexer says whether an ancestor of pid is a
// multiplexer server, by the name of its executable. known is false when a
// process record on the way cannot be read.
func ancestryReachesMultiplexer(pid int) (reached, known bool) {
	ppid, _, ok := readProcLineage(pid)
	if !ok {
		return false, false
	}
	for depth := 0; depth < paneOriginMaxDepth && ppid > 1; depth++ {
		if slices.Contains(multiplexerServers, executableName(ppid)) {
			return true, true
		}
		next, _, ok := readProcLineage(ppid)
		if !ok {
			return false, false
		}
		ppid = next
	}
	return false, true
}

// executableName is the base name of the file process pid executes, or of its
// process name when the executable cannot be read.
func executableName(pid int) string {
	info := readProcessInfo(pid)
	if info.exe != "" {
		return filepath.Base(info.exe)
	}
	return info.comm
}

// processInPane says whether process pid runs in the pane whose process is
// root: it is root, its controlling terminal is root's, or root is one of its
// ancestors. The terminal is tried first because it survives a reparented
// process, and the ancestry catches one that left the terminal's session, a
// sandbox that calls setsid. known is false when a process record on the way
// cannot be read, and in is then false too.
func processInPane(pid, root int) (in, known bool) {
	if pid == root {
		return true, true
	}
	ppid, tty, ok := readProcLineage(pid)
	if !ok {
		return false, false
	}
	if tty != 0 {
		if _, rootTTY, ok := readProcLineage(root); ok && rootTTY == tty {
			return true, true
		}
	}
	for depth := 0; depth < paneOriginMaxDepth && ppid > 1; depth++ {
		if ppid == root {
			return true, true
		}
		next, _, ok := readProcLineage(ppid)
		if !ok {
			return false, false
		}
		ppid = next
	}
	return false, true
}
