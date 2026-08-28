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

// A convergence harness for N clients on one session.
//
// The four multi-client geometry bugs of this week (8de5a589, 846b7d28,
// b71def75, a5d974ed) were one fault repeated: a piece of layout authority
// still lived in the client, so two clients held two opinions about one
// rectangle. Each was found by hand, reproduced by hand, and pinned by a test
// written for that one shape. This runs the class instead of the instances: N
// headless clients attach at sizes they chose themselves, a seeded random
// sequence of ordinary actions is played into them one at a time, and after
// every action the fleet has to converge.
//
// # What converged means here
//
// Two halves, because there are two authorities and they own different things.
//
// Client against client, exactly equal: every pane's rectangle (x, y, width,
// height), every pane's guest grid (the cells its shell is given), its
// workspace, its z, its minimized and floating flags; which pane is focused;
// the current workspace; the tiling flag, the layout mode and the master ratio;
// the pane-geometry arithmetic (shared borders, pane gap); and the negotiated
// box (the session's effective size and its chrome reserve).
//
// Client against daemon: the set of panes and their workspace, minimized and
// floating flags; which pane is focused; and - the one that matters most - the
// size the daemon is actually running each pane's shell at has to be the guest
// grid every client draws it in. A PTY has exactly one size, so this is not a
// convention, it is arithmetic: if the clients disagree, one of them is lying
// to the person reading it.
//
// # Why not byte-equal frames
//
// Too strong, and wrong for a reason the design depends on. Clients attach at
// their own terminal sizes; the session's box is the minimum over them, so a
// larger client legitimately draws a blank band the smaller one does not have.
// Theme, colours, border style, glyphs, title position and dimming are
// per-client and meant to be (see pane_geometry.go). Input mode is per-viewer.
// A frame comparison would fail on every one of those and would have to be
// weakened until it asserted nothing.
//
// # Why not something weaker
//
// "The clients stopped talking to each other" is the obvious weaker predicate,
// and it is what the existing ping-pong test measures. It is not enough on its
// own: every divergence this harness found on the tree it was written against
// was a quiet one. The clients had nothing left to say and were sat on
// different arithmetic, which no amount of watching the traffic can see. Quiet
// is necessary and not sufficient, so this asserts the state and uses quiet
// only as the sampling condition.
//
// # What is deliberately not asserted
//
// The daemon's own copy of the pane rectangles. The daemon stores what the last
// client to sync pushed; a client that retiles because it disagreed with a
// peer's sync must not push the result (that is the applyingPeerSync guard, and
// removing it is what makes two clients argue forever), so the daemon can
// legitimately hold rectangles nobody is drawing. What the daemon does own is
// the PTY size, and that is asserted.
//
// The geometry of panes on a workspace nobody is showing. Tiling walks the
// current workspace, so nothing lays those panes out, and two clients that last
// showed that workspace at different moments hold different rectangles with
// nothing wrong. Their daemon-owned fields are still compared.
//
// # What it is measured to catch
//
// Reverting 846b7d28 - the pane geometry arithmetic back onto the config
// globals - fails all five sequences, naming the panes that two clients give
// the same rectangle and different guest grids. Reverting the reserve
// negotiation of 8de5a589 - paneReserve returning this client's own chrome
// instead of the session's agreed reserve - fails three of five, naming the
// pane two clients put at different x.
//
// Two of the four it was written for it does not catch, which is worth saying
// out loud. b71def75's third fault (a layout that does not reach the edges is
// adopted anyway) needs a stale layout to arrive with no size change beside it
// to force a retile, and no action here produces one: every route that leaves a
// cramped layout on the daemon also moves the session's box, and the resize
// that follows retiles everyone. a5d974ed (a broadcast that beats the handlers
// is dropped) is a race the commit itself measured at 2 in 200 under load;
// reverting it leaves forty sequences green here, because the fleet registers
// its handlers immediately after attaching and there is rarely anything in
// flight to lose.
//
// # When to sample
//
// Never on a fixed sleep. After each action the harness delivers queued
// broadcasts until the fleet satisfies the predicate above and the queue is
// empty, or until a generous deadline expires; on expiry it reports the
// predicate's own complaint. A converged fleet reaches the condition in
// milliseconds, so the deadline is never the timing knob - it only bounds a
// failure. A separate cap on deliveries catches the other failure mode, two
// clients trading rectangles without end, which no deadline can tell from slow.
//
// # Reproducibility
//
// Every sequence is a seed. The seed and the whole action journal are printed
// on failure, and TUIOS_CONVERGE_SEED replays one.
//
// What the seed fixes is the actions: which client acts, what it does, and the
// sizes and configs the fleet was built with. It does not fix the order the
// daemon's broadcasts come back in, because those travel over real sockets
// through real goroutines. A divergence that needed a particular interleaving
// replays as a probability rather than a certainty, so the failure says so and
// says to repeat the seed.

