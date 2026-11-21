package app

import (
	"image"
	"image/color"
	"os"
	"runtime/debug"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/pool"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	uv "github.com/charmbracelet/ultraviolet"
	"tuios.dev/vt"
)

func (m *OS) GetCanvas(render bool) *frameCanvas {
	syncOverlaySettings(&m.Settings)

	rw, rh := m.GetRenderWidth(), m.GetRenderHeight()
	desktop := m.surfaceGround(surfaceDesktop)
	if m.renderCanvas == nil {
		m.renderCanvas = &frameCanvas{Buffer: *uv.NewBuffer(rw, rh)}
	} else {
		m.renderCanvas.Resize(rw, rh)
	}
	canvas := m.renderCanvas

	m.sessionView = m.computeSessionView()
	var cullLeft, cullRight, cullTop, cullBottom int

	layersPtr := pool.GetLayerSlice()
	layers := (*layersPtr)[:0]
	defer pool.PutLayerSlice(layersPtr)

	var direct directPane
	if render {
		direct = m.chooseDirectPane()
		if direct.w == nil {
			m.directHold.drawn = false
		}
	}
	defer direct.release()

	m.resetScrollbarRects()
	m.pruneWindowButtonRects()
	m.gradient.on = false
	gradient := m.gradientOn()

	topMargin := m.GetTopMargin()
	viewportHeight := m.GetUsableHeight()
	leftMargin := m.GetLeftMargin()
	rightClip := leftMargin + m.GetContentWidth()
	cullLeft, cullRight = leftMargin, rightClip
	cullTop, cullBottom = 0, viewportHeight+topMargin
	if m.sessionView.on {
		vis := m.sessionView.visible()
		cullLeft, cullRight = max(cullLeft, vis.Min.X), min(cullRight, vis.Max.X)
		cullTop, cullBottom = vis.Min.Y, min(cullBottom, vis.Max.Y)
	}

	zoomedWindow := m.zoomedWindow()
	zoomCovers := m.zoomCoversRegion(zoomedWindow)

	grounds := m.frameGrounds()
	paintGround := grounds.any()
	if paintGround {
		if m.paneContentRects == nil {
			m.paneContentRects = make(map[string]image.Rectangle, len(m.Windows))
		}
		clear(m.paneContentRects)
	}

	var animatingWindows map[*terminal.Window]struct{}
	if len(m.Animations) > 0 {
		animatingWindows = make(map[*terminal.Window]struct{}, len(m.Animations))
		for _, anim := range m.Animations {
			if !anim.Complete {
				animatingWindows[anim.Window] = struct{}{}
			}
		}
	}

	inScratch := m.InScratchView()
	_, scratchInner, _ := m.scratchRegion()
	for i := range m.Windows {
		window := m.Windows[i]

		backdrop := false
		if window.Workspace != m.CurrentWorkspace {
			if !inScratch || window.Workspace != m.scratchBase {
				continue
			}
			backdrop = true
		}

		_, isAnimating := animatingWindows[window]

		if window.Minimized && !isAnimating {
			continue
		}

		if zoomedWindow != nil && window != zoomedWindow && !window.IsPopup && zoomCovers && !backdrop {
			continue
		}

		margin := 5
		if isAnimating {
			margin = 20
		}

		clipL, clipT, clipR, clipB := leftMargin, topMargin, rightClip, viewportHeight+topMargin
		inBox := inScratch && !backdrop
		if inBox {
			clipL, clipT = scratchInner.X, scratchInner.Y
			clipR, clipB = scratchInner.X+scratchInner.W, scratchInner.Y+scratchInner.H
		}

		isVisible := window.X+window.Width >= cullLeft-margin &&
			window.X <= cullRight+margin &&
			window.Y+window.Height >= cullTop-margin &&
			window.Y <= cullBottom+margin
		if inBox {
			isVisible = isVisible && window.X+window.Width > clipL && window.X < clipR &&
				window.Y+window.Height > clipT && window.Y < clipB
		}

		if !isVisible {
			continue
		}

		if paintGround {
			m.paneContentRects[window.ID] = paneContentRect(window)
		}

		isFullyVisible := window.X >= clipL && window.Y >= clipT &&
			window.X+window.Width <= clipR &&
			window.Y+window.Height <= clipB
		barClip := rightClip
		if inBox && !isFullyVisible {
			barClip = -1
		}

		isFocused := m.FocusedWindow == i && m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows)
		isMultifocused := len(m.MultifocusSet) > 0 && m.MultifocusSet[window.ID]
		borderColorObj, byState := m.paneBorderColor(window, isFocused, isMultifocused)
		if gradient && isFocused && !byState && !rendersBorderless(window) && !m.zenBordersHidden(true) {
			m.markGradient(window.ID, image.Rect(window.X, window.Y, window.X+window.Width, window.Y+window.Height), borderColorObj)
		}

		zIndex := windowLayerZ(window, isAnimating)
		if backdrop {
			zIndex += scratchBackdropZ
		}

		borderStale := window.CachedLayer != nil && !sameColor(window.CachedLayerBorder, borderColorObj)

		if window.CachedLayer != nil && !borderStale && !window.Dirty && !window.ContentDirty && !window.PositionDirty {
			if renderTraceEnabled {
				traceLayerHold(window, isFocused, "clean")
			}
			layers = append(layers, window.CachedLayer)
			if windowNeedsScrollbar(window, &m.Settings) {
				if sbLayer := m.renderScrollbarLayer(window, barClip, zIndex+1, isFocused); sbLayer != nil {
					layers = append(layers, sbLayer)
				}
			}
			continue
		}

		if window.Terminal != nil && window.Terminal.IsSyncActive() &&
			window.CachedLayer != nil && !borderStale &&
			window.CachedLayer.GetX() == window.X &&
			window.CachedLayer.GetY() == window.Y &&
			window.CachedLayer.GetZ() == zIndex {
			if renderTraceEnabled {
				traceLayerHold(window, isFocused, "sync-2026")
			}
			layers = append(layers, window.CachedLayer)
			if windowNeedsScrollbar(window, &m.Settings) {
				if sbLayer := m.renderScrollbarLayer(window, barClip, zIndex+1, isFocused); sbLayer != nil {
					layers = append(layers, sbLayer)
				}
			}
			continue
		}

		needsRedraw := window.CachedLayer == nil || borderStale ||
			window.Dirty || window.ContentDirty || window.PositionDirty ||
			window.CachedLayer.GetX() != window.X ||
			window.CachedLayer.GetY() != window.Y ||
			window.CachedLayer.GetZ() != zIndex

		if !needsRedraw || (!isFocused && !isFullyVisible && !window.ContentDirty && !window.PositionDirty && !window.IsBeingManipulated && window.CachedLayer != nil) {
			if renderTraceEnabled {
				traceLayerHold(window, isFocused, "not-needed")
			}
			layers = append(layers, window.CachedLayer)
			continue
		}

		var boxContent string
		if window == direct.w {
			window.RenderedCols, window.RenderedRows = window.ContentWidth(), window.ContentHeight()
			boxContent = m.windowBox(window, isFocused, borderColorObj, m.blankBody(window.ContentWidth(), window.ContentHeight()))
		} else {
			boxContent = m.renderWindowBox(window, i, isFocused, borderColorObj)
		}

		clippedContent, finalX, finalY := boxContent, window.X, window.Y
		if !isFullyVisible {
			if inBox {
				clippedContent, finalX, finalY = clipWindowContent(
					boxContent,
					window.X-clipL, window.Y-clipT,
					clipR-clipL, clipB-clipT,
				)
				finalX += clipL
				finalY += clipT
			} else {
				clippedContent, finalX, finalY = clipWindowContent(
					boxContent,
					window.X, window.Y,
					rightClip, viewportHeight+topMargin,
				)
			}
		}

		if renderTraceEnabled {
			traceLayerBuild(window, isFocused, boxContent, clippedContent,
				window.X, window.Y, finalX, finalY, zIndex, clipR, clipB)
		}

		if window == direct.w {
			window.CachedLayer = m.directBoxLayer(window.ID, clippedContent, finalX, finalY, zIndex)
		} else {
			window.CachedLayer = lipgloss.NewLayer(clippedContent).X(finalX).Y(finalY).Z(zIndex).ID(window.ID)
		}
		window.CachedLayerBorder = borderColorObj
		layers = append(layers, window.CachedLayer)

		if windowNeedsScrollbar(window, &m.Settings) {
			if sbLayer := m.renderScrollbarLayer(window, barClip, zIndex+1, isFocused); sbLayer != nil {
				layers = append(layers, sbLayer)
			}
		}

		if window.Terminal == nil || !window.Terminal.IsSyncActive() {
			window.ClearDirtyFlags()
		}
	}

	if frame := m.renderScratchFrame(); frame != nil {
		layers = append(layers, frame)
	}
	layers = append(layers, m.renderStackRows()...)

	if m.panesBorderless() {
		if sepLayers := m.renderSeparatorOverlay(); len(sepLayers) > 0 {
			layers = append(layers, sepLayers...)
		}
	}

	paneLayers := len(layers)

	if render {
		if sidebarLayer := m.renderSidebar(); sidebarLayer != nil {
			layers = append(layers, sidebarLayer)
		}
		if sidebarLayer := m.renderSecondarySidebar(); sidebarLayer != nil {
			layers = append(layers, sidebarLayer)
		}
		if pipLayer := m.renderPiP(); pipLayer != nil {
			layers = append(layers, pipLayer)
		}
		if card := m.renderSetupCard(); card != nil {
			layers = append(layers, card)
		}
		overlays := m.renderOverlays()
		layers = append(layers, overlays...)

		if m.Settings.DockbarPosition != "hidden" {
			dockLayer := m.renderDock()
			layers = append(layers, dockLayer)
		}

		if tip := m.renderTooltip(); tip != nil {
			layers = append(layers, tip)
		}

		if label := m.renderLinkLabel(); label != nil {
			layers = append(layers, label)
		}

		if label := m.renderFileDragLabel(); label != nil {
			layers = append(layers, label)
		}

		if label := m.renderDnDLabel(); label != nil {
			layers = append(layers, label)
		}

		if mark := m.renderViewMark(); mark != nil {
			layers = append(layers, mark)
		}

		layers = m.renderLeftover(layers)

		m.kittyChrome = chromeRectsAbovePanes(m.kittyChrome[:0], layers[paneLayers:])
	} else {
		m.SidebarHits = m.SidebarHits[:0]
		m.motion.rail = m.motion.rail[:0]
	}

	keepable := direct.w != nil && !m.anyEffectLive() && !m.imagesOnScreen()
	if len(m.frameRows) != canvas.Height() {
		m.frameRows = make([]bool, canvas.Height())
		m.frameRowsAll = true
	}
	kept := m.composeLayers(canvas, layers, paneLayers, &keepRequest{ground: desktop, keepable: keepable, pane: &direct})
	m.canvasKept = kept
	if !kept {
		m.frameRowsAll = true
	}

	return canvas
}

