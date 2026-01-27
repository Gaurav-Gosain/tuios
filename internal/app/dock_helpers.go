package app

import (
	"fmt"
	"sort"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// DockItem represents a single item in the dock
type DockItem struct {
	WindowIndex int
	Label       string
	Width       int // Total width including circles
}

// DockLayout contains calculated layout information for the dock
type DockLayout struct {
	LeftText       string
	LeftWidth      int
	RightWidth     int
	CenterStartX   int
	ItemPositions  []ItemPosition // Position of each dock item
	TruncatedCount int            // Number of items that don't fit
	VisibleItems   []DockItem     // Items that fit and should be displayed
	ModeInfo       ModeInfo       // Mode display information for styling
}

// ItemPosition holds the position and size of a dock item
type ItemPosition struct {
	StartX      int
	EndX        int
	WindowIndex int
}

// CalculateDockLayout calculates the layout for the dock including positions of all items.
// This function is shared between rendering (render.go) and mouse handling (mouse.go)
// to ensure consistent positioning.
func (m *OS) CalculateDockLayout() DockLayout {
	layout := DockLayout{}

	// Build left side text (compact format)
	layout.LeftText, layout.LeftWidth, layout.ModeInfo = m.buildDockLeftText()

	// Calculate right side width
	layout.RightWidth = m.calculateDockRightWidth()

	// Get all dock items
	allItems := m.getDockItems()

	// Calculate how many items fit and their positions
	layout.calculateItemPositions(m.GetRenderWidth(), allItems)

	return layout
}

// ModeInfo contains mode display information
type ModeInfo struct {
	Block     string // The character to display (e.g., "█")
	Color     string // Hex color for the block
	CursorPos string // Cursor position for copy mode (empty otherwise)
	IsTiling  bool   // Whether tiling mode is active
	NextSplit string // Next split direction when tiling ("V" or "H")
}

// buildDockLeftText builds the left side of the dock (mode + workspace info)
// Returns the text, width, and mode info for styling
func (m *OS) buildDockLeftText() (string, int, ModeInfo) {
	focusedWindow := m.GetFocusedWindow()

	// Build mode info (will be styled with colors in render.go)
	modeInfo := ModeInfo{
		Block:    "█",
		IsTiling: m.AutoTiling,
	}

	// Get next split direction if tiling is active
	if m.AutoTiling {
		tree := m.WorkspaceTrees[m.CurrentWorkspace]
		if tree != nil {
			modeInfo.NextSplit = tree.GetNextSplitDirection()
		} else {
			modeInfo.NextSplit = "V" // Default to vertical
		}
	}

	var modeText string
	var modeLabel string

	if m.Mode == TerminalMode {
		if focusedWindow != nil && focusedWindow.CopyMode != nil && focusedWindow.CopyMode.Active {
			// Copy mode
			modeInfo.Color = theme.ColorToString(theme.DockColorCopy())
			modeInfo.CursorPos = fmt.Sprintf("%d:%d", focusedWindow.CopyMode.CursorY, focusedWindow.CopyMode.CursorX)
			modeLabel = " " + modeInfo.CursorPos + " "
		} else {
			// Terminal mode
			modeInfo.Color = theme.ColorToString(theme.DockColorTerminal())
			// Add tiling indicator for terminal mode (with split direction)
			if m.AutoTiling {
				modeLabel = config.GetDockModeIconTiling() + modeInfo.NextSplit
			} else {
				modeLabel = config.GetDockModeIconTerminal()
			}
		}
	} else {
		// Window mode
		modeInfo.Color = theme.ColorToString(theme.DockColorWindow())
		// Add tiling indicator for window mode (with split direction)
		if m.AutoTiling {
			modeLabel = config.GetDockModeIconTiling() + modeInfo.NextSplit
		} else {
			modeLabel = config.GetDockModeIconWindow()
		}
	}

	// Build pill-style mode indicator with configurable semicircles
	// This will be styled in render.go with the mode color
	modeText = config.GetDockPillLeftChar() + modeLabel + config.GetDockPillRightChar()

	// Count terminals (all windows across all workspaces)
	totalTerminals := 0
	for i := 1; i <= m.NumWorkspaces; i++ {
		totalTerminals += m.GetWorkspaceWindowCount(i)
	}

	// Count workspaces being used (workspaces with at least 1 window)
	workspacesUsed := 0
	for i := 1; i <= m.NumWorkspaces; i++ {
		if m.GetWorkspaceWindowCount(i) > 0 {
			workspacesUsed++
		}
	}

	// Build workspace text with stats using configurable icons
	// Format: "2:3 • 5  3 " where:
	// - 2:3 = workspace 2, 3 windows in current
	// - 5  = 5 terminals total (space before icon)
	// - 3  = 3 workspaces in use (space before icon)
	windowsInCurrent := m.GetWorkspaceWindowCount(m.CurrentWorkspace)
	workspaceText := fmt.Sprintf(" %d:%d%s%d %s %d %s ",
		m.CurrentWorkspace,
		windowsInCurrent,
		config.GetDockSeparator(),
		totalTerminals,
		config.GetDockIconTerminalCount(),
		workspacesUsed,
		config.GetDockIconWorkspaceCount())

	// Combine mode and workspace
	leftText := modeText + workspaceText

	// Calculate actual rendered width (handles Unicode, Nerd Fonts, etc.)
	// Use lipgloss.Width instead of len() to get proper display width
	width := lipgloss.Width(modeText) + lipgloss.Width(workspaceText) + 4 // +4 for margins/padding

	return leftText, width, modeInfo
}

// calculateDockRightWidth calculates the width of the right side of the dock
func (m *OS) calculateDockRightWidth() int {
	focusedWindow := m.GetFocusedWindow()
	inCopyMode := focusedWindow != nil && focusedWindow.CopyMode != nil && focusedWindow.CopyMode.Active

	if inCopyMode {
		// In copy mode, help text can be very long - estimate based on copy mode state
		// These widths match the actual help text lengths in render.go
		switch focusedWindow.CopyMode.State {
		case terminal.CopyModeNormal:
			return 110 // Length of normal mode help text + padding
		case terminal.CopyModeSearch:
			return 60 // Length of search mode help text + padding
		case terminal.CopyModeVisualChar:
			return 90 // Length of visual char mode help text + padding
		case terminal.CopyModeVisualLine:
			return 35 // Length of visual line mode help text + padding
		default:
			return 32
		}
	}

	return 32 // CPU graph (~19 chars) + space + RAM (~11 chars) = ~31 chars
}

// getDockItems returns all dock items (minimized windows in current workspace)
func (m *OS) getDockItems() []DockItem {
	// Find all minimized/minimizing windows in current workspace
	dockWindows := []int{}
	for i, window := range m.Windows {
		if window.Workspace == m.CurrentWorkspace && (window.Minimized || window.Minimizing) {
			dockWindows = append(dockWindows, i)
		}
	}

	// Sort by minimize order (oldest first)
	sort.Slice(dockWindows, func(i, j int) bool {
		return m.Windows[dockWindows[i]].MinimizeOrder < m.Windows[dockWindows[j]].MinimizeOrder
	})

	// Build dock items
	items := make([]DockItem, 0, len(dockWindows))
	itemNumber := 1

	for _, windowIndex := range dockWindows {
		window := m.Windows[windowIndex]

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

		// Calculate width: 2 for circles (left + right) + actual rendered label width
		// Use lipgloss.Width to get proper display width (handles Unicode, emojis, etc.)
		itemWidth := lipgloss.Width(config.GetDockPillLeftChar()) +
			lipgloss.Width(labelText) +
			lipgloss.Width(config.GetDockPillRightChar())

		items = append(items, DockItem{
			WindowIndex: windowIndex,
			Label:       labelText,
			Width:       itemWidth,
		})

		itemNumber++
	}

	return items
}

// calculateItemPositions determines which items fit and their X positions
func (layout *DockLayout) calculateItemPositions(screenWidth int, allItems []DockItem) {
	// Calculate total width of all items (including spaces between)
	totalItemsWidth := 0
	for i, item := range allItems {
		totalItemsWidth += item.Width
		if i > 0 {
			totalItemsWidth++ // Space between items
		}
	}

	// Calculate available space for dock items
	availableSpace := screenWidth - layout.LeftWidth - layout.RightWidth - totalItemsWidth
	if availableSpace < 0 {
		// Items don't fit - need to truncate
		layout.truncateItems(screenWidth, allItems)
		return
	}

	// All items fit - calculate center positioning
	leftSpacer := max(availableSpace/2, 0)

	layout.CenterStartX = layout.LeftWidth + leftSpacer
	layout.VisibleItems = allItems
	layout.TruncatedCount = 0

	// Calculate position of each item
	currentX := layout.CenterStartX
	layout.ItemPositions = make([]ItemPosition, 0, len(allItems))

	for i, item := range allItems {
		// Add space before item (except first)
		if i > 0 {
			currentX++
		}

		layout.ItemPositions = append(layout.ItemPositions, ItemPosition{
			StartX:      currentX,
			EndX:        currentX + item.Width,
			WindowIndex: item.WindowIndex,
		})

		currentX += item.Width
	}
}

// truncateItems calculates which items fit when space is limited
func (layout *DockLayout) truncateItems(screenWidth int, allItems []DockItem) {
	const truncationIndicatorWidth = 4 // " ..." width

	// Calculate max width available for items
	maxItemsWidth := max(screenWidth-layout.LeftWidth-layout.RightWidth-truncationIndicatorWidth-4, 0)

	// Find how many complete items fit
	currentWidth := 0
	visibleCount := 0

	for i, item := range allItems {
		itemWidthWithSpace := item.Width
		if i > 0 {
			itemWidthWithSpace++ // Space before item
		}

		if currentWidth+itemWidthWithSpace <= maxItemsWidth {
			currentWidth += itemWidthWithSpace
			visibleCount++
		} else {
			break
		}
	}

	// Set visible items
	if visibleCount > 0 {
		layout.VisibleItems = allItems[:visibleCount]
	} else {
		layout.VisibleItems = []DockItem{}
	}
	layout.TruncatedCount = len(allItems) - visibleCount

	// Recalculate total width including truncation indicator
	totalWidth := currentWidth
	if layout.TruncatedCount > 0 {
		totalWidth += 1 + truncationIndicatorWidth // space + "..."
	}

	// Calculate center positioning
	availableSpace := screenWidth - layout.LeftWidth - layout.RightWidth - totalWidth
	leftSpacer := max(availableSpace/2, 0)

	layout.CenterStartX = layout.LeftWidth + leftSpacer

	// Calculate positions
	currentX := layout.CenterStartX
	layout.ItemPositions = make([]ItemPosition, 0, len(layout.VisibleItems))

	for i, item := range layout.VisibleItems {
		// Add space before item (except first)
		if i > 0 {
			currentX++
		}

		layout.ItemPositions = append(layout.ItemPositions, ItemPosition{
			StartX:      currentX,
			EndX:        currentX + item.Width,
			WindowIndex: item.WindowIndex,
		})

		currentX += item.Width
	}
}
