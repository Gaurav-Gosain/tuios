package session

import (
	"os"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// harnessDaemons are the processes a harness runs its background sessions
// under, away from the pane that asked for them. Each is a program name and
// an argument it is started with, so the same program running in a pane is
// not taken for one. The name is matched against the process's name, its
// argv[0] and its executable, each reduced by daemonName. See
// harnessOutsidePane.
var harnessDaemons = []struct{ name, arg string }{
	// Claude Code's daemon, `claude daemon run`, and the pty host it starts
	// each background session on, `claude --bg-pty-host`.
	{"claude", "daemon"},
	{"claude", "--bg-pty-host"},
}

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
// Plenty of programs leave the pane and keep its environment for what they
// run: tmux, screen and zellij servers, the conmon helper of a rootless
// container (distrobox, toolbox, podman, docker), systemd-run --pty. A harness
// under one of them belongs to the pane that shows it, so the check does not
// ask whether the harness left the pane, but whether a harness daemon took it
// out.
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
//   - an ancestor of the harness outside the pane is a harness daemon
//     (harnessDaemons).
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
	return harnessDaemonAbove(harnessPID, root)
}

// harnessLeftPane says whether a hook report about window target of sess
// comes from a process in that pane whose harness no longer runs there.
//
// A harness runs its hook as a child and does not wait for it. A hook started
// just before the harness quits reaches the daemon after it: the process is
// still in the pane, on its terminal, but the harness it named is no longer
// its ancestor. Or the hook started after the quit, was given to init or a
// subreaper, and named that as its harness, or nothing at all. Its report set
// the pane's state with a harness record that could never read as exited, so
// the agent row stayed until the pane closed.
//
// The harness is taken to run in the pane only when it is the caller or an
// ancestor of it (callerRunsHarness) and is itself in the pane. A caller
// outside the pane is left alone: an agent in a container, or under a tmux
// server, is not judged by this pane's processes. So is anything that cannot
// be read.
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
	if harnessPID > 1 && callerRunsHarness(cs, harnessPID) {
		if in, known := processInPane(harnessPID, root); !known || in {
			return false
		}
	}
	return true
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

// harnessDaemonAbove says whether an ancestor of pid is a harness daemon, one
// in harnessDaemons, before the walk reaches a process in the pane whose
// process is root. It is false when a process record on the way cannot be
// read: only a positive reading refuses a report.
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

// isHarnessDaemon says whether a process is one of harnessDaemons.
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

// daemonName reduces a process name or path to the name a program is known
// by. A process that retitles itself keeps the name as the first word, as
// Claude Code's pty host shows as "claude bg-pty-host", and Nix runs a wrapped
// program as .name-wrapped.
func daemonName(s string) string {
	name, _, _ := strings.Cut(harness.BaseName(s), " ")
	return strings.TrimSuffix(strings.TrimPrefix(name, "."), "-wrapped")
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