const (
	// convergeClients is how many clients attach. Three rather than two: with
	// two, "the peer" is unambiguous and a bug that mis-addresses a broadcast
	// still reaches the only other client. Three is the smallest fleet where
	// forwarding to the wrong peer is visible.
	convergeClients = 3
	// convergeSteps is the actions per sequence, and convergeSeqs the
	// sequences. See TestMultiClientConvergence for the cost.
	convergeSteps = 18
	convergeSeqs  = 5
	// convergeSeed is the base seed. It is fixed so the normal suite runs the
	// same sequences every time and cannot become a new source of flake: this
	// is a regression test by default, and searching is what TUIOS_CONVERGE_SEED
	// and TUIOS_CONVERGE_SEQS are for.
	convergeSeed = 0x7c105
	// convergeMaxPanes bounds how many real shells a sequence can spawn.
	convergeMaxPanes = 5
	// convergeQuiet is how long the settler waits for another broadcast before
	// calling the queue empty. It is a poll interval, not a settle time: the
	// predicate decides when to stop, and this only decides how often it is
	// asked.
	convergeQuiet = 40 * time.Millisecond
	// convergeDeliveryCap bounds the deliveries one action may produce. An
	// action costs a handful; a fleet trading rectangles never stops, and this
	// is what tells the two apart.
	convergeDeliveryCap = 120
)

// procConfig is one simulated client process's copy of the geometry globals.
// The fleet runs in one process, so without this every client would read one
// set of globals and two clients could never disagree about the arithmetic -
// which is the disagreement 846b7d28 was about. Installing a client's own
// values before anything runs on its behalf is what makes the fleet behave like
// N processes with N config files. It doubles as a canary: any layout path that
// still reads a global instead of the session-settled model value shows up as a
// divergence.
type procConfig struct {
	shared bool
	gap    int
	// sidebarPos and dockPos are which edges this client keeps for its own
	// chrome, and they stay per-client: a rail on the left and a rail on the
	// right are two people's preferences, not a disagreement to settle. What
	// they do is make the reserve negotiation load-bearing, because clients
	// keeping different edges for themselves is the case the session's agreed
	// reserve exists to handle. Without them every client in the fleet asks for
	// the same chrome and the negotiation is a no-op no assertion can see.
	sidebarPos string
	dockPos    string
}

func (g procConfig) install() {
	config.Global.SharedBorders = g.shared
	config.Global.PaneGap = g.gap
	config.Global.SidebarPosition = g.sidebarPos
	config.Global.DockbarPosition = g.dockPos
}

// fleetClient is one attached client: its model, its connection, the size its
// terminal is, and the config its process was started with.
type fleetClient struct {
	m    *OS
	c    *session.TUIClient
	name string
	cols int
	rows int
	g    procConfig
}

// fleet is N clients on one session, plus the rig's control connection, which
// is how the daemon's own state is read.
type fleet struct {
	t   *testing.T
	r   *rig
	ex  *exchange
	cs  []*fleetClient
	rng *rand.Rand

	seed    uint64
	journal []string

	// daemonState is the newest state the daemon has broadcast. The control
	// connection is never a sync's source, so it is sent every one of them, and
	// what the daemon broadcasts is the merged state it holds. It is written by
	// that connection's read loop, so it is read under the lock and it is read
	// live rather than through the delivery queue: the daemon is the authority,
	// and the harness must never be able to satisfy itself with a stale copy of
	// what it is measuring against.
	mu          sync.Mutex
	daemonState *session.SessionState
}

func (f *fleet) daemon() *session.SessionState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.daemonState
}

// waitDaemon blocks until the daemon's published state satisfies cond. A client
// sync is sent without waiting for an answer - the daemon has no reply to make -
// so anything that reads the daemon after pushing to it has to wait on the
// state rather than on the send returning.
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

// --- bringing the fleet up ---------------------------------------------------