func windowLayerZ(window *terminal.Window, animating bool) int {
	if window.Zoomed {
		return config.ZIndexAnimating
	}
	if window.IsFloating {
		return min(config.ZIndexFloating+max(window.Z, 0), config.ZIndexFloatingTop)
	}
	if (animating || window.IsBeingManipulated) && !window.Tiled {
		return config.ZIndexAnimating
	}
	return window.Z
}

// HitZ is the rank a pointer hit test compares: the rank the pane is drawn at
// when it is still. A hit test that compared the raw Z disagreed with the
// frame whenever a floating pane's Z was below a tile's: a click on a popup,
// drawn on top, landed on the tile underneath it.
func HitZ(window *terminal.Window) int {
	return windowLayerZ(window, false)
}

func fitToContentBox(content string, w, h int) string {
	if w < 1 || h < 1 {
		return content
	}
	cw, ch := lipgloss.Size(content)
	if cw <= w && ch <= h {
		return content
	}
	return lipgloss.NewStyle().MaxWidth(w).MaxHeight(h).Render(content)
}

func (m *OS) zenBordersHidden(isFocused bool) bool {
	switch m.Settings.ZenMode {
	case config.ZenModeAlways:
		return !isFocused
	case config.ZenModeMouse:
		if m.pointerRecentlyMoved() {
			return false
		}
		return !isFocused
	default:
		return false
	}
}

