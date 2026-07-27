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

	// Calculate right side width. The estimate below is what the right block
	// would like; on a narrow screen it is capped at what the left block leaves,
	// otherwise the two together are wider than the dock and the right-hand end
	// (the system stats, or the copy-mode help) is drawn off the screen.
	layout.RightWidth = min(m.calculateDockRightWidth(), max(m.GetRenderWidth()-layout.LeftWidth, 0))

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

	// Add zoom indicator
	if focusedWindow != nil && focusedWindow.Zoomed {
		modeLabel += " Z"
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

	// Passive project-tape badge: when the focused window is inside a directory
	// carrying a .tuios.tape, a small status marker rides in the dock. It is
	// informational only; it opens no dialog and runs nothing.
	tapeBadge := ""
	if badge := m.tapeDockBadge(); badge != "" {
		tapeBadge = " " + badge + " "
	}

	// Combine mode and workspace
	leftText := modeText + workspaceText + tapeBadge

	// Calculate actual rendered width (handles Unicode, Nerd Fonts, etc.)
	// Use lipgloss.Width instead of len() to get proper display width
	width := lipgloss.Width(modeText) + lipgloss.Width(workspaceText) + lipgloss.Width(tapeBadge) + 4 // +4 for margins/padding

	return leftText, width, modeInfo
}

// copyModeHelpTexts returns the dock's copy-mode help line for a sub-state,
// longest first. The renderer takes the longest one that fits the room the dock
// has; on a narrow screen that is the shortest of them, which still names the
// keys that matter. Shared with the width calculation so the space reserved
// matches the line actually drawn.
func copyModeHelpTexts(state terminal.CopyModeState) []string {
	switch state {
	case terminal.CopyModeNormal:
		return []string{
			"hjkl:move w/b/e:word f/F/t/T:char /:search n/N:next/prev C-l:clear ;,:repeat v:visual y:yank i:term q:quit",
			"hjkl:move w/b/e:word /:search v:visual y:yank q:quit",
			"hjkl /:search v y q",
		}
	case terminal.CopyModeSearch:
		return []string{
			"Type to search  n/N:next/prev  Enter:done  Esc:cancel",
			"n/N:next  Enter:done  Esc:cancel",
		}
	case terminal.CopyModeVisualChar:
		return []string{
			"hjkl:extend w/b/e:word f/F/t/T:char ;,:repeat {/}:para %:bracket y:yank Esc:cancel",
			"hjkl:extend w/b/e:word y:yank Esc:cancel",
			"hjkl y:yank Esc",
		}
	case terminal.CopyModeVisualLine:
		return []string{"jk:extend  y:yank  Esc:cancel", "jk y Esc"}
	}
	return nil
}

// calculateDockRightWidth calculates the width of the right side of the dock
func (m *OS) calculateDockRightWidth() int {
	// A live message owns the right-hand block, ahead of the copy-mode help
	// line and the system meters both. It is measured here rather than only at
	// render time so the dock items are laid out against the room the message
	// actually takes, and so mouse hit-testing (which shares this layout) agrees
	// with what is drawn.
	//
	// This is also the fix for a message pushed while copy mode was active being
	// silently dropped. The help line used to hold the block unconditionally, so
	// the message was not crowded out, it was never rendered at all: a copy of
	// something that failed, which is when a message matters most, went nowhere.
	if block, ok := m.renderNotificationBlock(m.GetRenderWidth(), 0); ok {
		return block.Width
	}

	focusedWindow := m.GetFocusedWindow()
	inCopyMode := focusedWindow != nil && focusedWindow.CopyMode != nil && focusedWindow.CopyMode.Active

	if inCopyMode {
		// In copy mode the help line is the right-hand block. Measure the
		// longest variant rather than guessing at it, so a terminal with room
		// for it reserves exactly enough and one without falls to a shorter
		// line instead of being one cell short of the full one.
		texts := copyModeHelpTexts(focusedWindow.CopyMode.State)
		if len(texts) == 0 {
			return 32
		}
		return lipgloss.Width(texts[0]) + 2 // the help style's own padding
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
