package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Snap snaps the window at index i to the specified position.
func (m *OS) Snap(i int, quarter SnapQuarter) *OS {
	if i < 0 || i >= len(m.Windows) {
		return m
	}

	// Create and start snap animation
	anim := m.CreateSnapAnimation(i, quarter)
	if anim != nil {
		m.Animations = append(m.Animations, anim)
	} else {
		// No animation needed (already at target), but still resize terminal if needed
		win := m.Windows[i]
		_, _, targetWidth, targetHeight := m.calculateSnapBounds(quarter)

		// Enforce minimum size
		targetWidth = max(targetWidth, config.DefaultWindowWidth)
		targetHeight = max(targetHeight, config.DefaultWindowHeight)

		// Make sure terminal is properly sized even if no animation
		if win.Width != targetWidth || win.Height != targetHeight {
			win.Resize(targetWidth, targetHeight)
		}
	}

	return m
}

// SnapZoneAt names the snap a pointer released at (x, y) asks for, or NoSnap.
//
// It lives beside calculateSnapBounds on purpose. The two were once computed in
// different places by different rules: the bounds partitioned the content
// region while the zones were read off the raw screen, so with a right-hand
// sidebar the right zone sat underneath the rail. A pane dragged to the visible
// right edge snapped nothing, and reaching the zone at all meant dragging into
// the sidebar. Keeping them adjacent is what stops that drifting again.
func (m *OS) SnapZoneAt(x, y int) SnapQuarter {
	const edge = 5

	topMargin := m.GetTopMargin()
	bottomEdge := topMargin + m.GetUsableHeight()
	leftEdge := m.GetLeftMargin()
	rightEdge := m.GetRenderWidth() - m.GetRightMargin()

	atLeft := x <= leftEdge+edge
	atRight := x >= rightEdge-edge
	atTop := y <= topMargin+edge
	atBottom := y >= bottomEdge-edge

	switch {
	case atTop && atLeft:
		return SnapTopLeft
	case atTop && atRight:
		return SnapTopRight
	case atBottom && atLeft:
		return SnapBottomLeft
	case atBottom && atRight:
		return SnapBottomRight
	case atTop:
		return SnapFullScreen
	case atLeft:
		return SnapLeft
	case atRight:
		return SnapRight
	}
	return NoSnap
}

func (m *OS) calculateSnapBounds(quarter SnapQuarter) (x, y, width, height int) {
	// Snapping partitions the content region, not the raw screen: a sidebar
	// reserves columns the same way the dock reserves rows, so a left-snapped
	// pane starts at the left margin and a fullscreen snap fills the content box.
	usableHeight := m.GetUsableHeight()
	left := m.GetLeftMargin()
	contentWidth := m.GetContentWidth()
	halfWidth := contentWidth / 2
	midX := left + halfWidth
	halfHeight := usableHeight / 2
	topMargin := m.GetTopMargin()

	switch quarter {
	case SnapLeft:
		return left, topMargin, halfWidth, usableHeight
	case SnapRight:
		return midX, topMargin, contentWidth - halfWidth, usableHeight
	case SnapTopLeft:
		return left, topMargin, halfWidth, halfHeight
	case SnapTopRight:
		return midX, topMargin, halfWidth, halfHeight
	case SnapBottomLeft:
		return left, halfHeight + topMargin, halfWidth, usableHeight - halfHeight
	case SnapBottomRight:
		return midX, halfHeight + topMargin, halfWidth, usableHeight - halfHeight
	case SnapFullScreen:
		return left, topMargin, contentWidth, usableHeight
	case Unsnap:
		return left + contentWidth/4, usableHeight/4 + topMargin, halfWidth, halfHeight
	default:
		return left + contentWidth/4, usableHeight/4 + topMargin, halfWidth, halfHeight
	}
}

