package app

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// sidebarRenderCache holds a fully styled rail so a frame composed for an
// unrelated reason can reuse it. It is keyed by sidebarSignature, a cheap fold
// of every input the rows depend on; when the signature is unchanged the rows
// cannot have changed, so the lipgloss styling and the BuildSessionTree walk
// (which locks the daemon client) are both skipped. Theme and config changes
// go through MarkAllDirty, which drops the cache outright.
type sidebarRenderCache struct {
	valid      bool
	sig        uint64
	lines      []string
	panel      string
	w          int
	hits       []sidebarRowHit
	sessionIDs []string
	nav        []sidebarNavRow
	sections   [sidebarSectionCount][2]int
	stripRows  []sidebarStripRow
}

// invalidate drops the cached rail, forcing the next frame to rebuild. Called
// from MarkAllDirty so a theme swap, config reload, or full repaint restyles.
func (c *sidebarRenderCache) invalidate() { c.valid = false }

// sidebarPanel is the rail as the one string the layer takes. The join is
// cached alongside the rows because renderSidebar runs on every composed frame,
// and joining the rows there rebuilt the whole rail on frames the cache had just
// declared unchanged, which is the one thing a render cache exists to stop.
//
// An animating rail is not cached (see sidebarPanelLines), so it joins every
// frame, which is correct: its rows are different every frame.
func (m *OS) sidebarPanel() (string, int) {
	lines, w := m.sidebarPanelLines()
	if lines == nil {
		return "", w
	}
	if m.sidebarCache.valid && m.sidebarCache.panel != "" {
		return m.sidebarCache.panel, w
	}
	panel := strings.Join(lines, "\n")
	if m.sidebarCache.valid {
		m.sidebarCache.panel = panel
	}
	return panel, w
}

// sidebarPanelLines builds the sidebar's rows, reusing the cached rail when
// nothing that affects it has changed since the last frame. A scrolling marquee
// or an in-progress drag animates every frame, so neither is ever served cached.
func (m *OS) sidebarPanelLines() ([]string, int) {
	animating := m.SidebarMarqueeKey != "" || m.SidebarDrag.Dragging
	sig := m.sidebarSignature()
	if !animating && m.sidebarCache.valid && m.sidebarCache.sig == sig {
		// Restore the per-frame side effects the mouse handlers read; the model
		// truncates and refills these buffers on a real rebuild, so hand back copies.
		m.SidebarHits = append(m.SidebarHits[:0], m.sidebarCache.hits...)
		m.SidebarSessionIDs = append(m.SidebarSessionIDs[:0], m.sidebarCache.sessionIDs...)
		m.SidebarNav = append(m.SidebarNav[:0], m.sidebarCache.nav...)
		m.sidebarSectionY = m.sidebarCache.sections
		m.sidebarStripRows = append(m.sidebarStripRows[:0], m.sidebarCache.stripRows...)
		return m.sidebarCache.lines, m.sidebarCache.w
	}

	lines, w := m.sidebarPanelLinesForTree(m.BuildSessionTree())

	m.sidebarCache = sidebarRenderCache{
		valid:      !animating,
		sig:        sig,
		lines:      lines,
		w:          w,
		hits:       append([]sidebarRowHit(nil), m.SidebarHits...),
		sessionIDs: append([]string(nil), m.SidebarSessionIDs...),
		nav:        append([]sidebarNavRow(nil), m.SidebarNav...),
		sections:   m.sidebarSectionY,
		stripRows:  append([]sidebarStripRow(nil), m.sidebarStripRows...),
	}
	return lines, w
}

