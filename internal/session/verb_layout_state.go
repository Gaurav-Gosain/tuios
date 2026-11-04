package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
)

// set-layout-state: the rest of a workspace's layout for a client that draws
// the panes itself. layout-tree reads it beside the tree (layoutStateOf), and
// set-layout-state writes it: which layout the workspace uses, whether tiling
// is on, the master-stack shape and splits, the scrolling strip, the pane
// order, and the rectangles of floating panes. Each is a field the terminal
// client already keeps in the session and takes from every state it is sent,
// so a change made here is laid out by every attached client.

// maxLayoutStateWindows bounds the rectangles one call may set.
const maxLayoutStateWindows = 256

// errLayoutStateSame refuses a call that would change nothing, so it does
// not advance Version and wake every client for nothing.
var errLayoutStateSame = errors.New("layout state changes nothing")

// LayoutStatePayload is what set-layout-state changes. A nil field is left
// alone.
type LayoutStatePayload struct {
	Workspace      int                       `json:"workspace"`
	Mode           string                    `json:"mode"`
	Tiling         *bool                     `json:"tiling"`
	MasterRatio    *float64                  `json:"master_ratio"`
	StackRatio     *float64                  `json:"stack_ratio"`
	MasterSplits   *layout.MasterSplits      `json:"master_splits"`
	MasterPosition string                    `json:"master_position"`
	MasterCount    int                       `json:"master_count"`
	ScrollColumns  *[]SerializedScrollColumn `json:"scroll_columns"`
	ViewportX      *int                      `json:"viewport_x"`
	Order          []string                  `json:"order"`
	Windows        []WindowRect              `json:"windows"`
}

