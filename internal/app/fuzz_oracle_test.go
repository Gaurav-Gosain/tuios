package app

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/charmbracelet/x/ansi"
)

// The oracle. A fuzzer is worth exactly as much as this file: without it the
// loop is a very slow way to discover that the program does not segfault.
//
// Every rule here is one the suite already pins for a particular arrangement,
// generalised to hold after any action. Where the existing test walks a fixed
// matrix, the rule below is stated without reference to the arrangement, so a
// combination nobody wrote a case for is still covered.
//
// Rules are ordered cheapest first, and Check returns at the first rule that
// breaks. That keeps the common path (nothing is wrong) off the expensive
// render, and it makes the shrinker's job well defined: one failing run names
// one rule.

// fuzzClock is a monotonic stand-in for wall time. Ticks carry a timestamp and
// several code paths compare it against a stored one, so a run driven by
// time.Now would take a different branch on a slow machine and stop
// reproducing.
var fuzzTicks atomic.Int64

func fuzzClock() time.Time {
	return time.Unix(0, 0).Add(time.Duration(fuzzTicks.Add(16)) * time.Millisecond)
}

func vio(rule, format string, args ...any) []fuzz.Violation {
	return []fuzz.Violation{{Rule: rule, Detail: fmt.Sprintf(format, args...)}}
}

// Check runs the oracle against the current model.
//
// A panic raised by a rule is a finding rather than a crash. Update recovers
// its own panics, but the render does not: View runs inside bubbletea's frame
// loop, where a panic takes the process down and the user loses every pane. So
// the oracle recovers one here and reports it, which also lets the shrinker
// minimise it instead of the run dying with the binary.
func (f *fuzzOS) Check() (found []fuzz.Violation) {
	defer func() {
		if r := recover(); r != nil {
			found = vio("render-panic", "%v (after %s, %d panes, %dx%d)",
				r, f.lastAction, len(f.m.Windows), f.m.Width, f.m.Height)
		}
	}()
	m := f.m
	for _, rule := range []func(*fuzzOS) []fuzz.Violation{
		checkNoRecoveredPanic,
		checkModelIndexes,
		checkPaneSizeAgreement,
		checkNoSpuriousResize,
		checkLayoutIsDisjoint,
		checkGestureNeedsAButton,
		checkScrollbarColumn,
		checkFrameFitsTheHost,
		checkRailAddressing,
		checkRailSignatureFollowsTheRail,
		checkGuestCellsAreNotPaintedOver,
	} {
		if vs := rule(f); len(vs) > 0 {
			for i := range vs {
				vs[i].Detail += fmt.Sprintf(" [after %s, %d panes, %dx%d]",
					f.lastAction, len(m.Windows), m.Width, m.Height)
			}
			return vs
		}
	}
	return nil
}

// Update recovers a panic rather than letting it out, so a panic is not a
// crash; it is a log line and a frame that did nothing. That makes it invisible
// to a fuzzer that only watches for a crash, which is why it is checked first.
func checkNoRecoveredPanic(f *fuzzOS) []fuzz.Violation {
	for _, msg := range f.m.LogMessages {
		if strings.Contains(msg.Message, "recovered panic") {
			line, _, _ := strings.Cut(msg.Message, "\n")
			return vio("panic", "%s", line)
		}
	}
	return nil
}

// The indexes the rest of the model dereferences. A focus index past the end of
// the slice is a crash the next time anything reaches for the focused pane, and
// a workspace outside the range is a lookup that silently returns nothing.
func checkModelIndexes(f *fuzzOS) []fuzz.Violation {
	m := f.m
	if m.FocusedWindow < -1 || m.FocusedWindow >= len(m.Windows) {
		return vio("focus-index", "FocusedWindow %d with %d panes", m.FocusedWindow, len(m.Windows))
	}
	if m.CurrentWorkspace < 1 || m.CurrentWorkspace > m.NumWorkspaces {
		return vio("workspace-range", "CurrentWorkspace %d outside 1..%d", m.CurrentWorkspace, m.NumWorkspaces)
	}
	for i, w := range m.Windows {
		if w == nil {
			return vio("nil-pane", "Windows[%d] is nil", i)
		}
		if w.Workspace < 1 || w.Workspace > m.NumWorkspaces {
			return vio("workspace-range", "%s sits on workspace %d, outside 1..%d", w.ID, w.Workspace, m.NumWorkspaces)
		}
	}
	return nil
}