func (m *OS) pointerRecentlyMoved() bool {
	return !m.lastPointerAt.IsZero() && time.Since(m.lastPointerAt) <= m.Settings.ZenReveal()
}

func rendersBorderless(window *terminal.Window) bool {
	return window.Tiled
}

func (m *OS) renderWindowBox(window *terminal.Window, index int, isFocused bool, borderColorObj color.Color) string {
	return m.windowBox(window, isFocused, borderColorObj, m.renderTerminal(window, isFocused, m.Mode == TerminalMode))
}

func (m *OS) windowBox(window *terminal.Window, isFocused bool, borderColorObj color.Color, content string) string {
	if m.isTMLPopup(window) {
		return m.renderTMLDialogBox(window, fitToContentBox(content, window.ContentWidth(), window.ContentHeight()))
	}
	preShaped := !preShapedDisabled &&
		window.RenderedCols == window.ContentWidth() &&
		window.RenderedRows == window.ContentHeight()
	if !preShaped {
		content = fitToContentBox(content, window.ContentWidth(), window.ContentHeight())
	}
	if rendersBorderless(window) {
		m.recordWindowButtons(window.ID, nil)
		return content
	}
	if m.zenBordersHidden(isFocused) {
		return m.renderWindowBoxZen(window, content, preShaped)
	}
	if preShaped {
		if out, ok := m.fastWindowBox(content, window, borderColorObj,
			m.workspacePosition(window), m.AutoTiling); ok {
			return out
		}
	}
	box := sizeContentBox(lipgloss.NewStyle().
		Align(lipgloss.Left).
		AlignVertical(lipgloss.Top).
		Border(getBorder(&m.Settings)).
		BorderTop(false), window, preShaped)
	return m.addToBorder(
		box.BorderForeground(borderColorObj).Render(content),
		window.ContentWidth(),
		borderColorObj,
		window,
		m.workspacePosition(window),
		m.AutoTiling,
	)
}

