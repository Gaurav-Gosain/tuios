package session

import (
	"os"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

var harnessDaemons = []struct{ name, arg string }{
	{"claude", "daemon"},
	{"claude", "--bg-pty-host"},
}

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
	if harnessPID == root || onPaneTerminal(harnessPID, root) {
		return false
	}
	return harnessDaemonAbove(harnessPID, root)
}

func onPaneTerminal(pid, root int) bool {
	_, tty, ok := readProcLineage(pid)
	if !ok || tty == 0 {
		return false
	}
	_, rootTTY, ok := readProcLineage(root)
	return ok && rootTTY == tty
}

func (d *Daemon) harnessLeftPane(cs *connState, sess *Session, target string, harnessPID int) bool {
	if cs == nil || cs.viaLink || cs.paneOnly || cs.peerPID <= 1 || cs.peerPID == os.Getpid() {
		return false
	}
	_, root := sess.paneProcess(target)
	if root <= 1 {
		return false
	}
	if in, known := processInPane(cs.peerPID, root); !known || !in {
		return false
	}
	if parent, _, ok := readProcLineage(cs.peerPID); ok && parent == root {
		return false
	}
	if harnessPID > 1 && callerRunsHarness(cs, harnessPID) {
		if in, known := processInPane(harnessPID, root); !known || in {
			return false
		}
	}
	return true
}

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

func harnessDaemonAbove(pid, root int) bool {
	ppid, _, ok := readProcLineage(pid)
	if !ok {
		return false
	}
	_, rootTTY, rootOK := readProcLineage(root)
	for depth := 0; depth < paneOriginMaxDepth && ppid > 1; depth++ {
		next, tty, ok := readProcLineage(ppid)
		if !ok || ppid == root || (rootOK && tty != 0 && tty == rootTTY) {
			return false
		}
		if isHarnessDaemon(readProcessInfo(ppid)) {
			return true
		}
		ppid = next
	}
	return false
}

func isHarnessDaemon(info foregroundInfo) bool {
	if len(info.argv) < 2 {
		return false
	}
	names := []string{daemonName(info.comm), daemonName(info.argv[0]), daemonName(info.exe)}
	for _, d := range harnessDaemons {
		if slices.Contains(names, d.name) && slices.Contains(info.argv[1:], d.arg) {
			return true
		}
	}
	return false
}

func daemonName(s string) string {
	name, _, _ := strings.Cut(harness.BaseName(s), " ")
	return strings.TrimSuffix(strings.TrimPrefix(name, "."), "-wrapped")
}

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
