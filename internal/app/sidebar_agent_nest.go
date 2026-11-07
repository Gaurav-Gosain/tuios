package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Child agents under their parent in the rail's agents section.
//
// An agent another agent started carries a parent link (the daemon's
// agent_parent.go). The section draws its row under its parent's row, one
// step in per level, the way a pane's subagent rows sit under it. The
// section's own order still decides where a family goes: a family is drawn
// where its first member in that order would be, so a child that needs the
// person lifts its parent's rows with it to the top instead of being buried
// under a parent at rest. Inside a family the children keep the section's
// order too.
//
// Only a row whose parent is itself a row of the list nests. A parent that is
// folded away, filtered out or closed leaves its child as a row of its own.

// sidebarAgentNestStep is how many cells a child row steps in per level, and
// sidebarAgentNestMax the deepest level drawn: a deeper row steps in no
// further, so a long chain cannot push the names off a narrow rail.
const (
	sidebarAgentNestStep = 2
	sidebarAgentNestMax  = 3
)

// nestIndent is how many cells the entry's row steps in.
func (e sidebarAgentEntry) nestIndent() int {
	return min(e.Nest, sidebarAgentNestMax) * sidebarAgentNestStep
}

// agentParentNode is the parent of a live window, for the session tree.
func agentParentNode(w *terminal.Window) *sessiontree.PaneRef {
	if !w.AgentChild || w.StartedByWindow == "" {
		return nil
	}
	return &sessiontree.PaneRef{Session: w.StartedBySession, Window: w.StartedByWindow}
}

// agentParentFromWire is the parent a session listing names, for the tree.
func agentParentFromWire(ref *session.PaneRef) *sessiontree.PaneRef {
	if ref == nil || ref.Window == "" {
		return nil
	}
	return &sessiontree.PaneRef{Session: ref.Session, Window: ref.Window}
}

// sidebarNestAgents orders agents so each child row follows its parent's, and
// sets every row's Nest. Rows with no parent in the list keep their order.
// It returns agents itself when no row has a parent in the list, which is
// the common case and costs one pass.
func sidebarNestAgents(agents []sidebarAgentEntry) []sidebarAgentEntry {
	type key struct{ session, window string }
	keyOf := func(e sidebarAgentEntry) key { return key{e.SessionID, e.WindowID} }
	at := make(map[key]int, len(agents))
	for i, e := range agents {
		if e.Host == "" && e.Fold == 0 && !e.sidebarSubRow() {
			at[keyOf(e)] = i
		}
	}
	// parentOf[i] is the index of i's parent row, -1 for none.
	parentOf := make([]int, len(agents))
	nested := false
	for i, e := range agents {
		parentOf[i] = -1
		if e.Parent == nil || e.Host != "" {
			continue
		}
		if p, ok := at[key{e.Parent.Session, e.Parent.Window}]; ok && p != i {
			parentOf[i] = p
			nested = true
		}
	}
	if !nested {
		for i := range agents {
			agents[i].Nest = 0
		}
		return agents
	}
	children := make(map[int][]int)
	for i, p := range parentOf {
		if p >= 0 {
			children[p] = append(children[p], i)
		}
	}
	// top is the first row of i's family, guarded against a loop the daemon
	// never makes but a stale listing could. Every row of a loop is still
	// drawn once: emit reaches the whole loop from any row of it.
	top := func(i int) int {
		for steps := 0; parentOf[i] >= 0 && steps < len(agents); steps++ {
			i = parentOf[i]
		}
		return i
	}
	out := make([]sidebarAgentEntry, 0, len(agents))
	done := make([]bool, len(agents))
	var emit func(i, depth int)
	emit = func(i, depth int) {
		if done[i] {
			return
		}
		done[i] = true
		e := agents[i]
		e.Nest = depth
		out = append(out, e)
		for _, c := range children[i] {
			emit(c, depth+1)
		}
	}
	for i := range agents {
		if !done[i] {
			emit(top(i), 0)
		}
	}
	return out
}
