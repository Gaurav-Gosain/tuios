package app

import (
	"fmt"
	"image/color"
	"runtime"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/pool"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/charmbracelet/lipgloss/v2"
	"github.com/charmbracelet/x/vt"
)

const (
	// LeftHalfCircle is the Unicode character for a left half circle.
	LeftHalfCircle string = string(rune(0xe0b6))
	// RightHalfCircle is the Unicode character for a right half circle.
	RightHalfCircle string = string(rune(0xe0b4))
	// BorderTopLeft is the Unicode character for the top-left border.
	BorderTopLeft string = string(rune(0x256d))
	// BorderTopRight is the Unicode character for the top-right border.
	BorderTopRight string = string(rune(0x256e))
	// BorderHorizontal is the Unicode character for a horizontal border.
	BorderHorizontal string = string(rune(0x2500))
)

// RightString returns a right-aligned string with decorative borders.
func RightString(str string, width int, color color.Color) string {
	spaces := width - lipgloss.Width(str)
	style := pool.GetStyle()
	defer pool.PutStyle(style)
	fg := style.Foreground(color)

	if spaces < 0 {
		return ""
	}

	return fg.Render(BorderTopLeft+strings.Repeat(BorderHorizontal, spaces)) +
		str +
		fg.Render(BorderTopRight)
}

func makeRounded(content string, color color.Color) string {
	style := pool.GetStyle()
	defer pool.PutStyle(style)
	render := style.Foreground(color).Render
	content = render(LeftHalfCircle) + content + render(RightHalfCircle)
	return content
}

func addToBorder(content string, color color.Color, window *terminal.Window, isRenaming bool, renameBuffer string, isTiling bool) string {
	width := max(
		// Ensure width is never negative
		lipgloss.Width(content)-2, 0)
	buttonStyle := lipgloss.NewStyle().Background(color).Foreground(lipgloss.Color("#000000"))
	cross := buttonStyle.Render(" ⤫ ")
	dash := buttonStyle.Render(" — ")

	// Only show maximize button if not in tiling mode
	var border string
	if isTiling {
		border = makeRounded(lipgloss.JoinHorizontal(lipgloss.Top, dash, cross), color)
	} else {
		square := buttonStyle.Render(" □ ")
		border = makeRounded(lipgloss.JoinHorizontal(lipgloss.Top, dash, square, cross), color)
	}
	centered := RightString(border, width, color)

	// Add bottom border with window name
	bottomBorderStyle := lipgloss.NewStyle().Foreground(color)

	// Get the window name to display (only show custom names)
	windowName := ""
	if window.CustomName != "" {
		windowName = window.CustomName
	}

	// If renaming, show the rename buffer with cursor
	if isRenaming {
		windowName = renameBuffer + "_"
	}

	// Only process name if we have one to show
	var bottomBorder string
	if windowName != "" {
		// Truncate if too long (leave space for pill style)
		maxNameLen := width - 6 // Space for circles and padding
		if maxNameLen > 0 && len(windowName) > maxNameLen {
			if maxNameLen > 3 {
				windowName = windowName[:maxNameLen-3] + "..."
			} else {
				windowName = "..."
			}
		}

		// Create pill-style name badge
		nameStyle := lipgloss.NewStyle().
			Background(color).
			Foreground(lipgloss.Color("#000000"))

		leftCircle := bottomBorderStyle.Render(LeftHalfCircle)
		nameText := nameStyle.Render(" " + windowName + " ")
		rightCircle := bottomBorderStyle.Render(RightHalfCircle)
		nameBadge := leftCircle + nameText + rightCircle

		// Calculate padding for centering the badge
		badgeWidth := lipgloss.Width(nameBadge)
		totalPadding := width - badgeWidth

		// Ensure padding is never negative
		if totalPadding < 0 {
			// If badge is too wide, just use plain border
			bottomBorder = bottomBorderStyle.Render("╰" + strings.Repeat("─", width) + "╯")
		} else {
			leftPadding := totalPadding / 2
			rightPadding := totalPadding - leftPadding

			// Create bottom border with centered name badge
			bottomBorder = bottomBorderStyle.Render("╰"+strings.Repeat("─", leftPadding)) +
				nameBadge +
				bottomBorderStyle.Render(strings.Repeat("─", rightPadding)+"╯")
		}
	} else {
		// Plain bottom border without name
		bottomBorder = bottomBorderStyle.Render("╰" + strings.Repeat("─", width) + "╯")
	}

	// Join top border, content, and bottom border
	result := lipgloss.JoinVertical(lipgloss.Top, centered, content)
	// Replace the last line (original bottom border) with our custom bottom border
	lines := strings.Split(result, "\n")
	if len(lines) > 0 {
		lines[len(lines)-1] = bottomBorder
	}
	return strings.Join(lines, "\n")
}