// The three-way size agreement, generalised off the matrix in
// pane_size_announce_test.go: whatever the arrangement, a guest is told the
// size it can draw in, the emulator holds that grid, and the frame the pane
// renders fits the box the renderer clips it to.
func checkPaneSizeAgreement(f *fuzzOS) []fuzz.Violation {
	m := f.m
	if f.deferring() {
		// A live deferral means the announcement is stale on purpose: the size
		// is still moving and the expensive half is being held back. Asserting
		// agreement here would assert the opposite of the documented design.
		// The next Tick settles it, and the rule applies again from there.
		return nil
	}
	for _, w := range visibleFuzzPanes(m) {
		cw, ch := w.ContentWidth(), w.ContentHeight()
		if cw <= 0 || ch <= 0 {
			// A pane with no room to draw is a legitimate state on a viewport
			// too small to hold one, and the sizes below are meaningless there.
			continue
		}
		if ew, eh := w.Terminal.Width(), w.Terminal.Height(); ew != cw || eh != ch {
			return vio("pane-size", "%s emulator %dx%d, drawable %dx%d", w.ID, ew, eh, cw, ch)
		}
		aw, ah := w.AnnouncedSize()
		if aw != cw || ah != ch {
			return vio("pane-size", "%s announced %dx%d, drawable %dx%d", w.ID, aw, ah, cw, ch)
		}
		if rec := f.told[w.ID]; rec != nil && rec.calls > 0 && (rec.w != aw || rec.h != ah) {
			return vio("pane-size", "%s PTY told %dx%d, announced %dx%d", w.ID, rec.w, rec.h, aw, ah)
		}
		gw, gh := lipgloss.Size(m.renderTerminal(w, false, false))
		if gw > cw || gh > ch {
			return vio("pane-overflow", "%s draws %dx%d into a %dx%d box", w.ID, gw, gh, cw, ch)
		}
	}
	return nil
}

// Generalised from TestWorkspaceSwitchSendsNoSpuriousWinch: not just a
// workspace switch, but any action at all. A pane whose drawable size did not
// move must not have been told anything, because every announcement is a
// SIGWINCH and a full-screen program redraws on each one.
// The baseline is the last settled state rather than the previous action,
// because a resize defers its PTY half and drains it on a later action. Charging
// that drain to whichever action happened to trigger it would report the drain
// as spurious every time, which is the opposite of what the rule is for.
func checkNoSpuriousResize(f *fuzzOS) []fuzz.Violation {
	if f.deferring() {
		return nil
	}
	now := drawableSizes(f.m)
	var bad []fuzz.Violation
	for id, before := range f.settledDrawable {
		after, still := now[id]
		if !still || after != before {
			continue
		}
		if got, was := f.told[id], f.settledCalls[id]; got != nil && got.calls > was {
			bad = vio("spurious-winch", "%s drawable stayed %dx%d across %s and still took %d PTY resizes",
				id, before[0], before[1], f.lastAction, got.calls-was)
			break
		}
	}
	f.settledDrawable, f.settledCalls = now, callCounts(f.told)
	return bad
}

