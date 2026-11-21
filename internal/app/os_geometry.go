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

	anim := m.CreateSnapAnimation(i, quarter)
	if anim != nil {
		m.addAnimation(anim)
	} else {
		win := m.Windows[i]
		_, _, targetWidth, targetHeight := m.calculateSnapBounds(quarter)

		targetWidth = max(targetWidth, config.DefaultWindowWidth)
		targetHeight = max(targetHeight, config.DefaultWindowHeight)

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
	rightEdge := m.GetLayoutWidth() - m.GetRightMargin()

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
		return
	}

	if oldWidth <= 0 || oldHeight <= 0 || newWidth <= 0 || newHeight <= 0 {
		return
	}

	oldUsableHeight := oldHeight - m.GetTopMargin() - m.GetBottomMargin()

	newUsableHeight := m.GetUsableHeight()
	newRenderWidth := m.GetLayoutWidth()

	widthScale := float64(newRenderWidth) / float64(oldWidth)
	heightScale := float64(newUsableHeight) / float64(oldUsableHeight)

	m.LogInfo("[SCALE] Scaling windows: width %.2fx, height %.2fx", widthScale, heightScale)

	for _, win := range m.Windows {
		if win.Minimized {
			continue
		}

		win.X = int(float64(win.X) * widthScale)
		win.Y = int(float64(win.Y) * heightScale)
		win.Width = int(float64(win.Width) * widthScale)
		win.Height = int(float64(win.Height) * heightScale)

		if win.Width < config.DefaultWindowWidth {
			win.Width = config.DefaultWindowWidth
		}
		if win.Height < config.DefaultWindowHeight {
			win.Height = config.DefaultWindowHeight
		}

		leftMargin := m.GetLeftMargin()
		contentRight := leftMargin + m.GetContentWidth()
		if win.Width > contentRight-leftMargin {
			win.Width = contentRight - leftMargin
		}
		if win.Height > newUsableHeight {
			win.Height = newUsableHeight
		}

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

		win.MarkPositionDirty()
		win.Resize(win.Width, win.Height)
	}
}

