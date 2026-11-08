package main

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
)

// One state per agent (Mac round 6, workflow S1). The agents view reads
// list-agents and the Inbox (ck.agents, ck.items); the session rows, the
// workspace chips, the footer and the Dock read the pane statuses each
// state push brings. They came at different moments and were mapped by
// different rules, so one agent could spin in the list while its session
// row was at rest. Now ckShownStatus is the one state: each push lays its
// pane fields on the agents' rows (followPanes), and every pane that has an
// agent row takes its status from it (shareAgentStates).

// ckPushed is what a state push said of a pane's agent.
type ckPushed struct {
	state, reason, msg string
	at                 int64
}

// followPanes lays what this push says of each local agent pane on its row
// in the agents list, so that the list is never older than the rail. A
// fetch that began before the push and lands after it gets the push laid
// on again (ckRefetched).
func (a *App) followPanes() {
	ck := &a.ck
	pushed := map[string]ckPushed{}
	for i := range a.st.Sessions {
		s := &a.st.Sessions[i]
		if a.hostOf(s.ID).remote() {
			continue
		}
		for _, list := range [][]proto.Pane{s.Panes, s.Others} {
			for _, p := range list {
				if p.AgentState != "" {
					pushed[p.ID] = ckPushed{p.AgentState, p.AgentReason, p.Message, p.AgentAt}
				}
			}
		}
	}
	ck.pushed, ck.pushedAt = pushed, time.Now()
	ck.layPushed()
}

// layPushed lays the last push on the agents' rows.
func (ck *cockpit) layPushed() {
	for i := range ck.agents {
		ag := &ck.agents[i]
		if ag.Host != "" {
			continue
		}
		p, ok := ck.pushed[ag.Window]
		if !ok || p.at < ag.StateAt {
			continue
		}
		if p.state == "done" && ag.State != "done" {
			// It finished since the list was read: unread until the
			// next read says it was seen, as the daemon keeps it.
			ag.FinishedUnread = true
		}
		ag.State, ag.Reason, ag.StateAt, ag.Message = p.state, p.reason, p.at, p.msg
	}
}

// ckRefetched lays the last push again on a list whose read began before
// the push came: the read's rows are older than it.
func (ck *cockpit) ckRefetched(began time.Time) {
	if ck.pushedAt.After(began) {
		ck.layPushed()
	}
}

// shareAgentStates gives every local pane that has a row in the agents
// list the state that row shows, then the session's lead pane and its
// workspaces' marks from those panes again: the session row, the chip, the
// footer and the Dock's badge say what the list says. A pane with no agent
// row, an exited one and a host's keep the state their push gave.
func (a *App) shareAgentStates() {
	ck := &a.ck
	if !ck.loaded || len(ck.agents) == 0 {
		return
	}
	rows := make(map[string]*client.Agent, len(ck.agents))
	for i := range ck.agents {
		if ck.agents[i].Host == "" {
			rows[ck.agents[i].Window] = &ck.agents[i]
		}
	}
	take := func(p *proto.Pane) bool {
		ag := rows[p.ID]
		if ag == nil || p.Status == proto.Exited {
			return false
		}
		st, unread := a.ckShownStatus(*ag, a.ckItemOf(*ag))
		if st == statusDone {
			// A seen finish is at rest: the pane's status has no done,
			// and its mark comes from the row (paneMark).
			st = proto.Idle
		}
		if p.Status == st && p.Unread == unread {
			return false
		}
		p.Status, p.Unread = st, unread
		return true
	}
	for i := range a.st.Sessions {
		s := &a.st.Sessions[i]
		if a.hostOf(s.ID).remote() {
			continue
		}
		changed := false
		for j := range s.Panes {
			changed = take(&s.Panes[j]) || changed
		}
		for j := range s.Others {
			changed = take(&s.Others[j]) || changed
		}
		if !changed {
			continue
		}
		if j := proto.Lead(s.Panes, s.Focus); j >= 0 {
			s.Pane, s.Lead = s.Panes[j], s.Panes[j].ID
		}
		for w := range s.Workspaces {
			ws := &s.Workspaces[w]
			st, unread, seen := proto.Idle, false, false
			for _, list := range [][]proto.Pane{s.Panes, s.Others} {
				for _, p := range list {
					n := p.Workspace
					if n == 0 {
						n = s.Workspace
					}
					if n != ws.N {
						continue
					}
					seen = true
					unread = unread || p.Unread
					if statusOrder(p.Status) > statusOrder(st) {
						st = p.Status
					}
				}
			}
			if seen {
				ws.Status, ws.Unread = st, unread
			}
		}
	}
}

// statusOrder orders statuses by how much they ask of the person, as the
// client ranks a workspace's panes.
func statusOrder(s proto.Status) int {
	switch s {
	case proto.Waiting:
		return 4
	case proto.Failed:
		return 3
	case proto.Working:
		return 2
	case proto.Idle:
		return 1
	}
	return 0
}
