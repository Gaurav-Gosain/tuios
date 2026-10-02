package app

import (
	"fmt"
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Some chrome only means something to a person who runs agents: the prefix
// menu's Inbox lines, the palette's agent entries and its "@ state" hint, and
// the agent rows of the Alerts settings. None of it is removed. It waits until
// an agent has been seen, so a person who never runs one is not shown controls
// for something that is not there.

// agentsPresent reports whether anything an agent leaves behind is in view
// right now: a pane with an agent state or a named harness in the attached
// session or in any session the client has listed, an Inbox item, or mail.
// Reads only what the client already holds; nothing here asks the daemon.
func (m *OS) agentsPresent() bool {
	for _, w := range m.Windows {
		if w != nil && (w.AgentState != "" || w.AgentHarness != "") {
			return true
		}
	}
	if len(m.Inbox.Items) > 0 || len(m.AgentMail.Messages) > 0 {
		return true
	}
	if c := m.DaemonClient; c != nil {
		for _, name := range c.AvailableSessionNames() {
			for _, w := range c.SessionWindows(name) {
				if w.AgentState != "" || w.AgentHarness != "" {
					return true
				}
			}
		}
	}
	return false
}

// noteAgentsSeen persists the flag the first time an agent is in view. Called
// from the paths an agent's state, an Inbox item or mail arrives by, so the
// render path never writes the state file.
func (m *OS) noteAgentsSeen() {
	if m.SidebarAgentsSeen || !m.agentsPresent() {
		return
	}
	m.SidebarAgentsSeen = true
	m.saveSidebarState()
}

// prefixMenuGroups is the which-key menu after the prefix key, in its
// sections. The Inbox's lines wait until an agent has been seen, and a section
// they leave empty goes with them; the keys work either way. The review line
// is left out on a daemon that cannot review, where the key does what an
// unbound key does.
func (m *OS) prefixMenuGroups() []config.KeybindingGroup {
	groups := config.GetPrefixKeybindingGroups("", m.IsDaemonSession)
	seen, review := m.agentsSeen(), m.reviewSupported()
	// With multifocus on, the copy-mode key enters multi copy mode, and the
	// menu says so: this is where a multifocus user finds out it exists.
	multi, multiOK := m.MultiCopyEligible()
	out := groups[:0]
	for _, g := range groups {
		if multiOK {
			for i := range g.Bindings {
				if g.Bindings[i].Key == config.WhichKeyCopyPasteKey {
					g.Bindings[i].Description = fmt.Sprintf("Multi copy (%d)/paste image", multi)
				}
			}
		}
		if !seen {
			g.Bindings = slices.DeleteFunc(g.Bindings, config.IsAgentPrefixKeybinding)
		}
		if !review {
			g.Bindings = slices.DeleteFunc(g.Bindings, config.IsReviewPrefixKeybinding)
		}
		g.Bindings = slimPrefixBindings(g.Bindings)
		if len(g.Bindings) > 0 {
			out = append(out, g)
		}
	}
	return out
}

// prefixMenuBindings is prefixMenuGroups as one list.
func (m *OS) prefixMenuBindings() []config.Keybinding {
	var out []config.Keybinding
	for _, g := range m.prefixMenuGroups() {
		out = append(out, g.Bindings...)
	}
	return out
}