func sizeContentBox(box lipgloss.Style, window *terminal.Window, preShaped bool) lipgloss.Style {
	box = box.Height(window.Height - 1)
	if preShaped {
		return box
	}
	return box.Width(window.Width)
}

func (m *OS) renderWindowBoxZen(window *terminal.Window, content string, preShaped bool) string {
	box := sizeContentBox(lipgloss.NewStyle().
		Align(lipgloss.Left).
		AlignVertical(lipgloss.Top).
		Border(lipgloss.HiddenBorder()).
		BorderTop(false), window, preShaped)
	return strings.Repeat(" ", window.Width) + "\n" + box.Render(content)
}

var fastPathDisabled = os.Getenv("TUIOS_NO_FASTPATH") == "1"

var preShapedDisabled = os.Getenv("TUIOS_NO_PRESHAPED") == "1"

func (m *OS) crashView() tea.View {
	m.hideGraphicsForCrash()
	var view tea.View
	view.SetContent(RenderCrashScreen(m.crash, m.crashNotice, m.Width, m.Height))
	view.AltScreen = true
	view.MouseMode = tea.MouseModeAllMotion
	view.ReportFocus = true
	if m.screen != nil {
		m.screen.FrameString(view.Content, m.Width, m.Height)
		m.screen.Flush(view, true)
		m.screen.Commit()
	}
	return view
}

func (m *OS) safeComposeFrame() (frame viewContent, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			m.NoteCrash("drawing the screen", r, stack)
			m.LogError("recovered panic in render: %v (crash log: %s)\n%s", r, m.crashLogPath(), stack)
			frame, ok = "", false
		}
	}()
	return m.composeView(), true
}

func (m *OS) composeView() viewContent {
	if m.screen != nil {
		return m.screenFrame()
	}
	frame := m.composedFrame().Text()
	m.themeFx.lastText, m.themeFx.drawn = frame, true
	return viewContent(frame)
}

type viewContent string

type composed struct {
	canvas *frameCanvas
	text   string
}