func (m *OS) renderTerminal(window *terminal.Window, isFocused bool, inTerminalMode bool) string {
	// Smart caching: use cache if window is being manipulated OR if content hasn't changed
	if (window.IsBeingManipulated || !window.ContentDirty) && window.CachedContent != "" {
		return window.CachedContent
	}

	// For non-focused windows with rapidly changing content, use cache more aggressively
	if !isFocused && window.CachedContent != "" && len(window.CachedContent) > 0 {
		// Only update non-focused windows every few frames to reduce CPU usage
		return window.CachedContent
	}

	m.terminalMu.Lock()
	defer m.terminalMu.Unlock()

	if window.Terminal == nil {
		window.CachedContent = "Terminal not initialized"
		return window.CachedContent
	}

	screen := window.Terminal.Screen()
	if screen == nil {
		window.CachedContent = "No screen"
		return window.CachedContent
	}

	// Get cursor position
	cursor := screen.Cursor()
	cursorX := cursor.X
	cursorY := cursor.Y

	// Use string builder pool for efficient string building
	builder := pool.GetStringBuilder()
	defer pool.PutStringBuilder(builder)

	// Pre-allocate capacity for better performance
	estimatedSize := (window.Width - 2) * (window.Height - 2)
	builder.Grow(estimatedSize)

	// Build the terminal output with colors and styling
	maxY := min(window.Height-2, screen.Height())
	maxX := min(window.Width-2, screen.Width())

	// Use optimized rendering for background windows (preserve colors but skip expensive operations)
	useOptimizedRendering := !isFocused && !inTerminalMode

	for y := range maxY {
		if y > 0 {
			builder.WriteRune('\n')
		}

		// Use line builder for all windows to preserve styling
		lineBuilder := pool.GetStringBuilder()
		defer pool.PutStringBuilder(lineBuilder)

		for x := range maxX {
			cell := screen.Cell(x, y)

			// Get the character to display
			char := " "
			if cell.Rune != 0 {
				char = string(cell.Rune)
			}

			// Check if current position is within selection (either actively selecting or has selected text)
			isSelected := (window.IsSelecting || window.SelectedText != "") && m.isPositionInSelection(window, x, y)
			isCursorPos := isFocused && inTerminalMode && x == cursorX && y == cursorY

			// Check if current position is the selection cursor (only in selection mode and NOT in terminal mode)
			isSelectionCursor := m.SelectionMode && !inTerminalMode && isFocused &&
				x == window.SelectionCursor.X && y == window.SelectionCursor.Y

			// Determine if we need styling
			needsStyling := shouldApplyStyle(cell) || isCursorPos || isSelected || isSelectionCursor

			if needsStyling {
				var style lipgloss.Style

				if useOptimizedRendering && !isSelected {
					// Optimized styling for background windows - preserve colors but skip expensive attributes
					style = buildOptimizedCellStyle(cell)
				} else {
					// Full styling for focused windows or selected text
					style = buildCellStyle(cell, isCursorPos)
				}

				// Apply selection highlighting
				if isSelected {
					style = style.Background(lipgloss.Color("62")).Foreground(lipgloss.Color("15")) // Blue background, white text
				}

				// Apply selection cursor highlighting
				if isSelectionCursor {
					style = style.Background(lipgloss.Color("208")).Foreground(lipgloss.Color("0")) // Orange background, black text
				}

				lineBuilder.WriteString(style.Render(char))
			} else {
				lineBuilder.WriteString(char)
			}
		}

		builder.WriteString(lineBuilder.String())
	}

	// Cache the result more intelligently
	content := builder.String()
	window.CachedContent = content
	window.ContentDirty = false // Mark content as clean after rendering
	return content
}

func shouldApplyStyle(cell *vt.Cell) bool {
	return cell.Style.Fg != nil || cell.Style.Bg != nil || cell.Style.Attrs != 0
}

func buildOptimizedCellStyle(cell *vt.Cell) lipgloss.Style {
	// Fast styling for background windows - only colors, skip expensive attributes
	cellStyle := lipgloss.NewStyle()

	// Apply colors only (preserve the visual appearance)
	if cell.Style.Fg != nil {
		if ansiColor, ok := cell.Style.Fg.(lipgloss.ANSIColor); ok {
			cellStyle = cellStyle.Foreground(ansiColor)
		} else if color, ok := cell.Style.Fg.(color.Color); ok {
			cellStyle = cellStyle.Foreground(color)
		}
	}
	if cell.Style.Bg != nil {
		if ansiColor, ok := cell.Style.Bg.(lipgloss.ANSIColor); ok {
			cellStyle = cellStyle.Background(ansiColor)
		} else if color, ok := cell.Style.Bg.(color.Color); ok {
			cellStyle = cellStyle.Background(color)
		}
	}

	// Skip expensive attributes (bold, italic, etc.) for performance
	// This preserves colors while improving performance significantly

	return cellStyle
}

func buildCellStyle(cell *vt.Cell, isCursor bool) lipgloss.Style {
	// Build style efficiently
	cellStyle := lipgloss.NewStyle()

	// Handle cursor rendering first (most common fast path)
	if isCursor {
		// Show cursor by inverting colors
		fg := lipgloss.Color("#FFFFFF")
		bg := lipgloss.Color("#000000")
		if cell.Style.Fg != nil {
			if ansiColor, ok := cell.Style.Fg.(lipgloss.ANSIColor); ok {
				fg = ansiColor
			} else if color, ok := cell.Style.Fg.(color.Color); ok {
				fg = color
			}
		}
		if cell.Style.Bg != nil {
			if ansiColor, ok := cell.Style.Bg.(lipgloss.ANSIColor); ok {
				bg = ansiColor
			} else if color, ok := cell.Style.Bg.(color.Color); ok {
				bg = color
			}
		}
		return cellStyle.Background(fg).Foreground(bg)
	}

	// Apply colors only if needed
	if cell.Style.Fg != nil {
		if ansiColor, ok := cell.Style.Fg.(lipgloss.ANSIColor); ok {
			cellStyle = cellStyle.Foreground(ansiColor)
		} else if color, ok := cell.Style.Fg.(color.Color); ok {
			cellStyle = cellStyle.Foreground(color)
		}
	}
	if cell.Style.Bg != nil {
		if ansiColor, ok := cell.Style.Bg.(lipgloss.ANSIColor); ok {
			cellStyle = cellStyle.Background(ansiColor)
		} else if color, ok := cell.Style.Bg.(color.Color); ok {
			cellStyle = cellStyle.Background(color)
		}
	}

	// Apply attributes only if set (optimize common case)
	if cell.Style.Attrs != 0 {
		attrs := cell.Style.Attrs
		if attrs&1 != 0 { // Bold
			cellStyle = cellStyle.Bold(true)
		}
		if attrs&2 != 0 { // Faint
			cellStyle = cellStyle.Faint(true)
		}
		if attrs&4 != 0 { // Italic
			cellStyle = cellStyle.Italic(true)
		}
		if attrs&32 != 0 { // Reverse
			cellStyle = cellStyle.Reverse(true)
		}
		if attrs&128 != 0 { // Strikethrough
			cellStyle = cellStyle.Strikethrough(true)
		}
	}

	return cellStyle
}

