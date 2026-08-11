package app

import "github.com/Gaurav-Gosain/tuios/internal/config"

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
	w          int
	hits       []sidebarRowHit
	sessionIDs []string
}

// invalidate drops the cached rail, forcing the next frame to rebuild. Called
// from MarkAllDirty so a theme swap, config reload, or full repaint restyles.
func (c *sidebarRenderCache) invalidate() { c.valid = false }

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

	// View state: scroll, focus, and hover all restyle rows.
	mixI(m.SidebarScroll)
	mixI(m.FocusedWindow)
	mixB(m.SidebarHoverActive)
	mixI(m.SidebarHoverX)
	mixI(m.SidebarHoverY)

	// Session identity and the user's drag-defined order.
	mixS(m.SessionName)
	for _, o := range m.SidebarOrder {
		mixS(o)
	}

	// Expand state, order-independent so map iteration order does not matter.
	var collapsedFold uint64
	for id, collapsed := range m.SidebarCollapsed {
		e := uint64(1469598103934665603)
		for i := range len(id) {
			e ^= uint64(id[i])
			e *= prime
		}
		if collapsed {
			e ^= 0x9e3779b97f4a7c15
		}
		collapsedFold ^= e
	}
	mixU(collapsedFold)

	// Foreign-session data, folded by generation instead of by locking the client.
	if m.DaemonClient != nil {
		mixU(m.DaemonClient.CacheGen())
	}

	// Live windows in row order: id, label, agent state.
	for _, w := range m.Windows {
		if w == nil {
			continue
		}
		mixS(w.ID)
		mixS(m.railTitleShown(w))
		mixS(w.AgentState)
	}

	return h
}