func (m *OS) composedFrame() composed {
	canvas, text := m.composeFrameParts()
	return composed{canvas: canvas, text: text}
}

func (c composed) Text() string {
	if c.canvas != nil {
		return c.canvas.Render()
	}
	return c.text
}

func (m *OS) composeFrameParts() (*frameCanvas, string) {
	m.syncReportColors()
	m.noteThemeChange()
	m.noteThemeEvent()

	if m.themeFx.pending {
		m.keepThemeFrame()
	}
	m.canvasIsFrame = false
	if m.screensaver.active && m.screensaver.frame != "" {
		m.OverlayHits = m.OverlayHits[:0]
		m.SixelPassthrough.SetFrame(nil, 0, 0, 0, 0)
		return nil, m.screensaver.frame
	}
	m.closeStaleHints()
	m.PaneLabelsOpen()
	if window, ok := m.fullscreenFastWindow(); ok && !fastPathDisabled && m.screen == nil {
		m.motion.rail = m.motion.rail[:0]
		m.OverlayHits = m.OverlayHits[:0]
		frame := m.buildFullscreenFrame(window)
		if !strings.Contains(frame, vt.SixelMarkerLead) {
			m.SixelPassthrough.SetFrame(nil, 0, 0, 0, 0)
			return nil, frame
		}
	}
	canvas := m.GetCanvas(true)
	m.drawImageSymbols(canvas)
	m.drawEffects(canvas, time.Now())
	m.scanSixelFrame(canvas)
	m.canvasIsFrame = true
	return canvas, ""
}

func (m *OS) fullscreenFastWindow() (*terminal.Window, bool) {
	return m.loneWindow(false)
}

func (m *OS) loneWindow(direct bool) (*terminal.Window, bool) {
	if m.anyEffectLive() || m.Renaming() {
		return nil, false
	}
	if m.Settings.BorderFocusedGradient != "" && m.gradientOn() {
		return nil, false
	}
	if m.AnyOverlayOpen() || m.ShowScrollbackBrowser || m.ShowCacheStats || m.ShowTapeManager || m.ShowTapeReview ||
		m.PrefixActive || m.ContextMenu != nil || m.pathPicker != nil || m.pastePreviewOn() || m.Capture.Active || m.review.open ||
		m.quickLook.open {
		return nil, false
	}
	if m.TapeRecorder != nil && m.TapeRecorder.IsRecording() {
		return nil, false
	}
	if m.ShowKeys && len(m.RecentKeys) > 0 {
		return nil, false
	}
	if m.Tooltip.Source != tooltipNone {
		return nil, false
	}
	if m.panesBorderless() {
		if !direct {
			return nil, false
		}
		if splits, stack := m.dividerLines(m.GetBSPBounds()); len(splits) > 0 || len(stack) > 0 {
			return nil, false
		}
	}
	if m.viewShifted() {
		return nil, false
	}
	if m.pipWanted() && m.fastPathPaints() {
		return nil, false
	}
	if m.setupCardRecord() != nil {
		return nil, false
	}
	if !direct && (m.GetSidebarWidth() > 0 || m.secondarySidebarWidth() > 0) {
		return nil, false
	}
	if m.KittyPassthrough != nil && m.KittyPassthrough.HasPlacements() {
		return nil, false
	}

	visible := m.GetVisibleWindows()
	if len(visible) != 1 {
		return nil, false
	}
	window := visible[0]
	if _, dim := m.popupScrimTarget(); dim > 0 {
		return nil, false
	}
	if window.IsBeingManipulated {
		return nil, false
	}
	if m.SixelPassthrough != nil && m.SixelPassthrough.WindowHasImages(window.ID) {
		return nil, false
	}
	if windowNeedsScrollbar(window, &m.Settings) {
		return nil, false
	}
	if own := m.OwnLayoutReserve(); m.GetTopMargin() != own.Top || m.GetBottomMargin() != own.Bottom {
		return nil, false
	}
	left, cw, topMargin, usableH := 0, m.GetRenderWidth(), m.GetTopMargin(), m.GetUsableHeight()
	if direct {
		left, cw = m.GetLeftMargin(), m.GetContentWidth()
	}
	if window.X != left || window.Y != topMargin || window.Width != cw || window.Height != usableH {
		return nil, false
	}
	if m.fastPathPaints() && !fastPaintFits(window) {
		return nil, false
	}
	return window, true
}