// ClampWindowsToView ensures all floating windows are visible within the current terminal bounds.
// This is called when reattaching with a smaller terminal or when the terminal shrinks.
// Windows that would be off-screen are repositioned to remain visible.
func (m *OS) ClampWindowsToView() {
	if m.AutoTiling {
		return
	}

	usableHeight := m.GetUsableHeight()
	renderWidth := m.GetRenderWidth()
	leftMargin := m.GetLeftMargin()
	contentWidth := m.GetContentWidth()
	rightEdge := leftMargin + contentWidth
	topMargin := m.GetTopMargin()
	minVisibleX := 20
	minVisibleY := 3
	clampedCount := 0

	for _, win := range m.Windows {
		if win.Workspace != m.CurrentWorkspace || win.Minimized {
			continue
		}

		originalX, originalY := win.X, win.Y
		needsResize := false

		if win.Width > contentWidth {
			win.Width = contentWidth
			needsResize = true
		}
		if win.Height > usableHeight {
			win.Height = usableHeight
			needsResize = true
		}

		if win.Width < config.DefaultWindowWidth {
			win.Width = config.DefaultWindowWidth
			needsResize = true
		}
		if win.Height < config.DefaultWindowHeight {
			win.Height = config.DefaultWindowHeight
			needsResize = true
		}

		if win.X+win.Width < leftMargin+minVisibleX {
			win.X = leftMargin + minVisibleX - win.Width
		}
		if win.X > rightEdge-minVisibleX {
			win.X = rightEdge - minVisibleX
		}

		if win.Y < topMargin {
			win.Y = topMargin
		}
		maxY := topMargin + usableHeight - minVisibleY
		if win.Y > maxY {
			win.Y = maxY
		}

		if win.X != originalX || win.Y != originalY || needsResize {
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
	return m.clampReserve(m.paneReserve().Top, m.GetLayoutHeight())
}

// GetBottomMargin returns the rows reserved below the panes: the dock when it
// sits at the bottom, or whatever more a peer client reserves there.
func (m *OS) GetBottomMargin() int {
	return m.clampReserve(m.paneReserve().Bottom, m.GetLayoutHeight())
}

// GetDockbarContentYPosition returns the Y position of the dockbar
func (m *OS) GetDockbarContentYPosition() int {
	if m.Settings.DockbarPosition == "top" {
		return 0
	}

	return m.Height - 1
}

// GetTimeYPosition returns the Y position of the time display
func (m *OS) GetTimeYPosition() int {
	if m.badgeOnBottomRow() {
		return m.Height - 1
	}
	return 0
}

func (m *OS) badgeY(kind badgeKind) int {
	if kind == badgeLeader && m.Settings.DockbarPosition != "hidden" && m.Settings.DockHeight() >= 2 {
		if m.Settings.DockbarPosition == "top" {
			return m.Settings.DockHeight() - 1
		}
		return m.Height - m.Settings.DockHeight()
	}
	return m.GetTimeYPosition()
}

func (m *OS) badgeOnBottomRow() bool {
	return m.Settings.DockbarPosition == "top"
}

// GetUsableHeight returns the usable height excluding the dock. Auto-hide
// mode keeps the reservation so tiled windows have a stable layout. The dock
// only hides when a specific window (zoom/float) explicitly expands into its
// rows.
//
// The floor is not cosmetic. On a host shorter than the dock the subtraction
// goes negative, and every caller reads this as an extent: the render loop
// hands it to clipWindowContent as a viewport height, which then slices a line
// list by a negative bound and panics inside View, outside Update's recover.
func (m *OS) GetUsableHeight() int {
	return max(m.GetLayoutHeight()-m.GetTopMargin()-m.GetBottomMargin(), 0)
}

// GetRenderWidth returns the width to use for rendering.
// Against a daemon that sizes sessions by window_size it is the terminal's
// width: the session's panes are drawn as a view inside it (see
// pane_view.go). Against an older daemon it is the minimum of the terminal
// width and the session's.
func (m *OS) GetRenderWidth() int {
	if m.Width == 0 {
		if m.EffectiveWidth > 0 {
			return m.EffectiveWidth
		}
		return 0
	}
	if m.windowSizeAware() {
		return m.Width
	}
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
	if m.sidebarDrawing {
		return m.sidebarDrawingWidth
	}
	return m.sidebarWidthFor(m.sidebarPreferredWidth())
}

func (m *OS) sidebarPreferredWidth() int {
	if m.SidebarCollapsed {
		return config.SidebarGlyphWidth
	}
	return m.sidebarStoredWidth()
}

func (m *OS) sidebarStoredWidth() int {
	return max(m.sidebarWidthPreference(), config.SidebarNarrowWidth)
}

func (m *OS) sidebarWidthPreference() int {
	if m.SidebarWidthPref > 0 {
		return m.SidebarWidthPref
	}
	return m.Settings.SidebarWidth
}

func (m *OS) sidebarWidthFor(prefer int) int {
	if m.NoRail || !m.Settings.SidebarEnabled || m.Settings.SidebarPosition == "hidden" {
		return 0
	}
	return sidebarResponsiveWidth(prefer, m.GetRenderWidth())
}

func (m *OS) secondarySidebarWidth() int {
	if m.NoRail || m.UserConfig == nil {
		return 0
	}
	var cfg *config.SidebarEdgeConfig
	if m.legacySidebarEdge() == sidebarRight {
		cfg = m.UserConfig.Appearance.Sidebar.Left
	} else {
		cfg = m.UserConfig.Appearance.Sidebar.Right
	}
	if cfg == nil || cfg.Enabled == nil || !*cfg.Enabled {
		return 0
	}
	width := cfg.Width
	if width <= 0 {
		width = config.SidebarDefaultWidth
	}
	if m.secondaryWidthPref > 0 {
		width = m.secondaryWidthPref
	}
	if m.secondaryCollapsed {
		width = config.SidebarGlyphWidth
	}
	return sidebarResponsiveWidth(width, m.GetRenderWidth()-m.sidebarWidthFor(m.sidebarPreferredWidth()))
}

func sidebarResponsiveWidth(prefer, rw int) int {
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
// client tells the daemon it needs, never what it lays the panes out around.
// See GetLeftMargin.
//
// A rail opened only to hold the keyboard scope is not in it. It is drawn over
// the panes for as long as the scope is open, so opening it moves no pane on
// any client and leaves no blank band on another client's screen.
func (m *OS) OwnLayoutReserve() session.LayoutReserve {
	var r session.LayoutReserve
	secondary := m.secondarySidebarWidth()
	if m.legacySidebarEdge() == sidebarLeft {
		r.Right = secondary
	} else {
		r.Left = secondary
	}
	if !m.SidebarRevealedForFocus {
		switch m.Settings.SidebarPosition {
		case "left":
			r.Left = m.sidebarWidthFor(m.sidebarPreferredWidth())
		case "right":
			r.Right = m.sidebarWidthFor(m.sidebarPreferredWidth())
		}
	}
	switch m.Settings.DockbarPosition {
	case "top":
		r.Top = m.Settings.DockHeight()
	case "hidden":
	default:
		r.Bottom = m.Settings.DockHeight()
	}
	if m.badgeRowReserved() {
		if m.badgeOnBottomRow() {
			r.Bottom++
		} else {
			r.Top++
		}
	}
	return r
}

func (m *OS) paneReserve() session.LayoutReserve {
	own := m.OwnLayoutReserve()
	r := own.Max(m.SessionReserve)
	if m.DaemonClient == nil || m.DaemonClient.SessionWindowSize() != config.WindowSizeLatest {
		return r
	}
	if m.widthDiffers() {
		r.Left, r.Right = m.SessionReserve.Left, m.SessionReserve.Right
	}
	if m.heightDiffers() {
		r.Top, r.Bottom = m.SessionReserve.Top, m.SessionReserve.Bottom
	}
	return r
}

// GetLeftMargin returns the columns reserved on the left before any pane is
// placed: the session's agreed chrome reserve, never less than this client's own
// sidebar. The rail itself still draws at GetSidebarWidth; any agreed columns
// beyond it are left blank.
func (m *OS) GetLeftMargin() int {
	return m.clampReserve(m.paneReserve().Left, m.GetLayoutWidth())
}

// GetRightMargin returns the columns reserved on the right, on the same terms as
// GetLeftMargin.
func (m *OS) GetRightMargin() int {
	return m.clampReserve(m.paneReserve().Right, m.GetLayoutWidth())
}

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
	return max(m.GetLayoutWidth()-m.GetLeftMargin()-m.GetRightMargin(), 0)
}

// GetRenderHeight returns the height to use for rendering. See
// GetRenderWidth.
func (m *OS) GetRenderHeight() int {
	if m.Height == 0 {
		if m.EffectiveHeight > 0 {
			return m.EffectiveHeight
		}
		return 0
	}
	if m.windowSizeAware() {
		return m.Height
	}
	if m.EffectiveHeight > 0 && m.EffectiveHeight < m.Height {
		return m.EffectiveHeight
	}
	return m.Height
}
