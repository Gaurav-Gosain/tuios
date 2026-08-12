package app

import (
	"image/color"
	"os"
	"runtime/debug"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/pool"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

func (m *OS) GetCanvas(render bool) *lipgloss.Canvas {
	// Reuse the canvas across frames. Allocating a fresh one each frame was the
	// single largest source of allocations (a full-screen cell buffer per frame).
	// Resize is a no-op when the dimensions are unchanged; Clear resets the cells
	// in place. Safe because GetCanvas is only called from View on one goroutine.
	rw, rh := m.GetRenderWidth(), m.GetRenderHeight()
	if m.renderCanvas == nil {
		m.renderCanvas = lipgloss.NewCanvas(rw, rh)
	} else {
		m.renderCanvas.Resize(rw, rh)
		m.renderCanvas.Clear()
	}
	canvas := m.renderCanvas

	layersPtr := pool.GetLayerSlice()
	layers := (*layersPtr)[:0]
	defer pool.PutLayerSlice(layersPtr)

	// Scrollbar hit rects are recorded as the bars are drawn below, so the
	// previous frame's go first: a bar that has returned to the live tail or
	// slid under the rail must stop being grabbable with it.
	m.resetScrollbarRects()

	topMargin := m.GetTopMargin()
	viewportHeight := m.GetUsableHeight()
	// The sidebar reserves a horizontal band, so the content region a pane may
	// occupy runs from leftMargin to rightClip rather than 0 to the full render
	// width. leftClip/rightClip are the absolute screen columns the content is
	// clipped to; the sidebar layer (composed below at a high Z) paints over the
	// reserved band, so a pane that overruns into it is covered.
	leftMargin := m.GetLeftMargin()
	rightClip := leftMargin + m.GetContentWidth()

	// Hoist loop-invariants out of the per-window loop below.
	// The focused window and its zoom state are the same for every iteration.
	focusedWindow := m.GetFocusedWindow()
	focusedZoomed := focusedWindow != nil && focusedWindow.Zoomed

	// Precompute the set of windows with an active (incomplete) animation once
	// per frame instead of rescanning m.Animations for every window, which was
	// O(windows*animations).
	var animatingWindows map[*terminal.Window]struct{}
	if len(m.Animations) > 0 {
		animatingWindows = make(map[*terminal.Window]struct{}, len(m.Animations))
		for _, anim := range m.Animations {
			if !anim.Complete {
				animatingWindows[anim.Window] = struct{}{}
			}
		}
	}

	for i := range m.Windows {
		window := m.Windows[i]

		if window.Workspace != m.CurrentWorkspace {
			continue
		}

		_, isAnimating := animatingWindows[window]

		if window.Minimized && !isAnimating {
			continue
		}

		// When any window is zoomed, only render the zoomed window
		if focusedZoomed && window != focusedWindow {
			continue
		}

		margin := 5
		if isAnimating {
			margin = 20
		}

		isVisible := window.X+window.Width >= leftMargin-margin &&
			window.X <= rightClip+margin &&
			window.Y+window.Height >= -margin &&
			window.Y <= viewportHeight+topMargin+margin

		if !isVisible {
			continue
		}

		isFullyVisible := window.X >= leftMargin && window.Y >= topMargin &&
			window.X+window.Width <= rightClip &&
			window.Y+window.Height <= viewportHeight+topMargin

		isFocused := m.FocusedWindow == i && m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows)
		isMultifocused := len(m.MultifocusSet) > 0 && m.MultifocusSet[window.ID]
		var borderColorObj color.Color
		if isFocused {
			if m.Mode == TerminalMode {
				borderColorObj = theme.BorderFocusedTerminal()
			} else {
				borderColorObj = theme.BorderFocusedWindow()
			}
		} else if isMultifocused {
			// Multifocused windows get a distinct border color (yellow/orange)
			borderColorObj = lipgloss.Color("3")
		} else {
			borderColorObj = theme.BorderUnfocused()
		}

		// Effective z-index, computed once so the cached and freshly-rendered
		// paths place the window and its scrollbar at the same depth. Computing
		// it only in the fresh path left the cached path's scrollbar at a
		// different depth, so it flickered as the window toggled dirty/clean.
		zIndex := window.Z
		if window.IsFloating {
			zIndex = config.ZIndexSeparators + 1 + window.Z
		}
		if (isAnimating || window.IsBeingManipulated) && !window.Tiled {
			zIndex = config.ZIndexAnimating
		}

		if window.CachedLayer != nil && !window.Dirty && !window.ContentDirty && !window.PositionDirty {
			if renderTraceEnabled {
				traceLayerHold(window, isFocused, "clean")
			}
			layers = append(layers, window.CachedLayer)
			// Scrollbar layer (always fresh, not cached). Alt-screen apps (btop,
			// vim) have no scrollback, so drawing a scrollback thumb over them
			// only flickers as their content redraws.
			if windowNeedsScrollbar(window) {
				if sbLayer := m.renderScrollbarLayer(window, rightClip, zIndex+1, isFocused); sbLayer != nil {
					layers = append(layers, sbLayer)
				}
			}
			continue
		}

		// Synchronized output (DEC 2026): the guest has begun a frame and does
		// not want it shown until it closes the update. Hold the last complete
		// frame instead of rendering the half-updated buffer, which is what made
		// apps like btop flicker. ContentDirty stays set, so the frame that
		// arrives when the guest closes sync renders the finished screen. Only
		// hold when nothing but content changed (position/z match the cache).
		if window.Terminal != nil && window.Terminal.IsSyncActive() &&
			window.CachedLayer != nil &&
			window.CachedLayer.GetX() == window.X &&
			window.CachedLayer.GetY() == window.Y &&
			window.CachedLayer.GetZ() == zIndex {
			if renderTraceEnabled {
				traceLayerHold(window, isFocused, "sync-2026")
			}
			layers = append(layers, window.CachedLayer)
			if windowNeedsScrollbar(window) {
				if sbLayer := m.renderScrollbarLayer(window, rightClip, zIndex+1, isFocused); sbLayer != nil {
					layers = append(layers, sbLayer)
				}
			}
			continue
		}

		needsRedraw := window.CachedLayer == nil ||
			window.Dirty || window.ContentDirty || window.PositionDirty ||
			window.CachedLayer.GetX() != window.X ||
			window.CachedLayer.GetY() != window.Y ||
			window.CachedLayer.GetZ() != window.Z

		if !needsRedraw || (!isFocused && !isFullyVisible && !window.ContentDirty && !window.PositionDirty && !window.IsBeingManipulated && window.CachedLayer != nil) {
			// renderTerminal is never entered on this path, so without a line
			// here the trace simply stops for a window that has settled, which
			// reads misleadingly like a missing branch rather than a reused
			// layer.
			if renderTraceEnabled {
				traceLayerHold(window, isFocused, "not-needed")
			}
			layers = append(layers, window.CachedLayer)
			continue
		}

		boxContent := m.renderWindowBox(window, i, isFocused, borderColorObj)

		clippedContent, finalX, finalY := clipWindowContent(
			boxContent,
			window.X, window.Y,
			rightClip, viewportHeight+topMargin,
		)

		if renderTraceEnabled {
			traceLayerBuild(window, isFocused, boxContent, clippedContent,
				window.X, window.Y, finalX, finalY, zIndex, rightClip, viewportHeight+topMargin)
		}

		window.CachedLayer = lipgloss.NewLayer(clippedContent).X(finalX).Y(finalY).Z(zIndex).ID(window.ID)
		layers = append(layers, window.CachedLayer)

		// Scrollbar layer (always fresh, not cached). See the alt-screen note above.
		if windowNeedsScrollbar(window) {
			if sbLayer := m.renderScrollbarLayer(window, rightClip, zIndex+1, isFocused); sbLayer != nil {
				layers = append(layers, sbLayer)
			}
		}

		window.ClearDirtyFlags()
	}

	// Add shared border separator overlay when active (not in scrolling mode)
	if config.SharedBorders && m.AutoTiling && !m.UseScrollingLayout {
		if sepLayers := m.renderSeparatorOverlay(); len(sepLayers) > 0 {
			layers = append(layers, sepLayers...)
		}
	}

	if render {
		// The sidebar sits below the floating overlays but, like the dock, is a
		// reserved-region layer rather than an overlay panel. Compose it before
		// the overlays so a palette or settings panel still draws on top of it.
		if sidebarLayer := m.renderSidebar(); sidebarLayer != nil {
			layers = append(layers, sidebarLayer)
		}
		overlays := m.renderOverlays()
		layers = append(layers, overlays...)

		if config.DockbarPosition != "hidden" {
			dockLayer := m.renderDock()
			layers = append(layers, dockLayer)
		}

		// The hover label rides above the panes and the dock both. It is composed
		// last so it reads the rectangles this pass recorded rather than the
		// previous frame's: the rail anchors it by row and the dock's session
		// controls anchor it by column, and both are drawn above.
		if tip := m.renderTooltip(); tip != nil {
			layers = append(layers, tip)
		}
	} else {
		// Off the render path (e.g. state snapshots) nothing draws the sidebar,
		// so last frame's hit geometry must not linger and mis-route a click.
		m.SidebarHits = m.SidebarHits[:0]
	}

	canvas.Compose(lipgloss.NewCompositor(layers...))

	return canvas
}

