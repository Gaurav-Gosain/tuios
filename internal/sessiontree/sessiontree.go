// Package sessiontree is the shared data model behind every session-management
// surface: the sidebar, the picker, and the command-palette session/window
// entries. It is pure data and queries with no rendering and no bubbletea or
// app imports, so it is unit-testable and can be built from either the client's
// live session state or the daemon's projection of another session.
//
// The three surfaces read one Tree so they can never disagree about what
// sessions and windows exist, which is current, or what agent state a session
// rolls up to. Glyphs are still chosen by the app's agentStateIndicator; this
// package only decides the rolled-up state string, in one place.
package sessiontree

// NodeKind distinguishes a session row from a window row in the tree.
type NodeKind int

const (
	// KindSession is a session node; its Children are its windows.
	KindSession NodeKind = iota
	// KindWindow is a window node under a session.
	KindWindow
)

// Node is one row in the tree: a session or a window under it.
type Node struct {
	Kind NodeKind
	// ID is the session name for a session node, or the window ID for a window.
	ID    string
	Title string
	// AgentState is the raw agent-state string (matching session.AgentState:
	// "", "working", "needs_input", "idle", "done", "errored"). For a session
	// node it is the rolled-up state of its windows.
	AgentState string
	// Attached is true for a session that has any client attached. Always false
	// for window nodes.
	Attached bool
	// IsCurrent marks the session this client is attached to, or the currently
	// focused window within it.
	IsCurrent bool
	// WindowCount is the number of windows in a session node. Zero for windows.
	WindowCount int
	// Children are the window nodes of a session. Nil for a window node, and nil
	// for a session whose windows are not known yet (a non-attached session over
	// the coarse control protocol), which still carries a rolled-up glyph and a
	// WindowCount and expands to real children once selected.
	Children []Node
}

// Tree is the full set of sessions, each with its windows when known.
type Tree struct {
	Sessions []Node
}

// WindowInput is the caller's per-window data, adapted from the app's live
// windows or the daemon's window-list projection.
type WindowInput struct {
	ID         string
	Title      string
	AgentState string
	// Focused marks the currently focused window in its session.
	Focused bool
}

// SessionInput is the caller's per-session data. Windows may be nil for a
// non-attached session whose per-window detail is not available yet; WindowCount
// then carries the count for the collapsed row.
type SessionInput struct {
	Name        string
	Attached    bool
	IsCurrent   bool
	WindowCount int
	Windows     []WindowInput
}

// agentStatePriority ranks agent states for roll-up. A higher number wins, so a
// session showing any errored or blocked window surfaces that over calmer
// states. Order: errored > needs_input > working > done > idle > none.
func agentStatePriority(s string) int {
	switch s {
	case "errored":
		return 5
	case "needs_input":
		return 4
	case "working":
		return 3
	case "done":
		return 2
	case "idle":
		return 1
	default: // "none" and any unknown value
		return 0
	}
}

// RollUpState returns the highest-priority state among the given states, which
// is what a collapsed session row shows for its windows. An empty input rolls
// up to "" (none). This is the single definition of the roll-up ordering.
func RollUpState(states []string) string {
	best := ""
	bestPri := -1
	for _, s := range states {
		if p := agentStatePriority(s); p > bestPri {
			best, bestPri = s, p
		}
	}
	if bestPri <= 0 {
		return ""
	}
	return best
}

// BuildSession builds one session node. When Windows are present it builds the
// window children and rolls their states up into the session's AgentState and
// sets WindowCount from the children; otherwise it keeps the coarse WindowCount
// and leaves Children nil.
func BuildSession(s SessionInput) Node {
	node := Node{
		Kind:        KindSession,
		ID:          s.Name,
		Title:       s.Name,
		Attached:    s.Attached,
		IsCurrent:   s.IsCurrent,
		WindowCount: s.WindowCount,
	}
	if len(s.Windows) == 0 {
		return node
	}

	children := make([]Node, 0, len(s.Windows))
	states := make([]string, 0, len(s.Windows))
	for _, w := range s.Windows {
		children = append(children, Node{
			Kind:       KindWindow,
			ID:         w.ID,
			Title:      w.Title,
			AgentState: w.AgentState,
			IsCurrent:  w.Focused,
		})
		states = append(states, w.AgentState)
	}
	node.Children = children
	node.WindowCount = len(children)
	node.AgentState = RollUpState(states)
	return node
}

// Build assembles the full tree, preserving the order the sessions are given.
func Build(sessions []SessionInput) Tree {
	nodes := make([]Node, 0, len(sessions))
	for _, s := range sessions {
		nodes = append(nodes, BuildSession(s))
	}
	return Tree{Sessions: nodes}
}
