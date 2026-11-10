package session

import (
	"encoding/json"

	"github.com/Gaurav-Gosain/tuios/internal/layout"
)

// The layout engine's types (arrange_engine.go). guios's protocol uses them
// as they are (internal/guios/proto aliases them), so the engine guios runs
// against an older daemon and the one the arrange verb runs here take and
// give the same values.

// ArrangeView is a workspace's panes laid out: the box, each pane's
// rectangle, the floating panes in order, and the line between the masters
// and the stack in the master-stack layout.
type ArrangeView struct {
	Box   ArrangeRect            `json:"box"`
	Rects map[string]ArrangeRect `json:"rects"`
	Float []string               `json:"float,omitempty"`
	// Master is the line between the masters and the stack in the
	// master-stack layout, which a drag moves; nil when there is none to
	// move, as in the grid.
	Master *MasterLine `json:"master,omitempty"`
}

// MasterLine is where the masters end: the cell At across the box, a
// line from top to bottom when Vertical, and the masters before it (left
// or above) when Before.
type MasterLine struct {
	Vertical bool `json:"vertical"`
	At       int  `json:"at"`
	Before   bool `json:"before"`
}

// ArrangeRect is a rectangle in cells.
type ArrangeRect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// ArrangeResult is what a change to the layout did beyond the layout:
// Focus is the pane it moved the focus to, "" when it moved none.
type ArrangeResult struct {
	Focus string `json:"focus,omitempty"`
}

// ArrangeParams changes a session's layout: Op "mode" (Mode "bsp",
// "master-stack", "scrolling" or "floating"), "next", "tiling", "rotate",
// "equalize", "master.position", "master.add", "master.remove",
// "master.swap", "master.ratio" (Ratio), "scroll.left", "scroll.right",
// "scroll.width", "scroll.max", "scroll.consume", "scroll.expel",
// "scroll.moveLeft", "scroll.moveRight", "scroll.view" (Delta cells),
// "scroll.reveal", "rect" (Pane to Rect, snapped when At is at an edge),
// "snap" (Mode "left", "right", "top-left", "top-right", "bottom-left",
// "bottom-right", "full" or "unsnap"), "float", "swap" (Pane and
// Target), "minimize" or "restore" (Pane when it is minimized, else the
// Delta-th minimized pane in the order they were minimized, every one with
// 0). Pane is the pane it acts on, the focused one when empty.
type ArrangeParams struct {
	Session string       `json:"session"`
	Op      string       `json:"op"`
	Mode    string       `json:"mode,omitempty"`
	Pane    string       `json:"pane,omitempty"`
	Target  string       `json:"target,omitempty"`
	Rect    *ArrangeRect `json:"rect,omitempty"`
	At      *ArrangeRect `json:"at,omitempty"`
	Ratio   float64      `json:"ratio,omitempty"`
	Delta   int          `json:"delta,omitempty"`
	// For place: the split the client asked for (Asked, Target, Mode the
	// side), else Target the pane the focus was on before the new panes,
	// and Slot where the one new pane tiled before it floated.
	Asked bool       `json:"asked,omitempty"`
	Slot  *FloatSlot `json:"slot,omitempty"`
}

// VerbCaller runs one of the daemon's verbs and returns its JSON result: the
// connection of a client (guios against a daemon from before arrange), or the
// daemon's own call for the client that asked (the arrange verb).
type VerbCaller func(verb string, params any) (json.RawMessage, error)

// ToLayoutNode is a session tree node as internal/layout reads it.
func ToLayoutNode(n *SerializedBSPNode) *layout.SerializedNode { return toLayoutNode(n) }