// GetCanvas returns the main rendering canvas with all layers.
func (m *OS) GetCanvas(render bool) *lipgloss.Canvas {
	canvas := lipgloss.NewCanvas()

	// Get layers slice from pool
	layersPtr := pool.GetLayerSlice()
	layers := (*layersPtr)[:0] // Reset length but keep capacity
	defer pool.PutLayerSlice(layersPtr)

	// Pre-compute viewport bounds for culling
	viewportWidth := m.Width
	viewportHeight := m.GetUsableHeight()

	// Create consistent window style
	box := lipgloss.NewStyle().
		Align(lipgloss.Left).
		AlignVertical(lipgloss.Top).
		Foreground(lipgloss.Color("#FFFFFF")).
		Border(lipgloss.RoundedBorder()).
		BorderTop(false)

	for i := range m.Windows {
		window := m.Windows[i]

		// Skip windows not in current workspace
		if window.Workspace != m.CurrentWorkspace {
			continue
		}

		// Check if this window is being animated
		isAnimating := false
		for _, anim := range m.Animations {
			if anim.Window == m.Windows[i] && !anim.Complete {
				isAnimating = true
				break
			}
		}

		// Skip minimized windows unless they're animating
		if window.Minimized && !isAnimating {
			continue
		}

		// Enhanced visibility culling with tighter bounds for better performance
		// Skip windows completely outside viewport (with small margin for animations)
		margin := 5
		if isAnimating {
			margin = 20 // Larger margin for animating windows
		}

		isVisible := window.X+window.Width >= -margin &&
			window.X <= viewportWidth+margin &&
			window.Y+window.Height >= -margin &&
			window.Y <= viewportHeight+margin

		if !isVisible {
			continue
		}

		// Additional optimization: skip expensive operations for barely visible windows
		isFullyVisible := window.X >= 0 && window.Y >= 0 &&
			window.X+window.Width <= viewportWidth &&
			window.Y+window.Height <= viewportHeight

		// Ensure focused window index is valid
		isFocused := m.FocusedWindow == i && m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows)
		borderColor := "#FAAAAA"
		if isFocused {
			borderColor = "#AFFFFF"
			if m.Mode == TerminalMode {
				borderColor = "#AAFFAA" // Green when in terminal mode
			}
		}

		// Enhanced cache checking with early exit for clean windows
		if window.CachedLayer != nil && !window.Dirty && !window.ContentDirty && !window.PositionDirty {
			// Fast path: window is completely clean, use cached layer
			layers = append(layers, window.CachedLayer)
			continue
		}

		// More detailed cache validation only for potentially dirty windows
		needsRedraw := window.CachedLayer == nil ||
			window.Dirty || window.ContentDirty || window.PositionDirty ||
			window.CachedLayer.GetX() != window.X ||
			window.CachedLayer.GetY() != window.Y ||
			window.CachedLayer.GetZ() != window.Z

		// Background window optimization: defer expensive redraws unless critical
		if !needsRedraw || (!isFocused && !isFullyVisible && !window.ContentDirty && window.CachedLayer != nil) {
			layers = append(layers, window.CachedLayer)
			continue
		}

		// Get terminal content
		content := m.renderTerminal(window, isFocused, m.Mode == TerminalMode)

		// Check if this window is being renamed
		isRenaming := m.RenamingWindow && i == m.FocusedWindow

		boxContent := addToBorder(
			box.Width(window.Width).
				Height(window.Height-1).
				BorderForeground(lipgloss.Color(borderColor)).
				Render(content),
			lipgloss.Color(borderColor),
			window,
			isRenaming,
			m.RenameBuffer,
			m.AutoTiling,
		)

		// Give animating windows highest Z-index so they appear on top
		zIndex := window.Z
		if isAnimating {
			zIndex = config.ZIndexAnimating // High z-index for animating windows
		}

		// Cache the layer
		window.CachedLayer = lipgloss.NewLayer(boxContent).X(window.X).Y(window.Y).Z(zIndex).ID(window.ID)
		layers = append(layers, window.CachedLayer)

		// Clear dirty flags after rendering
		window.ClearDirtyFlags()
	}

	if render {
		// Add overlays
		overlays := m.renderOverlays()
		layers = append(layers, overlays...)

		// Always add dock layer (shows empty dock area when no minimized windows)
		dockLayer := m.renderDock()
		layers = append(layers, dockLayer)
	}

	canvas.AddLayers(layers...)
	return canvas
}

