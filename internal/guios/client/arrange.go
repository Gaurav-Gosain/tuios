package client

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/adrg/xdg"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// tuios's layout engine, run here. A workspace's layout is the session's:
// the BSP tree (layout-tree), and beside it the layout in use, whether tiling
// is on, the master-stack shape, the scrolling strip and the floating
// rectangles (the rest of layout-tree's answer). guios lays the panes out
// from them with internal/layout, the code the TUI lays them out with, in the
// cells of the session's box, and draws the cells at its own scale. A change
// made here goes back to the session (set-layout-tree, set-layout-state), so
// a TUI on the same session lays the workspace out the same way, and every
// layout works with no TUI attached.

// Layout names as guios shows them. Floating is tiling off.
const (
	modeBSP      = config.LayoutModeBSP
	modeMaster   = config.LayoutModeMasterStack
	modeScroll   = config.LayoutModeScrolling
	modeFloating = "floating"
)

// layoutState is the rest of a workspace's layout, as layout-tree reads it.
type layoutState struct {
	Mode   string `json:"mode"`
	Tiling bool   `json:"tiling"`
	Box    struct {
		X int `json:"x"`
		Y int `json:"y"`
		W int `json:"w"`
		H int `json:"h"`
	} `json:"box"`
	Gap    int `json:"gap"`
	Master struct {
		Position   string              `json:"position"`
		Count      int                 `json:"count"`
		NoGrid     bool                `json:"no_grid"`
		Ratio      float64             `json:"ratio"`
		StackRatio float64             `json:"stack_ratio"`
		Splits     layout.MasterSplits `json:"splits"`
	} `json:"master"`
	Scroll struct {
		Columns     []session.SerializedScrollColumn `json:"columns"`
		ViewportX   *int                             `json:"viewport_x"`
		ColumnWidth int                              `json:"column_width"`
	} `json:"scroll"`
	Z        map[string]int `json:"z"`
	Unplaced []string       `json:"unplaced"`
}

// layoutConf is what the engine reads from tuios's config.toml: the
// defaults a workspace nobody changed is laid out with, as the TUI reads
// them.
type layoutConf struct {
	set     config.Settings
	tiled   bool   // [startup] tiled
	layout  string // [startup] layout
	path    string
	modTime time.Time
}

var (
	confMu sync.Mutex
	conf   *layoutConf
	// arrangeMu runs the layout ops one at a time.
	arrangeMu sync.Mutex
)

// loadLayoutConf reads config.toml, again only when it changed.
func loadLayoutConf() *layoutConf {
	confMu.Lock()
	defer confMu.Unlock()
	path, _ := xdg.SearchConfigFile("tuios/config.toml")
	var mod time.Time
	if fi, err := os.Stat(path); err == nil {
		mod = fi.ModTime()
	}
	if conf != nil && conf.path == path && conf.modTime.Equal(mod) {
		return conf
	}
	cfg, err := config.LoadUserConfig()
	if err != nil || cfg == nil {
		cfg = config.DefaultConfig()
	}
	conf = &layoutConf{
		set:     config.AppearanceFrom(cfg, config.Overrides{}),
		tiled:   cfg.Startup.Tiled,
		layout:  cfg.Startup.Layout,
		path:    path,
		modTime: mod,
	}
	return conf
}

// mode is the layout the workspace shows. A session no client has laid out
// says neither a mode nor tiling, and takes [startup] as a TUI attaching to
// it would.
func (ti *treeInfo) mode() string {
	ls := ti.ls
	if ls.Mode == "" && !ls.Tiling {
		c := loadLayoutConf()
		if !c.tiled {
			return modeFloating
		}
		return validMode(c.layout)
	}
	if !ls.Tiling {
		return modeFloating
	}
	return validMode(ls.Mode)
}

// tiledMode is the layout tiling uses, on or off.
func (ti *treeInfo) tiledMode() string {
	if ti.ls.Mode == "" {
		return validMode(loadLayoutConf().layout)
	}
	return validMode(ti.ls.Mode)
}

func validMode(m string) string {
	if m == modeMaster || m == modeScroll {
		return m
	}
	return modeBSP
}

