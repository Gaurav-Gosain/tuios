package app

import (
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

const (
	convergeClients     = 3
	convergeSteps       = 18
	convergeSeqs        = 5
	convergeSeed        = 0x7c105
	convergeMaxPanes    = 5
	convergeQuiet       = 40 * time.Millisecond
	convergeDeliveryCap = 120
)

type procConfig struct {
	shared     bool
	gap        int
	sidebarPos string
	dockPos    string
}

func (g procConfig) install() {
	config.Global.SharedBorders = g.shared
	config.Global.PaneGap = g.gap
	config.Global.SidebarPosition = g.sidebarPos
	config.Global.DockbarPosition = g.dockPos
}

type fleetClient struct {
	m    *OS
	c    *session.TUIClient
	name string
	cols int
	rows int
	g    procConfig
}

type fleet struct {
	t   *testing.T
	r   *rig
	ex  *exchange
	cs  []*fleetClient
	rng *rand.Rand

	seed    uint64
	journal []string

	mu          sync.Mutex
	daemonState *session.SessionState
}

func (f *fleet) daemon() *session.SessionState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.daemonState
}

func (f *fleet) waitDaemon(what string, cond func(*session.SessionState) bool) {
	f.t.Helper()
	deadline := time.Now().Add(rigWait)
	for {
		if cond(f.daemon()) {
			return
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

const (
	fleetHolderCols = 200
	fleetHolderRows = 60
	fleetMinCols    = 70
	fleetMaxCols    = 150
	fleetMinRows    = 22
	fleetMaxRows    = 46
)

func newFleet(t *testing.T, seed uint64) *fleet {
	t.Helper()

	prevShared, prevGap := config.Global.SharedBorders, config.Global.PaneGap
	prevAnim := config.Global.Motion
	prevSidebar := config.Global.SidebarEnabled
	prevSidebarWidth := config.Global.SidebarWidth
	prevSidebarPos := config.Global.SidebarPosition
	prevDockPos := config.Global.DockbarPosition
	config.Global.Motion = config.MotionNone
	config.Global.SidebarEnabled = true
	config.Global.SidebarWidth = 24
	t.Cleanup(func() {
		config.Global.SharedBorders, config.Global.PaneGap = prevShared, prevGap
		config.Global.Motion = prevAnim
		config.Global.SidebarEnabled = prevSidebar
		config.Global.SidebarWidth = prevSidebarWidth
		config.Global.SidebarPosition = prevSidebarPos
		config.Global.DockbarPosition = prevDockPos
	})

	f := &fleet{t: t, seed: seed, rng: rand.New(rand.NewPCG(seed, seed^0x9e3779b9))}

	base := procConfig{
		shared:     config.Global.SharedBorders,
		gap:        config.Global.PaneGap,
		sidebarPos: config.Global.SidebarPosition,
		dockPos:    config.Global.DockbarPosition,
	}
	base.install()

	f.r = newRigSized(t, 2, fleetHolderCols, fleetHolderRows)
	f.ex = &exchange{t: t}

	f.r.ctl.OnStateSync(func(state *session.SessionState, _, _ string) {
		f.mu.Lock()
		if f.daemonState == nil || state.SnapshotSeq == 0 || state.SnapshotSeq > f.daemonState.SnapshotSeq {
			f.daemonState = state
		}
		f.mu.Unlock()
	})

	f.r.m.AutoTiling = true
	f.r.m.TileAllWindows()
	f.r.m.SyncStateToDaemon()
	f.waitDaemon("the tiled session to reach the daemon", func(st *session.SessionState) bool {
		return st != nil && st.AutoTiling && st.PaneGeometry != nil && len(st.WorkspaceTrees) > 0
	})

	for i := range convergeClients {
		g := procConfig{
			shared:     f.rng.IntN(2) == 0,
			gap:        f.rng.IntN(3),
			sidebarPos: []string{"left", "right"}[f.rng.IntN(2)],
			dockPos:    []string{"bottom", "top"}[f.rng.IntN(2)],
		}
		g.install()
		cols := fleetMinCols + f.rng.IntN(fleetMaxCols-fleetMinCols+1)
		rows := fleetMinRows + f.rng.IntN(fleetMaxRows-fleetMinRows+1)
		p := joinPeerOS(t, f.r, cols, rows)
		fc := &fleetClient{m: p.m, c: p.c, name: string(rune('A' + i)), cols: cols, rows: rows, g: g}
		f.cs = append(f.cs, fc)
		f.route(fc)
		f.trace(fc, "attached")
		fc.m.AnnounceLayoutReserve()
		fc.commit()
		f.trace(fc, "announced and pushed")
		f.settle(fmt.Sprintf("client %s joining", fc.name))
	}
	return f
}

func (f *fleet) route(fc *fleetClient) {
	fc.c.OnStateSync(func(state *session.SessionState, trigger, sourceID string) {
		f.ex.enqueue(func() {
			f.ex.n++
			fc.g.install()
			if err := fc.m.ApplyStateSyncFrom(state, sourceID); err != nil {
				f.t.Errorf("client %s: apply state sync: %v", fc.name, err)
			}
			fc.m.AnnounceLayoutReserve()
			f.trace(fc, "applied a "+trigger+" sync v"+strconv.Itoa(state.Version))
		})
	})
	fc.c.OnSessionResize(func(width, height, clientCount int, reserve session.LayoutReserve) {
		gen := fc.c.SessionLayoutGeneration()
		f.ex.enqueue(func() {
			f.ex.n++
			fc.g.install()
			fc.m.Update(SessionResizeMsg{
				Width:       width,
				Height:      height,
				ClientCount: clientCount,
				Reserve:     reserve,
				Generation:  gen,
			})
			f.trace(fc, fmt.Sprintf("took session %dx%d reserve %+v", width, height, reserve))
		})
	})
}

var convergeModes = os.Getenv("TUIOS_CONVERGE_MODES") != ""

var convergeTrace = os.Getenv("TUIOS_CONVERGE_TRACE") != ""

func (f *fleet) trace(fc *fleetClient, what string) {
	if !convergeTrace {
		return
	}
	v := viewOf(fc.m)
	f.t.Logf("  %s %s -> own %+v session %+v render %dx%d tiling=%t shared=%t gap=%d panes %s",
		fc.name, what, fc.m.OwnLayoutReserve(), v.reserve, fc.m.GetRenderWidth(), fc.m.GetRenderHeight(),
		v.autoTiling, v.shared, v.gap, v.panesString())
}

func (fc *fleetClient) commit() {
	fc.m.SyncStateToDaemon()
	fc.m.AnnounceLayoutReserve()
}

type paneView struct {
	pty       string
	x, y      int
	w, h      int
	cw, ch    int
	workspace int
	z         int
	minimized bool
	floating  bool
	zoomed    bool
}

func (p paneView) String() string {
	return fmt.Sprintf("%s ws%d @%d,%d %dx%d guest %dx%d z%d min=%t float=%t zoom=%t",
		shortID(p.pty), p.workspace, p.x, p.y, p.w, p.h, p.cw, p.ch, p.z, p.minimized, p.floating, p.zoomed)
}

type clientView struct {
	panes       []paneView
	focus       string
	workspace   int
	autoTiling  bool
	layoutMode  string
	masterRatio float64
	shared      bool
	gap         int
	reserve     session.LayoutReserve
	effW, effH  int
	scrollX     int
}

func viewOf(m *OS) clientView {
	v := clientView{
		workspace:   m.CurrentWorkspace,
		autoTiling:  m.AutoTiling,
		layoutMode:  m.LayoutModeName(),
		masterRatio: m.MasterRatio,
		shared:      m.SharedBorders,
		gap:         m.PaneGap,
		reserve:     m.SessionReserve,
		effW:        m.EffectiveWidth,
		effH:        m.EffectiveHeight,
	}
	v.scrollX = -1
	if sl := m.WorkspaceScrollingLayouts[m.CurrentWorkspace]; sl != nil {
		v.scrollX = sl.ViewportX
	}
	if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
		v.focus = m.Windows[m.FocusedWindow].PTYID
	}
	for _, w := range m.Windows {
		if w == nil {
			continue
		}
		p := paneView{
			pty:       w.PTYID,
			workspace: w.Workspace,
			z:         w.Z,
			minimized: w.Minimized,
			floating:  w.IsFloating,
			zoomed:    w.Zoomed,
		}
		if w.Workspace == m.CurrentWorkspace {
			p.x, p.y = w.X, w.Y
			p.w, p.h = w.Width, w.Height
			p.cw, p.ch = w.ContentWidth(), w.ContentHeight()
		}
		v.panes = append(v.panes, p)
	}
	slices.SortFunc(v.panes, func(a, b paneView) int { return strings.Compare(a.pty, b.pty) })
	return v
}

func (v clientView) panesString() string {
	parts := make([]string, 0, len(v.panes))
	for _, p := range v.panes {
		parts = append(parts, p.String())
	}
	return strings.Join(parts, " | ")
}

func (v clientView) diff(o clientView) string {
	switch {
	case v.effW != o.effW || v.effH != o.effH:
		return fmt.Sprintf("session size %dx%d vs %dx%d", v.effW, v.effH, o.effW, o.effH)
	case v.reserve != o.reserve:
		return fmt.Sprintf("chrome reserve %+v vs %+v", v.reserve, o.reserve)
	case v.shared != o.shared:
		return fmt.Sprintf("shared borders %t vs %t", v.shared, o.shared)
	case v.gap != o.gap:
		return fmt.Sprintf("pane gap %d vs %d", v.gap, o.gap)
	case v.workspace != o.workspace:
		return fmt.Sprintf("current workspace %d vs %d", v.workspace, o.workspace)
	case v.autoTiling != o.autoTiling:
		return fmt.Sprintf("tiling %t vs %t", v.autoTiling, o.autoTiling)
	case v.layoutMode != o.layoutMode:
		return fmt.Sprintf("layout mode %q vs %q", v.layoutMode, o.layoutMode)
	case v.masterRatio != o.masterRatio:
		return fmt.Sprintf("master ratio %.4f vs %.4f", v.masterRatio, o.masterRatio)
	case v.focus != o.focus:
		return fmt.Sprintf("focused pane %s vs %s", shortID(v.focus), shortID(o.focus))
	case v.layoutMode == LayoutModeScrolling && v.scrollX != o.scrollX:
		return fmt.Sprintf("the strip is scrolled to %d vs %d", v.scrollX, o.scrollX)
	case len(v.panes) != len(o.panes):
		return fmt.Sprintf("%d panes vs %d panes", len(v.panes), len(o.panes))
	}
	for i := range v.panes {
		a, b := v.panes[i], o.panes[i]
		if a.pty != b.pty {
			return fmt.Sprintf("pane %d is %s on one and %s on the other", i, shortID(a.pty), shortID(b.pty))
		}
		if a != b {
			return fmt.Sprintf("pane %s: %s vs %s", shortID(a.pty), a, b)
		}
	}
	return ""
}

func (f *fleet) diverged() string {
	base := viewOf(f.cs[0].m)
	for _, fc := range f.cs[1:] {
		if d := base.diff(viewOf(fc.m)); d != "" {
			return fmt.Sprintf("clients %s and %s disagree: %s", f.cs[0].name, fc.name, d)
		}
	}

	st := f.daemon()
	if st == nil {
		return "the daemon has not published a state yet"
	}
	daemonPanes := map[string]session.WindowState{}
	for _, ws := range st.Windows {
		daemonPanes[ws.PTYID] = ws
	}
	if len(daemonPanes) != len(base.panes) {
		return fmt.Sprintf("the daemon holds %d panes, the clients hold %d", len(daemonPanes), len(base.panes))
	}
	for _, p := range base.panes {
		ws, ok := daemonPanes[p.pty]
		if !ok {
			return fmt.Sprintf("the clients hold pane %s and the daemon does not", shortID(p.pty))
		}
		if ws.Workspace != p.workspace || ws.Minimized != p.minimized || ws.IsFloating != p.floating || ws.Zoomed != p.zoomed {
			return fmt.Sprintf("pane %s: the daemon has ws%d min=%t float=%t zoom=%t, the clients ws%d min=%t float=%t zoom=%t",
				shortID(p.pty), ws.Workspace, ws.Minimized, ws.IsFloating, ws.Zoomed, p.workspace, p.minimized, p.floating, p.zoomed)
		}
	}
	if st.CurrentWorkspace != base.workspace {
		return fmt.Sprintf("the daemon is on workspace %d, the clients on %d", st.CurrentWorkspace, base.workspace)
	}
	if st.AutoTiling != base.autoTiling {
		return fmt.Sprintf("the daemon has tiling %t, the clients %t", st.AutoTiling, base.autoTiling)
	}
	if st.LayoutMode != "" && st.LayoutMode != base.layoutMode {
		return fmt.Sprintf("the daemon has the %s layout, the clients the %s layout", st.LayoutMode, base.layoutMode)
	}
	if st.MasterRatio != base.masterRatio {
		return fmt.Sprintf("the daemon has master ratio %.4f, the clients %.4f", st.MasterRatio, base.masterRatio)
	}
	if pg := st.PaneGeometry; pg == nil {
		return "the daemon holds no agreed pane geometry"
	} else if pg.SharedBorders != base.shared || pg.PaneGap != base.gap {
		return fmt.Sprintf("the daemon has shared borders %t gap %d, the clients %t and %d",
			pg.SharedBorders, pg.PaneGap, base.shared, base.gap)
	}
	if daemonFocus := f.daemonFocusPTY(st); daemonFocus != base.focus {
		return fmt.Sprintf("the daemon focuses %s, the clients focus %s", shortID(daemonFocus), shortID(base.focus))
	}

	for _, p := range base.panes {
		if p.minimized || p.workspace != base.workspace {
			continue
		}
		dw, dh, err := f.ptySize(p.pty)
		if err != nil {
			return fmt.Sprintf("pane %s: cannot read the daemon's size: %v", shortID(p.pty), err)
		}
		if dw != p.cw || dh != p.ch {
			return fmt.Sprintf("pane %s: the daemon runs the shell at %dx%d, every client draws it at %dx%d",
				shortID(p.pty), dw, dh, p.cw, p.ch)
		}
	}
	return ""
}

func (f *fleet) daemonFocusPTY(st *session.SessionState) string {
	for _, ws := range st.Windows {
		if ws.ID == st.FocusedWindowID {
			return ws.PTYID
		}
	}
	return ""
}

func (f *fleet) ptySize(ptyID string) (int, int, error) {
	st, err := f.r.ctl.GetTerminalState(ptyID, -1, 0)
	if err != nil {
		return 0, 0, err
	}
	if st == nil {
		return 0, 0, fmt.Errorf("no state")
	}
	return st.Width, st.Height, nil
}

func (f *fleet) settle(what string) {
	f.t.Helper()
	f.ex.n = 0
	deadline := time.Now().Add(rigWait)
	var last string
	for {
		if n := f.ex.settle(convergeDeliveryCap, convergeQuiet); n >= convergeDeliveryCap {
			f.fail(what, fmt.Sprintf("the fleet was still trading state after %d deliveries", n))
		}
		last = f.diverged()
		if !f.ex.queued() && last == "" {
			return
		}
		if time.Now().After(deadline) {
			if last == "" {
				last = "the queue never emptied"
			}
			f.fail(what, last)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (f *fleet) fail(what, why string) {
	f.t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s\n", what, why)
	fmt.Fprintf(&b, "\nreplay with TUIOS_CONVERGE_SEED=%d\n", f.seed)
	fmt.Fprintf(&b, "(the seed fixes the actions, not the order real sockets deliver in,\n")
	fmt.Fprintf(&b, " so a divergence that needed a particular interleaving wants -count=N)\n")
	fmt.Fprintf(&b, "\nactions:\n")
	for i, a := range f.journal {
		fmt.Fprintf(&b, "  %2d. %s\n", i+1, a)
	}
	fmt.Fprintf(&b, "\nclients:\n")
	for _, fc := range f.cs {
		v := viewOf(fc.m)
		fmt.Fprintf(&b, "  %s (terminal %dx%d, config shared=%t gap=%d)\n",
			fc.name, fc.cols, fc.rows, fc.g.shared, fc.g.gap)
		fc.g.install()
		fmt.Fprintf(&b, "     rail on the %s, own reserve %+v\n", fc.g.sidebarPos, fc.m.OwnLayoutReserve())
		fmt.Fprintf(&b, "     session %dx%d reserve %+v ws%d %s tiling=%t shared=%t gap=%d focus %s\n",
			v.effW, v.effH, v.reserve, v.workspace, v.layoutMode, v.autoTiling, v.shared, v.gap, shortID(v.focus))
		fmt.Fprintf(&b, "     panes %s\n", v.panesString())
		fmt.Fprintf(&b, "     bsp ids %s tree leaves %v\n", bspIDs(fc.m), treeLeaves(fc.m))
	}
	if st := f.daemon(); st != nil {
		fmt.Fprintf(&b, "  daemon ws%d focus %s\n", st.CurrentWorkspace, shortID(f.daemonFocusPTY(st)))
		for _, ws := range st.Windows {
			dw, dh, err := f.ptySize(ws.PTYID)
			if err != nil {
				fmt.Fprintf(&b, "     %s ws%d shell unreadable: %v\n", shortID(ws.PTYID), ws.Workspace, err)
				continue
			}
			fmt.Fprintf(&b, "     %s ws%d shell %dx%d min=%t float=%t zoom=%t\n",
				shortID(ws.PTYID), ws.Workspace, dw, dh, ws.Minimized, ws.IsFloating, ws.Zoomed)
		}
	}
	f.t.Fatal(b.String())
}

func bspIDs(m *OS) string {
	parts := make([]string, 0, len(m.Windows))
	for _, w := range m.Windows {
		parts = append(parts, fmt.Sprintf("%s=%d", shortID(w.PTYID), m.WindowToBSPID[w.ID]))
	}
	slices.Sort(parts)
	return strings.Join(parts, " ")
}

func treeLeaves(m *OS) []int {
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil {
		return nil
	}
	ids := tree.GetAllWindowIDs()
	slices.Sort(ids)
	return ids
}

func visibleIdx(m *OS) []int {
	var out []int
	for i, w := range m.Windows {
		if w != nil && w.Workspace == m.CurrentWorkspace && !w.Minimized {
			out = append(out, i)
		}
	}
	return out
}

func (f *fleet) step() string {
	fc := f.cs[f.rng.IntN(len(f.cs))]
	fc.g.install()
	m := fc.m

	pick := f.rng.IntN(10)
	if pick == 9 && len(f.cs) < 3 {
		pick = 3
	}
	switch pick {
	case 0:
		cols := fleetMinCols + f.rng.IntN(fleetMaxCols-fleetMinCols+1)
		rows := fleetMinRows + f.rng.IntN(fleetMaxRows-fleetMinRows+1)
		fc.cols, fc.rows = cols, rows
		m.Width, m.Height = cols, rows
		m.DaemonClient.SetOwnLayoutReserve(m.OwnLayoutReserve())
		if err := m.DaemonClient.NotifyTerminalSize(cols, rows); err != nil {
			f.t.Fatalf("client %s: announce terminal size: %v", fc.name, err)
		}
		if m.AutoTiling {
			m.TileAllWindows()
		} else {
			m.ClampWindowsToView()
		}
		fc.commit()
		return fmt.Sprintf("client %s resized its terminal to %dx%d", fc.name, cols, rows)

	case 1:
		if len(m.Windows) >= convergeMaxPanes {
			return ""
		}
		m.AddWindow("")
		fc.commit()
		return fmt.Sprintf("client %s opened a pane", fc.name)

	case 2:
		vis := visibleIdx(m)
		if len(m.Windows) <= 1 || len(vis) == 0 {
			return ""
		}
		i := vis[f.rng.IntN(len(vis))]
		id := shortID(m.Windows[i].PTYID)
		m.DeleteWindow(i)
		fc.commit()
		return fmt.Sprintf("client %s closed pane %s", fc.name, id)

	case 3:
		vis := visibleIdx(m)
		if len(vis) < 2 {
			return ""
		}
		if f.rng.IntN(2) == 0 {
			m.CycleToNextVisibleWindow()
		} else {
			m.CycleToPreviousVisibleWindow()
		}
		fc.commit()
		return fmt.Sprintf("client %s moved focus to %s", fc.name, shortID(viewOf(m).focus))

	case 4:
		if m.GetFocusedWindow() == nil {
			return ""
		}
		id := shortID(m.GetFocusedWindow().PTYID)
		zoomed := m.GetFocusedWindow().Zoomed
		m.ToggleZoom()
		fc.commit()
		if zoomed {
			return fmt.Sprintf("client %s unzoomed %s", fc.name, id)
		}
		return fmt.Sprintf("client %s zoomed %s", fc.name, id)

	case 5:
		if convergeModes {
			m.ToggleLayoutMode()
		} else {
			_ = m.NextLayout()
		}
		fc.commit()
		return fmt.Sprintf("client %s switched to the %s layout", fc.name, m.LayoutModeName())

	case 6:
		ws := 1 + f.rng.IntN(min(3, m.NumWorkspaces))
		if ws == m.CurrentWorkspace {
			return ""
		}
		m.SwitchToWorkspace(ws)
		fc.commit()
		return fmt.Sprintf("client %s switched to workspace %d", fc.name, ws)

	case 7:
		if f.rng.IntN(2) == 0 {
			v := !m.SharedBorders
			m.SetSharedBordersSetting(v)
			fc.g.shared = v
			fc.commit()
			return fmt.Sprintf("client %s set shared borders to %t", fc.name, v)
		}
		v := (m.PaneGap + 1) % 3
		m.SetPaneGapSetting(v)
		fc.g.gap = v
		fc.commit()
		return fmt.Sprintf("client %s set the pane gap to %d", fc.name, v)

	case 9:
		gone := fc.name
		for _, w := range m.Windows {
			w.Close()
		}
		if err := fc.c.Detach(); err != nil {
			f.t.Fatalf("client %s detach: %v", gone, err)
		}
		_ = fc.c.Close()
		f.cs = slices.DeleteFunc(f.cs, func(o *fleetClient) bool { return o == fc })
		f.journal = append(f.journal, fmt.Sprintf("client %s detached", gone))
		f.settle("client " + gone + " detaching")

		cols := fleetMinCols + f.rng.IntN(fleetMaxCols-fleetMinCols+1)
		rows := fleetMinRows + f.rng.IntN(fleetMaxRows-fleetMinRows+1)
		fc.g.install()
		p := joinPeerOS(f.t, f.r, cols, rows)
		back := &fleetClient{m: p.m, c: p.c, name: gone, cols: cols, rows: rows, g: fc.g}
		f.cs = append(f.cs, back)
		f.route(back)
		back.m.AnnounceLayoutReserve()
		back.commit()
		return fmt.Sprintf("client %s came back at %dx%d", gone, cols, rows)

	default:
		m.SidebarCollapsed = !m.SidebarCollapsed
		fc.commit()
		return fmt.Sprintf("client %s set its rail collapsed to %t", fc.name, m.SidebarCollapsed)
	}
}

func (f *fleet) run(steps int) {
	f.t.Helper()
	for range steps {
		what := f.step()
		if what == "" {
			continue
		}
		f.journal = append(f.journal, what)
		f.settle(what)
	}
}

func convergeEnvInt(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// TestMultiClientConvergence is the harness. It runs in the normal suite
// rather than behind a build tag: convergeSeqs sequences of convergeSteps
// actions across convergeClients clients costs a few seconds, which is a
// fraction of what internal/app already spends, and a harness only somebody
// remembers to run is a harness that finds bugs after they ship.
//
// To run it longer, or to replay a failure:
//
//	TUIOS_CONVERGE_SEQS=200 go test ./internal/app -run Convergence
//	TUIOS_CONVERGE_SEED=12345 go test ./internal/app -run Convergence
//	TUIOS_CONVERGE_SEED=random TUIOS_CONVERGE_SEQS=50 go test ./internal/app -run Convergence
//
// TUIOS_CONVERGE_MODES turns on an action this tree does not converge under, and
// names the three bugs it reproduces above. TUIOS_CONVERGE_TRACE prints a line
// per delivery, which is how a failing seed is read.
func TestMultiClientConvergence(t *testing.T) {
	seqs := convergeEnvInt("TUIOS_CONVERGE_SEQS", convergeSeqs)
	steps := convergeEnvInt("TUIOS_CONVERGE_STEPS", convergeSteps)

	base := uint64(convergeSeed)
	if v := os.Getenv("TUIOS_CONVERGE_SEED"); v != "" {
		if v == "random" {
			base = uint64(time.Now().UnixNano())
		} else if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			base = n
			seqs = 1
		}
	}
	t.Logf("%d sequences of %d actions across %d clients, base seed %d", seqs, steps, convergeClients, base)

	for i := range seqs {
		seed := base + uint64(i)*0x9e3779b97f4a7c15
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			f := newFleet(t, seed)
			f.run(steps)
		})
	}
}
