package session

import "github.com/Gaurav-Gosain/tuios/internal/tape"

// Keymap actions the daemon runs with a TUI attached (parity gap 1, step 4,
// batch A; issue #162).
//
// Step 1 gave the daemon a form of these actions for a session with no TUI.
// With a TUI attached, run-command still routed them to it: the TUI changed
// its own copy and pushed it. So the daemon owned the focus, a pane's
// workspace and whether it is minimized for one caller and the push owned
// them for another, and two clients that changed them at once each pushed a
// state built on the one it read. Here the daemon runs them whoever asks:
// run-command, guios, and the TUI's own key, which sends the action as an
// intent (TUIClient.DaemonActions).
//
// A client that predates this runs the action in its own state when its key
// is pressed, and expects a routed one to reach it. While one is attached the
// daemon routes these as before, so the two never act on one field by two
// paths.

// daemonActionNames are the keymap actions of batch A, the ones ending in a
// digit by their stem. last_pane is not one: the TUI goes back to the pane
// it focused before on any workspace, from its own record, and the daemon's
// headless form only within the current workspace. Until the two agree it
// stays the TUI's, as last_workspace does.
var daemonActionNames = map[string]bool{
	"break_pane":           true,
	"close_workspace":      true,
	"move_workspace_left":  true,
	"move_workspace_right": true,
	"minimize_window":      true,
	"restore_all":          true,
}

// daemonActionStems are the batch A actions named with a workspace or a
// window number from 1 to 9.
var daemonActionStems = []string{"select_window_", "send_to_workspace_", "move_and_follow_"}

// IsDaemonAction reports whether the keymap action name is one the daemon
// runs itself with a TUI attached, when the daemon says so in its welcome.
func IsDaemonAction(name string) bool {
	if daemonActionNames[name] {
		return true
	}
	for _, stem := range daemonActionStems {
		if _, ok := actionDigit(name, stem); ok {
			return true
		}
	}
	return false
}

// runsAction reports whether the daemon runs the command itself on sess even
// with a TUI attached: a batch A action, with every attached TUI one that
// sends those actions as intents. The tape's MinimizeWindow and
// RestoreWindow count with them.
func (d *Daemon) runsAction(sess *Session, command string, args []string) bool {
	switch command {
	case string(tape.CommandTypeAction):
		if len(args) != 1 || !IsDaemonAction(args[0]) {
			return false
		}
	case string(tape.CommandTypeMinimizeWindow), string(tape.CommandTypeRestoreWindow):
		// minimize_window resolves to the tape's MinimizeWindow, which the
		// daemon runs with no TUI already. RestoreWindow is its pair.
	default:
		return false
	}
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()
	for _, cs := range d.clients {
		cs.mu.Lock()
		old := cs.sessionID == sess.ID && cs.isTUIClient && cs.attached && !cs.daemonActions
		cs.mu.Unlock()
		if old {
			return false
		}
	}
	return true
}

// DaemonActions reports whether the daemon runs the actions IsDaemonAction
// names. When it does, the client sends its key for one as an intent and
// takes the result from the state push.
func (c *TUIClient) DaemonActions() bool {
	return c != nil && c.daemonActions.Load()
}