// fitToContentBox trims a rendered pane body to the window's content
// rectangle.
//
// A pane's frame is its rectangle, and nothing it draws may land outside it.
// renderTerminal does not guarantee that on its own: the unfocused fast path
// returns the emulator's own Render(), sized by the emulator rather than by the
// window, and a window's rectangle can change without the emulator following it
// in the same frame. A snap animation is the ordinary way that happens - it
// interpolates X, Y, Width and Height every tick and deliberately leaves the VT
// alone until the transition ends, so mid-animation the body is still the size
// the pane used to be.
//
// lipgloss's Width and Height pad but never truncate, so an oversized body used
// to push the box past the pane: the bottom border landed a row or more below
// the pane's own bottom edge, over the neighbouring pane or in the status bar.
// Trimming here keeps the box a function of the window rectangle alone.
//
// The common case costs one lipgloss.Size, which is a single pass over a string
// the caller has already built, and changes nothing.
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

// rendersBorderless reports whether window is drawn with no border box of its
// own, so its rectangle is guest output from edge to edge and nothing may be
// painted on its perimeter.
//
// It is bare window.Tiled because that is what the geometry already says:
// ContentWidth, ContentHeight and BorderOffset (internal/terminal) reserve
// border cells on !Tiled alone, and Resize sizes the emulator by the same rule.
// A renderer predicate that disagreed with those would either overflow the
// pane's rectangle or paint over the guest's own columns.
//
// Tiling assigns Tiled from config.SharedBorders, so in practice a borderless
// pane is a tiled pane under shared borders. There the lines between panes are
// a compositor overlay (renderSeparatorOverlay) sitting in the gaps between
// rectangles rather than chrome belonging to either neighbour, which is exactly
// why no pane has a spare column.
//
// Zoom does not change this. A zoomed pane under shared borders is still
// borderless and full-rect; the separator overlay stands down because a zoomed
// pane has no neighbours to be separated from, so it has no border cell either.
// The scrollbar no longer consults this: it draws inside the rectangle, so it
// needs no border cell to exist.
func rendersBorderless(window *terminal.Window) bool {
	return window.Tiled
}