// Generalised from assertNoOverlap and TestApplyLayoutNeverOverlaps: tiled
// panes partition their region, so no two of them may claim a cell. Two panes
// over one cell means whichever draws second wins and the other's guest is
// invisible.
func checkLayoutIsDisjoint(f *fuzzOS) []fuzz.Violation {
	// Only the modes that claim to partition. The scrolling layout is a strip
	// wider than the viewport that the user scrolls along, so its columns are
	// not a partition of the region and overlap there means something else.
	if f.m.LayoutModeName() == LayoutModeScrolling || !f.hasRoomToDraw() {
		return nil
	}
	wins := tiledRects(f.m)
	for _, w := range wins {
		// A zoomed pane covers the others by design and they are not drawn, so
		// there is no rectangle to partition while one is up.
		if w.Zoomed {
			return nil
		}
		// A pane clamped to the floor is the layout saying the region cannot
		// hold this many panes. Overlap is the documented consequence of the
		// clamp, so there is no partition to assert until the region grows.
		if w.Width <= config.MinWindowWidth || w.Height <= config.MinWindowHeight {
			return nil
		}
	}
	for i := range wins {
		for j := i + 1; j < len(wins); j++ {
			a, b := wins[i], wins[j]
			ox := min(a.X+a.Width, b.X+b.Width) - max(a.X, b.X)
			oy := min(a.Y+a.Height, b.Y+b.Height) - max(a.Y, b.Y)
			if ox > 0 && oy > 0 {
				return vio("layout-overlap", "%s (%d,%d %dx%d) overlaps %s (%d,%d %dx%d) by %dx%d",
					a.ID, a.X, a.Y, a.Width, a.Height, b.ID, b.X, b.Y, b.Width, b.Height, ox, oy)
			}
		}
	}
	return nil
}

// Generalised from TestGestureCannotSurviveAFrameWithNoButtonHeld. The original
// pins the backstop in isolation; here the rule is that no sequence of any
// inputs can reach a frame where a gesture is live and no button is down.
// A stuck gesture is the pane that stops accepting input for the rest of the
// session.
func checkGestureNeedsAButton(f *fuzzOS) []fuzz.Violation {
	m := f.m
	if m.pointerDown {
		return nil
	}
	// Ending the gesture is what the frame does, so the rule is checked where
	// the frame checks it: after the backstop has had its chance to run.
	m.endGestureWithoutButton()
	switch {
	case m.Resizing:
		return vio("stuck-gesture", "Resizing with no button held")
	case m.BorderResizing:
		return vio("stuck-gesture", "BorderResizing with no button held")
	case m.BorderResizeEdge != BorderEdgeNone:
		return vio("stuck-gesture", "BorderResizeEdge %v with no button held", m.BorderResizeEdge)
	case m.InteractionMode:
		return vio("stuck-gesture", "InteractionMode with no button held")
	}
	for _, w := range m.Windows {
		if w.IsBeingManipulated {
			return vio("stuck-gesture", "%s still manipulated with no button held", w.ID)
		}
	}
	return nil
}

// The scrollbar is an overlay in the pane's own last content column, so a
// column outside that range is a thumb painted over the neighbour or over the
// divider.
func checkScrollbarColumn(f *fuzzOS) []fuzz.Violation {
	for _, w := range visibleFuzzPanes(f.m) {
		if !windowNeedsScrollbar(w) {
			continue
		}
		col := scrollbarColumn(w)
		lo := w.X + w.BorderOffset()
		if hi := lo + w.ContentWidth(); col < lo || col >= hi {
			return vio("scrollbar-column", "%s thumb at column %d, content is [%d,%d)", w.ID, col, lo, hi)
		}
	}
	return nil
}

// The frame is what the host terminal is handed. One row too many scrolls the
// screen and pushes the top line into the host's scrollback; one column too
// many wraps and corrupts every row below. Neither is recoverable without a
// full redraw, and both have shipped.
func checkFrameFitsTheHost(f *fuzzOS) []fuzz.Violation {
	m := f.m
	rows := f.frameRows()
	if m.GetRenderHeight() <= 0 || m.GetRenderWidth() <= 0 {
		return nil
	}
	if len(rows) > m.GetRenderHeight() {
		return vio("frame-size", "frame is %d rows, host is %d", len(rows), m.GetRenderHeight())
	}
	for y, row := range rows {
		if w := ansi.StringWidth(row); w > m.GetRenderWidth() {
			return vio("frame-size", "frame row %d is %d cells wide, host is %d", y, w, m.GetRenderWidth())
		}
	}
	return nil
}