// sidebarSignature folds every input the rendered rows depend on into one
// value, allocation-free (an inlined FNV-1a). Geometry and view state come from
// the model; the live windows contribute id, title, and agent state in order;
// foreign-session data is summarised by the client's cache generation so the
// daemon mutex is not taken per frame. A changed signature forces a rebuild; an
// unchanged one guarantees identical rows.
func (m *OS) sidebarSignature() uint64 {
	const prime = 1099511628211
	h := uint64(1469598103934665603)
	mixU := func(v uint64) {
		for range 8 {
			h ^= v & 0xff
			h *= prime
			v >>= 8
		}
	}
	mixI := func(v int) { mixU(uint64(v)) }
	mixB := func(b bool) {
		if b {
			mixU(1)
		} else {
			mixU(2)
		}
	}
	mixS := func(s string) {
		mixU(uint64(len(s)))
		for i := range len(s) {
			h ^= uint64(s[i])
			h *= prime
		}
	}

	// Geometry and layout knobs.
	mixI(m.GetSidebarWidth())
	mixI(m.GetUsableHeight())
	mixI(m.GetTopMargin())
	mixI(m.GetRenderWidth())
	mixS(config.SidebarPosition)
	mixB(config.SidebarShowWindows)
	mixB(config.SidebarShowGlyphs)
	mixB(config.SidebarShowCounts)

	// The glyph set the rows are drawn from. ASCII mode swaps the collapse
	// chevrons and the agent-state indicators for their fallbacks, and both it
	// and the border style pick the character of the edge rule facing the panes,
	// so a rail drawn before either moved is not the rail this frame draws.
	mixB(config.UseASCIIOnly)
	mixS(config.BorderStyle)

	// View state: scroll, focus, and hover all restyle rows. Each section holds
	// its own offset, so all three are folded.
	mixI(m.SidebarScrollS)
	mixI(m.SidebarScrollT)
	mixI(m.SidebarScrollA)
	// The agents offset is derived from the anchor on any frame whose sort has
	// moved, so the anchor picks the rows that are drawn just as directly as the
	// offset does, and a frame drawn under one anchor cannot be served from an
	// entry keyed on another.
	mixB(m.sidebarAgentAnchor.Valid)
	mixI(m.sidebarAgentAnchor.Offset)
	mixS(m.sidebarAgentAnchor.SessionID)
	mixS(m.sidebarAgentAnchor.WindowID)
	mixI(m.FocusedWindow)
	mixB(m.SidebarHoverActive)
	mixI(m.SidebarHoverX)
	mixI(m.SidebarHoverY)

	// The peek swaps the whole terminals section and re-marks its header, so a
	// peeked frame and a resting one can never share a cache entry.
	mixS(m.SidebarPeek)

	// The files section: whether it is on, what it is showing, and how far down
	// it is scrolled are all drawn state, and the layout string decides which
	// sections are stacked in what order, which is every line of the rail.
	//
	// Gen is what makes a refresh visible. The path can be the same directory
	// before and after a reload, and a listing that changed underneath it would
	// otherwise be served from the entry keyed on the old one. Loading is folded
	// separately because it is a row the section draws and it flips without the
	// path or the generation moving.
	mixS(config.SidebarSections)
	mixB(config.SidebarFileIcons)
	mixI(int(m.filesView.Show))
	mixS(m.filesView.Dir)
	mixS(m.filesView.Origin)
	mixS(m.filesView.Err)
	mixB(m.filesView.Loading)
	mixI(m.SidebarScrollF)
	mixU(m.filesView.Gen)

	// The agents section's two controls decide which rows it holds and in what
	// order, so both are drawn state and both are folded. The tokens themselves
	// change ink with them, which is the other half of what the frame shows.
	mixS(m.sidebarAgentsFilter())
	mixS(m.sidebarAgentsSort())

	// Rail keyboard focus: the accent edge and the cursor-row highlight both
	// depend on it, so a focus change or a cursor move must rebuild.
	mixB(m.SidebarFocused)
	mixI(m.SidebarCursor)

	// Which terminal rows carry a workspace tag turns on which workspace is
	// current; the per-window workspaces themselves are folded in below. The
	// names print in the tag, so renaming a workspace has to restyle the rows on
	// it. Order-independent, so map iteration order does not matter.
	mixI(m.CurrentWorkspace)
	// The order arranges the rail's terminals section and the dock's pills both,
	// and a draft one is live for the length of a drag, so it is drawn state and
	// belongs in here.
	for _, ws := range m.WorkspaceOrder {
		mixI(ws)
	}
	mixB(m.dockWorkspaceDrag.Dragging)
	for _, ws := range m.dockWorkspaceDrag.Order {
		mixI(ws)
	}

	var wsFold uint64
	for ws, name := range m.WorkspaceNames {
		e := uint64(1469598103934665603) ^ uint64(ws)
		e *= prime
		for i := range len(name) {
			e ^= uint64(name[i])
			e *= prime
		}
		wsFold ^= e
	}
	mixU(wsFold)

	// Session identity and the user's drag-defined order.
	mixS(m.SessionName)
	for _, o := range m.SidebarOrder {
		mixS(o)
	}

	// Foreign-session data, folded by generation instead of by locking the client.
	if m.DaemonClient != nil {
		mixU(m.DaemonClient.CacheGen())
	}

	// A rename in flight is not folded in: the buffer lives in its own dialog
	// and the rail keeps drawing the old name, so typing no longer rebuilds the
	// whole rail once per keystroke.

	// An open accent picker previews the colour under its cursor on the row it
	// targets, so the rail rebuilds exactly on picker navigation, which is
	// already a frame, and never otherwise. No tick. Only what the preview
	// actually draws is folded, so a closed picker's leftover cursor cannot hold
	// the rail on a signature it no longer renders.
	mixB(m.ShowAccentPicker)
	if m.ShowAccentPicker {
		mixI(int(m.AccentPickerTarget))
		mixS(m.AccentPickerTargetID)
		preview, ok := m.accentPreview(m.AccentPickerTarget, m.AccentPickerTargetID)
		if !ok {
			mixI(-1)
		} else {
			mixU(preview.fold())
		}
	}

	// Session colours mark the sessions and agents sections. They are derived
	// from the session names, which are folded above and, for foreign sessions,
	// covered by the cache generation; the attached session's explicit accent is
	// the one input nothing else carries, and it is folded only while the
	// colours are actually drawn.
	mixB(config.SessionColors)
	if config.SessionColors {
		mixS(m.SessionAccent)
	}

	// Live windows in row order: id, label, agent state, harness, workspace,
	// accent.
	for _, w := range m.Windows {
		if w == nil {
			continue
		}
		mixS(w.ID)
		mixS(m.railTitleShown(w))
		mixS(w.AgentState)
		// The agents section prints which agent a row is running and the note it
		// reported, so a pane that swaps harness or says something new redraws
		// even when its state and title hold still.
		mixS(w.AgentHarness)
		mixS(w.AgentMessage)
		// The agents section prints the age of the state, so the row changes on a
		// minute boundary with no other input moving. Folding the whole timestamp
		// would rebuild the rail on every frame; the minute bucket rebuilds it at
		// most once a minute per pane, on a frame that was happening anyway.
		mixI(int(agentElapsedBucket(w.AgentStateAt)))
		mixI(w.Workspace)
		if accent, ok := m.WindowAccent(w.ID); ok {
			mixU(accent.fold())
		} else {
			mixI(-1)
		}
	}

	// Unread bits, order-independent and over every window rather than only the
	// live ones: a foreign session's done pane is ranked and coloured by this
	// too, and the daemon's cache generation cannot see a purely local look.
	var seenFold uint64
	for id, seen := range m.SidebarAgentSeen {
		if !seen {
			continue
		}
		e := uint64(1469598103934665603)
		for i := range len(id) {
			e ^= uint64(id[i])
			e *= prime
		}
		seenFold ^= e
	}
	mixU(seenFold)

	return h
}