// ScaleWindowsToTerminal proportionally scales all windows when terminal size changes.
// This is called when restoring from daemon state to ensure windows fit the new terminal size.
// oldWidth/oldHeight are the terminal dimensions when state was saved.
// newWidth/newHeight are the current terminal dimensions.
func (m *OS) ScaleWindowsToTerminal(oldWidth, oldHeight, newWidth, newHeight int) {
	if m.AutoTiling {
		return // Tiling mode handles its own layout
	}

	if oldWidth <= 0 || oldHeight <= 0 || newWidth <= 0 || newHeight <= 0 {
		return // Invalid dimensions
	}

	oldUsableHeight := oldHeight - m.GetTopMargin()
	if config.DockbarPosition != "hidden" {
		oldUsableHeight -= 1
	}

	newUsableHeight := m.GetUsableHeight()
	newRenderWidth := m.GetRenderWidth()

	widthScale := float64(newRenderWidth) / float64(oldWidth)
	heightScale := float64(newUsableHeight) / float64(oldUsableHeight)

	m.LogInfo("[SCALE] Scaling windows: width %.2fx, height %.2fx", widthScale, heightScale)

	for _, win := range m.Windows {
		if win.Minimized {
			continue
		}

		// Scale position and size
		win.X = int(float64(win.X) * widthScale)
		win.Y = int(float64(win.Y) * heightScale)
		win.Width = int(float64(win.Width) * widthScale)
		win.Height = int(float64(win.Height) * heightScale)

		// Ensure minimum size
		if win.Width < config.DefaultWindowWidth {
			win.Width = config.DefaultWindowWidth
		}
		if win.Height < config.DefaultWindowHeight {
			win.Height = config.DefaultWindowHeight
		}

		// Ensure windows don't exceed the content region beside a reserved
		// sidebar band, matching ClampWindowsToView.
		leftMargin := m.GetLeftMargin()
		contentRight := leftMargin + m.GetContentWidth()
		if win.Width > contentRight-leftMargin {
			win.Width = contentRight - leftMargin
		}
		if win.Height > newUsableHeight {
			win.Height = newUsableHeight
		}

		// Ensure position keeps window inside the content region
		if win.X < leftMargin {
			win.X = leftMargin
		}
		if win.Y < 0 {
			win.Y = 0
		}
		if win.X+win.Width > contentRight {
			win.X = contentRight - win.Width
		}
		if win.Y+win.Height > newUsableHeight {
			win.Y = newUsableHeight - win.Height
		}

		// Mark dirty and resize PTY
		win.MarkPositionDirty()
		win.Resize(win.Width, win.Height)
	}
}

// ClampWindowsToView ensures all floating windows are visible within the current terminal bounds.
// This is called when reattaching with a smaller terminal or when the terminal shrinks.
// Windows that would be off-screen are repositioned to remain visible.
func (m *OS) ClampWindowsToView() {
	if m.AutoTiling {
		return // Tiling mode handles its own layout
	}

	usableHeight := m.GetUsableHeight()
	renderWidth := m.GetRenderWidth()
	// The sidebar reserves a horizontal band the same way the dock reserves a
	// vertical one, so a floating pane is clamped against the content region
	// [leftMargin, leftMargin+contentWidth), not the full screen. Without this a
	// pane could be shoved wholly under the sidebar and become unreachable.
	leftMargin := m.GetLeftMargin()
	contentWidth := m.GetContentWidth()
	rightEdge := leftMargin + contentWidth
	topMargin := m.GetTopMargin()
	minVisibleX := 20 // Minimum visible horizontal pixels (matches mouse.go)
	minVisibleY := 3  // Minimum visible vertical rows (matches mouse.go)
	clampedCount := 0

	for _, win := range m.Windows {
		if win.Workspace != m.CurrentWorkspace || win.Minimized {
			continue
		}

		originalX, originalY := win.X, win.Y
		needsResize := false

		// Clamp window size to fit within the content region if larger
		if win.Width > contentWidth {
			win.Width = contentWidth
			needsResize = true
		}
		if win.Height > usableHeight {
			win.Height = usableHeight
			needsResize = true
		}

		// Ensure minimum size
		if win.Width < config.DefaultWindowWidth {
			win.Width = config.DefaultWindowWidth
			needsResize = true
		}
		if win.Height < config.DefaultWindowHeight {
			win.Height = config.DefaultWindowHeight
			needsResize = true
		}

		// Clamp X position: ensure at least minVisibleX pixels are visible within
		// the content region, so a pane can never hide fully behind the sidebar.
		if win.X+win.Width < leftMargin+minVisibleX {
			win.X = leftMargin + minVisibleX - win.Width
		}
		if win.X > rightEdge-minVisibleX {
			win.X = rightEdge - minVisibleX
		}

		// Clamp Y position: ensure at least minVisibleY rows visible, and can't go behind dock
		if win.Y < topMargin {
			win.Y = topMargin
		}
		maxY := topMargin + usableHeight - minVisibleY
		if win.Y > maxY {
			win.Y = maxY
		}

		// If position changed, mark as dirty and log
		if win.X != originalX || win.Y != originalY || needsResize {
			// The new viewport decides where this pane goes, so a snap still
			// heading somewhere the old one implied is obsolete. Left running it
			// stamps that rectangle back on the next tick without resizing the
			// emulator with it, and the pane draws at one size while its guest
			// writes at another.
			m.CancelSnapAnimation(win)
			win.MarkPositionDirty()
			if needsResize {
				win.Resize(win.Width, win.Height)
			}
			clampedCount++
		}
	}

	if clampedCount > 0 {
		m.LogInfo("[CLAMP] Repositioned %d windows to fit terminal bounds (%dx%d)", clampedCount, renderWidth, m.GetRenderHeight())
		m.SyncStateToDaemon()
	}
}