// WindowRect is a window's rectangle, in the cells of the session's box,
// and whether it floats over the tiling.
type WindowRect struct {
	Window   string `json:"window"`
	X        int    `json:"x"`
	Y        int    `json:"y"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Floating *bool  `json:"floating,omitempty"`
}

// layoutStateOf is the workspace's layout besides its tree, as layout-tree
// reports it.
func layoutStateOf(sess *Session, state *SessionState, ws int) map[string]any {
	mode := state.WorkspaceLayoutModes[ws]
	if mode == "" {
		mode = state.LayoutMode
	}
	w, h := sess.Size()
	r := sess.LayoutReserve()
	gap := 0
	if g := state.PaneGeometry; g != nil {
		gap = g.PaneGap
		if g.SharedBorders {
			gap = max(gap, 1)
		}
	}
	master := map[string]any{}
	if ml, ok := state.WorkspaceMasterLayout[ws]; ok {
		master["position"], master["count"], master["no_grid"] = ml.Position, ml.Count, ml.NoGrid
	}
	if v, ok := state.WorkspaceMasterRatio[ws]; ok {
		master["ratio"] = v
	} else if ws == state.CurrentWorkspace && state.MasterRatio > 0 {
		master["ratio"] = state.MasterRatio
	}
	if v, ok := state.WorkspaceStackRatio[ws]; ok {
		master["stack_ratio"] = v
	}
	if sp, ok := state.WorkspaceMasterSplits[ws]; ok && !sp.IsZero() {
		master["splits"] = sp
	}
	scroll := map[string]any{"columns": state.WorkspaceScrollColumns[ws]}
	if ws == state.CurrentWorkspace && state.ScrollStrip != nil {
		scroll["viewport_x"] = state.ScrollStrip.ViewportX
	}
	if g := state.PaneGeometry; g != nil && g.ScrollColumnWidth > 0 {
		scroll["column_width"] = g.ScrollColumnWidth
	}
	// The stacking order of the workspace's panes, and the ones no client
	// has placed: a client floating them draws them in that order, and
	// places those itself.
	z := map[string]int{}
	var unplaced []string
	for i := range state.Windows {
		if w := &state.Windows[i]; w.Workspace == ws {
			z[w.ID] = w.Z
			if w.Unplaced {
				unplaced = append(unplaced, w.ID)
			}
		}
	}
	return map[string]any{
		"z":        z,
		"unplaced": unplaced,
		"mode":     mode,
		"tiling":   state.AutoTiling,
		"box":      map[string]int{"x": r.Left, "y": r.Top, "w": max(w-r.Left-r.Right, 0), "h": max(h-r.Top-r.Bottom, 0)},
		"gap":      gap,
		"master":   master,
		"scroll":   scroll,
	}
}

// verbSetLayoutState answers set-layout-state.
func (d *Daemon) verbSetLayoutState(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		LayoutStatePayload
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	if p.Workspace == 0 {
		p.Workspace = sess.GetState().CurrentWorkspace
	}
	if p.Mode != "" && !slices.Contains([]string{config.LayoutModeBSP, config.LayoutModeMasterStack, config.LayoutModeScrolling}, p.Mode) {
		return nil, invalidParam("mode", "use bsp, master-stack or scrolling", config.LayoutModeBSP, config.LayoutModeMasterStack, config.LayoutModeScrolling)
	}
	if p.MasterPosition != "" && !slices.Contains(config.MasterPositions, p.MasterPosition) {
		return nil, invalidParam("master_position", "use one of the master positions", config.MasterPositions...)
	}
	if p.MasterCount != 0 && (p.MasterCount < config.MasterCountMin || p.MasterCount > config.MasterCountMax) {
		return nil, invalidParam("master_count", fmt.Sprintf("use a number from %d to %d", config.MasterCountMin, config.MasterCountMax))
	}
	for _, r := range []*float64{p.MasterRatio, p.StackRatio} {
		if r != nil && (*r < 0 || *r >= 1) {
			return nil, invalidParam("master_ratio", "use a ratio from 0 to 1, or 0 for the default")
		}
	}
	if len(p.Windows) > maxLayoutStateWindows || len(p.Order) > maxLayoutStateWindows {
		return nil, invalidParam("windows", fmt.Sprintf("set at most %d windows", maxLayoutStateWindows))
	}
	if p.ScrollColumns != nil && len(*p.ScrollColumns) > maxLayoutStateWindows {
		return nil, invalidParam("scroll_columns", fmt.Sprintf("set at most %d columns", maxLayoutStateWindows))
	}
	applied, err := sess.ApplyLayoutState(&p.LayoutStatePayload)
	if err != nil {
		return nil, invalidParam("workspace", err.Error())
	}
	if applied {
		sess.emit(SessionEvent{Type: EventLayoutChanged, Workspace: p.Workspace})
	}
	return map[string]any{"type": "layout_state_set", "session": sess.Name(), "workspace": p.Workspace, "changed": applied}, nil
}

// ApplyLayoutState writes a workspace's layout state, under mutateState so
// every attached client takes it. It reports whether anything changed.
func (s *Session) ApplyLayoutState(p *LayoutStatePayload) (bool, error) {
	snap, err := s.mutateStateLocked(func(state *SessionState) error {
		ws := p.Workspace
		if ws < 1 || ws > state.workspaceBound() {
			return fmt.Errorf("workspace %d is out of range", ws)
		}
		on := make(map[string]int, len(state.Windows))
		for i := range state.Windows {
			if state.Windows[i].Workspace == ws {
				on[state.Windows[i].ID] = i
			}
		}
		for _, r := range p.Windows {
			if _, ok := on[r.Window]; !ok {
				return fmt.Errorf("window %s is not on workspace %d", r.Window, ws)
			}
			if r.Width < 1 || r.Height < 1 || r.Width > 10000 || r.Height > 10000 || r.X < -10000 || r.Y < -10000 || r.X > 10000 || r.Y > 10000 {
				return fmt.Errorf("window %s has no usable rectangle", r.Window)
			}
		}
		if p.ScrollColumns != nil {
			seen := map[string]bool{}
			for _, c := range *p.ScrollColumns {
				for _, id := range c.Windows {
					if _, ok := on[id]; !ok || seen[id] {
						return fmt.Errorf("column window %s is not on workspace %d, or is in two columns", id, ws)
					}
					seen[id] = true
				}
			}
		}
		if _, ok := state.WorkspaceMasterLayout[ws]; !ok && (p.MasterPosition != "" || p.MasterCount != 0) && len(state.WorkspaceMasterLayout) >= maxMasterLayouts {
			return fmt.Errorf("the session already holds %d master layouts", maxMasterLayouts)
		}
		before := *state
		before.Windows = slices.Clone(state.Windows)
		changed := false
		set := func(ok bool) { changed = changed || ok }

		if p.Mode != "" {
			set(state.LayoutMode != p.Mode || state.WorkspaceLayoutModes[ws] != p.Mode)
			modes := maps.Clone(state.WorkspaceLayoutModes)
			if modes == nil {
				modes = map[int]string{}
			}
			modes[ws] = p.Mode
			state.WorkspaceLayoutModes = modes
			if ws == state.CurrentWorkspace {
				state.LayoutMode = p.Mode
			}
		}
		if p.Tiling != nil {
			set(state.AutoTiling != *p.Tiling)
			state.AutoTiling = *p.Tiling
		}
		if p.MasterRatio != nil {
			set(state.WorkspaceMasterRatio[ws] != *p.MasterRatio)
			m := maps.Clone(state.WorkspaceMasterRatio)
			if m == nil {
				m = map[int]float64{}
			}
			m[ws] = *p.MasterRatio
			state.WorkspaceMasterRatio = m
			if ws == state.CurrentWorkspace {
				state.MasterRatio = *p.MasterRatio
			}
		}
		if p.StackRatio != nil {
			set(state.WorkspaceStackRatio[ws] != *p.StackRatio)
			m := maps.Clone(state.WorkspaceStackRatio)
			if m == nil {
				m = map[int]float64{}
			}
			m[ws] = *p.StackRatio
			state.WorkspaceStackRatio = m
		}
		if p.MasterSplits != nil {
			set(!state.WorkspaceMasterSplits[ws].Equal(*p.MasterSplits))
			m := cloneMasterSplits(state.WorkspaceMasterSplits)
			if m == nil {
				m = map[int]layout.MasterSplits{}
			}
			m[ws] = p.MasterSplits.Clone()
			state.WorkspaceMasterSplits = m
		}
		if p.MasterPosition != "" || p.MasterCount != 0 {
			ml, ok := state.WorkspaceMasterLayout[ws]
			if !ok {
				ml = MasterLayoutState{Position: config.MasterPositionLeft, Count: 1}
			}
			if p.MasterPosition != "" {
				ml.Position = p.MasterPosition
			}
			if p.MasterCount != 0 {
				ml.Count = p.MasterCount
			}
			set(!ok || state.WorkspaceMasterLayout[ws] != ml)
			m := maps.Clone(state.WorkspaceMasterLayout)
			if m == nil {
				m = map[int]MasterLayoutState{}
			}
			m[ws] = ml
			state.WorkspaceMasterLayout = m
		}
		if p.ScrollColumns != nil {
			cols := slices.Clone(*p.ScrollColumns)
			set(!slices.EqualFunc(cols, state.WorkspaceScrollColumns[ws], func(a, b SerializedScrollColumn) bool {
				return slices.Equal(a.Windows, b.Windows) && a.Proportion == b.Proportion && a.FixedWidth == b.FixedWidth && a.Active == b.Active
			}))
			m := maps.Clone(state.WorkspaceScrollColumns)
			if m == nil {
				m = map[int][]SerializedScrollColumn{}
			}
			m[ws] = cols
			state.WorkspaceScrollColumns = m
		}
		if p.ViewportX != nil && ws == state.CurrentWorkspace {
			set(state.ScrollStrip == nil || state.ScrollStrip.ViewportX != *p.ViewportX)
			state.ScrollStrip = &ScrollStripState{ViewportX: *p.ViewportX}
		}
		if len(p.Order) > 1 {
			// The windows named take the places they hold between them, in
			// the order given; every other window keeps its place.
			var at []int
			var named []WindowState
			seen := map[string]bool{}
			for _, id := range p.Order {
				if i, ok := on[id]; ok && !seen[id] {
					seen[id] = true
					at = append(at, i)
					named = append(named, state.Windows[i])
				}
			}
			slices.Sort(at)
			wins := slices.Clone(state.Windows)
			for k, i := range at {
				set(wins[i].ID != named[k].ID)
				wins[i] = named[k]
			}
			state.Windows = wins
			for i := range wins {
				if wins[i].Workspace == ws {
					on[wins[i].ID] = i
				}
			}
		}
		top := 0
		for i := range state.Windows {
			top = max(top, state.Windows[i].Z)
		}
		for _, r := range p.Windows {
			w := &state.Windows[on[r.Window]]
			if w.X != r.X || w.Y != r.Y || w.Width != r.Width || w.Height != r.Height {
				set(true)
				w.X, w.Y, w.Width, w.Height = r.X, r.Y, r.Width, r.Height
				w.Unplaced = false
				// A pane moved by hand comes to the front.
				if w.Z < top {
					top++
					w.Z = top
				}
			}
			if r.Floating != nil && w.IsFloating != *r.Floating {
				set(true)
				w.IsFloating = *r.Floating
			}
		}
		if !changed {
			*state = before
			return errLayoutStateSame
		}
		return nil
	})
	switch {
	case errors.Is(err, errLayoutStateSame):
		return false, nil
	case err != nil:
		return false, err
	}
	s.publishState(snap)
	return true, nil
}
