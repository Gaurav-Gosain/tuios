package session

import "os"

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
	if id, ok := readEnvVarOf(harnessPID, "TUIOS_PANE_ID"); !ok || id != windowID {
		return false
	}
	in, known := processInPane(harnessPID, root)
	return known && !in
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
