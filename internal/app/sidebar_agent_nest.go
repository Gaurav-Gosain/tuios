package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

const (
	sidebarAgentNestStep = 2
	sidebarAgentNestMax  = 3
)

func (e sidebarAgentEntry) nestIndent() int {
	return min(e.Nest, sidebarAgentNestMax) * sidebarAgentNestStep
}

func agentParentNode(w *terminal.Window) *sessiontree.PaneRef {
	if !w.AgentChild || w.StartedByWindow == "" {
		return nil
	}
	return &sessiontree.PaneRef{Session: w.StartedBySession, Window: w.StartedByWindow}
}

func agentParentFromWire(ref *session.PaneRef) *sessiontree.PaneRef {
	if ref == nil || ref.Window == "" {
		return nil
	}
	return &sessiontree.PaneRef{Session: ref.Session, Window: ref.Window}
}

func sidebarNestAgents(agents []sidebarAgentEntry) []sidebarAgentEntry {
	type key struct{ session, window string }
	keyOf := func(e sidebarAgentEntry) key { return key{e.SessionID, e.WindowID} }
	at := make(map[key]int, len(agents))
	for i, e := range agents {
		if e.Host == "" && e.Fold == 0 && !e.sidebarSubRow() {
			at[keyOf(e)] = i
		}
	}
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