// fleetHolderCols and fleetHolderRows size the rig's own connections. Both the
// bootstrap control client and the session's minimum are measured across every
// attached client, so the control connection has to be at least as large as any
// client the harness will make, or it would pin the session's size itself.
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
	prevAnim := config.Global.AnimationsEnabled
	prevSidebar := config.Global.SidebarEnabled
	prevSidebarWidth := config.Global.SidebarWidth
	prevSidebarPos := config.Global.SidebarPosition
	prevDockPos := config.Global.DockbarPosition
	config.Global.AnimationsEnabled = false
	config.Global.SidebarEnabled = true
	config.Global.SidebarWidth = 24
	t.Cleanup(func() {
		config.Global.SharedBorders, config.Global.PaneGap = prevShared, prevGap
		config.Global.AnimationsEnabled = prevAnim
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

	// The rig's own client is left attached and passive. It is not one of the
	// fleet: it exists so the control connection and the session's minimum are
	// measured against a client larger than anything the harness will make, and
	// it stands in for the browser tab somebody left open at full screen.
	f.r = newRigSized(t, 2, fleetHolderCols, fleetHolderRows)
	f.ex = &exchange{t: t}

	// The control connection records the daemon's state for the oracle. It is
	// never a sync's source, so it is sent every broadcast, and what the daemon
	// broadcasts is the merged state it holds.
	f.r.ctl.OnStateSync(func(state *session.SessionState, _, _ string) {
		f.mu.Lock()
		f.daemonState = state
		f.mu.Unlock()
	})

	// The session is tiled before anyone joins, and the tiling topology is on
	// the daemon. That is what a client attaching to a session someone is
	// already using finds, and it is what makes the fleet's clients share one
	// tree: a tree is adopted from the attach reply and from a strictly newer
	// daemon state, never from a peer's push, so N clients that each build one
	// from scratch keep N different topologies until the next daemon-side
	// mutation. Starting them from one tree is the ordinary case; the other one
	// is a bug of its own and not the one this harness is measuring.
	f.r.m.AutoTiling = true
	f.r.m.TileAllWindows()
	f.r.m.SyncStateToDaemon()
	f.waitDaemon("the tiled session to reach the daemon", func(st *session.SessionState) bool {
		return st != nil && st.AutoTiling && st.PaneGeometry != nil && len(st.WorkspaceTrees) > 0
	})

	// The clients join one at a time, each settling before the next arrives.
	// That is the ordinary shape - a person opens a second terminal seconds
	// after the first, and the first has pushed after every key it saw - and it
	// is deliberately not the simultaneous case. Three clients that all attach
	// before any of them pushes each build their own tiling topology against
	// their own box, and a client push never carries the topology (see
	// newerState in ApplyStateSync), so nothing reconciles them until the next
	// daemon-side mutation. That is a divergence of its own and it is reported
	// as one; starting every sequence in it would only mean every sequence
	// failed for that one reason before an action had run.
	for i := range convergeClients {
		// Each client is a separate process as far as the geometry config is
		// concerned, so its globals are installed before it is built: NewOS
		// seeds the model from them, which is what a client walks in with.
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
		// What the first window-size message does in cmd/tuios: say what this
		// client keeps for its own chrome, then push.
		f.trace(fc, "attached")
		fc.m.AnnounceLayoutReserve()
		fc.commit()
		f.trace(fc, "announced and pushed")
		f.settle(fmt.Sprintf("client %s joining", fc.name))
	}
	return f
}

// route wires one client's incoming broadcasts into its model, by the route
// cmd/tuios and internal/input use: a state sync is applied and then this
// client's chrome is announced, because adopting a session's rail can have
// changed what this client keeps; a session resize goes through Update, which
// re-lays the panes out and announces in its turn.
func (f *fleet) route(fc *fleetClient) {
	fc.c.OnStateSync(func(state *session.SessionState, trigger, _ string) {
		f.ex.enqueue(func() {
			f.ex.n++
			fc.g.install()
			if err := fc.m.ApplyStateSync(state); err != nil {
				f.t.Errorf("client %s: apply state sync: %v", fc.name, err)
			}
			fc.m.AnnounceLayoutReserve()
			f.trace(fc, "applied a "+trigger+" sync v"+strconv.Itoa(state.Version))
		})
	})
	fc.c.OnSessionResize(func(width, height, clientCount int, reserve session.LayoutReserve) {
		f.ex.enqueue(func() {
			f.ex.n++
			fc.g.install()
			fc.m.Update(SessionResizeMsg{
				Width:       width,
				Height:      height,
				ClientCount: clientCount,
				Reserve:     reserve,
			})
			f.trace(fc, fmt.Sprintf("took session %dx%d reserve %+v", width, height, reserve))
		})
	})
}

// Two switches, both off by default, each guarding an action this tree does not
// yet converge under. They are off so the harness can guard everything else
// rather than being red for one known reason; they are here rather than deleted
// because a one-line reproduction of an open bug is worth more than a paragraph
// describing it.
//
// TUIOS_CONVERGE_ZOOM puts zooming back in the action set. Zoom is client-local
// state whose rectangle is session state: the pane's box is pushed and adopted
// everywhere, and the flag that says why is not. A peer reads the covering
// rectangle as a layout computed for somebody else's screen, tiles it away and
// resizes the shared shell, and the client that asked for the zoom is left
// drawing a guest grid the daemon is not running. Reproduce with
// TUIOS_CONVERGE_ZOOM=1 TUIOS_CONVERGE_SEED=8709371129874198873.
var convergeZoom = os.Getenv("TUIOS_CONVERGE_ZOOM") != ""

// TUIOS_CONVERGE_STALEPUSH restores the state push that internal/input makes
// after every input, including the one that asks the daemon for a pane. That
// push carries the state as it was before the pane existed, so it reaches the
// daemon after the creation it raced and is reconciled as stale - and
// reconcileStale keeps the pushing client's rectangles, so the old pane's
// whole-box rectangle becomes canonical and is broadcast to everyone. Nothing
// downstream can tell it from a current layout: tiledLayoutStale measures the
// box the panes span, and one pane covering the box spans it exactly. The
// client that adopts it draws a shell at twice the width the daemon runs it at.
// Reproduce with
// TUIOS_CONVERGE_STALEPUSH=1 TUIOS_CONVERGE_SEED=8709371129874198873.
var convergeStalePush = os.Getenv("TUIOS_CONVERGE_STALEPUSH") != ""

// TUIOS_CONVERGE_MODES makes the "another tiling layout" action cycle the three
// tiling modes. It is off for the same reason the two above are: this tree does
// not converge under it, and the three ways it does not are none of them about
// the action itself.
//
// Until this existed the action called NextLayout, which cycles saved layout
// templates rather than tiling modes. There are none under the test's own XDG
// tree, so every sequence ever run raised "No saved layouts" and stayed in BSP,
// while the journal said the layout had changed. The harness has therefore
// never seen master-stack or the scrolling strip.
//
// Turned on, three of the five default sequences fail, each naming a piece of
// layout authority still held by the client:
//
//   - the strip's column topology is not session state. A client that switches
//     to the scrolling layout builds its strip from its own window list, and a
//     peer that adopts the mode from the sync builds its own: two panes came
//     out stacked in one column on the client that made the switch (@24,2 and
//     @24,19) and side by side on the other two (@24,2 and @50,2), which the
//     failure names as the guest grid the stacked client keeps a border out of.
//     Reproduce with TUIOS_CONVERGE_MODES=1 TUIOS_CONVERGE_SEED=4354685564937353519.
//   - a pane opened in the scrolling layout is not placed by the strip on every
//     client: one held it at the box's corner while the others had it in a
//     column. Reproduce with
//     TUIOS_CONVERGE_MODES=1 TUIOS_CONVERGE_SEED=11400714819323706650.
//   - one pane alone on a workspace under master-stack with shared borders on:
//     one client hands its guest the whole rectangle and another deducts a
//     border, so the same rectangle carries two guest grids. Reproduce with
//     TUIOS_CONVERGE_MODES=1 TUIOS_CONVERGE_SEED=508165.
//
// The strip's offset - the thing this switch was added while pinning - is not
// among them: it is session state now (SessionState.ScrollStrip) and
// clientView compares it.
var convergeModes = os.Getenv("TUIOS_CONVERGE_MODES") != ""

// convergeTrace turns on a line per delivery, for reading a failing seed.
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

// commit is what internal/input does after any input that might have changed
// state: push, then say what this client keeps for its chrome. Nothing else is
// added here on purpose - a harness that helpfully re-announced every PTY size
// after every action would paper over exactly the bug it exists to find.
func (fc *fleetClient) commit() {
	fc.m.SyncStateToDaemon()
	fc.m.AnnounceLayoutReserve()
}

// --- the view each side holds ------------------------------------------------

type paneView struct {
	pty       string
	x, y      int
	w, h      int
	cw, ch    int
	workspace int
	z         int
	minimized bool
	floating  bool
}

func (p paneView) String() string {
	return fmt.Sprintf("%s ws%d @%d,%d %dx%d guest %dx%d z%d min=%t float=%t",
		shortID(p.pty), p.workspace, p.x, p.y, p.w, p.h, p.cw, p.ch, p.z, p.minimized, p.floating)
}

// clientView is everything a client has to agree with its peers about.
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
	// scrollX is where the scrolling strip is scrolled to on the workspace
	// being shown, and -1 when this client has no strip for it. It is compared
	// like the focus and the workspace, because it is the same kind of thing:
	// the strip is one row of columns and this is a place on it, so two clients
	// holding different offsets are showing different columns of one session.
	// It is also the term in every pane's x in that layout, so a divergence
	// here is a divergence in the rectangles, named at its source.
	scrollX int
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
	// Read, never created: asking for the strip would build one, and a client
	// that has never laid this workspace out has nothing to compare.
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
		}
		// Geometry is compared for the panes on the workspace being shown, and
		// only for those. A pane on another workspace is not being laid out by
		// anybody: tiling walks the current workspace, so the rectangle such a
		// pane holds is whatever it had when its workspace was last on screen,
		// and two clients that last showed it at different sizes hold different
		// numbers with nothing wrong. The daemon's own view of those panes -
		// which workspace, minimized, floating - is still compared, below.
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

