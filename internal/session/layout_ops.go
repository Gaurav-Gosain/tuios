package session

import (
	"errors"
	"maps"

	"github.com/Gaurav-Gosain/tuios/internal/layout"
)

// Layout edits the daemon makes itself: split a named pane, swap two, move one
// beside another and move a border. The split tree of each workspace is
// session state already (see layout_tree.go), so the daemon edits it as a tree
// op of its own: Version advances, every attached client takes the tree from
// the state and lays its panes out again, and a client that attaches later
// starts from it. None of these edits needs a client.
//
// With no client attached nobody measures the panes, so the daemon lays the
// edited workspace out itself, in the session's last size less the chrome
// reserve, and gives each pane's terminal that size. A client that attaches
// lays the panes out again in its own box, from the same tree.

// SplitPlacement says where a new window goes in the split tree.
type SplitPlacement struct {
	// Target is the id of the pane the split cuts in two.
	Target string
	// Side is the side of the target the new pane takes.
	Side layout.Side
	// Share is the new pane's share of the target's space, 0 for the
	// tree's default.
	Share float64
	// Place lays the workspace out in the daemon, for a session no client
	// shows.
	Place bool
}

// errSplitNotTiled refuses a split of a pane the split tree does not lay
// out: a floating, minimized or popup pane.
var errSplitNotTiled = errors.New("the pane is not tiled, so it cannot be split. Split a tiled pane, or tile this one first")

// layoutEdit is the session's split trees opened for editing, in the
// session's leaf numbering, under the state lock. The maps it writes are
// copies: a published snapshot shares the state's maps.
type layoutEdit struct {
	state *SessionState
	box   layout.Rect
	trees map[int]*layout.BSPTree
	ids   map[string]int
	owner map[int]string
	next  int
}

func newLayoutEdit(state *SessionState, box layout.Rect) *layoutEdit {
	e := &layoutEdit{state: state, box: box, trees: map[int]*layout.BSPTree{}, ids: maps.Clone(state.WindowToBSPID), owner: map[int]string{}}
	if e.ids == nil {
		e.ids = map[string]int{}
	}
	e.next = max(state.NextBSPWindowID, 1)
	for id, n := range e.ids {
		e.owner[n] = id
		e.next = max(e.next, n+1)
	}
	return e
}

// leaf is the session's leaf number for window id, given one if it has none.
func (e *layoutEdit) leaf(id string) int {
	if n, ok := e.ids[id]; ok && e.owner[n] == id {
		return n
	}
	n := e.next
	e.next++
	e.ids[id], e.owner[n] = n, id
	return n
}

// tiledPane reports whether the tree of w's workspace lays w out.
func tiledPane(w *WindowState) bool {
	return !w.Minimized && !w.IsFloating && !w.Popup && w.Rail == ""
}

// tree is the tree of ws, ready to edit. It holds every tiled pane of ws and
// nothing else: a leaf for a pane that left is taken out, and a pane the tree
// has not heard of goes in where the tree's scheme puts it, as a client's
// retile does. A workspace with no tree gets one.
func (e *layoutEdit) tree(ws int) *layout.BSPTree {
	if t := e.trees[ws]; t != nil {
		return t
	}
	t := layout.NewBSPTree()
	t.AutoScheme = layout.AutoScheme(e.state.TilingScheme)
	if wire := e.state.WorkspaceTrees[ws]; wire != nil && wire.Root != nil {
		t = (&layout.SerializedBSPTree{Root: toLayoutNode(wire.Root), AutoScheme: wire.AutoScheme, DefaultRatio: wire.DefaultRatio}).Deserialize()
	}
	on := map[string]bool{}
	for i := range e.state.Windows {
		if w := &e.state.Windows[i]; w.Workspace == ws && tiledPane(w) {
			on[w.ID] = true
		}
	}
	for _, n := range t.GetAllWindowIDs() {
		if !on[e.owner[n]] {
			t.RemoveWindow(n)
		}
	}
	for i := range e.state.Windows {
		w := &e.state.Windows[i]
		if !on[w.ID] || t.HasWindow(e.leaf(w.ID)) {
			continue
		}
		at := 0
		if ids := t.GetAllWindowIDs(); len(ids) > 0 {
			at = ids[len(ids)-1]
		}
		t.InsertWindow(e.leaf(w.ID), at, layout.SplitNone, 0, e.box, 0)
	}
	e.trees[ws] = t
	return t
}

