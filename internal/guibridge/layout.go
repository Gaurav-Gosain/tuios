package guibridge

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
)

// The split trees in the state, and the layout commands that edit them.
//
// The model is the authority for the tree. The renderer mirrors it from the
// state, works out pixel rectangles itself, and names each gesture's result as
// one command: put this pane beside that one, set this split's ratio. The
// model runs it, the tree changes, and the next state carries the new tree.

// Rect is a box in cells.
type Rect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

func rectOf(r layout.Rect) Rect { return Rect{X: r.X, Y: r.Y, W: r.W, H: r.H} }

// Tree is one workspace's split tree.
type Tree struct {
	// Bounds is the box the tree is laid out in, and Gap the cells kept
	// between two neighbouring panes. Rect on every node follows from them.
	Bounds Rect `json:"bounds"`
	Gap    int  `json:"gap"`
	// Scheme is where the tiler puts the next new pane: spiral,
	// longest_side, alternate or smart_split.
	Scheme string `json:"scheme"`
	Root   *Node  `json:"root"`
}

// Node is a split or a pane in a Tree.
type Node struct {
	// ID names the node while the tree holds it. A split keeps its ID across
	// every edit that keeps the split, so set-ratio names a split by it. A
	// tree that another client replaces comes back with new IDs.
	ID uint64 `json:"id"`
	// Window is set on a leaf: the pane's window ID.
	Window string `json:"window,omitempty"`
	// Axis is set on a split: "x" puts A left of B, "y" puts A above B.
	Axis string `json:"axis,omitempty"`
	// Ratio is the share of the split's box that A takes, 0 to 1.
	Ratio float64 `json:"ratio,omitempty"`
	// Rect is the node's box, in cells.
	Rect Rect  `json:"rect"`
	A    *Node `json:"a,omitempty"`
	B    *Node `json:"b,omitempty"`
}

// exportTrees returns every workspace's tree, keyed by workspace number as a
// string (JSON object keys are strings). Nil while tiling is off or the
// layout on screen is not the split tree.
func exportTrees(o *app.OS) map[string]*Tree {
	if o.LayoutName() != app.LayoutModeBSP {
		return nil
	}
	bounds := o.GetBSPBounds()
	gap := o.SeparatorGap()
	out := map[string]*Tree{}
	for ws, tree := range o.WorkspaceTrees {
		if tree == nil || tree.IsEmpty() {
			continue
		}
		out[strconv.Itoa(ws)] = &Tree{
			Bounds: rectOf(bounds),
			Gap:    gap,
			Scheme: tree.AutoScheme.String(),
			Root:   exportNode(o, tree.Root, bounds, gap),
		}
	}
	return out
}

func exportNode(o *app.OS, n *layout.TileNode, box layout.Rect, gap int) *Node {
	if n == nil {
		return nil
	}
	node := &Node{ID: n.ID, Rect: rectOf(box)}
	if n.IsLeaf() {
		node.Window = o.BSPIDToWindowID[n.WindowID]
		return node
	}
	node.Axis = "y"
	if n.SplitType == layout.SplitVertical {
		node.Axis = "x"
	}
	node.Ratio = n.SplitRatio
	near, far := layout.SplitBounds(n, box, gap)
	node.A = exportNode(o, n.Left, near, gap)
	node.B = exportNode(o, n.Right, far, gap)
	return node
}

// parseSide reads the side of a move: left, right, top or bottom.
func parseSide(s string) (layout.Side, error) {
	switch s {
	case "left":
		return layout.SideLeft, nil
	case "right":
		return layout.SideRight, nil
	case "top":
		return layout.SideUp, nil
	case "bottom":
		return layout.SideDown, nil
	}
	return 0, fmt.Errorf("side %q is not left, right, top or bottom", s)
}

// runLayout runs one layout command. The result says what changed, for the
// renderer to check its own prediction against.
func runLayout(o *app.OS, c Command) (*Result, error) {
	switch c.Op {
	case "set-ratio":
		if c.Split == 0 {
			return nil, errors.New("set-ratio needs split, a split's node id")
		}
		got, err := o.SetSplitRatio(c.Split, c.Ratio)
		if err != nil {
			return nil, err
		}
		return &Result{Ratio: got}, nil
	case "swap":
		if c.A == "" || c.B == "" {
			return nil, errors.New("swap needs a and b, two pane ids")
		}
		return nil, o.SwapWindowsByID(c.A, c.B)
	case "move":
		side, err := parseSide(c.Side)
		if err != nil {
			return nil, err
		}
		if c.ID == "" || c.Target == "" {
			return nil, errors.New("move needs id and target, two pane ids")
		}
		return nil, o.MovePaneBeside(c.ID, c.Target, side)
	case "move-root":
		side, err := parseSide(c.Side)
		if err != nil {
			return nil, err
		}
		if c.ID == "" {
			return nil, errors.New("move-root needs id, a pane id")
		}
		return nil, o.MovePaneToRoot(c.ID, side)
	case "equalize":
		if o.LayoutName() == app.LayoutFloating {
			return nil, errors.New("tiling is off")
		}
		o.EqualizeSplits()
		o.SyncStateToDaemon()
		return nil, nil
	case "float-geometry":
		if c.ID == "" || c.Rect == nil {
			return nil, errors.New("float-geometry needs id and rect")
		}
		return nil, o.SetFloatingGeometry(c.ID, c.Rect.X, c.Rect.Y, c.Rect.W, c.Rect.H)
	case "toggle-float":
		id := c.ID
		if id == "" {
			if f := o.GetFocusedWindow(); f != nil {
				id = f.ID
			}
		}
		if id == "" {
			return nil, errors.New("no pane to float")
		}
		return nil, o.ToggleFloatingByID(id)
	case "":
		return nil, errors.New("layout needs op")
	}
	return nil, fmt.Errorf("layout op %q is not known", c.Op)
}