// GetTopMargin returns the margin at the top (reserved space for the dockbar
// when positioned at "top").
func (m *OS) GetTopMargin() int {
	return m.clampReserve(m.paneReserve().Top, m.GetRenderHeight())
}

// GetBottomMargin returns the rows reserved below the panes: the dock when it
// sits at the bottom, or whatever more a peer client reserves there.
func (m *OS) GetBottomMargin() int {
	return m.clampReserve(m.paneReserve().Bottom, m.GetRenderHeight())
}

// GetDockbarContentYPosition returns the Y position of the dockbar
func (m *OS) GetDockbarContentYPosition() int {
	if config.DockbarPosition == "top" {
		return 0
	}

	return m.Height - 1
}

// GetTimeYPosition returns the Y position of the time display
func (m *OS) GetTimeYPosition() int {
	if config.DockbarPosition == "top" {
		return m.Height - 1
	}

	return 0
}

// GetUsableHeight returns the usable height excluding the dock. Auto-hide
// mode keeps the reservation so tiled windows have a stable layout  - the dock
// only hides when a specific window (zoom/float) explicitly expands into its
// rows.
//
// The floor is not cosmetic. On a host shorter than the dock the subtraction
// goes negative, and every caller reads this as an extent: the render loop
// hands it to clipWindowContent as a viewport height, which then slices a line
// list by a negative bound and panics inside View, outside Update's recover.
func (m *OS) GetUsableHeight() int {
	return max(m.GetRenderHeight()-m.GetTopMargin()-m.GetBottomMargin(), 0)
}

// GetRenderWidth returns the width to use for rendering.
// In multi-client mode, this is the minimum of the terminal width and
// the effective session width (min of all connected clients).
func (m *OS) GetRenderWidth() int {
	// If terminal size not yet known, use effective size if available
	if m.Width == 0 {
		if m.EffectiveWidth > 0 {
			return m.EffectiveWidth
		}
		return 0
	}
	// Use minimum of terminal and effective size
	if m.EffectiveWidth > 0 && m.EffectiveWidth < m.Width {
		return m.EffectiveWidth
	}
	return m.Width
}

// GetSidebarWidth is the single function that folds the configured sidebar
// width together with the narrow-screen breakpoints, exactly as overlay.FitWidth
// folds a panel's preferred width with the screen width. It returns the number
// of columns the sidebar actually reserves, or 0 when the sidebar is off, hidden,
// or the screen is too narrow to carry it.
//
// Breakpoints, measured against the render width:
//
//	>= 90 cols: full sidebar at config.SidebarWidth
//	60-89 cols: narrow rail (glyph + short name)
//	40-59 cols: glyph-only rail
//	< 40 cols:  auto-hidden regardless of config
//
// After picking a variant it enforces a pane floor: the content area never drops
// below SidebarMinPaneFloor columns, so the sidebar can never squeeze the panes
// to nothing. If the floor would be violated it steps down to a narrower variant
// first, then hides.
func (m *OS) GetSidebarWidth() int {
	return m.sidebarWidthFor(m.sidebarPreferredWidth())
}

// sidebarPreferredWidth is the width the user is asking for: the stored one, or
// the glyph strip while the rail is collapsed. Collapse rides the same path the
// breakpoints do rather than short-circuiting them, so a collapsed rail on a
// wide screen and a rail the screen collapsed for itself are one state.
func (m *OS) sidebarPreferredWidth() int {
	if m.SidebarCollapsed {
		return config.SidebarGlyphWidth
	}
	return m.sidebarStoredWidth()
}

// sidebarStoredWidth is the expanded width the rail returns to, never below the
// narrow variant: an expand that lands back on the glyph strip is not an expand.
func (m *OS) sidebarStoredWidth() int {
	return max(m.sidebarWidthPreference(), config.SidebarNarrowWidth)
}

// sidebarWidthPreference is the expanded width this session asks for: the one
// dragged or synced, falling back to the configured default when nobody has
// asked for anything. It is model state rather than a global because the rail
// is shared with the session's other clients and because one tuios-web process
// serves several sessions at once, where a global is one drag away from
// resizing a rail nobody touched.
func (m *OS) sidebarWidthPreference() int {
	if m.SidebarWidthPref > 0 {
		return m.SidebarWidthPref
	}
	return config.SidebarWidth
}

