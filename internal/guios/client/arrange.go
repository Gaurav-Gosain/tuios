package client

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// tuios's layout engine. A workspace's layout is the session's: the BSP tree
// (layout-tree), and beside it the layout in use, whether tiling is on, the
// master-stack shape, the scrolling strip and the floating rectangles. guios
// lays the panes out from them with internal/layout, the code the TUI lays
// them out with, in the cells of the session's box, and draws the cells at
// its own scale.
//
// A change to the layout is the daemon's (the arrange verb, issue #162): it
// runs the engine (internal/session/arrange_engine.go) one op at a time per
// session, so two clients that change a layout at once both land. A daemon
// from before arrange gets the same engine run here, through guios's own
// connection, as guios did before.

// Layout names as guios shows them. Floating is tiling off.
const (
	modeBSP      = session.ArrangeModeBSP
	modeMaster   = session.ArrangeModeMaster
	modeScroll   = session.ArrangeModeScroll
	modeFloating = session.ArrangeModeFloating
)

type treeInfo = session.ArrangeTree

func tiled(wins []windowRow) []windowRow { return session.ArrangeTiled(wins) }

// The smallest pane the layout makes, and the refusal of a change that
// would make one smaller (the engine's, internal/session).
const (
	MinPaneCols = session.MinPaneCols
	MinPaneRows = session.MinPaneRows
)

var ErrTooSmall = session.ErrTooSmall

var (
	// arrangeMu runs the layout ops one at a time, here as the daemon does.
	arrangeMu sync.Mutex
	// noArrange says the daemon answered that it has no arrange verb.
	noArrange atomic.Bool
)

// settle writes back what a build worked out that the session does not hold
// yet, when no TUI is there to: the strip's columns with the panes it lacks
// in them, and a place for each floating pane no client has placed. A TUI
// does both itself. Without this, a pane the strip lacks would move each
// time the focus does, and an unplaced pane each time another is.
func (k *stateKeeper) settle(name string, ti *treeInfo, wins []windowRow, focus string, a *proto.Arrange) {
	if ti.TUI {
		return
	}
	params := map[string]any{}
	switch ti.Mode() {
	case modeScroll:
		sl, ids := ti.Strip(tiled(wins), focus)
		cols := session.ArrangeColumns(sl, ids)
		same := len(cols) == len(ti.LS.Scroll.Columns)
		for i := 0; same && i < len(cols); i++ {
			same = slices.Equal(cols[i].Windows, ti.LS.Scroll.Columns[i].Windows)
		}
		if !same {
			params["scroll_columns"] = cols
		}
	case modeFloating:
		var rs []session.WindowRect
		for _, w := range wins {
			if r, ok := a.Rects[w.ID]; ok && slices.Contains(ti.LS.Unplaced, w.ID) {
				rs = append(rs, session.WindowRect{Window: w.ID, X: r.X, Y: r.Y, Width: r.W, Height: r.H})
			}
		}
		if len(rs) > 0 {
			params["windows"] = rs
		}
	}
	if len(params) == 0 {
		return
	}
	params["session"], params["workspace"] = name, ti.WS
	go func() { _, _ = k.c.verb("set-layout-state", params) }()
}

// grid is guios's pane area in cells, as the app last measured it.
var grid struct {
	sync.Mutex
	r layout.Rect
}

// setGrid records guios's pane area in cells, and reports whether it
// changed.
func setGrid(cols, rows int) bool {
	grid.Lock()
	defer grid.Unlock()
	r := layout.Rect{W: cols, H: rows}
	if r == grid.r {
		return false
	}
	grid.r = r
	return true
}

func currentGrid() layout.Rect {
	grid.Lock()
	defer grid.Unlock()
	return grid.r
}

// arrangeOp is one change to a workspace's layout: the daemon's arrange,
// or the engine run here against a daemon from before it.
func (c *Client) arrangeOp(p proto.ArrangeParams) (proto.ArrangeResult, error) {
	arrangeMu.Lock()
	defer arrangeMu.Unlock()
	defer c.kickState()
	c.st.mu.Lock()
	ti, ok := c.st.trees[p.Session]
	name := c.st.sessionName[p.Session]
	wins := slices.Clone(c.st.shown[p.Session])
	c.st.mu.Unlock()
	if !ok || name == "" {
		return proto.ArrangeResult{}, errors.New("no such session")
	}
	// A pane floating out of the split tree: where it tiled, kept here,
	// where the state build puts it back when it tiles again (layoutOf).
	floated := func(pane string) {
		cur := c.st.readTree(name, ti.WS)
		if !cur.UseTree || cur.Tree == nil {
			return
		}
		if slot, ok := session.SlotOf(cur.Tree.Root, cur.Leaves, pane); ok {
			c.st.mu.Lock()
			c.st.floated[pane] = slot
			c.st.mu.Unlock()
		}
	}
	if !noArrange.Load() {
		if p.Op == "float" {
			pane := p.Pane
			if pane == "" {
				for _, w := range wins {
					if w.Focused {
						pane = w.ID
					}
				}
			}
			for _, w := range wins {
				if w.ID == pane && !w.Floating {
					floated(pane)
				}
			}
		}
		g := currentGrid()
		args := map[string]any{"session": name, "workspace": ti.WS, "op": p.Op, "mode": p.Mode, "pane": p.Pane, "target": p.Target,
			"ratio": p.Ratio, "delta": p.Delta, "cols": g.W, "rows": g.H}
		if p.Rect != nil {
			args["rect"] = p.Rect
		}
		if p.At != nil {
			args["at"] = p.At
		}
		raw, err := c.verb("arrange", args)
		var ve *session.VerbCallError
		if errors.As(err, &ve) && strings.Contains(ve.Message, ErrTooSmall.Error()) {
			// The daemon's refusal of a pane too small, as the app tells it.
			err = ErrTooSmall
		}
		var old *OldDaemonError
		if !errors.As(err, &old) {
			var r proto.ArrangeResult
			if err == nil {
				_ = jsonx.Unmarshal(raw, &r)
			}
			return r, err
		}
		noArrange.Store(true)
	}
	// The layout as the session has it now, so ops in a row each build on
	// the one before.
	ti = c.st.readTree(name, ti.WS)
	focus := p.Pane
	if focus == "" {
		for _, w := range wins {
			if w.Focused {
				focus = w.ID
			}
		}
	}
	return session.RunArrange(c.verb, name, ti, wins, focus, p, floated)
}
