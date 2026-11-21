package session

import (
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/plural"
)

var itoa = strconv.Itoa

const selectWriteMax = 32

const selectAskMax = fanMaxCount

type selectedPane struct {
	sess   *Session
	window WindowState
	group  string
}

func (p selectedPane) key() string { return localAttentionHost + "/" + p.window.ID }

func (p selectedPane) label() string {
	return p.sess.Name() + "/" + windowLabelOf(p.window) + " (" + shortWindowID(p.window.ID) + ")"
}

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

func (d *Daemon) paneSelectorTarget(sess *Session, w WindowState, group string, cards []TaskState) SelectorTarget {
	var tasks, stages []string
	for _, tk := range cards {
		tasks = append(tasks, tk.ID)
		stages = append(stages, tk.Stage)
	}
	var parent, root string
	if w.AgentLink != nil && w.StartedBy != nil {
		parent, root = w.StartedBy.Window, w.AgentLink.Root.Window
	}
	role := ""
	if w.Delegation != nil {
		role = w.Delegation.Role
	}
	return SelectorTarget{
		Role:     role,
		Parent:   parent,
		Root:     root,
		Host:     localAttentionHost,
		Session:  sess.Name(),
		Name:     windowLabelOf(w),
		State:    w.ShownAgentState().Name(),
		Harness:  firstNonEmpty(w.AgentHarness, sess.agentClaimFor(w.ID).harness),
		Cwd:      w.Cwd,
		Group:    group,
		NeedsYou: w.AgentState.NeedsYou(),
		Silent:   w.AgentReason == AgentReasonSilent && w.AgentState == AgentStateWorking,
		Tasks:    tasks,
		Stages:   stages,
	}
}

func (d *Daemon) selectPanes(sel *Selector, all bool) []selectedPane {
	sessions := d.manager.AllSessions()
	slices.SortFunc(sessions, func(a, b *Session) int { return strings.Compare(a.Name(), b.Name()) })
	var out []selectedPane
	for _, sess := range sessions {
		st := sess.GetState()
		group := ""
		if st.Worktree != nil {
			group = st.Worktree.Group
		}
		held := heldTasks(st.Tasks)
		for i := range st.Windows {
			w := st.Windows[i]
			if !all && !isAgentWindow(w) {
				continue
			}
			if sel.Match(d.paneSelectorTarget(sess, w, group, held[w.ID])) {
				out = append(out, selectedPane{sess: sess, window: w, group: group})
			}
		}
	}
	return out
}

func selectionKeys(panes []selectedPane) []string {
	keys := make([]string, 0, len(panes))
	for _, p := range panes {
		keys = append(keys, p.key())
	}
	return keys
}

func refuseSelectFromHostedPane(cs *connState) *verbError {
	if cs == nil || !cs.paneOnly {
		return nil
	}
	return hintedVerbError(ErrVerbForbidden, "a pane on another machine cannot address panes by selector", &VerbHint{
		Param:  "select",
		Detail: "Nothing was done. A hosted pane's calls act as the window it is drawn in and nothing else.",
	})
}

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
		return nil, hintedVerbError(ErrVerbConfirmRequired, "the selector matches "+itoa(len(panes))+" "+plural.Word(len(panes), "pane", "panes")+"; nothing was sent", &VerbHint{
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