// The rail's addressing rules, from sidebar_invariants_test.go, plus the
// stronger one from sidebar_strip_hits_test.go: a recorded rectangle covers
// exactly the cells the renderer painted for that target, both edge columns
// included. Several real bugs were a target one column bigger or smaller than
// it looked.
func checkRailAddressing(f *fuzzOS) []fuzz.Violation {
	m := f.m
	if !config.SidebarEnabled || m.GetSidebarWidth() <= 0 {
		return nil
	}
	f.frameRows() // the hits and nav lists are published by the render

	// Index for index: every hit names a nav row, in order. nav also carries
	// the rows scrolled out of sight, so the relation is a subsequence.
	j := 0
	for i, hit := range m.SidebarHits {
		want := navRowOf(hit)
		for j < len(m.SidebarNav) && !sidebarNavRowsEqual(m.SidebarNav[j], want) {
			j++
		}
		if j >= len(m.SidebarNav) {
			return vio("rail-hits-nav", "hit %d %+v has no nav row after the ones already matched", i, want)
		}
		j++
	}

	w := m.GetSidebarWidth()
	x0 := 0
	if config.SidebarPosition == "right" {
		x0 = m.GetRenderWidth() - w
	}
	top, bottom := m.GetTopMargin(), m.GetTopMargin()+m.GetUsableHeight()
	for i, h := range m.SidebarHits {
		if h.X0 < x0 || h.X1 > x0+w || h.X0 >= h.X1 {
			return vio("rail-hit-band", "hit %d spans columns [%d,%d), the band is [%d,%d)", i, h.X0, h.X1, x0, x0+w)
		}
		if h.Y0 < top || h.Y1 > bottom {
			return vio("rail-hit-band", "hit %d spans rows [%d,%d), the band is [%d,%d)", i, h.Y0, h.Y1, top, bottom)
		}
		if i > 0 {
			prev := m.SidebarHits[i-1]
			if !(h.Y0 > prev.Y0 || (h.Y0 == prev.Y0 && h.X0 >= prev.X1)) {
				return vio("rail-hit-band", "hit %d at (%d,%d) overlaps or precedes hit %d at (%d,%d)",
					i, h.X0, h.Y0, i-1, prev.X0, prev.Y0)
			}
		}
		// Both edge columns, and both edge rows, belong to the target that
		// recorded them. A rect wider than the paint routes a click on the
		// neighbour here; narrower, and the cell that looks clickable is not.
		for _, c := range [][2]int{{h.X0, h.Y0}, {h.X1 - 1, h.Y0}, {h.X0, h.Y1 - 1}, {h.X1 - 1, h.Y1 - 1}} {
			got, ok := m.sidebarRowAt(c[0], c[1])
			if !ok || got != h {
				return vio("rail-hit-cells", "hit %d spans [%d,%d)x[%d,%d) but cell (%d,%d) resolves elsewhere",
					i, h.X0, h.X1, h.Y0, h.Y1, c[0], c[1])
			}
		}
	}

	if n := len(m.SidebarNav); n == 0 {
		if m.SidebarCursor != 0 {
			return vio("rail-cursor", "cursor %d on a rail with no navigable rows", m.SidebarCursor)
		}
	} else if m.SidebarCursor < 0 || m.SidebarCursor >= n {
		return vio("rail-cursor", "cursor %d outside the %d rows the frame published", m.SidebarCursor, n)
	}
	return nil
}

// The cache contract, in the direction that is a correctness bug: the rail's
// drawn output moved and the signature did not, so the next frame serves the
// stale rail. The opposite direction (a signature that moves for undrawn state)
// costs a rebuild rather than a wrong pixel and has its own table test.
func checkRailSignatureFollowsTheRail(f *fuzzOS) []fuzz.Violation {
	m := f.m
	if !config.SidebarEnabled || m.GetSidebarWidth() <= 0 {
		f.prevSignature, f.prevRail = "", ""
		return nil
	}
	lines, _ := m.sidebarPanelLines()
	rail := stripANSIForTrace(strings.Join(lines, "\n"))
	sig := fmt.Sprint(m.sidebarSignature())

	prevSig, prevRail := f.prevSignature, f.prevRail
	f.prevSignature, f.prevRail = sig, rail
	if prevSig == "" || prevRail == rail || sig != prevSig {
		return nil
	}
	return vio("rail-signature", "the rail redrew with the signature unmoved at %s", sig)
}

