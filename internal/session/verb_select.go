package session

import (
	"os"
	"slices"
	"strconv"
	"strings"
)

var itoa = strconv.Itoa

// The daemon's half of selectors: parsing one against this machine, resolving
// it to panes, and the confirm step every write addressed by one goes through.
// The syntax and the matcher are in selector.go.

// selectWriteMax bounds how many panes one write addressed by selector may
// reach. A selector that matches more is almost always a mistake, and the
// remedy is a narrower one, which the refusal says.
const selectWriteMax = 32

// selectAskMax bounds a broadcast ask. Every ask types into a pane and waits
// for it, so this is the fan-out bound rather than the message one.
const selectAskMax = fanMaxCount

// selectedPane is one pane a selector resolved to on this machine.
type selectedPane struct {
	sess   *Session
	window WindowState
	group  string
}

// key names the pane for the confirm token. Window ids are uuids, unique on
// the daemon, and the host keeps a local key apart from a far one.
func (p selectedPane) key() string { return localAttentionHost + "/" + p.window.ID }

// label is how a refusal lists the pane: session/name and the short id.
func (p selectedPane) label() string {
	return p.sess.Name + "/" + windowLabelOf(p.window) + " (" + shortWindowID(p.window.ID) + ")"
}

// parseVerbSelector parses a selector parameter for this daemon: a leading ~
// in a cwd term is this machine's home, a program name in a harness term is the
// harness it names, and this machine's own name in a host term is local.
func (d *Daemon) parseVerbSelector(text string) (*Selector, *verbError) {
	home, _ := os.UserHomeDir()
	sel, err := ParseSelector(text, home)
	if err != nil {
		return nil, hintedVerbError(ErrVerbInvalidParams, "select: "+err.Error(), &VerbHint{
			Param:    "select",
			Accepted: SelectorKeys,
			Detail:   "A selector is space-separated key:value terms, all of which must match; a term's comma-separated values are alternatives. Example: harness:codex state:idle,done session:api-*",
		})
	}
	if reg := d.agentMatcher.registry; reg != nil {
		sel.mapValues(SelectorHarness, func(v string) string {
			if strings.ContainsAny(v, "*?[") {
				return v
			}
			if m, _, ok := reg.Resolve(v); ok {
				return strings.ToLower(m.ID)
			}
			return v
		})
	}
	if self := d.manager.HostName(); self != "" {
		sel.mapValues(SelectorHost, func(v string) string {
			if v == self {
				return localAttentionHost
			}
			return v
		})
	}
	return sel, nil
}

// paneSelectorTarget is what a selector reads from one pane of this machine.
func (d *Daemon) paneSelectorTarget(sess *Session, w WindowState, group string) SelectorTarget {
	return SelectorTarget{
		Host:     localAttentionHost,
		Session:  sess.Name,
		Name:     windowLabelOf(w),
		State:    w.AgentState.Name(),
		Harness:  firstNonEmpty(w.AgentHarness, sess.agentClaimFor(w.ID).harness),
		Cwd:      w.Cwd,
		Group:    group,
		NeedsYou: w.AgentState.NeedsYou(),
	}
}

// selectPanes resolves a selector to the panes of this machine it matches, in
// session name order and window order inside a session. Only agent panes are
// considered unless all is set, because a selector addresses agents: a shell
// in the same session is not something to ask or message.
func (d *Daemon) selectPanes(sel *Selector, all bool) []selectedPane {
	sessions := d.manager.AllSessions()
	slices.SortFunc(sessions, func(a, b *Session) int { return strings.Compare(a.Name, b.Name) })
	var out []selectedPane
	for _, sess := range sessions {
		st := sess.GetState()
		group := ""
		if st.Worktree != nil {
			group = st.Worktree.Group
		}
		for i := range st.Windows {
			w := st.Windows[i]
			if !all && !isAgentWindow(w) {
				continue
			}
			if sel.Match(d.paneSelectorTarget(sess, w, group)) {
				out = append(out, selectedPane{sess: sess, window: w, group: group})
			}
		}
	}
	return out
}

// selectionKeys is the keys of a set of panes, for SelectionToken.
func selectionKeys(panes []selectedPane) []string {
	keys := make([]string, 0, len(panes))
	for _, p := range panes {
		keys = append(keys, p.key())
	}
	return keys
}

// refuseSelectFromHostedPane is the refusal a pane on another machine gets for
// naming a selector. Its calls are pinned to the window it is drawn in, and a
// selector would reach past it into every session here.
func refuseSelectFromHostedPane(cs *connState) *verbError {
	if cs == nil || !cs.paneOnly {
		return nil
	}
	return hintedVerbError(ErrVerbForbidden, "a pane on another machine cannot address panes by selector", &VerbHint{
		Param:  "select",
		Detail: "Nothing was done. A hosted pane's calls act as the window it is drawn in and nothing else.",
	})
}

// resolveSelection is the confirm step of a write addressed by selector. It
// resolves the selector to agent panes and returns them only when confirm is
// the token of exactly that set. Otherwise nothing is written, and the error
// lists the set and carries its token, so the caller looks before it writes
// and the write goes to what it looked at.
func (d *Daemon) resolveSelection(cs *connState, text, confirm string, max int) ([]selectedPane, *verbError) {
	if verr := refuseSelectFromHostedPane(cs); verr != nil {
		return nil, verr
	}
	sel, verr := d.parseVerbSelector(text)
	if verr != nil {
		return nil, verr
	}
	panes := d.selectPanes(sel, false)
	if len(panes) == 0 {
		return nil, hintedVerbError(ErrVerbWindowNotFound, "no agent pane matches the selector "+echoName(sel.String()), &VerbHint{
			Param:   "select",
			Verb:    "list-agents",
			Command: "tuios list-agents --select '" + sel.String() + "'",
			Detail:  "Nothing was sent. list-agents with the same selector shows what it matches; a selector reaches agent panes only, and a term the pane cannot answer (a group outside a fan-out, a cwd nothing reported) does not match.",
		})
	}
	if len(panes) > max {
		return nil, hintedVerbError(ErrVerbInvalidParams, "the selector matches "+itoa(len(panes))+" panes, more than the "+itoa(max)+" one call may reach", &VerbHint{
			Param:  "select",
			Detail: "Nothing was sent. Narrow the selector with another term, such as session: or state:.",
		})
	}
	labels := make([]string, 0, len(panes))
	for _, p := range panes {
		labels = append(labels, p.label())
	}
	token := SelectionToken(selectionKeys(panes))
	switch confirm {
	case token:
		return panes, nil
	case "":
		return nil, hintedVerbError(ErrVerbConfirmRequired, "the selector matches "+itoa(len(panes))+" "+plural(len(panes), "pane", "panes")+"; nothing was sent", &VerbHint{
			Param:     "confirm",
			Available: labels,
			Confirm:   token,
			Detail:    "A selector never sends on its own. Check the panes listed in available, then call again with confirm set to the token in confirm.",
		})
	default:
		return nil, hintedVerbError(ErrVerbConfirmRequired, "the panes the selector matches changed since the token was issued; nothing was sent", &VerbHint{
			Param:     "confirm",
			Available: labels,
			Confirm:   token,
			Detail:    "A pane joined or left the selection between the look and the write. Check the panes listed in available now, then call again with the new token.",
		})
	}
}