func (m *OS) renderOverlays() []*lipgloss.Layer {
	var layers []*lipgloss.Layer

	// Time and status overlay in top-left corner (always visible)
	currentTime := time.Now().Format("15:04:05")
	var statusText string

	if m.PrefixActive {
		// Show prefix indicator with time
		statusText = "PREFIX | " + currentTime
	} else {
		statusText = currentTime
	}

	timeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#a0a0b0")).
		Bold(true).
		Padding(0, 1)

	// Highlight when prefix is active
	if m.PrefixActive {
		timeStyle = timeStyle.
			Background(lipgloss.Color("#ff6b6b")).
			Foreground(lipgloss.Color("#ffffff"))
	} else {
		timeStyle = timeStyle.
			Background(lipgloss.Color("#1a1a2e"))
	}

	renderedTime := timeStyle.Render(statusText)

	// Position time in top-left corner
	timeX := 1
	timeLayer := lipgloss.NewLayer(renderedTime).
		X(timeX).
		Y(0).
		Z(config.ZIndexTime). // High Z to appear above windows
		ID("time")

	layers = append(layers, timeLayer)

	// Welcome message when no windows exist
	if len(m.Windows) == 0 {
		// Clean ASCII art with Unicode
		asciiArt := `████████╗██╗   ██╗██╗ ██████╗ ███████╗
╚══██╔══╝██║   ██║██║██╔═══██╗██╔════╝
   ██║   ██║   ██║██║██║   ██║███████╗
   ██║   ██║   ██║██║██║   ██║╚════██║
   ██║   ╚██████╔╝██║╚██████╔╝███████║
   ╚═╝    ╚═════╝ ╚═╝ ╚═════╝ ╚══════╝`

		// Styled title
		title := lipgloss.NewStyle().
			Foreground(lipgloss.Color("14")).
			Bold(true).
			Render(asciiArt)

		subtitle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("11")).
			Render("Terminal UI Operating System")

		instruction := lipgloss.NewStyle().
			Foreground(lipgloss.Color("7")).
			Render("Press 'n' to create a window, '?' for help")

		content := lipgloss.JoinVertical(lipgloss.Center,
			title,
			"",
			subtitle,
			"",
			instruction,
		)

		// Simple border with subtle color
		boxStyle := lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("6")).
			Padding(1, 2)

		// Use lipgloss.Place for proper centering
		centeredContent := lipgloss.Place(
			m.Width, m.Height,
			lipgloss.Center, lipgloss.Center,
			boxStyle.Render(content),
		)

		welcomeLayer := lipgloss.NewLayer(centeredContent).
			X(0).Y(0).Z(1).ID("welcome")

		layers = append(layers, welcomeLayer)
	}

	// Help overlay - always available regardless of windows
	if m.ShowHelp {
		// Styled help content
		helpTitle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("14")).
			Bold(true).
			Render("Help")

		keyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("11")).
			Render

		descStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("7")).
			Render

		// Calculate available height for help content
		// Account for border (2), padding (2 vertical), and centering margins
		maxDisplayHeight := max(m.Height-8, 8)

		// Build ALL help lines with pre-allocated capacity to reduce allocations
		allHelpLines := make([]string, 0, 50) // Pre-allocate capacity for ~50 help lines
		allHelpLines = append(allHelpLines, helpTitle)
		allHelpLines = append(allHelpLines, "")

		// Show prefix status
		if m.WorkspacePrefixActive {
			activeStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("10")).
				Bold(true).
				Render
			allHelpLines = append(allHelpLines, activeStyle("WORKSPACE PREFIX ACTIVE - Select workspace (1-9)"))
			allHelpLines = append(allHelpLines, "")
		} else if m.MinimizePrefixActive {
			activeStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("10")).
				Bold(true).
				Render
			allHelpLines = append(allHelpLines, activeStyle("MINIMIZE PREFIX ACTIVE - Restore window (1-9)"))
			allHelpLines = append(allHelpLines, "")
		} else if m.TilingPrefixActive {
			activeStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("10")).
				Bold(true).
				Render
			allHelpLines = append(allHelpLines, activeStyle("WINDOW PREFIX ACTIVE - Terminal management"))
			allHelpLines = append(allHelpLines, "")
		} else if m.PrefixActive {
			activeStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("10")).
				Bold(true).
				Render
			allHelpLines = append(allHelpLines, activeStyle("PREFIX MODE ACTIVE - Enter command"))
			allHelpLines = append(allHelpLines, "")
		}

		allHelpLines = append(allHelpLines, keyStyle("WINDOW MANAGEMENT"))
		allHelpLines = append(allHelpLines, "")
		allHelpLines = append(allHelpLines, keyStyle("n")+"              "+descStyle("New window"))
		allHelpLines = append(allHelpLines, keyStyle("x")+"              "+descStyle("Close window"))
		allHelpLines = append(allHelpLines, keyStyle("r")+"              "+descStyle("Rename window"))
		allHelpLines = append(allHelpLines, keyStyle("m")+"              "+descStyle("Minimize window"))
		allHelpLines = append(allHelpLines, keyStyle("Shift+M")+"        "+descStyle("Restore all"))
		allHelpLines = append(allHelpLines, keyStyle("Tab")+"            "+descStyle("Next window"))
		allHelpLines = append(allHelpLines, keyStyle("Shift+Tab")+"      "+descStyle("Previous window"))
		allHelpLines = append(allHelpLines, keyStyle("1-9")+"            "+descStyle("Select window"))
		allHelpLines = append(allHelpLines, "")
		allHelpLines = append(allHelpLines, keyStyle("WORKSPACES"))
		allHelpLines = append(allHelpLines, "")

		// Detect OS and use appropriate modifier key name
		modifierKey := "Alt"
		if runtime.GOOS == "darwin" {
			modifierKey = "Opt"
		}

		allHelpLines = append(allHelpLines, keyStyle(modifierKey+"+1-9")+"        "+descStyle("Switch workspace"))
		allHelpLines = append(allHelpLines, keyStyle(modifierKey+"+Shift+1-9")+"  "+descStyle("Move window and follow"))
		allHelpLines = append(allHelpLines, keyStyle("Ctrl+B, w, 1-9")+" "+descStyle("Switch workspace (prefix)"))
		allHelpLines = append(allHelpLines, keyStyle("Ctrl+B, w, Shift+1-9")+" "+descStyle("Move window (prefix)"))
		allHelpLines = append(allHelpLines, "")
		allHelpLines = append(allHelpLines, keyStyle("MODES"))
		allHelpLines = append(allHelpLines, "")
		allHelpLines = append(allHelpLines, keyStyle("i, Enter")+"       "+descStyle("Insert mode"))
		allHelpLines = append(allHelpLines, keyStyle("t")+"              "+descStyle("Toggle tiling"))
		allHelpLines = append(allHelpLines, keyStyle("?")+"              "+descStyle("Toggle help"))

		if m.AutoTiling {
			allHelpLines = append(allHelpLines, "")
			allHelpLines = append(allHelpLines, keyStyle("TILING:"))
			allHelpLines = append(allHelpLines, keyStyle("Shift+H/L, Ctrl+←/→")+" "+descStyle("Swap left/right"))
			allHelpLines = append(allHelpLines, keyStyle("Shift+K/J, Ctrl+↑/↓")+" "+descStyle("Swap up/down"))
		} else {
			allHelpLines = append(allHelpLines, "")
			allHelpLines = append(allHelpLines, keyStyle("WINDOW SNAPPING:"))
			allHelpLines = append(allHelpLines, keyStyle("h, l")+"           "+descStyle("Snap left/right"))
			allHelpLines = append(allHelpLines, keyStyle("1-4")+"            "+descStyle("Snap to corners"))
			allHelpLines = append(allHelpLines, keyStyle("f")+"              "+descStyle("Fullscreen"))
			allHelpLines = append(allHelpLines, keyStyle("u")+"              "+descStyle("Unsnap"))
		}

		allHelpLines = append(allHelpLines, "")
		allHelpLines = append(allHelpLines, keyStyle("TEXT SELECTION:"))
		allHelpLines = append(allHelpLines, keyStyle("s")+"              "+descStyle("Toggle selection mode"))
		allHelpLines = append(allHelpLines, keyStyle("Ctrl+S")+"         "+descStyle("Toggle selection (from terminal)"))
		allHelpLines = append(allHelpLines, keyStyle("Mouse drag")+"     "+descStyle("Select text (mouse)"))
		allHelpLines = append(allHelpLines, keyStyle("Arrow keys")+"     "+descStyle("Move cursor"))
		allHelpLines = append(allHelpLines, keyStyle("Shift+Arrow")+"    "+descStyle("Extend selection"))
		allHelpLines = append(allHelpLines, keyStyle("c")+"              "+descStyle("Copy selected text"))
		allHelpLines = append(allHelpLines, keyStyle("Ctrl+V")+"         "+descStyle("Paste from clipboard"))
		allHelpLines = append(allHelpLines, keyStyle("Esc")+"            "+descStyle("Clear selection"))

		allHelpLines = append(allHelpLines, "")
		allHelpLines = append(allHelpLines, keyStyle("WINDOW NAVIGATION:"))
		allHelpLines = append(allHelpLines, keyStyle("Ctrl+↑/↓")+"       "+descStyle("Swap/maximize windows"))

		allHelpLines = append(allHelpLines, "")
		allHelpLines = append(allHelpLines, keyStyle("SYSTEM:"))
		allHelpLines = append(allHelpLines, keyStyle("Ctrl+L")+"         "+descStyle("Toggle log viewer"))

		allHelpLines = append(allHelpLines, "")
		allHelpLines = append(allHelpLines, keyStyle("PREFIX (Ctrl+B) - Works in all modes:"))
		allHelpLines = append(allHelpLines, keyStyle("c")+"              "+descStyle("Create window"))
		allHelpLines = append(allHelpLines, keyStyle("x")+"              "+descStyle("Close window"))
		allHelpLines = append(allHelpLines, keyStyle(",/r")+"            "+descStyle("Rename window"))
		allHelpLines = append(allHelpLines, keyStyle("n/Tab")+"          "+descStyle("Next window"))
		allHelpLines = append(allHelpLines, keyStyle("p/Shift+Tab")+"    "+descStyle("Previous window"))
		allHelpLines = append(allHelpLines, keyStyle("0-9")+"            "+descStyle("Jump to window"))
		allHelpLines = append(allHelpLines, keyStyle("space")+"          "+descStyle("Toggle tiling"))
		allHelpLines = append(allHelpLines, keyStyle("w")+"              "+descStyle("Workspace commands"))
		allHelpLines = append(allHelpLines, keyStyle("m")+"              "+descStyle("Minimize commands"))
		allHelpLines = append(allHelpLines, keyStyle("t")+"              "+descStyle("Window commands"))
		allHelpLines = append(allHelpLines, keyStyle("d")+"              "+descStyle("Detach from terminal"))
		allHelpLines = append(allHelpLines, keyStyle("s")+"              "+descStyle("Toggle selection mode"))
		allHelpLines = append(allHelpLines, keyStyle("Ctrl+B")+"         "+descStyle("Send literal Ctrl+B"))
		allHelpLines = append(allHelpLines, "")
		allHelpLines = append(allHelpLines, keyStyle("WINDOW PREFIX (Ctrl+B, t):"))
		allHelpLines = append(allHelpLines, keyStyle("n")+"              "+descStyle("New window"))
		allHelpLines = append(allHelpLines, keyStyle("x")+"              "+descStyle("Close window"))
		allHelpLines = append(allHelpLines, keyStyle("r")+"              "+descStyle("Rename window"))
		allHelpLines = append(allHelpLines, keyStyle("Tab/Shift+Tab")+"  "+descStyle("Next/Previous window"))
		allHelpLines = append(allHelpLines, keyStyle("t")+"              "+descStyle("Toggle tiling mode"))
		allHelpLines = append(allHelpLines, "")
		allHelpLines = append(allHelpLines, keyStyle("q, Ctrl+C")+"      "+descStyle("Quit"))

		// Apply scrolling
		// Ensure scroll offset is within bounds
		maxScroll := max(len(allHelpLines)-maxDisplayHeight, 0)
		if m.HelpScrollOffset > maxScroll {
			m.HelpScrollOffset = maxScroll
		}
		if m.HelpScrollOffset < 0 {
			m.HelpScrollOffset = 0
		}

		// Get the visible portion based on scroll
		var helpLines []string
		startIdx := m.HelpScrollOffset
		endIdx := min(startIdx+maxDisplayHeight, len(allHelpLines))

		helpLines = allHelpLines[startIdx:endIdx]

		// Add scroll indicators
		if m.HelpScrollOffset > 0 {
			// Can scroll up
			helpLines[0] = keyStyle("↑") + "              " + descStyle("(scroll up for more)")
		}
		if endIdx < len(allHelpLines) {
			// Can scroll down
			helpLines[len(helpLines)-1] = keyStyle("↓") + "              " + descStyle("(scroll down for more)")
		}

		content := lipgloss.JoinVertical(lipgloss.Left, helpLines...)

		// Simple border with subtle color
		helpStyle := lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("6")).
			Padding(1, 2)

		renderedHelp := helpStyle.Render(content)

		// Calculate the actual height of the rendered help
		helpHeight := lipgloss.Height(renderedHelp)

		// If help is too tall, reduce content and re-render
		if helpHeight > m.Height-2 {
			// Recalculate with less content
			maxDisplayHeight = max(m.Height-10, 5)

			// Re-slice the content
			endIdx := min(m.HelpScrollOffset+maxDisplayHeight, len(allHelpLines))
			helpLines = allHelpLines[m.HelpScrollOffset:endIdx]

			// Re-add scroll indicators
			if m.HelpScrollOffset > 0 && len(helpLines) > 0 {
				helpLines[0] = keyStyle("↑") + "              " + descStyle("(scroll up for more)")
			}
			if endIdx < len(allHelpLines) && len(helpLines) > 0 {
				helpLines[len(helpLines)-1] = keyStyle("↓") + "              " + descStyle("(scroll down for more)")
			}

			content = lipgloss.JoinVertical(lipgloss.Left, helpLines...)
			renderedHelp = helpStyle.Render(content)
		}

		// Use lipgloss.Place for proper centering
		centeredHelp := lipgloss.Place(
			m.Width, m.Height,
			lipgloss.Center, lipgloss.Center,
			renderedHelp,
		)

		helpLayer := lipgloss.NewLayer(centeredHelp).
			X(0).Y(0).Z(config.ZIndexHelp).ID("help")

		layers = append(layers, helpLayer)
	}

	// Log viewer overlay
	if m.ShowLogs {
		// Build log viewer content
		logTitle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("14")).
			Bold(true).
			Render("System Logs")

		// Calculate available height
		maxDisplayHeight := max(m.Height-8, 8)

		var logLines []string
		logLines = append(logLines, logTitle)
		logLines = append(logLines, "")

		// Add log messages with color coding
		startIdx := max(m.LogScrollOffset, 0)

		displayCount := 0
		for i := startIdx; i < len(m.LogMessages) && displayCount < maxDisplayHeight-3; i++ {
			msg := m.LogMessages[i]

			// Color code by level
			var levelColor string
			switch msg.Level {
			case "ERROR":
				levelColor = "9" // Red
			case "WARN":
				levelColor = "11" // Yellow
			default:
				levelColor = "10" // Green
			}

			timeStr := msg.Time.Format("15:04:05")
			levelStr := lipgloss.NewStyle().
				Foreground(lipgloss.Color(levelColor)).
				Render(fmt.Sprintf("[%s]", msg.Level))

			logLine := fmt.Sprintf("%s %s %s", timeStr, levelStr, msg.Message)
			logLines = append(logLines, logLine)
			displayCount++
		}

		// Add scroll indicator if needed
		if len(m.LogMessages) > maxDisplayHeight-3 {
			scrollInfo := fmt.Sprintf("Showing %d-%d of %d logs (↑/↓ to scroll)",
				startIdx+1, startIdx+displayCount, len(m.LogMessages))
			logLines = append(logLines, "")
			logLines = append(logLines, lipgloss.NewStyle().
				Foreground(lipgloss.Color("8")).
				Render(scrollInfo))
		}

		// Join and style
		logContent := strings.Join(logLines, "\n")

		// Create bordered box
		logBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("12")).
			Padding(1, 2).
			Width(80).
			Background(lipgloss.Color("#1a1a2a")).
			Render(logContent)

		// Center the log viewer
		centeredLogs := lipgloss.Place(m.Width, m.Height,
			lipgloss.Center, lipgloss.Center, logBox)

		logLayer := lipgloss.NewLayer(centeredLogs).
			X(0).Y(0).Z(config.ZIndexLogs).ID("logs")

		layers = append(layers, logLayer)
	}

	// Which-key style overlay for prefix commands (appears after delay)
	if m.PrefixActive && !m.ShowHelp && time.Since(m.LastPrefixTime) > config.WhichKeyDelay {
		keyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("11")).
			Bold(true)
		descStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("7"))

		var helpLines []string

		if m.WorkspacePrefixActive {
			// Workspace prefix commands
			helpLines = []string{
				keyStyle.Render("Workspace Commands:"),
				"",
				keyStyle.Render("1-9") + "        " + descStyle.Render("Switch to workspace"),
				keyStyle.Render("Shift+1-9") + "  " + descStyle.Render("Move window to workspace"),
				keyStyle.Render("Esc") + "        " + descStyle.Render("Cancel"),
			}
		} else if m.MinimizePrefixActive {
			// Minimize prefix commands
			// Count minimized windows in current workspace
			minimizedCount := 0
			for _, win := range m.Windows {
				if win.Minimized && win.Workspace == m.CurrentWorkspace {
					minimizedCount++
				}
			}
			helpLines = []string{
				keyStyle.Render("Minimize Commands:"),
				"",
				keyStyle.Render("m") + "       " + descStyle.Render("Minimize focused window"),
				keyStyle.Render("1-9") + "     " + descStyle.Render(fmt.Sprintf("Restore window (%d minimized)", minimizedCount)),
				keyStyle.Render("Shift+M") + " " + descStyle.Render("Restore all"),
				keyStyle.Render("Esc") + "     " + descStyle.Render("Cancel"),
			}
		} else if m.TilingPrefixActive {
			// Window prefix commands
			helpLines = []string{
				keyStyle.Render("Window Commands:"),
				"",
				keyStyle.Render("n") + "         " + descStyle.Render("New window"),
				keyStyle.Render("x") + "         " + descStyle.Render("Close window"),
				keyStyle.Render("r") + "         " + descStyle.Render("Rename window"),
				keyStyle.Render("Tab") + "       " + descStyle.Render("Next window"),
				keyStyle.Render("Shift+Tab") + " " + descStyle.Render("Previous window"),
				keyStyle.Render("t") + "         " + descStyle.Render("Toggle tiling mode"),
				keyStyle.Render("Esc") + "       " + descStyle.Render("Cancel"),
			}
		} else {
			// General prefix commands
			helpLines = []string{
				keyStyle.Render("Prefix Commands:"),
				"",
				keyStyle.Render("c") + "   " + descStyle.Render("Create window"),
				keyStyle.Render("x") + "   " + descStyle.Render("Close window"),
				keyStyle.Render(",") + "   " + descStyle.Render("Rename window"),
				keyStyle.Render("n") + "   " + descStyle.Render("Next window"),
				keyStyle.Render("p") + "   " + descStyle.Render("Previous window"),
				keyStyle.Render("0-9") + " " + descStyle.Render("Jump to window"),
				keyStyle.Render("w") + "   " + descStyle.Render("Workspace commands..."),
				keyStyle.Render("m") + "   " + descStyle.Render("Minimize commands..."),
				keyStyle.Render("t") + "   " + descStyle.Render("Window commands..."),
				keyStyle.Render("d") + "   " + descStyle.Render("Detach (exit terminal)"),
				keyStyle.Render("s") + "   " + descStyle.Render("Selection mode"),
				keyStyle.Render("?") + "   " + descStyle.Render("Toggle help"),
			}
		}

		content := lipgloss.JoinVertical(lipgloss.Left, helpLines...)

		// Style the overlay with border
		overlayStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#ff6b6b")).
			Background(lipgloss.Color("#1a1a2e")).
			Padding(1, 2)

		renderedOverlay := overlayStyle.Render(content)

		// Position in bottom-right corner with some padding
		overlayWidth := lipgloss.Width(renderedOverlay)
		overlayHeight := lipgloss.Height(renderedOverlay)
		overlayX := m.Width - overlayWidth - 2
		overlayY := m.Height - overlayHeight - 3 // Above status bar

		whichKeyLayer := lipgloss.NewLayer(renderedOverlay).
			X(overlayX).
			Y(overlayY).
			Z(config.ZIndexWhichKey). // Above other overlays
			ID("whichkey")

		layers = append(layers, whichKeyLayer)
	}

	// Render notifications
	if len(m.Notifications) > 0 {
		// Clean up expired notifications
		m.CleanupNotifications()

		// Render active notifications
		notifY := 1       // Start position from top
		notifSpacing := 4 // Space between notifications
		for i, notif := range m.Notifications {
			if i >= 3 { // Max 3 notifications visible
				break
			}

			// Calculate opacity based on animation and lifetime
			opacity := 1.0
			if notif.Animation != nil {
				elapsed := time.Since(notif.Animation.StartTime)
				if elapsed < notif.Animation.Duration {
					opacity = float64(elapsed) / float64(notif.Animation.Duration)
				}
			}

			// Fade out in last 500ms
			timeLeft := notif.Duration - time.Since(notif.StartTime)
			if timeLeft < config.NotificationFadeOutDuration {
				opacity *= float64(timeLeft) / float64(config.NotificationFadeOutDuration)
			}

			// Skip if fully transparent
			if opacity <= 0 {
				continue
			}

			// Style based on type
			var bgColor, borderColor, fgColor, icon string
			switch notif.Type {
			case "error":
				bgColor = "#2a1515"
				borderColor = "#ff4444"
				fgColor = "#ff6666"
				icon = "✕"
			case "warning":
				bgColor = "#2a2515"
				borderColor = "#ffaa00"
				fgColor = "#ffcc00"
				icon = "⚠"
			case "success":
				bgColor = "#152a15"
				borderColor = "#44ff44"
				fgColor = "#66ff66"
				icon = "✓"
			default:
				bgColor = "#151a2a"
				borderColor = "#4488ff"
				fgColor = "#66aaff"
				icon = "ℹ"
			}

			// Calculate dynamic max width based on screen size (leave space for margins)
			maxNotifWidth := min(
				// Leave 8 chars margin (4 on each side)
				// Minimum width
				max(
					m.Width-8,
					20,
				),
				// Maximum width for readability
				60,
			)

			// Truncate message if it's too long (accounting for icon and padding)
			message := notif.Message
			maxMessageLen := maxNotifWidth - 8 // Account for icon, spaces, and padding
			if len(message) > maxMessageLen {
				message = message[:maxMessageLen-3] + "..."
			}

			// Build notification content with better spacing
			notifContent := fmt.Sprintf(" %s  %s ", icon, message)

			// Style the notification with border
			notifBox := lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color(borderColor)).
				Background(lipgloss.Color(bgColor)).
				Foreground(lipgloss.Color(fgColor)).
				Padding(0, 1).
				Bold(true).
				MaxWidth(maxNotifWidth).
				Render(notifContent)

			// Position in top-right corner with margin, ensure it doesn't go off-screen
			notifX := max(m.Width-lipgloss.Width(notifBox)-2, 0)
			currentY := notifY + (i * notifSpacing)

			// Create notification layer
			notifLayer := lipgloss.NewLayer(notifBox).
				X(notifX).Y(currentY).Z(config.ZIndexNotifications).
				ID(fmt.Sprintf("notif-%s", notif.ID))

			layers = append(layers, notifLayer)
		}
	}

	return layers
}