// diff names the first way two clients disagree, or "" when they do not. It
// reports the field and both values rather than a boolean, because the whole
// point of the harness is that a failure says what diverged.
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

// --- the predicate -----------------------------------------------------------

// diverged reports how the fleet has failed to converge, or "" when it has.
func (f *fleet) diverged() string {
	base := viewOf(f.cs[0].m)
	for _, fc := range f.cs[1:] {
		if d := base.diff(viewOf(fc.m)); d != "" {
			return fmt.Sprintf("clients %s and %s disagree: %s", f.cs[0].name, fc.name, d)
		}
	}

	// The daemon's half. Its state is what it last broadcast, which is the
	// merged state it holds.
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
		if ws.Workspace != p.workspace || ws.Minimized != p.minimized || ws.IsFloating != p.floating {
			return fmt.Sprintf("pane %s: the daemon has ws%d min=%t float=%t, the clients ws%d min=%t float=%t",
				shortID(p.pty), ws.Workspace, ws.Minimized, ws.IsFloating, p.workspace, p.minimized, p.floating)
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

	// The one that is arithmetic rather than convention: a PTY has one size,
	// and it has to be the grid every client is drawing its guest in.
	for _, p := range base.panes {
		if p.minimized || p.workspace != base.workspace {
			// A pane nobody is showing is not being drawn at any size, so its
			// shell keeps whatever it last had. Only what is on screen has a
			// size to agree about.
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

// --- quiescence --------------------------------------------------------------

// settle delivers broadcasts until the fleet has converged and nothing is left
// in the queue, and fails the test if that has not happened by the deadline.
//
// The condition is the state and the empty queue together, never a fixed sleep.
// The pair matters: converged-but-still-talking is the ping-pong bug, where the
// predicate holds for an instant between two rectangles, and quiet-but-diverged
// is a5d974ed, where a dropped broadcast leaves two clients on different
// arithmetic with nothing left to say. Either one alone would pass one of the
// two.
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
		if _, queued := f.ex.take(); !queued && last == "" {
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

// fail reports a divergence the way this harness has to: the seed that
// reproduces it, the journal that reached it, what diverged, and every client's
// view so the reader can see which one is the odd one out.
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
			fmt.Fprintf(&b, "     %s ws%d shell %dx%d min=%t float=%t\n",
				shortID(ws.PTYID), ws.Workspace, dw, dh, ws.Minimized, ws.IsFloating)
		}
	}
	f.t.Fatal(b.String())
}

// bspIDs is the integer id this client has given each of its panes. The tiling
// tree is written in those ids and is adopted from the daemon wholesale, so two
// clients that numbered the same pane differently is a divergence waiting to
// happen and worth printing beside the rectangles.
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

// --- the actions -------------------------------------------------------------

// visible lists the panes a client can act on: on screen now, not minimized.
func visibleIdx(m *OS) []int {
	var out []int
	for i, w := range m.Windows {
		if w != nil && w.Workspace == m.CurrentWorkspace && !w.Minimized {
			out = append(out, i)
		}
	}
	return out
}

// step plays one action on one client and returns what it did, or "" when the
// action had nothing to act on and was skipped.
func (f *fleet) step() string {
	fc := f.cs[f.rng.IntN(len(f.cs))]
	fc.g.install()
	m := fc.m

	pick := f.rng.IntN(10)
	if pick == 4 && !convergeZoom {
		pick = 3
	}
	if pick == 9 && len(f.cs) < 3 {
		pick = 3
	}
	switch pick {
	case 0: // the terminal this client runs in changed size
		cols := fleetMinCols + f.rng.IntN(fleetMaxCols-fleetMinCols+1)
		rows := fleetMinRows + f.rng.IntN(fleetMaxRows-fleetMinRows+1)
		fc.cols, fc.rows = cols, rows
		m.Width, m.Height = cols, rows
		// What the tea.WindowSizeMsg case in update.go does, in its order: tell
		// the daemon the new viewport and the chrome that goes with it, then lay
		// the panes out. The two halves ride one message so the size and the
		// reserve cannot disagree for a frame. It is spelled out rather than
		// routed through AnnounceLayoutReserve because that one sends nothing
		// when the reserve has not moved, and a viewport that moved has to be
		// announced whether the rail changed with it or not.
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

	case 1: // a new pane
		if len(m.Windows) >= convergeMaxPanes {
			return ""
		}
		m.AddWindow("")
		// The push that a real keystroke makes alongside the intent is behind a
		// switch, and it is off by default because it fails on this tree. See
		// convergeStalePush.
		if convergeStalePush {
			fc.commit()
		}
		return fmt.Sprintf("client %s opened a pane", fc.name)

	case 2: // close a pane
		vis := visibleIdx(m)
		if len(m.Windows) <= 1 || len(vis) == 0 {
			return ""
		}
		i := vis[f.rng.IntN(len(vis))]
		id := shortID(m.Windows[i].PTYID)
		m.DeleteWindow(i)
		// As with opening a pane: the push a real keystroke makes alongside the
		// intent is behind convergeStalePush, because it races the daemon-side
		// close it asked for.
		if convergeStalePush {
			fc.commit()
		}
		return fmt.Sprintf("client %s closed pane %s", fc.name, id)

	case 3: // focus another pane
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

	case 4: // zoom the focused pane, or unzoom it
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

	case 5: // another tiling layout
		// The tiling modes are behind a switch because this tree does not
		// converge under them; see convergeModes. NextLayout is what the action
		// did before, and under the test's own XDG tree it does nothing at all.
		if convergeModes {
			m.ToggleLayoutMode()
		} else {
			m.NextLayout()
		}
		fc.commit()
		return fmt.Sprintf("client %s switched to the %s layout", fc.name, m.LayoutModeName())

	case 6: // another workspace
		ws := 1 + f.rng.IntN(min(3, m.NumWorkspaces))
		if ws == m.CurrentWorkspace {
			return ""
		}
		m.SwitchToWorkspace(ws)
		fc.commit()
		return fmt.Sprintf("client %s switched to workspace %d", fc.name, ws)

	case 7: // this client's user changed the pane geometry arithmetic
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

	case 9: // this client's terminal goes away and comes back
		// A client leaving is the one event nothing on the remaining clients
		// caused: the session's box grows back around them, and the layout the
		// departing client had pushed - computed for the smaller box - is the
		// last thing the daemon holds. It is also the window a5d974ed lived in:
		// a broadcast landing between the rejoining client's read loop starting
		// and its handlers existing.
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

	default: // fold or unfold this client's rail, which moves its chrome
		m.SidebarCollapsed = !m.SidebarCollapsed
		fc.commit()
		return fmt.Sprintf("client %s set its rail collapsed to %t", fc.name, m.SidebarCollapsed)
	}
}

// run plays one whole sequence, converging after every action.
func (f *fleet) run(steps int) {
	f.t.Helper()
	for i := 0; i < steps; i++ {
		what := f.step()
		if what == "" {
			continue
		}
		f.journal = append(f.journal, what)
		f.settle(what)
	}
}

// --- the tests ---------------------------------------------------------------

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
// Two switches turn on actions this tree does not converge under; both name the
// bug they reproduce above. TUIOS_CONVERGE_TRACE prints a line per delivery,
// which is how a failing seed is read.
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