func (m *OS) fastPathPaints() bool {
	return m.surfaceGround(surfacePane).on() || m.surfaceGround(surfaceChrome).on() ||
		(m.surfaceGround(surfaceDock).on() && m.Settings.DockbarPosition != "hidden")
}

func (m *OS) focusedBorderColor(state color.Color) color.Color {
	if state != nil {
		return state
	}
	tint, tintTerminal, _, tinted := m.sessionBorderTint()
	switch {
	case m.Mode == TerminalMode && tinted:
		return tintTerminal
	case m.Mode == TerminalMode:
		return theme.BorderFocusedTerminalOn(m.host.bg)
	case tinted:
		return tint
	}
	return theme.BorderFocusedWindowOn(m.host.bg)
}

func (m *OS) paneBorderColor(window *terminal.Window, isFocused, isMultifocused bool) (c color.Color, byState bool) {
	switch {
	case isFocused:
		state := m.focusedStateBorder(window)
		return m.focusedBorderColor(state), state != nil
	case m.MultiCopyParked(window.ID):
		return theme.BorderUnfocusedOn(m.host.bg), false
	case isMultifocused:
		return theme.BorderMultifocus(), false
	}
	return m.unfocusedBorderColor(), false
}

func (m *OS) unfocusedBorderColor() color.Color {
	if _, _, quiet, tinted := m.sessionBorderTint(); tinted {
		return quiet
	}
	return theme.BorderUnfocusedOn(m.host.bg)
}

func (m *OS) focusedStateBorder(w *terminal.Window) color.Color {
	if w == nil {
		return nil
	}
	if w.CopyModeVisible() {
		if c := theme.BorderFocusedCopy(); c != nil {
			return c
		}
	}
	if m.Nested() {
		return theme.UI().Info
	}
	if c := theme.BorderFocusedAttention(); c != nil && m.windowMarkState(w) == "needs_input" {
		return c
	}
	return nil
}

func (m *OS) buildFullscreenFrame(window *terminal.Window) string {
	m.resetScrollbarRects()
	m.pruneWindowButtonRects()

	isFocused := m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) && m.Windows[m.FocusedWindow] == window
	isMultifocused := len(m.MultifocusSet) > 0 && m.MultifocusSet[window.ID]
	borderColorObj, _ := m.paneBorderColor(window, isFocused, isMultifocused)

	windowIndex := -1
	for i := range m.Windows {
		if m.Windows[i] == window {
			windowIndex = i
			break
		}
	}
	boxContent := m.renderWindowBox(window, windowIndex, isFocused, borderColorObj)
	if window.Terminal == nil || !window.Terminal.IsSyncActive() {
		window.ClearDirtyFlags()
	}
	window.CachedLayer = nil

	if m.fastPathPaints() {
		var dockStr string
		if m.Settings.DockbarPosition != "hidden" {
			dockStr, _ = m.renderDockString()
		}
		return m.paintFullscreenFrame(window, boxContent, dockStr, m.Settings.DockbarPosition)
	}
	frame := boxContent
	if m.Settings.DockbarPosition != "hidden" {
		dockStr, _ := m.renderDockString()
		if m.Settings.DockbarPosition == "top" {
			frame = dockStr + "\n" + boxContent
		} else {
			frame = boxContent + "\n" + dockStr
		}
	}
	if l := m.renderPiP(); l != nil {
		frame = pipSplice(frame, l.GetContent(), l.GetX(), l.GetY(), l.Width(), m.GetRenderWidth(), &m.pip.spliceRows, &m.pip.spliceCells)
	}
	return frame
}

func (m *OS) chargeRenderCost(d time.Duration) {
	for _, w := range m.Windows {
		if w != nil {
			w.ChargeRenderCost(d)
		}
	}
}

