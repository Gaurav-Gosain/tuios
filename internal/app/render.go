package app

import (
	"image/color"
	"os"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/pool"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

func (m *OS) GetCanvas(render bool) *lipgloss.Canvas {
	canvas := lipgloss.NewCanvas()

	layersPtr := pool.GetLayerSlice()
	layers := (*layersPtr)[:0]
	defer pool.PutLayerSlice(layersPtr)

	topMargin := m.GetTopMargin()
	viewportWidth := m.GetRenderWidth()
	viewportHeight := m.GetUsableHeight()

	box := lipgloss.NewStyle().
		Align(lipgloss.Left).
		AlignVertical(lipgloss.Top).
		Foreground(lipgloss.Color("#FFFFFF")).
		Border(getBorder()).
		BorderTop(false)

	for i := range m.Windows {
		window := m.Windows[i]

		if window.Workspace != m.CurrentWorkspace {
			continue
		}

		isAnimating := false
		// Only check animations if there are any active
		if len(m.Animations) > 0 {
			for _, anim := range m.Animations {
				if anim.Window == m.Windows[i] && !anim.Complete {
					isAnimating = true
					break
				}
			}
		}

		if window.Minimized && !isAnimating {
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
		var borderColorObj color.Color
		if isFocused {
			if m.Mode == TerminalMode {
				borderColorObj = theme.BorderFocusedTerminal()
			} else {
				borderColorObj = theme.BorderFocusedWindow()
			}
		} else {
			borderColorObj = theme.BorderUnfocused()
		}

		if window.CachedLayer != nil && !window.Dirty && !window.ContentDirty && !window.PositionDirty {
			layers = append(layers, window.CachedLayer)
			continue
		}

		needsRedraw := window.CachedLayer == nil ||
			window.Dirty || window.ContentDirty || window.PositionDirty ||
			window.CachedLayer.GetX() != window.X ||
			window.CachedLayer.GetY() != window.Y ||
			window.CachedLayer.GetZ() != window.Z

		if !needsRedraw || (!isFocused && !isFullyVisible && !window.ContentDirty && !window.IsBeingManipulated && window.CachedLayer != nil) {
			layers = append(layers, window.CachedLayer)
			continue
		}

		content := m.renderTerminal(window, isFocused, m.Mode == TerminalMode)

		isRenaming := m.RenamingWindow && i == m.FocusedWindow

		boxContent := addToBorder(
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

		zIndex := window.Z
		if isAnimating {
			zIndex = config.ZIndexAnimating
		}

		clippedContent, finalX, finalY := clipWindowContent(
			boxContent,
			window.X, window.Y,
			viewportWidth, viewportHeight+topMargin,
		)

		window.CachedLayer = lipgloss.NewLayer(clippedContent).X(finalX).Y(finalY).Z(zIndex).ID(window.ID)
		layers = append(layers, window.CachedLayer)

		window.ClearDirtyFlags()
	}

	if render {
		overlays := m.renderOverlays()
		layers = append(layers, overlays...)

		if config.DockbarPosition != "hidden" {
			dockLayer := m.renderDock()
			layers = append(layers, dockLayer)
		}
	}

	canvas.AddLayers(layers...)
	return canvas
}

func (m *OS) View() tea.View {
	var view tea.View

	content := lipgloss.Sprint(m.GetCanvas(true).Render())

	view.SetContent(content)

	view.AltScreen = true
	view.MouseMode = tea.MouseModeAllMotion
	view.ReportFocus = true
	view.DisableBracketedPasteMode = false

	return view
}

func (m *OS) GetKittyGraphicsCmd() tea.Cmd {
	if m.KittyPassthrough == nil {
		return nil
	}

	// Always refresh placements if there are any - this handles window movement
	if m.KittyPassthrough.HasPlacements() {
		m.KittyPassthrough.RefreshAllPlacements(func() map[string]*WindowPositionInfo {
			result := make(map[string]*WindowPositionInfo)
			for _, w := range m.Windows {
				if w.Workspace == m.CurrentWorkspace && !w.Minimized {
					scrollbackLen := 0
					if w.Terminal != nil {
						scrollbackLen = w.Terminal.ScrollbackLen()
					}
					result[w.ID] = &WindowPositionInfo{
						WindowX:            w.X,
						WindowY:            w.Y,
						ContentOffsetX:     1,
						ContentOffsetY:     1,
						Width:              w.Width,
						Height:             w.Height,
						Visible:            true,
						ScrollbackLen:      scrollbackLen,
						ScrollOffset:       w.ScrollbackOffset,
						IsBeingManipulated: w.IsBeingManipulated,
						WindowZ:            w.Z,
						IsAltScreen:        w.IsAltScreen,
					}
				}
			}
			return result
		})
	}

	// Always flush pending output - this includes delete commands even after placements are removed
	data := m.KittyPassthrough.FlushPending()
	if len(data) == 0 {
		return nil
	}
	kittyPassthroughLog("GetKittyGraphicsCmd: flushing %d bytes", len(data))
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return nil
	}
	_, _ = tty.Write(data)
	_ = tty.Close()
	return nil
}

func (m *OS) GetSixelGraphicsCmd() tea.Cmd {
	if m.SixelPassthrough == nil {
		return nil
	}

	// Refresh placements for all windows
	if m.SixelPassthrough.PlacementCount() > 0 {
		m.SixelPassthrough.RefreshAllPlacements(func(windowID string) *WindowPositionInfo {
			for _, w := range m.Windows {
				if w.ID == windowID && w.Workspace == m.CurrentWorkspace && !w.Minimized {
					scrollbackLen := 0
					if w.Terminal != nil {
						scrollbackLen = w.Terminal.ScrollbackLen()
					}
					return &WindowPositionInfo{
						WindowX:            w.X,
						WindowY:            w.Y,
						ContentOffsetX:     1,
						ContentOffsetY:     1,
						Width:              w.Width,
						Height:             w.Height,
						Visible:            true,
						ScrollbackLen:      scrollbackLen,
						ScrollOffset:       w.ScrollbackOffset,
						IsBeingManipulated: w.IsBeingManipulated,
						WindowZ:            w.Z,
						IsAltScreen:        w.IsAltScreen,
					}
				}
			}
			return nil
		})
	}

	// Get pending sixel output and write to /dev/tty (like Kitty passthrough)
	data := m.SixelPassthrough.FlushPending()
	if len(data) == 0 {
		return nil
	}
	sixelPassthroughLog("GetSixelGraphicsCmd: flushing %d bytes", len(data))
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return nil
	}
	_, _ = tty.Write(data)
	_ = tty.Close()
	return nil
}
