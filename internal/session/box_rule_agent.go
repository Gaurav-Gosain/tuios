package session

// boxRuleHarness is the harness whose input box rule reads pane target's
// screen: the pane's harness while its agent runs there, and "" once the agent
// has left and the pane's own shell holds the terminal again. A Codex that
// quit leaves the shell's prompt on screen, and Codex's box rule read that
// prompt as a draft the person had typed, so the queue held every message to
// the pane. With "" the pane is read by the shell rule, as any plain pane is.
//
// The shell holds the terminal when its process group is the foreground
// group. A pane started on the agent itself has no shell: its root process is
// the agent, so the root process is checked by name too. A pane on another
// machine, or a platform whose kernel does not say, keeps the harness, as it
// did before.
func (d *Daemon) boxRuleHarness(sess *Session, target WindowState, pty *PTY) string {
	hid := firstNonEmpty(target.AgentHarness, sess.agentClaimFor(target.ID).harness)
	if hid == "" || pty == nil || pty.IsExited() {
		return hid
	}
	if _, _, remote := pty.remoteForeground(); remote {
		return hid
	}
	pid := pty.ShellPID()
	if pid <= 0 {
		return hid
	}
	if pgid, ok := readForegroundPGID(pid); ok && pgid == pid && !d.agentMatcher.isAgent(readProcessInfo(pid)) {
		return ""
	}
	return hid
}