// sidebarWidthFor is GetSidebarWidth against a hypothetical preferred width, so
// the footer stepper can ask what a width would actually get it before offering
// the step. GetSidebarWidth is this with the configured preference.
func (m *OS) sidebarWidthFor(prefer int) int {
	if !config.SidebarEnabled || config.SidebarPosition == "hidden" {
		return 0
	}
	rw := m.GetRenderWidth()
	if rw <= 0 {
		return 0
	}

	var w int
	switch {
	case rw < config.SidebarBreakpointGlyph:
		return 0
	case rw < config.SidebarBreakpointNarrow:
		w = config.SidebarGlyphWidth
	case rw < config.SidebarBreakpointFull:
		w = min(max(prefer, config.SidebarGlyphWidth), config.SidebarNarrowWidth)
	default:
		w = max(prefer, config.SidebarGlyphWidth)
	}

	// Enforce the pane floor by stepping down to a narrower variant rather than
	// starving the content area.
	if rw-w < config.SidebarMinPaneFloor {
		switch {
		case rw-config.SidebarNarrowWidth >= config.SidebarMinPaneFloor:
			w = config.SidebarNarrowWidth
		case rw-config.SidebarGlyphWidth >= config.SidebarMinPaneFloor:
			w = config.SidebarGlyphWidth
		default:
			return 0
		}
	}
	return w
}

// OwnLayoutReserve is the chrome this client draws around the panes: its own
// sidebar rail and its own dock, in its own configuration. It is what this
// client tells the daemon it needs, never what it lays the panes out around -
// see GetLeftMargin.
func (m *OS) OwnLayoutReserve() session.LayoutReserve {
	var r session.LayoutReserve
	switch config.SidebarPosition {
	case "left":
		r.Left = m.GetSidebarWidth()
	case "right":
		r.Right = m.GetSidebarWidth()
	}
	switch config.DockbarPosition {
	case "top":
		r.Top = config.DockHeight
	case "hidden":
	default:
		r.Bottom = config.DockHeight
	}
	return r
}

// paneReserve is the chrome the panes are laid out around, which is not the
// same question as what this client draws. Every client attached is looking at
// the same PTYs and a PTY has exactly one size, so the box the panes are
// partitioned into has to be identical on every client. The daemon settles that
// by taking the largest reserve any client asks for; this returns it, floored by
// this client's own chrome so that a client on its own, or one whose
// announcement has not landed yet, still reserves what it is about to draw.
//
// The consequence is deliberate: a client with less chrome than the session
// agreed leaves the difference blank rather than handing it to the panes. A
// client absorbs its own chrome; it never moves the panes to make room.
func (m *OS) paneReserve() session.LayoutReserve {
	return m.OwnLayoutReserve().Max(m.SessionReserve)
}

// GetLeftMargin returns the columns reserved on the left before any pane is
// placed: the session's agreed chrome reserve, never less than this client's own
// sidebar. The rail itself still draws at GetSidebarWidth; any agreed columns
// beyond it are left blank.
func (m *OS) GetLeftMargin() int {
	return m.clampReserve(m.paneReserve().Left, m.GetRenderWidth())
}

// GetRightMargin returns the columns reserved on the right, on the same terms as
// GetLeftMargin.
func (m *OS) GetRightMargin() int {
	return m.clampReserve(m.paneReserve().Right, m.GetRenderWidth())
}

// clampReserve keeps a reserve from eating the screen it is measured against. A
// reserve is agreed across clients and a client can be narrower than the one
// that asked for it, so the arithmetic has to survive a reserve wider than this
// client's viewport rather than hand a negative extent to a renderer.
func (m *OS) clampReserve(reserve, extent int) int {
	if reserve <= 0 || extent <= 0 {
		return 0
	}
	return min(reserve, extent)
}

// GetContentWidth returns the width available to panes: the render width less the
// left and right reserved margins. This is what tiling partitions and what the
// pane floor is enforced against.
func (m *OS) GetContentWidth() int {
	// Floored, the way GetUsableHeight is floored. The reserve is agreed across
	// clients and this client can be the narrow one, so the subtraction has to
	// survive a reserve as wide as the viewport rather than hand a negative
	// extent to a renderer or a tiler.
	return max(m.GetRenderWidth()-m.GetLeftMargin()-m.GetRightMargin(), 0)
}

// GetRenderHeight returns the height to use for rendering.
// In multi-client mode, this is the minimum of the terminal height and
// the effective session height (min of all connected clients).
func (m *OS) GetRenderHeight() int {
	// If terminal size not yet known, use effective size if available
	if m.Height == 0 {
		if m.EffectiveHeight > 0 {
			return m.EffectiveHeight
		}
		return 0
	}
	// Use minimum of terminal and effective size
	if m.EffectiveHeight > 0 && m.EffectiveHeight < m.Height {
		return m.EffectiveHeight
	}
	return m.Height
}
