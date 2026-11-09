package session

import (
	"errors"
	"math"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/adrg/xdg"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
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
	ArrangeModeBSP      = config.LayoutModeBSP
	ArrangeModeMaster   = config.LayoutModeMasterStack
	ArrangeModeScroll   = config.LayoutModeScrolling
	ArrangeModeFloating = "floating"
)

// ArrangeLayout is the rest of a workspace's layout, as layout-tree reads it.
type ArrangeLayout struct {
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
		Columns     []SerializedScrollColumn `json:"columns"`
		ViewportX   *int                     `json:"viewport_x"`
		ColumnWidth int                      `json:"column_width"`
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
func (ti *ArrangeTree) Mode() string {
	ls := ti.LS
	if ls.Mode == "" && !ls.Tiling {
		c := loadLayoutConf()
		if !c.tiled {
			return ArrangeModeFloating
		}
		return arrangeValidMode(c.layout)
	}
	if !ls.Tiling {
		return ArrangeModeFloating
	}
	return arrangeValidMode(ls.Mode)
}

// tiledMode is the layout tiling uses, on or off.
func (ti *ArrangeTree) TiledMode() string {
	if ti.LS.Mode == "" {
		return arrangeValidMode(loadLayoutConf().layout)
	}
	return arrangeValidMode(ti.LS.Mode)
}

func arrangeValidMode(m string) string {
	if m == ArrangeModeMaster || m == ArrangeModeScroll {
		return m
	}
	return ArrangeModeBSP
}

// bounds is the box the panes are laid out in, in cells. With a TUI
// attached it is the TUI's, the box the session settled on, so both lay
// the panes out in the same cells. With none it is guios's own pane area
// in cells (setGrid): the session's size is then whatever a client last
// set, as small as 20x12 for a session guios made, and a layout worked out
// in so few cells rounds its rows and columns to nothing, while the
// scrolling strip's columns and a floating pane's smallest size fill it.
func (ti *ArrangeTree) Bounds() layout.Rect {
	if !ti.TUI && ti.Grid.W >= 4 && ti.Grid.H >= 2 {
		return ti.Grid
	}
	b := ti.LS.Box
	if b.W < 4 || b.H < 2 || !ti.TUI {
		// With no TUI the box is the daemon's own 80 by 24, no client's:
		// before guios measured its grid (a window not drawn yet, or out
		// of sight), new panes went into it, where no split fits, and the
		// layout came out by timing (QM-009). A pane area of a common
		// window is closer.
		return layout.Rect{W: 160, H: 45}
	}
	return layout.Rect{X: b.X, Y: b.Y, W: b.W, H: b.H}
}

// bspTree is the workspace's tree, its leaves numbered as the session
// numbers them, with the window of each number and the number of each
// window. A workspace with no tree gets an empty one with the configured
// scheme, as the TUI makes one.
func (ti *ArrangeTree) BSPTree() (*layout.BSPTree, map[int]string, map[string]int) {
	var t *layout.BSPTree
	if ti.Tree != nil && ti.Tree.Root != nil {
		t = (&layout.SerializedBSPTree{Root: toLayoutNode(ti.Tree.Root), AutoScheme: ti.Tree.AutoScheme, DefaultRatio: ti.Tree.DefaultRatio}).Deserialize()
	} else {
		t = layout.NewBSPTree()
		scheme := loadLayoutConf().set.TilingScheme
		if scheme == "" {
			scheme = config.TilingSchemeSpiral
		}
		t.AutoScheme = layout.ParseAutoScheme(scheme)
	}
	leaves := make(map[int]string, len(ti.Leaves))
	nums := make(map[string]int, len(ti.Leaves))
	for n, id := range ti.Leaves {
		leaves[n] = id
		nums[id] = n
	}
	return t, leaves, nums
}

// number is a window's leaf number, a new one for a window the tree lacks.
func LeafNumber(leaves map[int]string, nums map[string]int, id string) int {
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

// sessionTree is t as set-layout-tree takes it.
func SessionTree(t *layout.BSPTree) *SerializedBSPTree {
	if t == nil || t.IsEmpty() {
		return nil
	}
	s := t.Serialize()
	return &SerializedBSPTree{Root: toSessionNode(s.Root), AutoScheme: s.AutoScheme, DefaultRatio: s.DefaultRatio}
}

// tiled is the workspace's panes the layout places, in the session's order:
// those that do not float over it.
func ArrangeTiled(wins []WindowRow) []WindowRow {
	out := make([]WindowRow, 0, len(wins))
	for _, w := range wins {
		if !w.Floating {
			out = append(out, w)
		}
	}
	return out
}

// masterParams is what the master-stack tiler reads for the workspace, as
// the TUI's masterParams builds it.
func (ti *ArrangeTree) MasterParams() layout.MasterParams {
	c := loadLayoutConf()
	m := ti.LS.Master
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
		Gap:        ti.LS.Gap,
	}
}

// strip is the workspace's scrolling strip, its windows numbered by their
// place in wins, as the TUI's GetOrCreateScrollingLayout builds it: the
// session's columns, a column for each pane they lack, and the focused
// pane's column focused.
func (ti *ArrangeTree) Strip(wins []WindowRow, focus string) (*layout.ScrollingLayout, []string) {
	c := loadLayoutConf()
	ids := make([]string, len(wins))
	num := make(map[string]int, len(wins))
	for i, w := range wins {
		ids[i] = w.ID
		num[w.ID] = i + 1
	}
	sl := layout.NewScrollingLayout()
	sl.Gap = ti.LS.Gap
	width := ti.LS.Scroll.ColumnWidth
	if width == 0 {
		width = c.set.ScrollColumnWidth
	}
	sl.DefaultWidth = float64(min(max(width, config.ScrollColumnWidthMin), c.set.GetScrollColumnMax())) / 100
	sl.MaxProportion = float64(c.set.GetScrollColumnMax()) / 100
	if len(c.set.ScrollColumnPresets) > 0 {
		sl.PresetWidths = c.set.ScrollColumnPresets
	}
	for _, col := range ti.LS.Scroll.Columns {
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
	if v := ti.LS.Scroll.ViewportX; v != nil {
		sl.ViewportX = *v
	} else {
		sl.EnsureFocusedVisible(ti.Bounds().W)
	}
	return sl, ids
}

// columns is a strip's columns as the session keeps them.
func ArrangeColumns(sl *layout.ScrollingLayout, ids []string) []SerializedScrollColumn {
	out := make([]SerializedScrollColumn, 0, len(sl.Columns))
	for _, col := range sl.Columns {
		sc := SerializedScrollColumn{Proportion: col.Proportion, FixedWidth: col.FixedWidth, Active: col.Active}
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
func (ti *ArrangeTree) Arrange(wins []WindowRow, focus string) *ArrangeView {
	b := ti.Bounds()
	a := &ArrangeView{Box: ArrangeRect{X: b.X, Y: b.Y, W: b.W, H: b.H}, Rects: map[string]ArrangeRect{}}
	put := func(id string, r layout.Rect) {
		a.Rects[id] = ArrangeRect{X: r.X, Y: r.Y, W: max(r.W, 1), H: max(r.H, 1)}
	}
	mode := ti.Mode()
	floats := wins
	if mode != ArrangeModeFloating {
		floats = nil
		for _, w := range wins {
			if w.Floating {
				floats = append(floats, w)
			}
		}
	}
	switch t := ArrangeTiled(wins); mode {
	case ArrangeModeMaster:
		mp := ti.MasterParams()
		rects := layout.CalculateMasterLayout(len(t), b.W, b.H, b.Y, mp)
		var masters layout.Rect
		for i, r := range rects {
			if i < len(t) {
				lr := layout.Rect{X: r.X + b.X, Y: r.Y, W: r.Width, H: r.Height}
				put(t[i].ID, lr)
				if i < mp.Count {
					masters = arrangeUnion(masters, lr, i == 0)
				}
			}
		}
		a.Master = arrangeMasterLine(masters, b, len(t) > mp.Count)
	case ArrangeModeScroll:
		sl, ids := ti.Strip(t, focus)
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
	z := ti.LS.Z
	slices.SortStableFunc(floats, func(x, y WindowRow) int { return z[x.ID] - z[y.ID] })
	step := 0
	for _, w := range floats {
		r := layout.Rect{X: w.X, Y: w.Y, W: w.Width, H: w.Height}
		if slices.Contains(ti.LS.Unplaced, w.ID) || r.W < 2 || r.H < 2 {
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

// union is the rectangle around a and b, b alone when first.
func arrangeUnion(a, b layout.Rect, first bool) layout.Rect {
	if first {
		return b
	}
	x, y := min(a.X, b.X), min(a.Y, b.Y)
	return layout.Rect{X: x, Y: y, W: max(a.X+a.W, b.X+b.W) - x, H: max(a.Y+a.H, b.Y+b.H) - y}
}

// masterLine is the edge of the masters that faces the stack, when the
// masters fill one side of the box: none in the grid, or with the masters
// in the centre, or with no stack.
func arrangeMasterLine(m, b layout.Rect, stack bool) *MasterLine {
	if !stack || m.W <= 0 {
		return nil
	}
	fullH, fullW := m.Y <= b.Y && m.Y+m.H >= b.Y+b.H, m.X <= b.X && m.X+m.W >= b.X+b.W
	switch {
	case fullH && m.X <= b.X:
		return &MasterLine{Vertical: true, At: m.X + m.W, Before: true}
	case fullH && m.X+m.W >= b.X+b.W:
		return &MasterLine{Vertical: true, At: m.X}
	case fullW && m.Y <= b.Y:
		return &MasterLine{At: m.Y + m.H, Before: true}
	case fullW && m.Y+m.H >= b.Y+b.H:
		return &MasterLine{At: m.Y}
	}
	return nil
}

// engine runs one op on a workspace.
type engine struct {
	call VerbCaller
	// floated, when set, hears of a pane about to float out of the split
	// tree, while the tree still holds it (toggleFloat).
	floated func(pane string)
	name    string
	ti      ArrangeTree
	wins    []WindowRow
	focus   string
	state   map[string]any // set-layout-state params gathered by the op
	result  ArrangeResult
}

func (e *engine) set(k string, v any) {
	if e.state == nil {
		e.state = map[string]any{"session": e.name, "workspace": e.ti.WS}
	}
	e.state[k] = v
	// The engine's own copy follows, so flush lays the panes out as the
	// session will have them.
	ls := &e.ti.LS
	switch k {
	case "mode":
		ls.Mode = v.(string)
	case "tiling":
		ls.Tiling = v.(bool)
	case "master_ratio":
		ls.Master.Ratio = arrangeFloat(v)
	case "stack_ratio":
		ls.Master.StackRatio = arrangeFloat(v)
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
		ls.Scroll.Columns = v.([]SerializedScrollColumn)
	case "viewport_x":
		x := v.(int)
		ls.Scroll.ViewportX = &x
	case "order":
		order := v.([]string)
		slices.SortStableFunc(e.wins, func(a, b WindowRow) int { return slices.Index(order, a.ID) - slices.Index(order, b.ID) })
	case "windows":
		for _, r := range v.([]WindowRect) {
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

func arrangeFloat(v any) float64 {
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
	if e.ti.LS.Tiling || e.ti.Mode() != ArrangeModeFloating {
		named := map[string]bool{}
		var rs []WindowRect
		if w, ok := e.state["windows"].([]WindowRect); ok {
			rs = w
			for _, r := range w {
				named[r.Window] = true
			}
		}
		cur := e.rects()
		for _, w := range ArrangeTiled(e.wins) {
			if r, ok := cur[w.ID]; ok && !named[w.ID] {
				rs = append(rs, WindowRect{Window: w.ID, X: r.X, Y: r.Y, Width: max(r.W, 1), Height: max(r.H, 1)})
			}
		}
		if len(rs) > 0 {
			e.state["windows"] = rs
		}
	}
	_, err := e.call("set-layout-state", e.state)
	return err
}

// writeTree writes the workspace's BSP tree, and keeps it as the engine's.
func (e *engine) writeTree(t *layout.BSPTree, leaves map[int]string) error {
	st := SessionTree(t)
	_, err := e.call("set-layout-tree", map[string]any{"session": e.name, "workspace": e.ti.WS, "tree": st, "leaves": leaves})
	e.ti.Tree, e.ti.Leaves = st, leaves
	return err
}

// rects is where the panes are now, in cells.
func (e *engine) rects() map[string]layout.Rect {
	out := map[string]layout.Rect{}
	if e.ti.Mode() == ArrangeModeBSP {
		t, leaves, nums := e.ti.BSPTree()
		var ids []int
		for _, w := range ArrangeTiled(e.wins) {
			ids = append(ids, LeafNumber(leaves, nums, w.ID))
		}
		t.PruneTo(ids)
		t.InsertMissing(ids, e.ti.Bounds(), e.ti.LS.Gap)
		for n, r := range t.ApplyLayout(e.ti.Bounds(), e.ti.LS.Gap) {
			out[leaves[n]] = r
		}
	}
	for id, r := range e.ti.Arrange(e.wins, e.focus).Rects {
		out[id] = layout.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H}
	}
	return out
}

func (e *engine) run(p ArrangeParams) error {
	switch p.Op {
	case "mode":
		return e.setMode(p.Mode, true)
	case "next":
		return e.nextMode()
	case "tiling":
		// Tiling on again keeps the split tree, as the TUI's toggle does.
		if e.ti.Mode() == ArrangeModeFloating {
			return e.setMode(e.ti.TiledMode(), false)
		}
		return e.setMode(ArrangeModeFloating, false)
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
	case "move":
		return e.moveBeside(p.Pane, p.Target, p.Mode)
	case "split.ratio":
		return e.splitRatio(p.Pane, p.Target, p.Ratio)
	case "place":
		return e.place(p)
	}
	return errors.New("no such layout change: " + p.Op)
}

// setMode switches the workspace's layout, as the TUI's Enable* rows do: a
// tree or a strip is built again from the panes, and tiling goes off with
// every pane left where it was.
func (e *engine) setMode(mode string, fresh bool) error {
	if mode == e.ti.Mode() {
		return nil
	}
	if mode == ArrangeModeFloating {
		// Every pane stays where it was, brought on screen when the
		// layout had it off, as the TUI's bringPanesIntoView does.
		cur := e.rects()
		b := e.ti.Bounds()
		var rs []WindowRect
		for _, w := range e.wins {
			if r, ok := cur[w.ID]; ok {
				if !w.Floating {
					r = layout.IntoView(r, b)
				}
				rs = append(rs, WindowRect{Window: w.ID, X: r.X, Y: r.Y, Width: r.W, Height: r.H})
			}
		}
		e.set("tiling", false)
		e.set("mode", e.ti.TiledMode())
		e.set("windows", rs)
		return e.flush()
	}
	switch {
	case mode == ArrangeModeBSP && fresh:
		// A fresh tree, as enableBSPLayout clears the old one and tiles.
		t, leaves, nums := e.ti.BSPTree()
		t.PruneTo(nil)
		var ids []int
		for _, w := range ArrangeTiled(e.wins) {
			ids = append(ids, LeafNumber(leaves, nums, w.ID))
		}
		t.InsertMissing(ids, e.ti.Bounds(), e.ti.LS.Gap)
		if err := e.writeTree(t, leaves); err != nil {
			return err
		}
	case mode == ArrangeModeScroll:
		// A strip of one column a pane, as enableScrollingLayout and
		// tiling on again build it.
		e.ti.LS.Scroll.Columns, e.ti.LS.Scroll.ViewportX = nil, nil
		sl, ids := e.ti.Strip(ArrangeTiled(e.wins), e.focus)
		sl.ClampViewport(e.ti.Bounds().W)
		e.set("scroll_columns", ArrangeColumns(sl, ids))
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
	switch e.ti.TiledMode() {
	case ArrangeModeScroll:
		return e.setMode(ArrangeModeBSP, true)
	case ArrangeModeBSP:
		if e.ti.Mode() == ArrangeModeFloating {
			e.set("mode", ArrangeModeMaster)
			return e.flush()
		}
		return e.setMode(ArrangeModeMaster, true)
	default:
		return e.setMode(ArrangeModeScroll, true)
	}
}

// treeOp rotates the focused pane's split, or evens out the splits.
func (e *engine) treeOp(op string) error {
	switch e.ti.Mode() {
	case ArrangeModeMaster:
		if op != "equalize" {
			return nil
		}
		// As equalizeMasterStack: the configured ratio, and equal shares.
		e.set("master_ratio", loadLayoutConf().set.MasterRatioFraction())
		e.set("stack_ratio", 0)
		e.set("master_splits", layout.MasterSplits{})
		return e.flush()
	case ArrangeModeBSP:
	default:
		return nil
	}
	t, leaves, nums := e.ti.BSPTree()
	var ids []int
	for _, w := range ArrangeTiled(e.wins) {
		ids = append(ids, LeafNumber(leaves, nums, w.ID))
	}
	bounds, gap := e.ti.Bounds(), e.ti.LS.Gap
	t.PruneTo(ids)
	t.InsertMissing(ids, bounds, gap)
	was := arrangeFit(t.ApplyLayout(bounds, gap))
	if op == "rotate" {
		n, ok := nums[e.focus]
		if !ok {
			return nil
		}
		t.RotateSplit(n)
	} else {
		t.EqualizeRatios()
	}
	if now := arrangeFit(t.ApplyLayout(bounds, gap)); now < 1 && now < was {
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
func arrangeFit(rects map[int]layout.Rect) float64 {
	f := math.Inf(1)
	for _, r := range rects {
		f = min(f, float64(r.W)/MinPaneCols, float64(r.H)/MinPaneRows)
	}
	return f
}

// masterOp changes the master-stack shape, as the TUI's master actions do.
func (e *engine) masterOp(p ArrangeParams) error {
	// As the TUI's: the shape is kept for the workspace in any tiled layout
	// and shows in master-stack. Only the swap needs master-stack on.
	if e.ti.Mode() == ArrangeModeFloating || (p.Op == "master.swap" && e.ti.Mode() != ArrangeModeMaster) {
		return nil
	}
	mp := e.ti.MasterParams()
	t := ArrangeTiled(e.wins)
	switch p.Op {
	case "master.position":
		// Mode names the side (set_master_position_SIDE); without it the
		// masters go to the next side (cycle_master_position).
		next := p.Mode
		if next == "" {
			i := slices.Index(config.MasterPositions, mp.Position)
			next = config.MasterPositions[(i+1)%len(config.MasterPositions)]
		} else if !slices.Contains(config.MasterPositions, next) {
			return errors.New("no such master position: " + next)
		}
		e.set("master_position", next)
		e.set("master_count", mp.Count)
	case "master.add", "master.remove":
		// As the TUI's AddMaster: no more masters than tiled panes.
		n := mp.Count + 1
		if p.Op == "master.add" && mp.Count >= len(t) {
			n = mp.Count
		}
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
		i := slices.IndexFunc(t, func(w WindowRow) bool { return w.ID == e.focus })
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
	switch e.ti.Mode() {
	case ArrangeModeBSP:
		t, leaves, nums := e.ti.BSPTree()
		na, oka := nums[a]
		nb, okb := nums[b]
		if !oka || !okb {
			return nil
		}
		t.SwapWindows(na, nb)
		return e.writeTree(t, leaves)
	case ArrangeModeScroll:
		sl, ids := e.ti.Strip(ArrangeTiled(e.wins), e.focus)
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
		e.set("scroll_columns", ArrangeColumns(sl, ids))
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
func (e *engine) scrollOp(p ArrangeParams) error {
	if e.ti.Mode() != ArrangeModeScroll {
		return nil
	}
	t := ArrangeTiled(e.wins)
	sl, ids := e.ti.Strip(t, e.focus)
	w := e.ti.Bounds().W
	// What the TUI's own actions do to the strip's offset: an action
	// scrolls to the focused column, a focus that moved some other way
	// only brings it on screen, a scroll moves it alone. The actions that
	// move the focus to another window hand it to the
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
	e.set("scroll_columns", ArrangeColumns(sl, ids))
	e.set("viewport_x", sl.ViewportX)
	if err := e.flush(); err != nil {
		return err
	}
	if n := sl.GetFocusedWindowID(); moves && n >= 1 && n <= len(ids) && ids[n-1] != e.focus {
		e.result.Focus = ids[n-1]
		_, _ = e.call("focus-window", map[string]any{"session": e.name, "window": ids[n-1]})
	}
	return nil
}

// setRect places a floating pane, or snaps it to a half, a quarter or the
// whole box when Snap names where the pointer let it go, as the TUI's drop
// does.
func (e *engine) setRect(p ArrangeParams) error {
	if p.Rect == nil || p.Pane == "" {
		return nil
	}
	b := e.ti.Bounds()
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
	e.set("windows", []WindowRect{{Window: p.Pane, X: r.X, Y: r.Y, Width: r.W, Height: r.H}})
	return e.flush()
}

// minimize minimizes the focused pane, or restores the Delta-th minimized
// pane of the workspace (1 the first), or every one with Delta 0, as the
// TUI's minimize menu does. The daemon keeps the flag (set-window), so it
// works with no TUI attached.
func (e *engine) minimize(p ArrangeParams) error {
	set := func(id string, on bool) error {
		_, err := e.call("set-window", map[string]any{"session": e.name, "window": id, "minimized": on})
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
	if raw, err := e.call("list-windows", map[string]any{"session": e.name}); err == nil {
		var wl struct {
			Windows []WindowRow `json:"windows"`
		}
		_ = jsonx.Unmarshal(raw, &wl)
		for _, w := range wl.Windows {
			if w.Minimized && w.Workspace == e.ti.WS {
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
	if !ok || e.focus == "" || e.ti.Mode() != ArrangeModeFloating {
		return nil
	}
	r := layout.SnapBounds(e.ti.Bounds(), q)
	e.set("windows", []WindowRect{{Window: e.focus, X: r.X, Y: r.Y, Width: max(r.W, 1), Height: max(r.H, 1)}})
	return e.flush()
}

// toggleFloat lifts the focused pane out of the tiling at the rectangle it
// has, or puts it back, as the TUI's ToggleFloating does.
func (e *engine) toggleFloat() error {
	if e.ti.Mode() == ArrangeModeFloating || e.focus == "" {
		return nil
	}
	i := slices.IndexFunc(e.wins, func(w WindowRow) bool { return w.ID == e.focus })
	if i < 0 {
		return nil
	}
	w := &e.wins[i]
	on := !w.Floating
	wr := WindowRect{Window: w.ID, X: w.X, Y: w.Y, Width: max(w.Width, 1), Height: max(w.Height, 1), Floating: &on}
	if r, ok := e.rects()[w.ID]; ok && on {
		wr.X, wr.Y, wr.Width, wr.Height = r.X, r.Y, r.W, r.H
	}
	w.Floating = on
	if on && e.floated != nil {
		e.floated(w.ID)
	}
	e.set("windows", []WindowRect{wr})
	if e.ti.Mode() == ArrangeModeScroll {
		sl, ids := e.ti.Strip(ArrangeTiled(e.wins), e.focus)
		e.set("scroll_columns", ArrangeColumns(sl, ids))
	}
	if err := e.flush(); err != nil {
		return err
	}
	// A pane put back goes where it tiled before it floated, or else where
	// the TUI puts a pane: the state build places a pane the tree lacks
	// (layoutOf).
	return nil
}

// ArrangeTree is what the session says about the shown workspace's layout.
type ArrangeTree struct {
	WS        int
	Tree      *SerializedBSPTree
	Leaves    map[int]string
	LS        ArrangeLayout
	Grid      layout.Rect // guios's pane area in cells (setGrid)
	UseTree   bool        // the tree arranges the panes, and guios's changes go back to it
	TUI       bool        // a TUI client is attached
	NumWS     int
	WSNames   map[int]string
	WSOrder   []int
	ZoomByTUI string // the pane a TUI zoomed on the shown workspace
}

// readTree reads the session's settings and the shown workspace's tree.
func ReadArrangeTree(call VerbCaller, name string, ws int, grid layout.Rect) ArrangeTree {
	ti := ArrangeTree{WS: ws, NumWS: 9, Grid: grid}
	if raw, err := call("session-info", map[string]any{"session": name}); err == nil {
		var si struct {
			TUI     bool              `json:"tui_attached"`
			NumWS   int               `json:"num_workspaces"`
			WSNames map[string]string `json:"workspace_names"`
			WSOrder []int             `json:"workspace_order"`
			Current int               `json:"current_workspace"`
		}
		if jsonx.Unmarshal(raw, &si) == nil {
			ti.TUI, ti.WSOrder = si.TUI, si.WSOrder
			if si.NumWS > 0 {
				ti.NumWS = si.NumWS
			}
			ti.WSNames = map[int]string{}
			for k, v := range si.WSNames {
				if n, err := strconv.Atoi(k); err == nil && v != "" {
					ti.WSNames[n] = v
				}
			}
		}
	}
	if raw, err := call("layout-tree", map[string]any{"session": name, "workspace": ws}); err == nil {
		var lt struct {
			Tree   *SerializedBSPTree `json:"tree"`
			Leaves map[string]string  `json:"leaves"`
		}
		if jsonx.Unmarshal(raw, &lt) == nil {
			_ = jsonx.Unmarshal(raw, &ti.LS)
			ti.Tree = lt.Tree
			ti.Leaves = make(map[int]string, len(lt.Leaves))
			for k, v := range lt.Leaves {
				if n, err := strconv.Atoi(k); err == nil {
					ti.Leaves[n] = v
				}
			}
		}
	}
	ti.UseTree = ti.Mode() == ArrangeModeBSP
	return ti
}

type WindowRow struct {
	ID          string           `json:"window_id"`
	PTY         string           `json:"pty_id"`
	Title       string           `json:"title"`
	DisplayName string           `json:"display_name"`
	Name        string           `json:"name"`
	CustomName  string           `json:"custom_name"`
	Cwd         string           `json:"cwd"`
	Width       int              `json:"width"`
	Height      int              `json:"height"`
	PID         int              `json:"pid"`
	Focused     bool             `json:"focused"`
	Minimized   bool             `json:"minimized"`
	Zoomed      bool             `json:"zoomed"`
	Floating    bool             `json:"floating"`
	Scratch     bool             `json:"scratch"`
	ScratchName string           `json:"scratch_name"`
	Rail        string           `json:"rail"`
	Workspace   int              `json:"workspace"`
	X           int              `json:"x"`
	Y           int              `json:"y"`
	Agent       string           `json:"agent_state"`
	AgentMsg    string           `json:"agent_message"`
	AgentKind   string           `json:"agent_kind"`
	AgentAt     int64            `json:"agent_state_at"`
	AgentReason string           `json:"agent_reason"`
	Harness     string           `json:"agent_harness"`
	Foreground  string           `json:"foreground_cmd"`
	Host        string           `json:"host"`
	Program     []map[string]any `json:"program_status"`
	// Exited and its exit status say the window's process ended; KeepOpen
	// says the window stays after that (new-window keep_open). A daemon
	// from before these fields sends neither, and guios closes none of its
	// windows (exits.go).
	// Kind and URI name a window a native renderer draws, and what it
	// shows: kind web is a browser pane on the page in uri. Empty for a
	// terminal.
	Kind string `json:"kind"`
	URI  string `json:"uri"`
	// Surface is a tml window's surface id.
	Surface  string `json:"surface"`
	Exited   bool   `json:"exited"`
	ExitCode int    `json:"exit_code"`
	Signal   int    `json:"signal"`
	KeepOpen bool   `json:"keep_open"`
}

// moveBeside puts pane beside target in the split tree, on side (left,
// right, up or down) of it, as a pane dropped on the edge of another.
func (e *engine) moveBeside(pane, target, side string) error {
	sd, ok := layout.ParseSide(side)
	if !ok || pane == "" || target == "" || pane == target || e.ti.Mode() != ArrangeModeBSP {
		return nil
	}
	t, leaves, nums := e.ti.BSPTree()
	na, oka := nums[pane]
	nb, okb := nums[target]
	if !oka || !okb || !t.MoveWindowBeside(na, nb, sd, 0) {
		return nil
	}
	return e.writeTree(t, leaves)
}

// splitRatio sets the split that parts pane from target, the nearest one
// with each on a side of it, to give pane's side ratio of the room: a
// divider dragged, or set back to the middle. A split is named by a pane on
// each side, which a client knows, not by the tree's own numbering.
func (e *engine) splitRatio(pane, target string, ratio float64) error {
	if pane == "" || target == "" || pane == target || ratio <= 0 || ratio >= 1 || e.ti.Mode() != ArrangeModeBSP {
		return nil
	}
	t, leaves, nums := e.ti.BSPTree()
	a, b := t.WindowToNode[nums[pane]], t.WindowToNode[nums[target]]
	if a == nil || b == nil {
		return nil
	}
	above := map[*layout.TileNode]*layout.TileNode{} // ancestor -> the child on pane's path
	for c := a; c.Parent != nil; c = c.Parent {
		above[c.Parent] = c
	}
	for c := b; c.Parent != nil; c = c.Parent {
		via, ok := above[c.Parent]
		if !ok {
			continue
		}
		p := c.Parent
		r := min(max(ratio, 0.1), 0.9)
		if via != p.Left {
			r = 1 - r
		}
		if p.SplitRatio == r {
			return nil
		}
		p.SplitRatio = r
		return e.writeTree(t, leaves)
	}
	return nil
}

// place puts the tiled panes the workspace's tree lacks into it, by tuios's
// rules (PlaceMissing), with what the client knows of them in p, and writes
// the tree. A TUI on the session places such panes itself.
func (e *engine) place(p ArrangeParams) error {
	if e.ti.Mode() != ArrangeModeBSP || e.ti.TUI {
		return nil
	}
	pc := PlaceContext{Asked: p.Asked, Target: p.Target, Side: p.Mode}
	if !p.Asked {
		pc.Prev = p.Target
	}
	if p.Slot != nil && p.Pane != "" {
		pc.Slots = map[string]FloatSlot{p.Pane: *p.Slot}
	}
	tree, leaves, changed, _ := PlaceMissing(e.ti, e.wins, e.focus, pc)
	if !changed || tree.IsEmpty() {
		return nil
	}
	return e.writeTree(tree, leaves)
}
