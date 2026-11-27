package session

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
)

// What holds a pane's terminal, as get-window and list-windows give it in
// foreground_state.
//
// WindowState.ForegroundCmd cannot answer this. It is empty at a shell prompt,
// and it is also empty for a pane the detector has not read yet: a restored
// pane before its first poll, and a pane with no agent between two polls (see
// PTY.detectScanDue). A script that reuses or closes an idle pane has to tell
// the two apart, so the state is read from the kernel when it is asked for,
// and a read that fails says so.
const (
	// foregroundShell: the pane's own shell holds its terminal and has no
	// other process in its group. That is a pane at its prompt. A job the
	// shell runs in the background has its own group and is not counted.
	foregroundShell = "shell"
	// foregroundProgramState: another process group holds the terminal, the
	// pane's shell has a process in its own group (a script's commands), or
	// the process the pane started is not a shell.
	foregroundProgramState = "program"
	// foregroundUnknown: the kernel did not say. The pane has no process on
	// this machine, the process has exited, or the platform does not tell
	// which group holds a terminal. A caller must not read it as idle.
	foregroundUnknown = "unknown"
)

// paneForeground is one read of a pane's foreground process group.
type paneForeground struct {
	shellPID int
	// pgid is the foreground process group of the pane's terminal, 0 when
	// it could not be read.
	pgid  int
	state string
	// procs are the group's leader and the members behind it, in the shape
	// the herdr API's pane.process_info gives them.
	procs []herdrProcessItem
}

// readPaneForeground reads which process group holds the pane's terminal and
// the processes in it. shell is the base name of the session's shell. With
// full false each process has its pid and name only (see seesArguments).
func readPaneForeground(pty *PTY, shell string, full bool) paneForeground {
	fg := paneForeground{state: foregroundUnknown}
	if pty == nil {
		return fg
	}
	fg.shellPID = pty.ShellPID()
	// An exited pane keeps its pid, and the kernel may have given that pid
	// to another process since.
	if fg.shellPID <= 0 || pty.IsExited() {
		return fg
	}
	pgid, ok := foregroundPGID(fg.shellPID)
	if !ok || pgid <= 0 {
		return fg
	}
	fg.pgid = pgid
	leader := readProcessInfo(pgid)
	leader.pid = pgid
	procs := []foregroundInfo{leader}
	if walk := foregroundGroup(pgid, agentGroupWalkLimit, agentGroupWalkDepth); walk != nil {
		for p := range walk {
			procs = append(procs, p)
		}
	}
	for _, p := range procs {
		if p.comm == "" && len(p.argv) == 0 {
			continue
		}
		item := herdrProcessItem{PID: p.pid, Name: p.comm}
		if item.Name == "" && len(p.argv) > 0 {
			item.Name = agentBaseName(p.argv[0])
		}
		if full {
			item.Argv, item.Cmdline = p.argv, strings.Join(p.argv, " ")
			if len(p.argv) > 0 {
				item.Argv0 = p.argv[0]
			}
			item.Cwd, _ = ptyspawn.ProcessCwd(p.pid)
		}
		fg.procs = append(fg.procs, item)
	}
	switch {
	case leader.comm == "" && len(leader.argv) == 0:
		// The leader left between the two reads.
	case pgid != fg.shellPID, len(procs) > 1, foregroundCommand(leader, true, shell) != "":
		fg.state = foregroundProgramState
	default:
		fg.state = foregroundShell
	}
	return fg
}

// seesArguments reports whether the caller on cs gets the arguments and the
// directory of the processes in a pane of sess. A command line and a
// directory can carry what never shows on the screen: a token passed as an
// argument, a private path. The read grant shows the screen, so it gets the
// pids and names. The arguments and the directories go to the person, to the
// pane itself, and to a pane that may type into this one (write on its
// session, or admin).
func (d *Daemon) seesArguments(cs *connState, sess *Session, windowID string) bool {
	pa := d.paneAuthority(cs)
	if pa == nil || pa.window == windowID {
		return true
	}
	return (pa.grants.Has(GrantAdmin) || pa.grants.Has(GrantWrite)) && d.paneWriteReach(pa, sess.Name()) == ""
}

// addForeground adds the foreground read of a window's pane to the window's
// entry: foreground_state always, and the process group and its processes
// when the kernel gave them.
func (d *Daemon) addForeground(cs *connState, sess *Session, win *WindowState, entry map[string]any) {
	fg := readPaneForeground(sess.GetPTY(win.PTYID), agentBaseName(sess.getShell()), d.seesArguments(cs, sess, win.ID))
	entry["foreground_state"] = fg.state
	if fg.pgid > 0 {
		entry["foreground_process_group_id"] = fg.pgid
	}
	if len(fg.procs) > 0 {
		entry["foreground_processes"] = fg.procs
	}
}