func (m *OS) renderDock() *lipgloss.Layer {
	// System info styles
	sysInfoStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#808090")).
		MarginRight(2)

	modeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#a0a0b0")).
		Bold(true).
		MarginRight(2)

	workspaceStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#b0b0c0")).
		Bold(true).
		MarginRight(2)

	// Get mode text
	modeText := "[W]"
	if m.Mode == TerminalMode {
		modeText = "[T]"
	}
	// Add tiling indicator
	if m.AutoTiling {
		modeText += " [T]" // Tiling mode icon
	}

	// Add workspace indicator with window counts
	workspaceText := ""
	for i := 1; i <= m.NumWorkspaces; i++ {
		count := m.GetWorkspaceWindowCount(i)
		if i == m.CurrentWorkspace {
			// Highlight current workspace
			if count > 0 {
				workspaceText += lipgloss.NewStyle().
					Background(lipgloss.Color("#4865f2")).
					Foreground(lipgloss.Color("#ffffff")).
					Bold(true).
					Render(fmt.Sprintf(" %d:%d ", i, count))
			} else {
				workspaceText += lipgloss.NewStyle().
					Background(lipgloss.Color("#4865f2")).
					Foreground(lipgloss.Color("#ffffff")).
					Bold(true).
					Render(fmt.Sprintf(" %d ", i))
			}
		} else if count > 0 {
			// Show workspaces with windows
			workspaceText += lipgloss.NewStyle().
				Foreground(lipgloss.Color("#808090")).
				Render(fmt.Sprintf(" %d:%d ", i, count))
		}
	}

	// Count minimized AND minimizing windows in current workspace
	dockWindows := []int{}
	for i, window := range m.Windows {
		if window.Workspace == m.CurrentWorkspace && (window.Minimized || window.Minimizing) {
			dockWindows = append(dockWindows, i)
			if len(dockWindows) >= 9 {
				break // Only show up to 9 items
			}
		}
	}

	// Build pill-style dock items
	var dockItemsStr string
	itemNumber := 1

	for _, windowIndex := range dockWindows {
		window := m.Windows[windowIndex]

		// Colors for active vs inactive
		bgColor := "#2a2a3e"
		fgColor := "#a0a0a8"
		if windowIndex == m.FocusedWindow && !window.Minimizing {
			bgColor = "#4865f2"
			fgColor = "#ffffff"
		}

		// Get window name (only custom names)
		windowName := window.CustomName

		// Format label based on whether we have a custom name
		var labelText string
		if windowName != "" {
			// Truncate if too long (max 12 chars for dock item)
			if len(windowName) > 12 {
				windowName = windowName[:9] + "..."
			}
			labelText = fmt.Sprintf(" %d:%s ", itemNumber, windowName)
		} else {
			// Just show the number if no custom name
			labelText = fmt.Sprintf(" %d ", itemNumber)
		}

		// Create pill-style item with circles and label
		leftCircle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(bgColor)).
			Render(LeftHalfCircle)

		nameLabel := lipgloss.NewStyle().
			Background(lipgloss.Color(bgColor)).
			Foreground(lipgloss.Color(fgColor)).
			Bold(windowIndex == m.FocusedWindow).
			Render(labelText)

		rightCircle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(bgColor)).
			Render(RightHalfCircle)

		// Add spacing between items
		if itemNumber > 1 {
			dockItemsStr += " "
		}
		dockItemsStr += leftCircle + nameLabel + rightCircle

		itemNumber++
	}

	// Build system info content
	// Left side: Mode, workspace, and window count
	leftInfo := lipgloss.JoinHorizontal(lipgloss.Top,
		modeStyle.Render(modeText),
		workspaceStyle.Render(workspaceText),
	)

	// Right side: CPU graph only
	cpuGraph := m.GetCPUGraph()
	rightInfo := sysInfoStyle.Render(cpuGraph)

	// Use fixed widths to prevent layout shifts
	// Left side needs more space for workspace indicators
	leftWidth := 30 // Enough for mode + workspace indicators
	// Right side is fixed width for CPU graph only
	rightWidth := 20 // CPU graph (19 chars)

	// Calculate center area
	centerWidth := lipgloss.Width(dockItemsStr)
	availableSpace := m.Width - leftWidth - rightWidth - centerWidth

	// Create spacers to center the dock items
	leftSpacer := availableSpace / 2
	rightSpacer := availableSpace - leftSpacer

	// Ensure non-negative spacers
	if leftSpacer < 0 {
		leftSpacer = 0
	}
	if rightSpacer < 0 {
		rightSpacer = 0
	}

	// Build the complete dock bar on a single line
	// Pad left and right info to fixed widths
	paddedLeftInfo := lipgloss.NewStyle().Width(leftWidth).Align(lipgloss.Left).Render(leftInfo)
	paddedRightInfo := lipgloss.NewStyle().Width(rightWidth).Align(lipgloss.Right).Render(rightInfo)

	dockBar := lipgloss.JoinHorizontal(
		lipgloss.Top,
		paddedLeftInfo,
		lipgloss.NewStyle().Width(leftSpacer).Render(""),
		lipgloss.NewStyle().Render(dockItemsStr),
		lipgloss.NewStyle().Width(rightSpacer).Render(""),
		paddedRightInfo,
	)

	// Add separator line above
	separator := lipgloss.NewStyle().
		Width(m.Width).
		Foreground(lipgloss.Color("#303040")).
		Render(strings.Repeat("─", m.Width))

	// Combine separator and dock bar
	fullDock := lipgloss.JoinVertical(lipgloss.Left,
		separator,
		dockBar,
	)

	// Return the dock layer positioned to show everything
	return lipgloss.NewLayer(fullDock).X(0).Y(m.Height - config.DockHeight).Z(config.ZIndexDock).ID("dock")
}

// isPositionInSelection checks if the given position is within the current text selection.
func (m *OS) isPositionInSelection(window *terminal.Window, x, y int) bool {
	// Return false if there's no selection (either actively selecting or completed selection)
	if !window.IsSelecting && window.SelectedText == "" {
		return false
	}

	// Normalize selection coordinates (ensure start <= end)
	startX, startY := window.SelectionStart.X, window.SelectionStart.Y
	endX, endY := window.SelectionEnd.X, window.SelectionEnd.Y

	// Swap if selection was made backwards
	if startY > endY || (startY == endY && startX > endX) {
		startX, endX = endX, startX
		startY, endY = endY, startY
	}

	// Check if position is within selection bounds
	if y < startY || y > endY {
		return false
	}
	if y == startY && y == endY {
		// Single line selection
		return x >= startX && x <= endX
	} else if y == startY {
		// First line of multi-line selection
		return x >= startX
	} else if y == endY {
		// Last line of multi-line selection
		return x <= endX
	} else {
		// Middle lines of multi-line selection
		return true
	}
}

// View returns the rendered view as a string.
func (m *OS) View() string {
	return lipgloss.Sprintln(m.GetCanvas(true).Render())
}
