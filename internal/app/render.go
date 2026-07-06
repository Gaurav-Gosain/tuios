package app

import (
	"image/color"
	"os"

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

	topMargin := m.GetTopMargin()
	viewportWidth := m.GetRenderWidth()
	viewportHeight := m.GetUsableHeight()

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

		isVisible := window.X+window.Width >= -margin &&
			window.X <= viewportWidth+margin &&
			window.Y+window.Height >= -margin &&
			window.Y <= viewportHeight+topMargin+margin

		if !isVisible {
			continue
		}

		isFullyVisible := window.X >= 0 && window.Y >= topMargin &&
			window.X+window.Width <= viewportWidth &&
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
			layers = append(layers, window.CachedLayer)
			// Scrollbar layer (always fresh, not cached). Alt-screen apps (btop,
			// vim) have no scrollback, so drawing a scrollback thumb over them
			// only flickers as their content redraws.
			if !window.Tiled && !window.IsAltScreen() && window.Terminal != nil && window.Terminal.ScrollbackLen() > 0 {
				if sbLayer := renderScrollbarLayer(window, borderColorObj, zIndex+1); sbLayer != nil {
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
			layers = append(layers, window.CachedLayer)
			if !window.Tiled && !window.IsAltScreen() && window.Terminal.ScrollbackLen() > 0 {
				if sbLayer := renderScrollbarLayer(window, borderColorObj, zIndex+1); sbLayer != nil {
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
			layers = append(layers, window.CachedLayer)
			continue
		}

		isTiledBorderless := window.Tiled && (!window.Zoomed || config.SharedBorders)
		boxContent := m.renderWindowBox(window, i, isFocused, borderColorObj)

		clippedContent, finalX, finalY := clipWindowContent(
			boxContent,
			window.X, window.Y,
			viewportWidth, viewportHeight+topMargin,
		)

		window.CachedLayer = lipgloss.NewLayer(clippedContent).X(finalX).Y(finalY).Z(zIndex).ID(window.ID)
		layers = append(layers, window.CachedLayer)

		// Scrollbar layer (always fresh, not cached). See the alt-screen note above.
		if !isTiledBorderless && !window.IsAltScreen() && window.Terminal != nil && window.Terminal.ScrollbackLen() > 0 {
			if sbLayer := renderScrollbarLayer(window, borderColorObj, zIndex+1); sbLayer != nil {
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
		overlays := m.renderOverlays()
		layers = append(layers, overlays...)

		if config.DockbarPosition != "hidden" {
			dockLayer := m.renderDock()
			layers = append(layers, dockLayer)
		}
	}

	canvas.Compose(lipgloss.NewCompositor(layers...))

	return canvas
}

// renderWindowBox renders a window's content, wrapped in its border unless the
// window is borderless. Shared by the compositor path and the fullscreen fast
// path so both produce identical output.
func (m *OS) renderWindowBox(window *terminal.Window, index int, isFocused bool, borderColorObj color.Color) string {
	content := m.renderTerminal(window, isFocused, m.Mode == TerminalMode)
	if window.Tiled && (!window.Zoomed || config.SharedBorders) {
		return content
	}
	box := lipgloss.NewStyle().
		Align(lipgloss.Left).
		AlignVertical(lipgloss.Top).
		Border(getBorder()).
		BorderTop(false)
	isRenaming := m.RenamingWindow && index == m.FocusedWindow
	return addToBorder(
		box.Width(window.Width).
			Height(window.Height-1).
			BorderForeground(borderColorObj).
			Render(content),
		borderColorObj,
		window,
		isRenaming,
		m.RenameBuffer,
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
	if len(m.Animations) > 0 || m.RenamingWindow {
		return nil, false
	}
	if m.ShowHelp || m.ShowCommandPalette || m.ShowSessionSwitcher || m.ShowLayoutPicker ||
		m.ShowQuitConfirm || m.ShowScrollbackBrowser || m.ShowLogs || m.ShowCacheStats ||
		m.ShowAggregateView || m.ShowTapeManager || m.PrefixActive {
		return nil, false
	}
	if (config.ShowClock && !config.HideClock) || (m.TapeRecorder != nil && m.TapeRecorder.IsRecording()) {
		return nil, false
	}
	if config.SharedBorders && m.AutoTiling && !m.UseScrollingLayout {
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
		content := m.composeFrame()
		m.cachedViewContent = content
		view.SetContent(content)
	}

	view.AltScreen = true

	// Dynamically select mouse tracking mode based on the child app's actual needs:
	// - Window management mode: AllMotion for hover effects (dock, UI)
	// - Terminal mode + child requested mode 1003 (any-event): AllMotion
	// - Terminal mode + child requested mode 1002 (button-event): CellMotion
	// - Terminal mode + child requested mode 1000/1001 (click only): CellMotion
	// - Terminal mode + no mouse mode (kakoune default, nano): CellMotion
	//
	// Using AllMotion for apps that only need click tracking (mode 1000) causes
	// a flood of motion events that get forwarded as phantom keypresses (#78).
	if m.Mode == TerminalMode {
		fw := m.GetFocusedWindow()
		useAllMotion := false
		if fw != nil && fw.Terminal != nil {
			useAllMotion = fw.Terminal.HasAllMotionMode()
		}
		if useAllMotion {
			view.MouseMode = tea.MouseModeAllMotion
		} else {
			view.MouseMode = tea.MouseModeCellMotion
		}
	} else {
		view.MouseMode = tea.MouseModeAllMotion
	}

	view.ReportFocus = true
	view.DisableBracketedPasteMode = false
	view.Cursor = m.getRealCursor()

	// Flush graphics AFTER setting view content. bubbletea will render the
	// text first, then we write graphics. This keeps them in the same frame
	// and prevents tearing between text and graphics updates.
	if !m.renderSkipped {
		// Hide images ONLY during full-screen overlays (help, palette, etc.).
		// Copy-mode scroll is NOT a reason to hide  - RefreshAllPlacements uses
		// the window's scrollback offset to reposition images so they scroll
		// naturally with the terminal content.
		hasOverlay := m.ShowHelp || m.ShowCommandPalette || m.ShowSessionSwitcher ||
			m.ShowLayoutPicker || m.ShowQuitConfirm || m.ShowScrollbackBrowser ||
			m.ShowLogs || m.ShowCacheStats || m.ShowAggregateView
		if hasOverlay {
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

	return view
}

func (m *OS) GetKittyGraphicsCmd() tea.Cmd {
	if m.KittyPassthrough == nil {
		return nil
	}

	// Always refresh placements if there are any - this handles window movement
	if m.KittyPassthrough.HasPlacements() {
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
				if w.Workspace == m.CurrentWorkspace && !w.Minimized {
					scrollbackLen := 0
					if w.Terminal != nil {
						scrollbackLen = w.Terminal.ScrollbackLen()
					}
					backing[n] = WindowPositionInfo{
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
					m.kittyPosMap[w.ID] = &backing[n]
					n++
				}
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
		m.SixelPassthrough.RefreshAllPlacements(func(windowID string) *WindowPositionInfo {
			w := m.sixelWinIndex[windowID]
			if w == nil {
				return nil
			}
			scrollbackLen := 0
			if w.Terminal != nil {
				scrollbackLen = w.Terminal.ScrollbackLen()
			}
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