// renderWindowBox renders a window's content, wrapped in its border unless the
// window is borderless. Shared by the compositor path and the fullscreen fast
// path so both produce identical output.
func (m *OS) renderWindowBox(window *terminal.Window, index int, isFocused bool, borderColorObj color.Color) string {
	content := fitToContentBox(
		m.renderTerminal(window, isFocused, m.Mode == TerminalMode),
		window.ContentWidth(), window.ContentHeight(),
	)
	if rendersBorderless(window) {
		return content
	}
	box := lipgloss.NewStyle().
		Align(lipgloss.Left).
		AlignVertical(lipgloss.Top).
		Border(getBorder()).
		BorderTop(false)
	// The title bar keeps showing the name the window still has while a rename
	// is in flight: the dialog owns the new one, so the two together are the
	// old-vs-new comparison.
	return addToBorder(
		box.Width(window.Width).
			Height(window.Height-1).
			BorderForeground(borderColorObj).
			Render(content),
		borderColorObj,
		window,
		m.workspacePosition(window),
		m.AutoTiling,
	)
}

// fastPathDisabled turns the fullscreen fast path off (TUIOS_NO_FASTPATH=1) so it
// can be compared against the compositor path.
var fastPathDisabled = os.Getenv("TUIOS_NO_FASTPATH") == "1"

