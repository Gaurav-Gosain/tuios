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

import "strconv"

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
	// DoneSeen is the unread bit of a "done" state: false while the user has
	// not looked at the finished pane yet. Meaningless for other states. On a
	// session node it belongs to the window that won the roll-up.
	DoneSeen bool
	// StateAt is when the pane entered AgentState, as a Unix nanosecond stamp,
	// or 0 when unknown. The rail reads it to show how long a pane has been in
	// its state, which is what tells a waiting agent from a busy one.
	StateAt int64
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
	// DoneSeen marks a finished pane the user has already looked at.
	DoneSeen bool
	// StateAt is when the pane entered AgentState (Unix nanoseconds), 0 if unknown.
	StateAt int64
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

// AgentRank ranks agent states for both the session roll-up and the sidebar's
// priority-ordered agents section. A higher number wins, so a session holding
// any errored or blocked window surfaces that over calmer states.
//
// done splits on the unread bit: a pane that finished and has not been looked
// at is the second most urgent thing on screen, and drops below working the
// moment it is seen. That decay is what stops a finished agent from sitting in
// the rail as permanent green noise.
//
// Order: errored > needs_input > done-unseen > working > done-seen > idle > none.
func AgentRank(state string, doneSeen bool) int {
	switch state {
	case "errored":
		return 6
	case "needs_input":
		return 5
	case "done":
		if doneSeen {
			return 2
		}
		return 4
	case "working":
		return 3
	case "idle":
		return 1
	default: // "none" and any unknown value
		return 0
	}
}

// RollUpState returns the highest-priority state among the given raw states,
// treating every done as unseen. Callers that track the unread bit roll up
// through BuildSession instead.
func RollUpState(states []string) string {
	best := ""
	bestRank := 0
	for _, s := range states {
		if r := AgentRank(s, false); r > bestRank {
			best, bestRank = s, r
		}
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
	bestRank := 0
	for _, w := range s.Windows {
		children = append(children, Node{
			Kind:       KindWindow,
			ID:         w.ID,
			Title:      w.Title,
			AgentState: w.AgentState,
			DoneSeen:   w.DoneSeen,
			StateAt:    w.StateAt,
			IsCurrent:  w.Focused,
		})
		if r := AgentRank(w.AgentState, w.DoneSeen); r > bestRank {
			node.AgentState, node.DoneSeen, bestRank = w.AgentState, w.DoneSeen, r
		}
	}
	node.Children = disambiguate(children)
	node.WindowCount = len(children)
	return node
}

// disambiguate makes every window row in a session self-identifying. Naming and
// command detection answer "which pane is this" for most panes, but a session of
// bare shells in one directory still resolves to one label repeated, and a list
// of identical rows cannot do the only job it has. Rows that still collide get
// their 1-based position appended, which is stable for a given window order and
// so does not move under the eye between frames.
func disambiguate(children []Node) []Node {
	counts := make(map[string]int, len(children))
	for _, c := range children {
		counts[c.Title]++
	}
	for i := range children {
		if counts[children[i].Title] > 1 {
			children[i].Title += " " + strconv.Itoa(i+1)
		}
	}
	return children
}

// Build assembles the full tree, preserving the order the sessions are given.
func Build(sessions []SessionInput) Tree {
	nodes := make([]Node, 0, len(sessions))
	for _, s := range sessions {
		nodes = append(nodes, BuildSession(s))
	}
	return Tree{Sessions: nodes}
}
