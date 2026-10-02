//go:build !slim

package session

import "github.com/Gaurav-Gosain/tuios/internal/harness"

// inputProfileFor is the input profile of the harness running in a window as
// the session last recorded it, or the default when there is none: a pane no
// harness has claimed still gets a prompt pasted and submitted with a carriage
// return, as it always did.
func (d *Daemon) inputProfileFor(sess *Session, windowID string) harness.InputProfile {
	reg := d.agentMatcher.registry
	if reg == nil || sess == nil {
		return harness.DefaultInputProfile()
	}
	sess.stateMu.RLock()
	hid := ""
	for i := range sess.state.Windows {
		if sess.state.Windows[i].ID == windowID {
			hid = sess.state.Windows[i].AgentHarness
			break
		}
	}
	sess.stateMu.RUnlock()
	return reg.InputProfile(hid)
}