// composeFrame renders the full frame, using the fullscreen fast path when it is
// eligible and falling back to the compositor otherwise.
func (m *OS) composeFrame() string {
	if window, ok := m.fullscreenFastWindow(); ok && !fastPathDisabled {
		return m.buildFullscreenFrame(window)
	}
	return lipgloss.Sprint(m.GetCanvas(true).Render())
}

// fullscreenFastWindow returns the single window that fills the content area with
// nothing overlapping it, or ok=false when the compositor is required: multiple
// visible windows, any overlay, separators, graphics, or active manipulation or
// animation. Pure: it does not mutate render state.
func (m *OS) fullscreenFastWindow() (*terminal.Window, bool) {
	if len(m.Animations) > 0 || m.Renaming() {
		return nil, false
	}
	if m.ShowHelp || m.ShowCommandPalette || m.ShowSessionSwitcher || m.ShowWorkspaceSwitcher || m.ShowLayoutPicker ||
		m.ShowQuitMenu || m.ShowScrollbackBrowser || m.ShowLogs || m.ShowCacheStats ||
		m.ShowAggregateView || m.ShowTapeManager || m.ShowTapeReview || m.ShowSettings || m.ShowThemePicker ||
		m.ShowAccentPicker || m.PrefixActive || m.ContextMenu != nil {
		return nil, false
	}
	if (config.ShowClock && !config.HideClock) || (m.TapeRecorder != nil && m.TapeRecorder.IsRecording()) {
		return nil, false
	}
	// The showkeys keycast is a compositor overlay (renderOverlays), which the
	// fast path skips entirely. A lone fullscreen window is the common terminal-mode
	// case, so without this a keypress captured into RecentKeys would never be drawn
	// until an unrelated redraw disqualified the fast path, which is the keycast lag.
	// Only fall back while there are keys to show; an empty history draws nothing, so
	// the fast path stays eligible when the overlay is idle.
	if m.ShowKeys && len(m.RecentKeys) > 0 {
		return nil, false
	}
	if config.SharedBorders && m.AutoTiling && !m.UseScrollingLayout {
		return nil, false
	}
	// The sidebar is a reserved-region layer the fast path does not compose. When
	// it reserves any columns a lone window no longer fills the screen, so fall
	// back to the compositor (which draws the sidebar and clips the pane to the
	// content region). Cheapest correct v1; can be optimised later.
	if m.GetSidebarWidth() > 0 {
		return nil, false
	}
	if m.KittyPassthrough != nil && m.KittyPassthrough.HasPlacements() {
		return nil, false
	}
	if m.SixelPassthrough != nil && m.SixelPassthrough.PlacementCount() > 0 {
		return nil, false
	}

	visible := m.GetVisibleWindows()
	if len(visible) != 1 {
		return nil, false
	}
	window := visible[0]
	if window.IsBeingManipulated || window.Minimizing {
		return nil, false
	}
	// The synchronized-output hold (DEC 2026) that suppresses btop flicker lives
	// only in the compositor path (GetCanvas). A sync-active guest must fall back
	// there, otherwise the fast path re-renders the half-updated buffer mid-frame
	// and the flicker returns for a zoomed window.
	if window.Terminal != nil && window.Terminal.IsSyncActive() {
		return nil, false
	}
	// A scrolled-back pane shows a scrollbar thumb, which only the compositor
	// draws as a separate layer. Fall back so a lone tiled/fullscreen window
	// does not silently lose it. At the live tail there is no thumb, so a deep
	// scrollback no longer costs the fast path.
	if windowNeedsScrollbar(window) {
		return nil, false
	}
	rw, topMargin, usableH := m.GetRenderWidth(), m.GetTopMargin(), m.GetUsableHeight()
	if window.X != 0 || window.Y != topMargin || window.Width != rw || window.Height != usableH {
		return nil, false
	}
	return window, true
}