func (m *OS) View() tea.View {
	if renderTraceEnabled {
		m.traceFrame()
	}
	var view tea.View
	var composed bool

	if notice := m.ExitNotice(); notice != "" {
		view.SetContent(notice + "\n")
		view.AltScreen = false
		if m.screen != nil {
			m.screen.Close()
			m.screen.Print(view.Content)
		}
		return view
	}

	if m.crash != nil {
		return m.crashView()
	}

	compose := func() bool {
		m.FlushPendingBSPSync()
		composeStart := time.Now()
		content, ok := m.safeComposeFrame()
		cost := time.Since(composeStart)
		m.chargeRenderCost(cost)
		m.wsSlide.noteFrameCost(cost)
		if !ok {
			return false
		}
		if content != m.cachedViewContent {
			composed = true
			if m.PostRenderWriter != nil {
				m.PostRenderWriter.FrameComposed()
			}
		}
		m.cachedViewContent = content
		if composed && !m.quietFrame {
			m.noteFrame()
			m.emulatorReleasePending = true
		}
		m.pointerMotionPending = false
		m.lastInteractionRender = time.Now()
		m.zenHidden = m.zenBordersHidden(false)
		view.SetContent(string(content))
		return true
	}

	if cur, _ := m.viewCursor(); m.renderSkipped && m.cachedViewContent != "" && !m.pipCoversCursor(cur) {
		view.SetContent(string(m.cachedViewContent))
	} else if !compose() {
		return m.crashView()
	}

	view.AltScreen = true

	view.MouseMode = tea.MouseModeAllMotion
	if m.MouseOff {
		view.MouseMode = tea.MouseModeNone
	}

	view.ReportFocus = true
	m.notePaneFocus()
	view.DisableBracketedPasteMode = false
	view.KeyboardEnhancements = m.keyboardEnhancements()
	var cursorShown bool
	view.Cursor, cursorShown = m.viewCursor()
	if m.pipCoversCursor(view.Cursor) {
		if !compose() {
			return m.crashView()
		}
		view.Cursor, cursorShown = m.viewCursor()
	}
	m.cursorPark.set(view.Cursor, cursorShown)
	view.WindowTitle = m.ClientTitle
	if view.WindowTitle == "" {
		view.WindowTitle = m.hostTitle()
	}

	if m.screen != nil {
		m.screen.Flush(view, composed)
	} else if cur := cursorState(view.Cursor); composed || cur != m.frameRate.lastCursor {
		m.frameRate.lastCursor = cur
		m.kickFlush()
	}

	if !m.renderSkipped {
		m.flushGraphicsForView()
		if !composed && m.PostRenderWriter != nil {
			m.PostRenderWriter.FlushIfDrawn()
		}
	}

	return view
}

func (m *OS) flushGraphicsForView() {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			m.NoteCrash("drawing images", r, stack)
			m.LogError("recovered panic in graphics flush: %v (crash log: %s)\n%s", r, m.crashLogPath(), stack)
		}
	}()

	m.keepHostPictures.Store(m.hostCaps().KittyGraphics && (m.KittyPassthrough == nil || !m.KittyPassthrough.inlineGraphics))

	for _, w := range m.Windows {
		if w != nil {
			w.PublishGeometry()
		}
	}

	hideImages := m.Resizing || m.ShowHelp || m.ShowCommandPalette || m.ShowLauncher || m.ShowAgentMail || m.ShowInbox ||
		m.gotoPicker.open || m.buffers.open || m.navigator.open || m.ShowLayoutPicker || m.ShowHostPicker || m.ShowQuitMenu || m.DaemonRestartOpen() || m.ShowScrollbackBrowser ||
		m.ShowLogs || m.msgView.open || m.ShowCacheStats ||
		m.ShowSettings || m.ShowThemePicker || m.themeEditor.open || m.presetPicker.open || m.ShowKeybindManager || m.ShowAccentPicker || m.ShowTapeManager || m.ShowTapeReview ||
		m.ShotPreview.Open || m.review.open || m.quickLook.open
	if m.KittyPassthrough != nil {
		m.KittyPassthrough.SetOverlayActive(hideImages)
		m.KittyPassthrough.SetChromeOccluders(m.pipOccluders(m.kittyChrome))
	}

	if m.ShowLauncher {
		m.flushLauncherIconsForFrame()
	} else {
		m.clearLauncherIcons()
	}
	m.flushReplyPreviewForFrame()
	m.flushScreenshotGraphicsForFrame()
	if hideImages {
		if m.KittyPassthrough != nil && m.KittyPassthrough.HasPlacements() {
			m.KittyPassthrough.HideAllPlacements()
		}
	} else {
		m.GetKittyGraphicsCmd()
		m.RefreshTextSizing()
		m.FlushTextSizing()
	}
}

