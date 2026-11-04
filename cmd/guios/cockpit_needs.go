package main

import (
	"slices"
	"sort"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/session"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// What needs the person. The Agents badge, the cockpit's Needs you group,
// the footer chip and the keys that go to what needs you (Ctrl+Shift+J,
// prefix o) all read this one list, so they never disagree. It is built
// from the daemon's agents and its Inbox, which the app reads whenever the
// daemon tells of a change, with the cockpit open or not.

// Need tiers, in the order the keys go through them.
const (
	needState = iota // the agent waits for input, or failed
	needMail         // mail for the person
	needItem         // another Inbox item: an approval a hook holds, a question
)

// ckNeed is one agent, or one Inbox item with no agent here, that needs
// the person.
type ckNeed struct {
	agent int // index in cockpit.agents, -1 for none
	item  *session.AttentionItem
	tier  int
	since int64 // unix nanoseconds
}

// needItemKind reports whether an Inbox item asks something of the person.
func needItemKind(it *session.AttentionItem) bool {
	return it.SnoozedUntil == 0 && it.Kind != session.AttentionFinished && it.Kind != session.AttentionOutbox
}

// needs lists what needs the person, one entry per agent, in the order the
// keys go to them: by tier, then the oldest wait first.
func (ck *cockpit) needs() []ckNeed {
	byKey := make(map[string]int, len(ck.agents))
	for i, ag := range ck.agents {
		if ag.Host == "" {
			byKey[ag.Session+"\x00"+ag.Window] = i
		}
	}
	at := map[int]int{} // agent -> index in out
	var out []ckNeed
	for i := range ck.agents {
		ag := ck.agents[i]
		if ag.State == "needs_input" || ag.State == "errored" {
			at[i] = len(out)
			out = append(out, ckNeed{agent: i, tier: needState, since: ag.StateAt})
		}
	}
	for i := range ck.items {
		it := &ck.items[i]
		if !needItemKind(it) {
			continue
		}
		tier := needItem
		switch it.Kind {
		case session.AttentionMail:
			tier = needMail
		case session.AttentionErrored:
			tier = needState
		}
		idx := -1
		if it.Window != "" && it.Host == "" {
			if j, ok := byKey[it.Session+"\x00"+it.Window]; ok {
				idx = j
			}
		}
		if idx < 0 {
			out = append(out, ckNeed{agent: -1, item: it, tier: tier, since: it.Since})
			continue
		}
		if k, ok := at[idx]; ok {
			n := &out[k]
			if n.item == nil {
				n.item = it
			}
			n.tier = min(n.tier, tier)
			if it.Since > 0 && (n.since == 0 || it.Since < n.since) {
				n.since = it.Since
			}
			continue
		}
		at[idx] = len(out)
		out = append(out, ckNeed{agent: idx, item: it, tier: tier, since: it.Since})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].tier != out[j].tier {
			return out[i].tier < out[j].tier
		}
		return out[i].since < out[j].since
	})
	return out
}

// needsCount is how many agents need the person: the badge's number. Before
// the daemon's lists are read, it counts the panes that wait.
func (a *App) needsCount() int {
	if a.ck.loaded {
		return len(a.ck.needs())
	}
	n := 0
	for _, s := range a.st.Sessions {
		if h := a.hostOf(s.ID); h.remote() {
			continue
		}
		for _, p := range s.Panes {
			if p.Status == proto.Waiting || p.Status == proto.Failed {
				n++
			}
		}
		for _, p := range s.Others {
			if p.Status == proto.Waiting || p.Status == proto.Failed {
				n++
			}
		}
	}
	return n
}

// needAgents is the set of agents that need the person, by their index.
func (ck *cockpit) needAgents() map[int]bool {
	set := map[int]bool{}
	for _, n := range ck.needs() {
		if n.agent >= 0 {
			set[n.agent] = true
		}
	}
	return set
}

// jumpToAttention goes to the next thing that needs the person, after the
// one in front of them: its pane, or the agents view for what is answered
// there (mail, an approval a hook holds, an item with no pane here).
func (a *App) jumpToAttention() {
	ck := &a.ck
	if !ck.loaded {
		a.refreshCockpit()
		if a.jumpTo(func(s *proto.Session) bool { return s.Status == proto.Waiting }) {
			return
		}
		a.toast(locales.ToastNothingNeedsYou())
		return
	}
	needs := ck.needs()
	if len(needs) == 0 {
		if a.jumpTo(func(s *proto.Session) bool { return s.Unread }) {
			return
		}
		a.toast(locales.ToastNothingNeedsYou())
		return
	}
	// The one in front of the person: the cockpit's selection while it is
	// open, else the focused pane.
	cur := -1
	if ck.open {
		if r, ok := a.ckSelected(); ok {
			cur = slices.IndexFunc(needs, func(n ckNeed) bool {
				return r.agent >= 0 && n.agent == r.agent || r.agent < 0 && n.item != nil && r.item != nil && n.item.ID == r.item.ID
			})
		}
	} else if s := a.session(a.selected); s != nil {
		pane := a.focusOf(s)
		cur = slices.IndexFunc(needs, func(n ckNeed) bool { return n.agent >= 0 && ck.agents[n.agent].Window == pane })
	}
	n := needs[(cur+1)%len(needs)]
	a.goToNeed(n)
}

// goToNeed shows one need: in the agents view when it is open or when its
// answer is there, else its pane.
func (a *App) goToNeed(n ckNeed) {
	ck := &a.ck
	inView := ck.open || n.agent < 0 || n.tier == needMail || n.item != nil && ckHeld(*n.item)
	if !inView {
		a.goToAgent(ck.agents[n.agent])
		return
	}
	if !ck.open {
		a.toggleCockpit()
	}
	ck.view = viewAgents
	key := ""
	if n.agent >= 0 {
		key = ck.agents[n.agent].Key()
	}
	ck.jumpTo = &ckJump{agent: key}
	if n.agent < 0 && n.item != nil {
		ck.jumpTo.item = n.item.ID
	}
}

// ckJump is a row the cockpit selects once its rows are built.
type ckJump struct {
	agent, item string
}

// ckTakeJump selects the row a jump asked for, once the rows are built.
func (a *App) ckTakeJump() {
	ck := &a.ck
	j := ck.jumpTo
	if j == nil {
		return
	}
	ck.jumpTo = nil
	for _, r := range ck.rows {
		if r.kind == ckRowHead || r.kind == ckRowMore {
			continue
		}
		if j.item != "" && r.item != nil && r.item.ID == j.item || j.agent != "" && r.agent >= 0 && ck.agents[r.agent].Key() == j.agent {
			a.ckSelect(r)
			ck.fleetFocus.Focus()
			return
		}
	}
}

// The cockpit's views.
const (
	viewAgents = iota
	viewBoard
	viewMail
)