// buildFullscreenFrame renders the window box and stacks it with the dock,
// skipping the compositor. Mutates render state (renders the window, clears its
// dirty flags), so it must only be called after eligibility is confirmed.
func (m *OS) buildFullscreenFrame(window *terminal.Window) string {
	// This path is only taken when no pane wants a bar, so nothing on the frame
	// it builds is grabbable.
	m.resetScrollbarRects()

	isFocused := m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) && m.Windows[m.FocusedWindow] == window
	var borderColorObj color.Color
	switch {
	case isFocused && m.Mode == TerminalMode:
		borderColorObj = theme.BorderFocusedTerminal()
	case isFocused:
		borderColorObj = theme.BorderFocusedWindow()
	default:
		borderColorObj = theme.BorderUnfocused()
	}

	windowIndex := -1
	for i := range m.Windows {
		if m.Windows[i] == window {
			windowIndex = i
			break
		}
	}
	boxContent := m.renderWindowBox(window, windowIndex, isFocused, borderColorObj)
	window.ClearDirtyFlags()
	// The fast path does not build a CachedLayer, so the one still held here was
	// captured the last time the compositor ran (potentially seconds ago). Nil it
	// so that when the fast path is later disqualified (tmux prefix, an overlay),
	// the compositor renders a fresh layer instead of appending a stale one and
	// rewinding the window a frame. Keep CachedContent for the render fast path.
	window.CachedLayer = nil

	if config.DockbarPosition == "hidden" {
		return boxContent
	}
	dockStr, _ := m.renderDockString()
	if config.DockbarPosition == "top" {
		return dockStr + "\n" + boxContent
	}
	return boxContent + "\n" + dockStr
}

func (m *OS) View() tea.View {
	var view tea.View

	// Fast path: return cached content when frame-skip determined nothing changed.
	// This avoids the expensive GetCanvas → ultraviolet render pipeline on idle ticks.
	if m.renderSkipped && m.cachedViewContent != "" {
		view.SetContent(m.cachedViewContent)
	} else {
		// A resize drag applies the new geometry to the windows immediately but
		// defers the matching BSP ratio sync, because the sync is whole-tree
		// work and motion events outnumber frames. The separator overlay reads
		// its positions from the tree and its highlight from live window
		// geometry, so composing while the two disagree draws the divider at the
		// position the drag has already left, in the unfocused color because it
		// is no longer on the focused pane's perimeter. Flushing here rather
		// than on the drag's own code path covers every way a frame can be
		// composed mid-drag, including PTY output, which arrives on a path that
		// knows nothing about the drag.
		m.FlushPendingBSPSync()
		content := m.composeFrame()
		m.cachedViewContent = content
		view.SetContent(content)
	}

	view.AltScreen = true

	// All-motion tracking, always. Every hover affordance tuios draws (the rail
	// rows and its footer controls, overlay rows, context menu rows) and
	// focus-follows-mouse are driven by motion with no button held, and only mode
	// 1003 makes the host report that. Asking for button-event tracking whenever
	// a pane held focus is what made hover look like it randomly stopped: it was
	// dead for as long as the user was in terminal mode, which is where they
	// spend their time, and alive again the moment they left it.
	//
	// This governs what the host reports to tuios. Forwarding to the guest is a
	// separate decision, filtered there against the guest's own mouse mode so an
	// app that asked for less than 1003 still sees only what it asked for (#78);
	// see guestWantsMotion.
	view.MouseMode = tea.MouseModeAllMotion

	view.ReportFocus = true
	view.DisableBracketedPasteMode = false
	view.Cursor = m.getRealCursor()

	// Flush graphics AFTER setting view content. bubbletea will render the
	// text first, then we write graphics. This keeps them in the same frame
	// and prevents tearing between text and graphics updates.
	if !m.renderSkipped {
		m.flushGraphicsForView()
	}

	return view
}

