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
	"github.com/Gaurav-Gosain/tuios/internal/ui"
	"github.com/charmbracelet/x/ansi"
	"tuios.dev/vt"
)

var fuzzTicks atomic.Int64

func fuzzClock() time.Time {
	return time.Unix(0, 0).Add(time.Duration(fuzzTicks.Add(16)) * time.Millisecond)
}

func vio(rule, format string, args ...any) []fuzz.Violation {
	return []fuzz.Violation{{Rule: rule, Detail: fmt.Sprintf(format, args...)}}
}

var fuzzRules = []struct {
	rules []fuzz.RuleInfo
	check func(*fuzzOS) []fuzz.Violation
}{
	{[]fuzz.RuleInfo{
		{Name: "panic", Family: "process", Doc: "Update swallowed a panic, so a frame did nothing and said nothing"},
	}, checkNoRecoveredPanic},

	{[]fuzz.RuleInfo{
		{Name: "focus-index", Family: "model", Doc: "the focused pane index is outside the pane slice"},
		{Name: "workspace-range", Family: "model", Doc: "a workspace number is outside the configured range"},
		{Name: "nil-pane", Family: "model", Doc: "the pane slice holds a nil"},
	}, checkModelIndexes},

	{[]fuzz.RuleInfo{
		{Name: "pane-size", Family: "geometry", Doc: "a guest was told a size its emulator does not hold"},
		{Name: "pane-overflow", Family: "geometry", Doc: "a pane rendered a frame larger than the box it is clipped to"},
	}, checkPaneSizeAgreement},

	{[]fuzz.RuleInfo{
		{Name: "spurious-winch", Family: "geometry", Doc: "a pane whose drawable size did not move was resized anyway"},
	}, checkNoSpuriousResize},

	{[]fuzz.RuleInfo{
		{Name: "layout-overlap", Family: "layout", Doc: "two tiled panes claim the same cell"},
	}, checkLayoutIsDisjoint},

	{[]fuzz.RuleInfo{
		{Name: "stuck-gesture", Family: "input", Doc: "a drag is live with no button held, so the pane stops taking input"},
	}, checkGestureNeedsAButton},

	{[]fuzz.RuleInfo{
		{Name: "scrollbar-column", Family: "render", Doc: "a scrollbar sits outside its own pane's last column"},
	}, checkScrollbarColumn},

	{[]fuzz.RuleInfo{
		{Name: "frame-size", Family: "layout", Doc: "the frame handed to the host is wider or taller than the host"},
	}, checkFrameFitsTheHost},

	{[]fuzz.RuleInfo{
		{Name: "rail-hits-nav", Family: "rail", Doc: "a rail hit target lands on a row the rail does not own"},
		{Name: "rail-hit-band", Family: "rail", Doc: "a rail hit target leaves the rail's own column band"},
		{Name: "rail-hit-cells", Family: "rail", Doc: "a rail hit rectangle does not cover the cells it was painted on"},
		{Name: "rail-cursor", Family: "rail", Doc: "the rail's cursor points at a row that is not selectable"},
	}, checkRailAddressing},

	{[]fuzz.RuleInfo{
		{Name: "rail-signature", Family: "rail", Doc: "the rail's drawn output moved and its cache signature did not"},
	}, checkRailSignatureFollowsTheRail},

	{[]fuzz.RuleInfo{
		{Name: "guest-cells", Family: "render", Doc: "something painted into a cell a pane's guest owns"},
	}, checkGuestCellsAreNotPaintedOver},

	{[]fuzz.RuleInfo{
		{Name: "divider-glyph", Family: "render", Doc: "a divider cell holds a glyph from outside the active border style"},
	}, checkDividersUseTheirOwnGlyphs},
}

var fuzzCheckRules = []fuzz.RuleInfo{
	{Name: "render-panic", Family: "process", Doc: "a rule panicked composing a frame, which in the real render loop kills the process"},
}

func (f *fuzzOS) Rules() []fuzz.RuleInfo {
	out := make([]fuzz.RuleInfo, 0, len(fuzzRules)+len(fuzzCheckRules))
	for _, r := range fuzzRules {
		out = append(out, r.rules...)
	}
	return append(out, fuzzCheckRules...)
}

func (f *fuzzOS) Check() (found []fuzz.Violation) {
	if f.m.QuitRequested {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			found = vio("render-panic", "%v (after %s, %d panes, %dx%d)",
				r, f.lastAction, len(f.m.Windows), f.m.Width, f.m.Height)
		}
	}()
	m := f.m
	for _, entry := range fuzzRules {
		if vs := entry.check(f); len(vs) > 0 {
			for i := range vs {
				vs[i].Detail += fmt.Sprintf(" [after %s, %d panes, %dx%d]",
					f.lastAction, len(m.Windows), m.Width, m.Height)
			}
			return vs
		}
	}
	return nil
}