// Nothing paints into a cell a pane's guest owns. Each pane writes a marker of
// its own, and the marker has to survive the composition: a divider that ran
// one cell too far, a toast that reached out of the dock, or a tooltip that
// clamped into the pane area all show up here as a missing marker.
func checkGuestCellsAreNotPaintedOver(f *fuzzOS) []fuzz.Violation {
	m := f.m
	panes := visibleFuzzPanes(m)
	// An overlay is drawn over the panes by design, so while one is up the
	// cells belong to it. The rule is about the chrome that is never supposed
	// to reach into a pane: dividers, toasts, tooltips, the scrollbar.
	if len(panes) == 0 || !f.hasRoomToDraw() || m.AnyOverlayOpen() || m.ShowHelp || m.ShowLogs {
		return nil
	}
	// The scrolling layout deliberately places panes past the edge of the
	// viewport, so a pane being absent from the frame is the layout working.
	if m.LayoutModeName() == LayoutModeScrolling {
		return nil
	}
	// A zoom draws one pane and hides the rest, so only the zoomed one owns any
	// cells; the others are legitimately absent from the frame.
	for _, w := range panes {
		if w.Zoomed {
			panes = []*terminal.Window{w}
			break
		}
	}
	marks := make([]string, len(panes))
	for i, w := range panes {
		if w.ContentWidth() < len(paneMarker(i)) || w.ContentHeight() < 1 {
			return nil // too small to hold a marker, so the check says nothing
		}
		marks[i] = paneMarker(i)
		w.LockIO()
		_, _ = w.Terminal.Write([]byte("\x1b[H\x1b[2J" + marks[i]))
		w.UnlockIO()
		w.MarkContentDirty()
	}
	g := f.renderGrid()
	for i, w := range panes {
		x, y := w.X+w.BorderOffset(), w.Y+w.BorderOffset()
		got := runesAt(g, x, y, len([]rune(marks[i])))
		if got != marks[i] {
			return vio("guest-cells", "%s owns (%d,%d) and wrote %q, the frame shows %q",
				w.ID, x, y, marks[i], got)
		}
	}
	return nil
}

// deferring reports whether a resize is still in flight, which is the one state
// where a stale announcement is correct. It reads the flags rather than calling
// resizeDeferralActive, because that call ends a stale deferral as a side
// effect and an oracle must not move the state it is judging.
func (f *fuzzOS) deferring() bool {
	return f.m.viewportResizing || f.m.Resizing || len(f.m.PendingResizes) > 0
}

// hasRoomToDraw reports whether there is a content region at all. On a viewport
// too small to hold one, panes clamp to a one-cell minimum and stack on top of
// each other; nothing is drawn, so the geometry rules have nothing to say.
func (f *fuzzOS) hasRoomToDraw() bool {
	b := f.m.GetBSPBounds()
	return b.W > 0 && b.H > 0 && f.m.GetRenderWidth() > 0 && f.m.GetRenderHeight() > 0
}

// visibleFuzzPanes is the set every rule above is stated over: the panes on the
// current workspace that the frame actually draws.
func visibleFuzzPanes(m *OS) []*terminal.Window {
	var out []*terminal.Window
	for _, w := range m.Windows {
		if w != nil && w.Workspace == m.CurrentWorkspace && !w.Minimized && w.Terminal != nil {
			out = append(out, w)
		}
	}
	return out
}

// frameRows composes the frame the host would receive, stripped of styling.
func (f *fuzzOS) frameRows() []string {
	if f.m.GetRenderWidth() <= 0 || f.m.GetRenderHeight() <= 0 {
		return nil
	}
	return strings.Split(stripANSIForTrace(lipgloss.Sprint(f.m.GetCanvas(true).Render())), "\n")
}

func (f *fuzzOS) renderGrid() [][]rune {
	rows := f.frameRows()
	g := make([][]rune, len(rows))
	for i, r := range rows {
		g[i] = []rune(r)
	}
	return g
}

func runesAt(g [][]rune, x, y, n int) string {
	if y < 0 || y >= len(g) || x < 0 {
		return ""
	}
	row := g[y]
	if x >= len(row) {
		return ""
	}
	return string(row[x:min(x+n, len(row))])
}