// flushGraphicsForView performs View()'s graphics side effects (kitty/sixel
// placement refresh, pending flush, host writes, and text sizing).
//
// It runs inside its own panic barrier. View() is called from bubbletea's
// event loop, which recovers panics only at the top of Program.Run: a panic
// escaping here returns from Run, and over SSH that ends the wish session.
// The graphics path re-encodes guest-controlled, high-rate image frames
// (kitty shm/file transports over SSH), so a single malformed or unhandled
// frame must degrade to a dropped frame, never a dead session. Update() has
// an equivalent per-event barrier; this is its render-side twin. (The
// teardown seen under a kitty shm flood over SSH was not a panic on this
// path; it was unserialized concurrent writes to the SSH channel, fixed by
// serialWriter in the server package. This barrier stays as containment for
// real panics.)
func (m *OS) flushGraphicsForView() {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			path := WriteCrashLog(r, stack)
			m.LogError("recovered panic in graphics flush: %v (crash log: %s)\n%s", r, path, stack)
		}
	}()

	// Republish every window's geometry snapshot for the PTY-reader
	// passthrough callbacks. This runs on the goroutine that owns the layout
	// fields, so the reads are safe, and it keeps the snapshots at most one
	// frame stale.
	for _, w := range m.Windows {
		if w != nil {
			w.PublishGeometry()
		}
	}

	// Hide images ONLY during full-screen overlays (help, palette, etc.) and
	// for the length of a resize gesture. Copy-mode scroll is NOT a reason to
	// hide  - RefreshAllPlacements uses the window's scrollback offset to
	// reposition images so they scroll naturally with the terminal content.
	//
	// A resize hides for the same reason an overlay does: an image is drawn in
	// host cells, and while the layout is moving under it the guest has not
	// been told the new size yet, so it smears across the panes for the length
	// of the drag. Hiding keeps the image data resident, so the gesture ending
	// puts it back with no round trip to whatever drew it.
	hideImages := m.Resizing || m.ShowHelp || m.ShowCommandPalette || m.ShowSessionSwitcher ||
		m.ShowWorkspaceSwitcher || m.ShowLayoutPicker || m.ShowQuitMenu || m.ShowScrollbackBrowser ||
		m.ShowLogs || m.ShowCacheStats || m.ShowAggregateView ||
		m.ShowSettings || m.ShowThemePicker || m.ShowAccentPicker || m.ShowTapeManager || m.ShowTapeReview
	if m.KittyPassthrough != nil {
		// Self-placed remote video images are hidden/dropped here, not by
		// HideAllPlacements (they are not in `placements`).
		m.KittyPassthrough.SetOverlayActive(hideImages)
	}
	if hideImages {
		if m.KittyPassthrough != nil && m.KittyPassthrough.HasPlacements() {
			m.KittyPassthrough.HideAllPlacements()
		}
		if m.SixelPassthrough != nil && m.SixelPassthrough.PlacementCount() > 0 {
			m.SixelPassthrough.HideAllPlacements()
			// Flush the clear commands
			data := m.SixelPassthrough.FlushPending()
			if len(data) > 0 {
				_, _ = os.Stdout.Write(data)
			}
		}
	} else {
		m.GetKittyGraphicsCmd()
		m.GetSixelGraphicsCmd()
		m.RefreshTextSizing()
		m.FlushTextSizing()
	}
}