// bounds is the box the panes are laid out in, in cells. With a TUI
// attached it is the TUI's, the box the session settled on, so both lay
// the panes out in the same cells. With none it is guios's own pane area
// in cells (setGrid): the session's size is then whatever a client last
// set, as small as 20x12 for a session guios made, and a layout worked out
// in so few cells rounds its rows and columns to nothing, while the
// scrolling strip's columns and a floating pane's smallest size fill it.
func (ti *treeInfo) bounds() layout.Rect {
	if !ti.tui && ti.grid.W >= 4 && ti.grid.H >= 2 {
		return ti.grid
	}
	b := ti.ls.Box
	if b.W < 4 || b.H < 2 {
		return layout.Rect{W: 160, H: 45}
	}
	return layout.Rect{X: b.X, Y: b.Y, W: b.W, H: b.H}
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

// bspTree is the workspace's tree, its leaves numbered as the session
// numbers them, with the window of each number and the number of each
// window. A workspace with no tree gets an empty one with the configured
// scheme, as the TUI makes one.
func (ti *treeInfo) bspTree() (*layout.BSPTree, map[int]string, map[string]int) {
	var t *layout.BSPTree
	if ti.tree != nil && ti.tree.Root != nil {
		t = (&layout.SerializedBSPTree{Root: toLayoutNode(ti.tree.Root), AutoScheme: ti.tree.AutoScheme, DefaultRatio: ti.tree.DefaultRatio}).Deserialize()
	} else {
		t = layout.NewBSPTree()
		scheme := loadLayoutConf().set.TilingScheme
		if scheme == "" {
			scheme = config.TilingSchemeSpiral
		}
		t.AutoScheme = layout.ParseAutoScheme(scheme)
	}
	leaves := make(map[int]string, len(ti.leaves))
	nums := make(map[string]int, len(ti.leaves))
	for n, id := range ti.leaves {
		leaves[n] = id
		nums[id] = n
	}
	return t, leaves, nums
}

// number is a window's leaf number, a new one for a window the tree lacks.
func number(leaves map[int]string, nums map[string]int, id string) int {
	if n, ok := nums[id]; ok {
		return n
	}
	n := 1
	for k := range leaves {
		n = max(n, k+1)
	}
	leaves[n], nums[id] = id, n
	return n
}

func toLayoutNode(n *session.SerializedBSPNode) *layout.SerializedNode {
	if n == nil {
		return nil
	}
	return &layout.SerializedNode{WindowID: n.WindowID, SplitType: n.SplitType, SplitRatio: n.SplitRatio, Left: toLayoutNode(n.Left), Right: toLayoutNode(n.Right)}
}

func toSessionNode(n *layout.SerializedNode) *session.SerializedBSPNode {
	if n == nil {
		return nil
	}
	return &session.SerializedBSPNode{WindowID: n.WindowID, SplitType: n.SplitType, SplitRatio: n.SplitRatio, Left: toSessionNode(n.Left), Right: toSessionNode(n.Right)}
}

// sessionTree is t as set-layout-tree takes it.
func sessionTree(t *layout.BSPTree) *session.SerializedBSPTree {
	if t == nil || t.IsEmpty() {
		return nil
	}
	s := t.Serialize()
	return &session.SerializedBSPTree{Root: toSessionNode(s.Root), AutoScheme: s.AutoScheme, DefaultRatio: s.DefaultRatio}
}

// tiled is the workspace's panes the layout places, in the session's order:
// those that do not float over it.
func tiled(wins []windowRow) []windowRow {
	out := make([]windowRow, 0, len(wins))
	for _, w := range wins {
		if !w.Floating {
			out = append(out, w)
		}
	}
	return out
}

// masterParams is what the master-stack tiler reads for the workspace, as
// the TUI's masterParams builds it.
func (ti *treeInfo) masterParams() layout.MasterParams {
	c := loadLayoutConf()
	m := ti.ls.Master
	pos, count, noGrid := c.set.MasterPosition, c.set.MasterCount, c.set.MasterNoGrid
	if m.Position != "" {
		pos, count, noGrid = m.Position, m.Count, m.NoGrid
	}
	ratio := m.Ratio
	if ratio <= 0 {
		ratio = c.set.MasterRatioFraction()
	}
	return layout.MasterParams{
		Position:   config.ValidMasterPosition(pos),
		Count:      config.ClampMasterCount(count),
		Ratio:      ratio,
		StackRatio: m.StackRatio,
		Splits:     m.Splits,
		Grid:       !noGrid,
		Gap:        ti.ls.Gap,
	}
}

// strip is the workspace's scrolling strip, its windows numbered by their
// place in wins, as the TUI's GetOrCreateScrollingLayout builds it: the
// session's columns, a column for each pane they lack, and the focused
// pane's column focused.
func (ti *treeInfo) strip(wins []windowRow, focus string) (*layout.ScrollingLayout, []string) {
	c := loadLayoutConf()
	ids := make([]string, len(wins))
	num := make(map[string]int, len(wins))
	for i, w := range wins {
		ids[i] = w.ID
		num[w.ID] = i + 1
	}
	sl := layout.NewScrollingLayout()
	sl.Gap = ti.ls.Gap
	width := ti.ls.Scroll.ColumnWidth
	if width == 0 {
		width = c.set.ScrollColumnWidth
	}
	sl.DefaultWidth = float64(min(max(width, config.ScrollColumnWidthMin), c.set.GetScrollColumnMax())) / 100
	sl.MaxProportion = float64(c.set.GetScrollColumnMax()) / 100
	if len(c.set.ScrollColumnPresets) > 0 {
		sl.PresetWidths = c.set.ScrollColumnPresets
	}
	for _, col := range ti.ls.Scroll.Columns {
		var wids []int
		for _, id := range col.Windows {
			if n := num[id]; n > 0 && !sl.HasWindow(n) {
				wids = append(wids, n)
			}
		}
		if len(wids) == 0 {
			continue
		}
		sl.Columns = append(sl.Columns, layout.ScrollColumn{WindowIDs: wids, Proportion: col.Proportion, FixedWidth: col.FixedWidth, Active: min(max(col.Active, 0), len(wids)-1)})
	}
	if n := num[focus]; n > 0 {
		sl.FocusColumnContaining(n)
	}
	for i := range wins {
		if !sl.HasWindow(i + 1) {
			sl.AddColumn(i + 1)
		}
	}
	if n := num[focus]; n > 0 {
		sl.FocusColumnContaining(n)
	}
	if v := ti.ls.Scroll.ViewportX; v != nil {
		sl.ViewportX = *v
	} else {
		sl.EnsureFocusedVisible(ti.bounds().W)
	}
	return sl, ids
}

// columns is a strip's columns as the session keeps them.
func columns(sl *layout.ScrollingLayout, ids []string) []session.SerializedScrollColumn {
	out := make([]session.SerializedScrollColumn, 0, len(sl.Columns))
	for _, col := range sl.Columns {
		sc := session.SerializedScrollColumn{Proportion: col.Proportion, FixedWidth: col.FixedWidth, Active: col.Active}
		for _, n := range col.WindowIDs {
			if n >= 1 && n <= len(ids) {
				sc.Windows = append(sc.Windows, ids[n-1])
			}
		}
		out = append(out, sc)
	}
	return out
}

// arrange is the rectangles of a workspace's panes in every layout but the
// split tree, and of the panes floating over any layout.
func (ti *treeInfo) arrange(wins []windowRow, focus string) *proto.Arrange {
	b := ti.bounds()
	a := &proto.Arrange{Box: proto.Rect{X: b.X, Y: b.Y, W: b.W, H: b.H}, Rects: map[string]proto.Rect{}}
	put := func(id string, r layout.Rect) {
		a.Rects[id] = proto.Rect{X: r.X, Y: r.Y, W: max(r.W, 1), H: max(r.H, 1)}
	}
	mode := ti.mode()
	floats := wins
	if mode != modeFloating {
		floats = nil
		for _, w := range wins {
			if w.Floating {
				floats = append(floats, w)
			}
		}
	}
	switch t := tiled(wins); mode {
	case modeMaster:
		mp := ti.masterParams()
		rects := layout.CalculateMasterLayout(len(t), b.W, b.H, b.Y, mp)
		var masters layout.Rect
		for i, r := range rects {
			if i < len(t) {
				lr := layout.Rect{X: r.X + b.X, Y: r.Y, W: r.Width, H: r.Height}
				put(t[i].ID, lr)
				if i < mp.Count {
					masters = union(masters, lr, i == 0)
				}
			}
		}
		a.Master = masterLine(masters, b, len(t) > mp.Count)
	case modeScroll:
		sl, ids := ti.strip(t, focus)
		sl.ClampViewport(b.W)
		for n, r := range sl.ComputePositions(b.W, b.H, b.Y) {
			if n >= 1 && n <= len(ids) {
				r.X += b.X
				put(ids[n-1], r)
			}
		}
	}
	// Floating panes keep their own rectangles, the topmost last. One with
	// no rectangle yet (a pane the daemon made, which no client placed)
	// takes the centred box, each one a step further down and right.
	z := ti.ls.Z
	slices.SortStableFunc(floats, func(x, y windowRow) int { return z[x.ID] - z[y.ID] })
	step := 0
	for _, w := range floats {
		r := layout.Rect{X: w.X, Y: w.Y, W: w.Width, H: w.Height}
		if slices.Contains(ti.ls.Unplaced, w.ID) || r.W < 2 || r.H < 2 {
			r = layout.SnapBounds(b, layout.Unsnap)
			r.X += 2 * step
			r.Y += step
			step++
		}
		put(w.ID, r)
		a.Float = append(a.Float, w.ID)
	}
	return a
}

// settle writes back what a build worked out that the session does not hold
// yet, when no TUI is there to: the strip's columns with the panes it lacks
// in them, and a place for each floating pane no client has placed. A TUI
// does both itself. Without this, a pane the strip lacks would move each
// time the focus does, and an unplaced pane each time another is.
func (k *stateKeeper) settle(name string, ti *treeInfo, wins []windowRow, focus string, a *proto.Arrange) {
	if ti.tui {
		return
	}
	params := map[string]any{}
	switch ti.mode() {
	case modeScroll:
		sl, ids := ti.strip(tiled(wins), focus)
		cols := columns(sl, ids)
		same := len(cols) == len(ti.ls.Scroll.Columns)
		for i := 0; same && i < len(cols); i++ {
			same = slices.Equal(cols[i].Windows, ti.ls.Scroll.Columns[i].Windows)
		}
		if !same {
			params["scroll_columns"] = cols
		}
	case modeFloating:
		var rs []session.WindowRect
		for _, w := range wins {
			if r, ok := a.Rects[w.ID]; ok && slices.Contains(ti.ls.Unplaced, w.ID) {
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
	params["session"], params["workspace"] = name, ti.ws
	go func() { _, _ = k.c.verb("set-layout-state", params) }()
}

// union is the rectangle around a and b, b alone when first.
func union(a, b layout.Rect, first bool) layout.Rect {
	if first {
		return b
	}
	x, y := min(a.X, b.X), min(a.Y, b.Y)
	return layout.Rect{X: x, Y: y, W: max(a.X+a.W, b.X+b.W) - x, H: max(a.Y+a.H, b.Y+b.H) - y}
}

// masterLine is the edge of the masters that faces the stack, when the
// masters fill one side of the box: none in the grid, or with the masters
// in the centre, or with no stack.
func masterLine(m, b layout.Rect, stack bool) *proto.MasterLine {
	if !stack || m.W <= 0 {
		return nil
	}
	fullH, fullW := m.Y <= b.Y && m.Y+m.H >= b.Y+b.H, m.X <= b.X && m.X+m.W >= b.X+b.W
	switch {
	case fullH && m.X <= b.X:
		return &proto.MasterLine{Vertical: true, At: m.X + m.W, Before: true}
	case fullH && m.X+m.W >= b.X+b.W:
		return &proto.MasterLine{Vertical: true, At: m.X}
	case fullW && m.Y <= b.Y:
		return &proto.MasterLine{At: m.Y + m.H, Before: true}
	case fullW && m.Y+m.H >= b.Y+b.H:
		return &proto.MasterLine{At: m.Y}
	}
	return nil
}

// arrangeOp is one change to a workspace's layout, worked out on the
// session's state and written back.
func (c *Client) arrangeOp(p proto.ArrangeParams) (proto.ArrangeResult, error) {
	// One at a time, in the order they came: each reads the layout the one
	// before it wrote.
	arrangeMu.Lock()
	defer arrangeMu.Unlock()
	c.st.mu.Lock()
	ti, ok := c.st.trees[p.Session]
	name := c.st.sessionName[p.Session]
	wins := slices.Clone(c.st.shown[p.Session])
	c.st.mu.Unlock()
	if !ok || name == "" {
		return proto.ArrangeResult{}, errors.New("no such session")
	}
	// The layout as the session has it now, so ops in a row each build on
	// the one before.
	ti = c.st.readTree(name, ti.ws)
	focus := p.Pane
	if focus == "" {
		for _, w := range wins {
			if w.Focused {
				focus = w.ID
			}
		}
	}
	e := &engine{c: c, name: name, ti: ti, wins: wins, focus: focus}
	defer c.kickState()
	err := e.run(p)
	return e.result, err
}

// engine runs one op on a workspace.
type engine struct {
	c      *Client
	name   string
	ti     treeInfo
	wins   []windowRow
	focus  string
	state  map[string]any // set-layout-state params gathered by the op
	result proto.ArrangeResult
}

func (e *engine) set(k string, v any) {
	if e.state == nil {
		e.state = map[string]any{"session": e.name, "workspace": e.ti.ws}
	}
	e.state[k] = v
	// The engine's own copy follows, so flush lays the panes out as the
	// session will have them.
	ls := &e.ti.ls
	switch k {
	case "mode":
		ls.Mode = v.(string)
	case "tiling":
		ls.Tiling = v.(bool)
	case "master_ratio":
		ls.Master.Ratio = toFloat(v)
	case "stack_ratio":
		ls.Master.StackRatio = toFloat(v)
	case "master_splits":
		ls.Master.Splits = v.(layout.MasterSplits)
	case "master_position":
		ls.Master.Position = v.(string)
		if ls.Master.Count == 0 {
			ls.Master.Count = 1
		}
	case "master_count":
		ls.Master.Count = v.(int)
	case "scroll_columns":
		ls.Scroll.Columns = v.([]session.SerializedScrollColumn)
	case "viewport_x":
		x := v.(int)
		ls.Scroll.ViewportX = &x
	case "order":
		order := v.([]string)
		slices.SortStableFunc(e.wins, func(a, b windowRow) int { return slices.Index(order, a.ID) - slices.Index(order, b.ID) })
	case "windows":
		for _, r := range v.([]session.WindowRect) {
			for i := range e.wins {
				if e.wins[i].ID == r.Window {
					e.wins[i].X, e.wins[i].Y, e.wins[i].Width, e.wins[i].Height = r.X, r.Y, r.Width, r.Height
					if r.Floating != nil {
						e.wins[i].Floating = *r.Floating
					}
				}
			}
		}
	}
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	}
	return 0
}

// flush writes what the op set. With tiling on it also writes the
// rectangle the layout now gives each tiled pane, as a TUI pushes the
// rectangles of a layout it changed: a TUI on the session takes a peer's
// layout by its rectangles.
func (e *engine) flush() error {
	if e.state == nil {
		return nil
	}
	if e.ti.ls.Tiling || e.ti.mode() != modeFloating {
		named := map[string]bool{}
		var rs []session.WindowRect
		if w, ok := e.state["windows"].([]session.WindowRect); ok {
			rs = w
			for _, r := range w {
				named[r.Window] = true
			}
		}
		cur := e.rects()
		for _, w := range tiled(e.wins) {
			if r, ok := cur[w.ID]; ok && !named[w.ID] {
				rs = append(rs, session.WindowRect{Window: w.ID, X: r.X, Y: r.Y, Width: max(r.W, 1), Height: max(r.H, 1)})
			}
		}
		if len(rs) > 0 {
			e.state["windows"] = rs
		}
	}
	_, err := e.c.verb("set-layout-state", e.state)
	return err
}

// writeTree writes the workspace's BSP tree, and keeps it as the engine's.
func (e *engine) writeTree(t *layout.BSPTree, leaves map[int]string) error {
	st := sessionTree(t)
	_, err := e.c.verb("set-layout-tree", map[string]any{"session": e.name, "workspace": e.ti.ws, "tree": st, "leaves": leaves})
	e.ti.tree, e.ti.leaves = st, leaves
	return err
}

// rects is where the panes are now, in cells.
func (e *engine) rects() map[string]layout.Rect {
	out := map[string]layout.Rect{}
	if e.ti.mode() == modeBSP {
		t, leaves, nums := e.ti.bspTree()
		var ids []int
		for _, w := range tiled(e.wins) {
			ids = append(ids, number(leaves, nums, w.ID))
		}
		t.PruneTo(ids)
		t.InsertMissing(ids, e.ti.bounds(), e.ti.ls.Gap)
		for n, r := range t.ApplyLayout(e.ti.bounds(), e.ti.ls.Gap) {
			out[leaves[n]] = r
		}
	}
	for id, r := range e.ti.arrange(e.wins, e.focus).Rects {
		out[id] = layout.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H}
	}
	return out
}

func (e *engine) run(p proto.ArrangeParams) error {
	switch p.Op {
	case "mode":
		return e.setMode(p.Mode, true)
	case "next":
		return e.nextMode()
	case "tiling":
		// Tiling on again keeps the split tree, as the TUI's toggle does.
		if e.ti.mode() == modeFloating {
			return e.setMode(e.ti.tiledMode(), false)
		}
		return e.setMode(modeFloating, false)
	case "rotate", "equalize":
		return e.treeOp(p.Op)
	case "master.position", "master.add", "master.remove", "master.swap", "master.ratio":
		return e.masterOp(p)
	case "scroll.left", "scroll.right", "scroll.width", "scroll.max", "scroll.consume", "scroll.expel",
		"scroll.moveLeft", "scroll.moveRight", "scroll.view", "scroll.reveal":
		return e.scrollOp(p)
	case "rect":
		return e.setRect(p)
	case "float":
		return e.toggleFloat()
	case "snap":
		return e.snap(p.Mode)
	case "minimize", "restore":
		return e.minimize(p)
	case "swap":
		return e.swap(p.Pane, p.Target)
	}
	return errors.New("no such layout change: " + p.Op)
}

// setMode switches the workspace's layout, as the TUI's Enable* rows do: a
// tree or a strip is built again from the panes, and tiling goes off with
// every pane left where it was.
func (e *engine) setMode(mode string, fresh bool) error {
	if mode == e.ti.mode() {
		return nil
	}
	if mode == modeFloating {
		// Every pane stays where it was, brought on screen when the
		// layout had it off, as the TUI's bringPanesIntoView does.
		cur := e.rects()
		b := e.ti.bounds()
		var rs []session.WindowRect
		for _, w := range e.wins {
			if r, ok := cur[w.ID]; ok {
				if !w.Floating {
					r = layout.IntoView(r, b)
				}
				rs = append(rs, session.WindowRect{Window: w.ID, X: r.X, Y: r.Y, Width: r.W, Height: r.H})
			}
		}
		e.set("tiling", false)
		e.set("mode", e.ti.tiledMode())
		e.set("windows", rs)
		return e.flush()
	}
	switch {
	case mode == modeBSP && fresh:
		// A fresh tree, as enableBSPLayout clears the old one and tiles.
		t, leaves, nums := e.ti.bspTree()
		t.PruneTo(nil)
		var ids []int
		for _, w := range tiled(e.wins) {
			ids = append(ids, number(leaves, nums, w.ID))
		}
		t.InsertMissing(ids, e.ti.bounds(), e.ti.ls.Gap)
		if err := e.writeTree(t, leaves); err != nil {
			return err
		}
	case mode == modeScroll:
		// A strip of one column a pane, as enableScrollingLayout and
		// tiling on again build it.
		e.ti.ls.Scroll.Columns, e.ti.ls.Scroll.ViewportX = nil, nil
		sl, ids := e.ti.strip(tiled(e.wins), e.focus)
		sl.ClampViewport(e.ti.bounds().W)
		e.set("scroll_columns", columns(sl, ids))
		e.set("viewport_x", sl.ViewportX)
	}
	e.set("mode", mode)
	e.set("tiling", true)
	return e.flush()
}

// nextMode is the TUI's cycle_layout: bsp, master-stack, scrolling, bsp.
// Leaving master-stack or scrolling with tiling off turns it on, and
// leaving bsp keeps it as it was, as toggleLayoutMode does.
func (e *engine) nextMode() error {
	switch e.ti.tiledMode() {
	case modeScroll:
		return e.setMode(modeBSP, true)
	case modeBSP:
		if e.ti.mode() == modeFloating {
			e.set("mode", modeMaster)
			return e.flush()
		}
		return e.setMode(modeMaster, true)
	default:
		return e.setMode(modeScroll, true)
	}
}

// treeOp rotates the focused pane's split, or evens out the splits.
func (e *engine) treeOp(op string) error {
	switch e.ti.mode() {
	case modeMaster:
		if op != "equalize" {
			return nil
		}
		// As equalizeMasterStack: the configured ratio, and equal shares.
		e.set("master_ratio", loadLayoutConf().set.MasterRatioFraction())
		e.set("stack_ratio", 0)
		e.set("master_splits", layout.MasterSplits{})
		return e.flush()
	case modeBSP:
	default:
		return nil
	}
	t, leaves, nums := e.ti.bspTree()
	var ids []int
	for _, w := range tiled(e.wins) {
		ids = append(ids, number(leaves, nums, w.ID))
	}
	bounds, gap := e.ti.bounds(), e.ti.ls.Gap
	t.PruneTo(ids)
	t.InsertMissing(ids, bounds, gap)
	was := fit(t.ApplyLayout(bounds, gap))
	if op == "rotate" {
		n, ok := nums[e.focus]
		if !ok {
			return nil
		}
		t.RotateSplit(n)
	} else {
		t.EqualizeRatios()
	}
	if now := fit(t.ApplyLayout(bounds, gap)); now < 1 && now < was {
		// Panes a few columns wide wrap every line, and a shell that
		// rewrapped its prompt keeps it broken when they grow back.
		return ErrTooSmall
	}
	return e.writeTree(t, leaves)
}

// MinPaneCols and MinPaneRows are the smallest pane, in cells, that a
// layout change may leave: a split, a rotate or an even out. They are the
// TUI's (layout.CanSplit), so both refuse the same splits.
const (
	MinPaneCols = config.DefaultWindowWidth
	MinPaneRows = config.DefaultWindowHeight
)

// ErrTooSmall refuses a layout change that would leave a pane smaller
// than MinPaneCols by MinPaneRows.
var ErrTooSmall = errors.New("a pane would be too small")

// fit is how the smallest of rects measures against the smallest pane: 1
// or more when every pane is at least MinPaneCols by MinPaneRows.
func fit(rects map[int]layout.Rect) float64 {
	f := math.Inf(1)
	for _, r := range rects {
		f = min(f, float64(r.W)/MinPaneCols, float64(r.H)/MinPaneRows)
	}
	return f
}

// masterOp changes the master-stack shape, as the TUI's master actions do.
func (e *engine) masterOp(p proto.ArrangeParams) error {
	if e.ti.mode() != modeMaster {
		return nil
	}
	mp := e.ti.masterParams()
	t := tiled(e.wins)
	switch p.Op {
	case "master.position":
		i := slices.Index(config.MasterPositions, mp.Position)
		e.set("master_position", config.MasterPositions[(i+1)%len(config.MasterPositions)])
		e.set("master_count", mp.Count)
	case "master.add", "master.remove":
		n := mp.Count + 1
		if p.Op == "master.remove" {
			n = mp.Count - 1
		}
		n = config.ClampMasterCount(n)
		if n == mp.Count {
			return nil
		}
		e.set("master_position", mp.Position)
		e.set("master_count", n)
	case "master.swap":
		// The focused pane and the first master change places, or the
		// first master and the next pane when the focus is the master.
		i := slices.IndexFunc(t, func(w windowRow) bool { return w.ID == e.focus })
		if i < 0 || len(t) < 2 {
			return nil
		}
		j := 0
		if i == 0 {
			j = 1
		}
		return e.swap(t[i].ID, t[j].ID)
	case "master.ratio":
		if p.Ratio <= 0 || p.Ratio >= 1 {
			return nil
		}
		e.set("master_ratio", min(max(p.Ratio, layout.MinSplitRatio), layout.MaxSplitRatio))
	}
	return e.flush()
}

// swap puts two panes in each other's place: in the tree, or in the order
// master-stack and scrolling take them.
func (e *engine) swap(a, b string) error {
	if a == "" || b == "" || a == b {
		return nil
	}
	switch e.ti.mode() {
	case modeBSP:
		t, leaves, nums := e.ti.bspTree()
		na, oka := nums[a]
		nb, okb := nums[b]
		if !oka || !okb {
			return nil
		}
		t.SwapWindows(na, nb)
		return e.writeTree(t, leaves)
	case modeScroll:
		sl, ids := e.ti.strip(tiled(e.wins), e.focus)
		ia, ib := slices.Index(ids, a)+1, slices.Index(ids, b)+1
		for ci := range sl.Columns {
			for j, n := range sl.Columns[ci].WindowIDs {
				switch n {
				case ia:
					sl.Columns[ci].WindowIDs[j] = ib
				case ib:
					sl.Columns[ci].WindowIDs[j] = ia
				}
			}
		}
		e.set("scroll_columns", columns(sl, ids))
		return e.flush()
	}
	var order []string
	for _, w := range e.wins {
		switch w.ID {
		case a:
			order = append(order, b)
		case b:
			order = append(order, a)
		default:
			order = append(order, w.ID)
		}
	}
	e.set("order", order)
	return e.flush()
}

// scrollOp moves about the scrolling strip, or changes its columns, as the
// TUI's scrolling actions do, and keeps the focused column on screen.
func (e *engine) scrollOp(p proto.ArrangeParams) error {
	if e.ti.mode() != modeScroll {
		return nil
	}
	t := tiled(e.wins)
	sl, ids := e.ti.strip(t, e.focus)
	w := e.ti.bounds().W
	// What the TUI's own actions do to the strip's offset: an action
	// scrolls to the focused column, a focus that moved some other way
	// only brings it on screen, a scroll moves it alone. The actions that
	// move the focus to another window hand it to the session.
	moves := false
	switch p.Op {
	case "scroll.left":
		sl.FocusLeft()
		moves = true
	case "scroll.right":
		sl.FocusRight()
		moves = true
	case "scroll.width":
		sl.CycleWidth()
	case "scroll.max":
		sl.MaximizeColumn()
	case "scroll.consume":
		sl.ConsumeWindow()
		moves = true
	case "scroll.expel":
		sl.ExpelWindow()
		moves = true
	case "scroll.moveLeft":
		sl.MoveColumnLeft()
	case "scroll.moveRight":
		sl.MoveColumnRight()
	}
	switch p.Op {
	case "scroll.view":
		// A wheel or a drag over the strip: the cells it moves.
		sl.ViewportX += p.Delta
	case "scroll.reveal":
		// The focus moved to Pane.
		sl.EnsureFocusedVisible(w)
	default:
		sl.ScrollToFocusedColumn(w)
	}
	sl.ClampViewport(w)
	e.set("scroll_columns", columns(sl, ids))
	e.set("viewport_x", sl.ViewportX)
	if err := e.flush(); err != nil {
		return err
	}
	if n := sl.GetFocusedWindowID(); moves && n >= 1 && n <= len(ids) && ids[n-1] != e.focus {
		e.result.Focus = ids[n-1]
		_, _ = e.c.verb("focus-window", map[string]any{"session": e.name, "window": ids[n-1]})
	}
	return nil
}

// setRect places a floating pane, or snaps it to a half, a quarter or the
// whole box when Snap names where the pointer let it go, as the TUI's drop
// does.
func (e *engine) setRect(p proto.ArrangeParams) error {
	if p.Rect == nil || p.Pane == "" {
		return nil
	}
	b := e.ti.bounds()
	r := layout.Rect{X: p.Rect.X, Y: p.Rect.Y, W: p.Rect.W, H: p.Rect.H}
	// The smallest pane the TUI lets a drag make.
	r.W = min(max(r.W, config.DefaultWindowWidth), b.W)
	r.H = min(max(r.H, config.DefaultWindowHeight), b.H)
	if p.At != nil {
		// A move: the pane stays on screen, so its header can be taken
		// again, unless the pointer let it go at an edge, which snaps it.
		r = layout.IntoView(r, b)
		if q := layout.SnapZoneAt(b, p.At.X, p.At.Y); q != layout.NoSnap {
			r = layout.SnapBounds(b, q)
		}
	}
	e.set("windows", []session.WindowRect{{Window: p.Pane, X: r.X, Y: r.Y, Width: r.W, Height: r.H}})
	return e.flush()
}

// minimize minimizes the focused pane, or restores the Delta-th minimized
// pane of the workspace (1 the first), or every one with Delta 0, as the
// TUI's minimize menu does. The daemon keeps the flag (set-window), so it
// works with no TUI attached.
func (e *engine) minimize(p proto.ArrangeParams) error {
	set := func(id string, on bool) error {
		_, err := e.c.verb("set-window", map[string]any{"session": e.name, "window": id, "minimized": on})
		return err
	}
	if p.Op == "minimize" {
		if e.focus == "" {
			return nil
		}
		return set(e.focus, true)
	}
	// Read from the session now: the state the app last built can be
	// older than the minimize just before this.
	var mins []string
	if raw, err := e.c.verb("list-windows", map[string]any{"session": e.name}); err == nil {
		var wl struct {
			Windows []windowRow `json:"windows"`
		}
		_ = json.Unmarshal(raw, &wl)
		for _, w := range wl.Windows {
			if w.Minimized && w.Workspace == e.ti.ws {
				mins = append(mins, w.ID)
			}
		}
	}
	if p.Delta > 0 {
		if p.Delta > len(mins) {
			return nil
		}
		e.result.Focus = mins[p.Delta-1]
		return set(mins[p.Delta-1], false)
	}
	for _, id := range mins {
		if err := set(id, false); err != nil {
			return err
		}
	}
	return nil
}

// snapNames are the snaps as the actions name them.
var snapNames = map[string]layout.Snap{
	"left": layout.SnapLeft, "right": layout.SnapRight, "full": layout.SnapFullScreen, "unsnap": layout.Unsnap,
	"top-left": layout.SnapTopLeft, "top-right": layout.SnapTopRight, "bottom-left": layout.SnapBottomLeft, "bottom-right": layout.SnapBottomRight,
}

// snap puts the focused floating pane in a half, a quarter or the whole
// box, or back in the middle, as the TUI's snap actions do with tiling off.
func (e *engine) snap(name string) error {
	q, ok := snapNames[name]
	if !ok || e.focus == "" || e.ti.mode() != modeFloating {
		return nil
	}
	r := layout.SnapBounds(e.ti.bounds(), q)
	e.set("windows", []session.WindowRect{{Window: e.focus, X: r.X, Y: r.Y, Width: max(r.W, 1), Height: max(r.H, 1)}})
	return e.flush()
}

// toggleFloat lifts the focused pane out of the tiling at the rectangle it
// has, or puts it back, as the TUI's ToggleFloating does.
func (e *engine) toggleFloat() error {
	if e.ti.mode() == modeFloating || e.focus == "" {
		return nil
	}
	i := slices.IndexFunc(e.wins, func(w windowRow) bool { return w.ID == e.focus })
	if i < 0 {
		return nil
	}
	w := &e.wins[i]
	on := !w.Floating
	wr := session.WindowRect{Window: w.ID, X: w.X, Y: w.Y, Width: max(w.Width, 1), Height: max(w.Height, 1), Floating: &on}
	if r, ok := e.rects()[w.ID]; ok && on {
		wr.X, wr.Y, wr.Width, wr.Height = r.X, r.Y, r.W, r.H
	}
	w.Floating = on
	e.set("windows", []session.WindowRect{wr})
	if e.ti.mode() == modeScroll {
		sl, ids := e.ti.strip(tiled(e.wins), e.focus)
		e.set("scroll_columns", columns(sl, ids))
	}
	if err := e.flush(); err != nil {
		return err
	}
	// A pane put back goes into the layout where the TUI puts it: the
	// state build places a pane the tree lacks (layoutOf).
	return nil
}
