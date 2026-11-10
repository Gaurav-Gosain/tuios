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

// IsDirectionAction reports whether name is a batch B action: a focus move,
// a swap or a resize step toward a side (parity gap 1, step 4, batch B). The
// daemon runs one with a TUI attached only where it arranges the layout
// itself, a tiled workspace in the split layout (daemonArranges): in the
// scrolling and master-stack layouts a side means a column or the master,
// which the attached client works out.
func IsDirectionAction(name string) bool {
	if _, ok := directionOf(name, "focus_", "terminal_focus_", "swap_"); ok {
		return true
	}
	_, _, ok := resizeStepOf(name)
	return ok
}

// runsAction reports whether the daemon runs the command itself on sess even
// with a TUI attached: a batch A action, or a batch B one on a workspace
// the daemon arranges, with every attached TUI one that sends those actions
// as intents. The tape's MinimizeWindow and
// RestoreWindow count with them.
func (d *Daemon) runsAction(sess *Session, command string, args []string) bool {
	switch command {
	case string(tape.CommandTypeAction):
		if len(args) != 1 {
			return false
		}
		switch name := args[0]; {
		case isStepAction(name) || IsDirectionAction(name):
			if !d.daemonArranges(sess, sess.GetState().CurrentWorkspace) {
				return false
			}
		case IsLayoutAction(name), IsDaemonAction(name):
		default:
			return false
		}
	case "RotateSplit", "EqualizeSplits", "ToggleTiling", "AddMaster", "RemoveMaster", "SwapWithMaster", "CycleMasterPosition":
		// Batch C1 under the tape's names (layoutTapeActions).
		if len(args) != 0 {
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

// tuiActionIntent reports whether msg is a TUI client's key for an action the
// daemon runs (runsAction). Such an intent runs off the client's read loop
// (runIntent): the action's path can route to an attached TUI and wait for
// its answer (focus-window's revealFocus, a routed verb), and that TUI may be
// the one whose read loop is running the action. It then waited out the
// routed verb's timeout, 10 seconds a key, and a second key pressed
// meanwhile reached the strip it had read before.
func (d *Daemon) tuiActionIntent(cs *connState, msg *Message) bool {
	cs.mu.Lock()
	tui := cs.isTUIClient
	cs.mu.Unlock()
	if !tui {
		return false
	}
	var p ExecuteCommandPayload
	if err := msg.ParsePayload(&p); err != nil || p.TapeScript != "" {
		return false
	}
	command, args, ok := resolveCommandName(p.CommandType, p.Args)
	if !ok {
		return false
	}
	sess := d.findTargetSession(cs, p.SessionName)
	return sess != nil && d.runsAction(sess, command, args)
}

// runIntent runs f after every intent this client sent before it, on a
// goroutine of its own that ends with the connection.
func (cs *connState) runIntent(f func()) {
	cs.intentOnce.Do(func() {
		cs.intents = make(chan func(), 64)
		go func() {
			for {
				select {
				case f := <-cs.intents:
					f()
				case <-cs.done:
					return
				}
			}
		}()
	})
	select {
	case cs.intents <- f:
	case <-cs.done:
	}
}