// snapshotPlacementScrollbackLens records every window's scrollback length
// while no passthrough lock is held, for the placement refresh callbacks to
// read afterwards.
//
// The callbacks run inside KittyPassthrough.RefreshAllPlacements and
// SixelPassthrough.RefreshAllPlacements, which hold kp.mu and sp.mu. Calling
// ScrollbackLenSync there would take a window's ioMu under those locks. The PTY
// reader takes the locks in the opposite order: it holds ioMu across
// Terminal.Write, which dispatches the kitty and sixel passthrough callbacks,
// and those take kp.mu and sp.mu. Two goroutines acquiring the same pair in
// opposite orders deadlock, so the ioMu side is lifted out to here.
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

	// Always refresh placements if there are any - this handles window movement
	if m.KittyPassthrough.HasPlacements() {
		m.snapshotPlacementScrollbackLens()
		m.KittyPassthrough.RefreshAllPlacements(func() map[string]*WindowPositionInfo {
			// Reuse a preallocated map and backing slice across frames. The
			// returned map and its values are only consumed within
			// RefreshAllPlacements, so reusing them avoids a fresh map plus a
			// heap *WindowPositionInfo per window every frame.
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
			n := 0
			for _, w := range m.Windows {
				// Include EVERY window, but mark off-workspace/minimized ones
				// Visible:false. RefreshAllPlacements then HIDES their images
				// (d=i, keeping the bytes in the host store) instead of deleting
				// tracking. Omitting them made info==nil, which RefreshAllPlacements
				// treats as "window gone" and permanently destroys the placement,
				// so a minimized icat/chafa image never reappeared on restore.
				// The info==nil delete is now reserved for windows genuinely
				// removed from m.Windows (closed).
				visible := w.Workspace == m.CurrentWorkspace && !w.Minimized
				// Snapshotted above, outside kp.mu; see
				// snapshotPlacementScrollbackLens for why it cannot be read here.
				scrollbackLen := m.placementScrollbackLen[w.ID]
				backing[n] = WindowPositionInfo{
					WindowX:            w.X,
					WindowY:            w.Y,
					ContentOffsetX:     w.BorderOffset(),
					ContentOffsetY:     w.BorderOffset(),
					Width:              w.Width,
					Height:             w.Height,
					Visible:            visible,
					ScrollbackLen:      scrollbackLen,
					ScrollOffset:       w.ScrollbackOffset,
					IsBeingManipulated: w.IsBeingManipulated,
					WindowZ:            w.Z,
					IsAltScreen:        w.IsAltScreen(),
					ScreenWidth:        screenWidth,
					ScreenHeight:       screenHeight,
				}
				m.kittyPosMap[w.ID] = &backing[n]
				n++
			}
			return m.kittyPosMap
		})
	}

	// Always flush pending output - this includes delete commands even after placements are removed
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

func (m *OS) GetSixelGraphicsCmd() tea.Cmd {
	if m.SixelPassthrough == nil {
		return nil
	}

	// Refresh placements for all windows
	if m.SixelPassthrough.PlacementCount() > 0 {
		// Build a window-by-ID index of eligible windows once per frame and
		// reuse it across placements, instead of rescanning m.Windows per
		// placement (which was O(placements*windows)).
		if m.sixelWinIndex == nil {
			m.sixelWinIndex = make(map[string]*terminal.Window, len(m.Windows))
		} else {
			clear(m.sixelWinIndex)
		}
		for _, w := range m.Windows {
			if w.Workspace == m.CurrentWorkspace && !w.Minimized {
				m.sixelWinIndex[w.ID] = w
			}
		}
		screenWidth := m.GetRenderWidth()
		screenHeight := m.GetRenderHeight()
		m.snapshotPlacementScrollbackLens()
		m.SixelPassthrough.RefreshAllPlacements(func(windowID string) *WindowPositionInfo {
			w := m.sixelWinIndex[windowID]
			if w == nil {
				return nil
			}
			// Snapshotted above, outside sp.mu; see
			// snapshotPlacementScrollbackLens for why it cannot be read here.
			scrollbackLen := m.placementScrollbackLen[w.ID]
			// Reuse a single value; the callback's result is consumed before
			// the next call, so a shared value avoids a per-call heap alloc.
			m.sixelPosValue = WindowPositionInfo{
				WindowX:            w.X,
				WindowY:            w.Y,
				ContentOffsetX:     w.BorderOffset(),
				ContentOffsetY:     w.BorderOffset(),
				Width:              w.Width,
				Height:             w.Height,
				Visible:            true,
				ScrollbackLen:      scrollbackLen,
				ScrollOffset:       w.ScrollbackOffset,
				IsBeingManipulated: w.IsBeingManipulated,
				WindowZ:            w.Z,
				IsAltScreen:        w.IsAltScreen(),
				ScreenWidth:        screenWidth,
				ScreenHeight:       screenHeight,
			}
			return &m.sixelPosValue
		})
	}

	// Get pending sixel output and write to stdout (same stream as bubbletea)
	// wrapped in synchronized update sequences to prevent tearing
	data := m.SixelPassthrough.FlushPending()
	if len(data) == 0 {
		return nil
	}
	// Write to stdout with sync wrapping (same approach as kitty graphics)
	_, _ = os.Stdout.Write([]byte("\x1b[?2026h")) // sync begin
	_, _ = os.Stdout.Write(data)
	_, _ = os.Stdout.Write([]byte("\x1b[?2026l")) // sync end
	return nil
}