// commit writes the trees back into the state, and turns tiling on: a split
// tree means nothing to a session that floats its panes. A session no client
// has named a layout for gets the split layout, which is what the trees draw.
func (e *layoutEdit) commit() {
	trees := maps.Clone(e.state.WorkspaceTrees)
	if trees == nil {
		trees = map[int]*SerializedBSPTree{}
	}
	for ws, t := range e.trees {
		if t.IsEmpty() {
			delete(trees, ws)
			continue
		}
		s := t.Serialize()
		trees[ws] = &SerializedBSPTree{Root: toSessionNode(s.Root), AutoScheme: s.AutoScheme, DefaultRatio: s.DefaultRatio}
	}
	e.state.WorkspaceTrees, e.state.WindowToBSPID, e.state.NextBSPWindowID = trees, e.ids, e.next
	e.state.AutoTiling = true
	if e.state.LayoutMode == "" {
		e.state.LayoutMode = "bsp"
	}
}

// paneSize is a terminal size the daemon gives a pane it laid out.
type paneSize struct {
	pty        string
	cols, rows int
}

// place lays out the panes of every workspace the edit opened in the edit's
// box, and returns the terminal sizes that changed. A zoomed pane takes the
// whole box. The box of a pane counts its border, as the box of a window the
// daemon makes does.
func (e *layoutEdit) place() []paneSize {
	var sizes []paneSize
	for ws, t := range e.trees {
		rects := t.ApplyLayout(e.box, 0)
		zoomed := ""
		for i := range e.state.Windows {
			if w := &e.state.Windows[i]; w.Workspace == ws && w.Zoomed {
				zoomed = w.ID
			}
		}
		for i := range e.state.Windows {
			w := &e.state.Windows[i]
			r, ok := rects[e.ids[w.ID]]
			if w.Workspace != ws || !ok {
				continue
			}
			if w.ID == zoomed {
				r = e.box
			}
			// Laid out now, so a client that attaches keeps the layout
			// instead of applying its startup layout (stateUnarranged).
			unplaced := w.Unplaced
			w.Unplaced = false
			if !unplaced && w.X == r.X && w.Y == r.Y && w.Width == r.W && w.Height == r.H {
				continue
			}
			w.X, w.Y, w.Width, w.Height = r.X, r.Y, r.W, r.H
			sizes = append(sizes, paneSize{w.PTYID, max(r.W-2, 1), max(r.H-2, 1)})
		}
	}
	return sizes
}

// layoutBox is the box the daemon lays panes out in: the session's last size
// less the chrome every client keeps.
func (s *Session) layoutBox() layout.Rect {
	w, h := s.Size()
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}
	r := s.LayoutReserve()
	return layout.Rect{X: r.Left, Y: r.Top, W: max(w-r.Left-r.Right, 2), H: max(h-r.Top-r.Bottom, 2)}
}

// arrangeLayout runs edit on the session's trees and publishes the result.
// edit returns the workspace it changed. With place, nobody shows the
// session, and the daemon lays that workspace out itself. A tree edit counts
// as a peer's change for every client (see peerTreeChangedLocked), so a tree
// a client worked out before it is refused.
func (s *Session) arrangeLayout(place bool, edit func(state *SessionState, e *layoutEdit) (int, error)) error {
	box := s.layoutBox()
	var sizes []paneSize
	err := s.mutateState(func(state *SessionState) error {
		e := newLayoutEdit(state, box)
		ws, err := edit(state, e)
		if err != nil {
			return err
		}
		// A floating session keeps its rectangles: only a tiled one, or
		// one this edit tiles, is laid out.
		place = place && ws > 0 && (state.AutoTiling || len(e.trees) > 0)
		if place {
			e.tree(ws)
		}
		if len(e.trees) > 0 {
			e.commit()
			for ws := range e.trees {
				s.noteTreeOpLocked(state.Version+1, "", ws)
			}
		}
		if place {
			sizes = e.place()
		}
		// A zoom changes no tree and still moves the panes.
		state.DaemonLayout = state.Version + 1
		return nil
	})
	s.resizePanes(sizes)
	return err
}

// resizePanes gives each pane the size the daemon laid it out at.
func (s *Session) resizePanes(sizes []paneSize) {
	for _, z := range sizes {
		if pty := s.GetPTY(z.pty); pty != nil {
			_ = pty.Resize(z.cols, z.rows)
		}
	}
}

func toLayoutNode(n *SerializedBSPNode) *layout.SerializedNode {
	if n == nil {
		return nil
	}
	return &layout.SerializedNode{WindowID: n.WindowID, SplitType: n.SplitType, SplitRatio: n.SplitRatio, Left: toLayoutNode(n.Left), Right: toLayoutNode(n.Right)}
}

func toSessionNode(n *layout.SerializedNode) *SerializedBSPNode {
	if n == nil {
		return nil
	}
	return &SerializedBSPNode{WindowID: n.WindowID, SplitType: n.SplitType, SplitRatio: n.SplitRatio, Left: toSessionNode(n.Left), Right: toSessionNode(n.Right)}
}