func checkNoRecoveredPanic(f *fuzzOS) []fuzz.Violation {
	for _, msg := range f.m.LogMessages {
		if strings.Contains(msg.Message, "recovered panic") {
			line, _, _ := strings.Cut(msg.Message, "\n")
			return vio("panic", "%s", line)
		}
	}
	return nil
}

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

func slidingPane(m *OS, w *terminal.Window) bool {
	for _, anim := range m.Animations {
		if anim.Type == ui.AnimationSnap && anim.Window == w {
			return true
		}
	}
	return false
}

func checkPaneSizeAgreement(f *fuzzOS) []fuzz.Violation {
	m := f.m
	if f.deferring() {
		return nil
	}
	for _, w := range visibleFuzzPanes(m) {
		if slidingPane(m, w) {
			continue
		}
		cw, ch := w.ContentWidth(), w.ContentHeight()
		if cw <= 0 || ch <= 0 {
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

func (f *fuzzOS) noteMoves() {
	for id, size := range drawableSizes(f.m) {
		if before, ok := f.settledDrawable[id]; ok && size != before {
			f.movedSinceSettled[id] = true
		}
	}
}

func checkNoSpuriousResize(f *fuzzOS) []fuzz.Violation {
	now := drawableSizes(f.m)
	f.noteMoves()
	if f.deferring() {
		return nil
	}
	var bad []fuzz.Violation
	for id, before := range f.settledDrawable {
		after, still := now[id]
		if !still || after != before || f.movedSinceSettled[id] {
			continue
		}
		if got, was := f.told[id], f.settledCalls[id]; got != nil && got.calls > was {
			bad = vio("spurious-winch", "%s drawable stayed %dx%d across %s and still took %d PTY resizes",
				id, before[0], before[1], f.lastAction, got.calls-was)
			break
		}
	}
	f.settledDrawable, f.settledCalls = now, callCounts(f.told)
	f.movedSinceSettled = map[string]bool{}
	return bad
}

func checkLayoutIsDisjoint(f *fuzzOS) []fuzz.Violation {
	if !f.m.AutoTiling || f.m.LayoutModeName() == LayoutModeScrolling || !f.hasRoomToDraw() {
		return nil
	}
	if f.m.Dragging || f.m.Resizing {
		return nil
	}
	for _, anim := range f.m.Animations {
		if anim.Type == ui.AnimationSnap {
			return nil
		}
	}
	wins := tiledRects(f.m)
	for _, w := range wins {
		if w.Zoomed {
			return nil
		}
		if w.Width <= config.DefaultWindowWidth || w.Height <= config.DefaultWindowHeight {
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

func checkGestureNeedsAButton(f *fuzzOS) []fuzz.Violation {
	m := f.m
	if m.pointerDown {
		return nil
	}
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

func checkScrollbarColumn(f *fuzzOS) []fuzz.Violation {
	for _, w := range visibleFuzzPanes(f.m) {
		if !windowNeedsScrollbar(w, &f.m.Settings) {
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

func checkRailAddressing(f *fuzzOS) []fuzz.Violation {
	m := f.m
	if !f.m.Settings.SidebarEnabled || m.GetSidebarWidth() <= 0 {
		return nil
	}
	if f.frameRows() == nil {
		return nil
	}

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
	if f.m.Settings.SidebarPosition == "right" {
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

func checkRailSignatureFollowsTheRail(f *fuzzOS) []fuzz.Violation {
	m := f.m
	if !f.m.Settings.SidebarEnabled || m.GetSidebarWidth() <= 0 {
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

func checkGuestCellsAreNotPaintedOver(f *fuzzOS) []fuzz.Violation {
	m := f.m
	panes := visibleFuzzPanes(m)
	if len(panes) == 0 || !f.framePaintsPanes() {
		return nil
	}
	marks := make([]string, len(panes))
	for i, w := range panes {
		if w.ContentWidth() < len(paneMarker(i)) || w.ContentHeight() < 1 {
			return nil
		}
		marks[i] = paneMarker(i)
		w.LockIO()
		writeMarkerAtOrigin(w.Terminal, marks[i])
		w.UnlockIO()
		w.MarkContentDirty()
	}
	g := f.renderGrid()
	for i, w := range panes {
		x, y := w.X+w.BorderOffset(), w.Y+w.BorderOffset()
		n := len([]rune(marks[i]))
		if m.InDockBand(y) || bandCovers(m, x, y, n) {
			continue
		}
		if !paneCellsAreOnScreen(m, x, y, n) {
			continue
		}
		if w.InCopyMode() {
			continue
		}
		if !w.Tiled && (w.Width < 3 || w.Height < 3) {
			continue
		}
		if coveredByAPaneAbove(m, panes, i, x, y, n) {
			continue
		}
		if leaderBadgeCovers(m, x, y, n) {
			continue
		}
		got := runesAt(g, x, y, n)
		if got != marks[i] {
			return vio("guest-cells", "%s owns (%d,%d) and wrote %q, the frame shows %q",
				w.ID, x, y, marks[i], got)
		}
	}
	return nil
}

func writeMarkerAtOrigin(t vt.Terminal, mark string) {
	const decom, declrmm = 6, 69
	modes := t.GetModes()
	region := t.ScrollRegion()

	var seq strings.Builder
	if modes[decom] {
		seq.WriteString("\x1b[?6l")
	}
	if modes[declrmm] {
		seq.WriteString("\x1b[s")
	}
	seq.WriteString("\x1b[H\x1b[2J")
	seq.WriteString(mark)
	if modes[declrmm] && !region.Empty() {
		fmt.Fprintf(&seq, "\x1b[%d;%ds", region.Min.X+1, region.Max.X)
	}
	if modes[decom] {
		seq.WriteString("\x1b[?6h")
	}
	_, _ = t.Write([]byte(seq.String()))
}

func paneCellsAreOnScreen(m *OS, x, y, n int) bool {
	return x >= 0 && y >= 0 && x+n <= m.GetRenderWidth() && y < m.GetRenderHeight()
}

func bandCovers(m *OS, x, y, n int) bool {
	for c := x; c < x+n; c++ {
		if m.SidebarBandContains(c, y) {
			return true
		}
	}
	return false
}

func leaderBadgeCovers(m *OS, x, y, n int) bool {
	text, kind := m.statusBadge()
	if kind != badgeLeader || y != m.badgeY(kind) {
		return false
	}
	end := badgeX + ansi.StringWidth(text) + 2
	return x < end && x+n > badgeX
}

func coveredByAPaneAbove(m *OS, panes []*terminal.Window, i, x, y, n int) bool {
	w := panes[i]
	for j, v := range panes {
		if j == i || !fuzzPaneDrawsAbove(v, j, w, i) {
			continue
		}
		if y < v.Y || y >= v.Y+v.Height {
			continue
		}
		if x < v.X+v.Width && x+n > v.X {
			return true
		}
	}
	return false
}

func fuzzPaneDrawsAbove(a *terminal.Window, ai int, b *terminal.Window, bi int) bool {
	az, bz := fuzzPaneZ(a), fuzzPaneZ(b)
	if az != bz {
		return az > bz
	}
	return ai > bi
}

func fuzzPaneZ(w *terminal.Window) int {
	return windowLayerZ(w, false)
}

func (f *fuzzOS) deferring() bool {
	return f.m.viewportResizing || f.m.Resizing || len(f.m.PendingResizes) > 0 ||
		f.m.announceGestureHeld
}

func checkDividersUseTheirOwnGlyphs(f *fuzzOS) []fuzz.Violation {
	m := f.m
	if !f.m.Settings.SharedBorders || !f.framePaintsPanes() {
		return nil
	}
	own := styleGlyphs(f.m.Settings.GetBorderForStyle())
	if own == "" {
		return nil
	}
	g := f.renderGrid()
	for _, c := range dividerCells(m) {
		if got := cellAt(g, c.X, c.Y); !strings.ContainsRune(own, got) {
			return vio("divider-glyph", "divider cell (%d,%d) is %q, outside style %q's own glyphs %q",
				c.X, c.Y, string(got), f.m.Settings.BorderStyle, own)
		}
	}
	return nil
}

func (f *fuzzOS) framePaintsPanes() bool {
	m := f.m
	if !f.hasRoomToDraw() || m.AnyOverlayOpen() || m.ContextMenuActive() || m.ShowHelp || m.ShowLogs ||
		m.Dragging || m.Resizing ||
		m.Renaming() || m.ScreensaverActive() || m.pipWanted() {
		return false
	}
	if m.SidebarRevealedForFocus {
		return false
	}
	if m.LayoutModeName() == LayoutModeScrolling {
		return false
	}
	for _, w := range visibleFuzzPanes(m) {
		if w.Zoomed {
			return false
		}
	}
	return true
}

func (f *fuzzOS) hasRoomToDraw() bool {
	b := f.m.GetBSPBounds()
	return b.W > 0 && b.H > 0 && f.m.GetRenderWidth() > 0 && f.m.GetRenderHeight() > 0
}

func visibleFuzzPanes(m *OS) []*terminal.Window {
	var out []*terminal.Window
	for _, w := range m.Windows {
		if w != nil && w.Workspace == m.CurrentWorkspace && !w.Minimized && w.Terminal != nil {
			out = append(out, w)
		}
	}
	return out
}

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
		var cells []rune
		for s := r; s != ""; {
			cluster, w := ansi.FirstGraphemeCluster(s, ansi.GraphemeWidth)
			if cluster == "" {
				break
			}
			s = s[len(cluster):]
			if w == 0 {
				continue
			}
			cells = append(cells, []rune(cluster)[0])
			for range w - 1 {
				cells = append(cells, 0)
			}
		}
		g[i] = cells
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