func (m *OS) snapshotPlacementScrollbackLens() {
	if m.placementScrollbackLen == nil {
		m.placementScrollbackLen = make(map[string]int, len(m.Windows))
	} else {
		clear(m.placementScrollbackLen)
	}
	for _, w := range m.Windows {
		if w == nil || w.Terminal == nil {
			continue
		}
		m.placementScrollbackLen[w.ID] = w.ScrollbackLenSync()
	}
}

func (m *OS) GetKittyGraphicsCmd() tea.Cmd {
	if m.KittyPassthrough == nil {
		return nil
	}

	if !m.KittyPassthrough.HasPlacements() {
		m.KittyPassthrough.hostScreenErased.Store(false)
	}

	if m.KittyPassthrough.HasPlacements() {
		m.snapshotPlacementScrollbackLens()
		m.KittyPassthrough.RefreshAllPlacements(func() map[string]*WindowPositionInfo {
			if m.kittyPosMap == nil {
				m.kittyPosMap = make(map[string]*WindowPositionInfo, len(m.Windows))
			} else {
				clear(m.kittyPosMap)
			}
			if cap(m.kittyPosBacking) < len(m.Windows) {
				m.kittyPosBacking = make([]WindowPositionInfo, len(m.Windows))
			}
			backing := m.kittyPosBacking[:len(m.Windows)]
			screenWidth := m.GetRenderWidth()
			screenHeight := m.GetRenderHeight()
			layoutX, layoutY := m.GetLeftMargin(), m.GetTopMargin()
			layoutW, layoutH := m.GetContentWidth(), m.GetUsableHeight()
			view := m.sessionView
			if view.on {
				layoutX, layoutY = view.clip.Min.X, view.clip.Min.Y
				layoutW, layoutH = view.clip.Dx(), view.clip.Dy()
			}
			n := 0
			for _, w := range m.Windows {
				visible := w.Workspace == m.CurrentWorkspace && !w.Minimized
				scrollbackLen := m.placementScrollbackLen[w.ID]
				announcedW, announcedH := w.AnnouncedSize()
				if announcedW <= 0 || announcedH <= 0 {
					announcedW, announcedH = w.ContentWidth(), w.ContentHeight()
				}
				backing[n] = WindowPositionInfo{
					WindowX:            w.X + view.dx,
					WindowY:            w.Y + view.dy,
					ContentOffsetX:     w.BorderOffset(),
					ContentOffsetY:     w.BorderOffset(),
					Width:              w.Width,
					Height:             w.Height,
					ContentWidth:       announcedW,
					ContentHeight:      announcedH,
					Visible:            visible,
					ScrollbackLen:      scrollbackLen,
					ScrollOffset:       w.ScrollbackOffset,
					IsBeingManipulated: w.IsBeingManipulated,
					WindowZ:            HitZ(w),
					IsAltScreen:        w.IsAltScreen(),
					ScreenWidth:        screenWidth,
					ScreenHeight:       screenHeight,
					LayoutX:            layoutX,
					LayoutY:            layoutY,
					LayoutW:            layoutW,
					LayoutH:            layoutH,
				}
				m.kittyPosMap[w.ID] = &backing[n]
				n++
			}
			return m.kittyPosMap
		})
	}

	data := m.KittyPassthrough.FlushPending()
	if len(data) == 0 {
		return nil
	}
	preview := string(data)
	if len(preview) > 200 {
		preview = preview[:200]
	}
	kittyPassthroughLog("GetKittyGraphicsCmd: flushing %d bytes, preview=%q", len(data), preview)
	m.KittyPassthrough.WriteToHost(data)
	return nil
}

func sameColor(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}
