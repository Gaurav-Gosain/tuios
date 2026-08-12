package app

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
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
	// ModeLabel is the mode pill's text without its caps; the renderer decides
	// whether it wears any. TrailText is what rides after the strip (the
	// project-tape badge), styled as passive information.
	ModeLabel      string
	TrailText      string
	LeftWidth      int
	RightWidth     int
	TruncatedCount int        // Number of items that don't fit
	VisibleItems   []DockItem // Items that fit and should be displayed
	ModeInfo       ModeInfo   // Mode display information for styling
	WorkspaceStrip dockWorkspaceStrip
}

// dockWorkspaceTab is one workspace chip in the dock's clickable strip.
//
// Label and Width travel together on purpose. A chip used to be a digit, so its
// width was a function of its number and the renderer and the hit-testing could
// each derive it. A named workspace makes the width a function of the label, and
// two places deriving the same width from state that has since moved is exactly
// how a minimized dock entry came to be unclickable while the cell to its right
// worked. Both are computed once, here, and everything downstream reads them.
type dockWorkspaceTab struct {
	Workspace int
	Label     string
	Active    bool
	Width     int
	// Add marks the trailing "+" tab, which opens the next free workspace rather
	// than switching to an existing one.
	Add bool
}

// dockWorkspaceHit is where a tab was drawn on the last frame. Recorded by the
// renderer rather than recomputed by the mouse handler so the two dock paths
// (compositor layer and fullscreen fast path) can never disagree about it.
type dockWorkspaceHit struct {
	X0, X1, Y, Workspace int
}

// dockWorkspaceArrowHit is where an overflow arrow was drawn, and which way it
// steps the strip. Recorded alongside the pills for the same reason: the
// gutters exist only while the strip overflows, so their columns are a fact
// about the frame rather than something a handler can work out.
type dockWorkspaceArrowHit struct {
	X0, X1, Y, Delta int
}

// dockItemHit is where a minimized entry was drawn on the last frame, recorded
// for the same reason its neighbouring workspace tab is. The entries are
// centred against the room the left and right regions leave, so recomputing
// their columns means recomputing the whole bar and getting the same answer
// from state that has moved on since the frame was painted. The mode pill and
// the stats readout both change width on their own, which walked the clickable
// region off the entry the user could see.
type dockItemHit struct {
	X0, X1, Y, WindowIndex int
}

// dockWorkspacePillGap is the bare column between two pills. The pills carry a
// fill of their own, so the gap is what makes them read as separate things
// rather than as one banded run, and it belongs to neither pill's hit rect.
const dockWorkspacePillGap = 1

// dockWorkspaceArrowWidth is one overflow gutter: the arrow and the column
// separating it from the pills. Both gutters are held open for as long as the
// strip scrolls at all, so reaching an end does not reflow the pills under the
// pointer that just clicked.
const dockWorkspaceArrowWidth = 2

// workspacePillWidth is the column span of a pill carrying the given label: the
// label, a column of padding either side of it, and the caps when they are
// switched on. The span follows the label and nothing else, so a named
// workspace and a numbered one are measured by the same rule.
func workspacePillWidth(label string) int {
	lc, rc := config.GetDockPillLeftChar(), config.GetDockPillRightChar()
	return lipgloss.Width(lc) + lipgloss.Width(rc) + lipgloss.Width(label) + 2
}

// workspacePillLabelMax caps a name on a pill. The dock strip sits beside the
// mode pill and the minimized entries, and a workspace called after a branch
// would otherwise push both off the bar.
const workspacePillLabelMax = 12

// workspacePillLabel is what a pill prints: the workspace's name when it has
// one, else its number, laundered as chrome and capped.
func (m *OS) workspacePillLabel(n int) string {
	label := printableTitle(m.WorkspaceLabel(n))
	if label == "" {
		label = strconv.Itoa(n)
	}
	return overlay.Truncate(label, workspacePillLabelMax)
}

// occupiedWorkspaces lists the workspaces worth showing, in order: those
// holding a window, plus the current one even when it is empty. Shared by the
// dock strip and the rail band so the two name the same set.
func (m *OS) occupiedWorkspaces() []int {
	ws := make([]int, 0, m.NumWorkspaces)
	for i := 1; i <= m.NumWorkspaces; i++ {
		if i == m.CurrentWorkspace || m.GetWorkspaceWindowCount(i) > 0 {
			ws = append(ws, i)
		}
	}
	return ws
}

// buildDockWorkspaceTabs returns the dock's workspace strip: every occupied
// workspace plus the current one, in order. Fewer than two means there is
// nowhere a click could take you, so the strip stays off and the idle dock is
// exactly what it was.
func (m *OS) buildDockWorkspaceTabs() []dockWorkspaceTab {
	if !config.DockWorkspaceTabs {
		return nil
	}
	tabs := make([]dockWorkspaceTab, 0, m.NumWorkspaces)
	for _, n := range m.occupiedWorkspaces() {
		label := m.workspacePillLabel(n)
		tabs = append(tabs, dockWorkspaceTab{
			Workspace: n,
			Label:     label,
			Active:    n == m.CurrentWorkspace,
			Width:     workspacePillWidth(label),
		})
	}
	// A trailing "+" opens the next empty workspace, so making one is a click
	// rather than a remembered keybind. With it appended even a single-workspace
	// session has two tabs, which is what makes the strip worth showing at all.
	if next := m.nextFreeWorkspace(); next > 0 {
		tabs = append(tabs, dockWorkspaceTab{Add: true, Label: "+", Width: workspacePillWidth("+")})
	}
	if len(tabs) < 2 {
		return nil
	}
	return tabs
}

// nextFreeWorkspace is the lowest-numbered workspace holding no windows, or 0
// when every one is in use.
func (m *OS) nextFreeWorkspace() int {
	for i := 1; i <= m.NumWorkspaces; i++ {
		if i != m.CurrentWorkspace && m.GetWorkspaceWindowCount(i) == 0 {
			return i
		}
	}
	return 0
}

// dockWorkspaceTabsWidth is the width every tab would take laid out at once,
// including the gaps between them and the column separating the strip from the
// mode pill. Zero when the strip is off. This is what the strip wants; what it
// gets is the budget planDockWorkspaceStrip is handed.
func dockWorkspaceTabsWidth(tabs []dockWorkspaceTab) int {
	if len(tabs) == 0 {
		return 0
	}
	w := 1
	for i, t := range tabs {
		if i > 0 {
			w += dockWorkspacePillGap
		}
		w += t.Width
	}
	return w
}

// dockWorkspaceStrip is the strip as this frame will draw it: the run of pills
// that fits, whether there is more workspace either side of that run, and the
// pinned "+".
//
// The "+" is held out of the scrolling run on purpose. It is not a workspace,
// it is the control that makes one, and a scrolled-away "+" would leave the
// only way to add a workspace behind an arrow the user has no reason to press.
type dockWorkspaceStrip struct {
	Pills     []dockWorkspaceTab
	Add       *dockWorkspaceTab
	MoreLeft  bool
	MoreRight bool
	// Scrolls is set once the pills stopped fitting: both arrow gutters are then
	// held open and Inner is the fixed span the pills are drawn into, so the
	// "+" and the readout behind it do not walk about as the strip scrolls.
	Scrolls bool
	Inner   int
	Width   int
}

// pillsSpan is the width of tabs[from:to) laid out with their gaps.
func pillsSpan(tabs []dockWorkspaceTab, from, to int) int {
	w := 0
	for i := from; i < to; i++ {
		if i > from {
			w += dockWorkspacePillGap
		}
		w += tabs[i].Width
	}
	return w
}

// pillsFitting is how many pills from first fit in width cells, at least one so
// a pill wider than the whole viewport is still shown rather than the strip
// going blank.
func pillsFitting(tabs []dockWorkspaceTab, first, width int) int {
	n := 0
	for i := first; i < len(tabs); i++ {
		if pillsSpan(tabs, first, i+1) > width {
			break
		}
		n++
	}
	return max(n, 1)
}

// planDockWorkspaceStrip decides what the strip draws inside budget columns and
// records the scroll offset it settled on.
//
// The offset is only ever pulled back to the current workspace when that
// workspace has changed since the last frame. A switch by keyboard therefore
// scrolls the strip to the pill it just made active, while a user reading along
// the strip with the arrows keeps the run they scrolled to.
func (m *OS) planDockWorkspaceStrip(budget int) dockWorkspaceStrip {
	tabs := m.buildDockWorkspaceTabs()
	if len(tabs) == 0 {
		return dockWorkspaceStrip{}
	}

	strip := dockWorkspaceStrip{Pills: tabs}
	if last := tabs[len(tabs)-1]; last.Add {
		strip.Add = &last
		strip.Pills = tabs[:len(tabs)-1]
	}

	// The leading column and the "+" are spent before the pills get a look, so a
	// dock too narrow for the workspaces still carries the control that makes
	// one.
	addSpan := 0
	if strip.Add != nil {
		addSpan = dockWorkspacePillGap + strip.Add.Width
	}
	avail := budget - 1 - addSpan
	if avail <= 0 || len(strip.Pills) == 0 {
		strip.Pills = nil
		strip.Width = 0
		if strip.Add != nil && budget >= 1+strip.Add.Width {
			strip.Width = 1 + strip.Add.Width
		} else {
			strip.Add = nil
		}
		return strip
	}

	if natural := pillsSpan(strip.Pills, 0, len(strip.Pills)); natural <= avail {
		m.dockWorkspaceScroll = 0
		m.dockWorkspaceScrollFor = m.CurrentWorkspace
		strip.Width = 1 + natural + addSpan
		return strip
	}

	inner := avail - 2*dockWorkspaceArrowWidth
	if inner < 1 {
		// Room for the gutters and nothing to put between them: the arrows would
		// scroll a strip with no pills in it.
		strip.Pills = nil
		strip.Width = 1 + addSpan
		return strip
	}
	strip.Scrolls, strip.Inner = true, inner

	all := strip.Pills
	first := m.dockWorkspaceScroll
	if m.CurrentWorkspace != m.dockWorkspaceScrollFor {
		m.dockWorkspaceScrollFor = m.CurrentWorkspace
		first = scrollToShow(all, m.activePillIndex(all), first, inner)
	}
	first = min(max(first, 0), lastScrollOffset(all, inner))
	m.dockWorkspaceScroll = first

	count := pillsFitting(all, first, inner)
	strip.Pills = all[first : first+count]
	strip.MoreLeft = first > 0
	strip.MoreRight = first+count < len(all)
	strip.Width = 1 + 2*dockWorkspaceArrowWidth + inner + addSpan
	return strip
}

// activePillIndex is where the current workspace sits in the strip, or 0 when
// it is not in it.
func (m *OS) activePillIndex(pills []dockWorkspaceTab) int {
	for i, p := range pills {
		if p.Active {
			return i
		}
	}
	return 0
}

// scrollToShow is the smallest move from first that brings pill active into the
// viewport, scrolling back for a pill off the left end and forward for one off
// the right.
func scrollToShow(pills []dockWorkspaceTab, active, first, inner int) int {
	if active < first {
		return active
	}
	for first < active && first+pillsFitting(pills, first, inner) <= active {
		first++
	}
	return first
}

// lastScrollOffset is the furthest the strip can scroll: the first offset whose
// run reaches the final pill. Scrolling past it would open dead columns at the
// right-hand end.
func lastScrollOffset(pills []dockWorkspaceTab, inner int) int {
	for i := 0; i < len(pills); i++ {
		if pillsSpan(pills, i, len(pills)) <= inner {
			return i
		}
	}
	return max(len(pills)-1, 0)
}

// DockWorkspaceAt returns the workspace whose dock tab covers the absolute cell
// (x, y), or 0 when none does.
func (m *OS) DockWorkspaceAt(x, y int) int {
	for _, h := range m.dockWorkspaceHits {
		if y == h.Y && x >= h.X0 && x < h.X1 {
			if h.Workspace == 0 {
				// The "+" tab: resolve it now so a click lands on a real
				// workspace even if the strip was drawn a frame ago.
				return m.nextFreeWorkspace()
			}
			return h.Workspace
		}
	}
	return 0
}

// ScrollDockWorkspacesAt steps the strip when (x, y) is on one of its overflow
// arrows, and reports whether it was.
//
// A click steps one pill. The pills are as wide as the names on them, so a page
// is a different distance every time and would skip past the workspace the user
// was reaching for; one pill per click is the same gesture the arrows on a tab
// strip have anywhere else.
func (m *OS) ScrollDockWorkspacesAt(x, y int) bool {
	for _, h := range m.dockWorkspaceArrowHits {
		if y == h.Y && x >= h.X0 && x < h.X1 {
			// The upper bound belongs to the next layout pass, which knows how
			// many pills fit; here only the floor is knowable.
			m.dockWorkspaceScroll = max(m.dockWorkspaceScroll+h.Delta, 0)
			return true
		}
	}
	return false
}

// CalculateDockLayout calculates the layout for the dock including positions of all items.
// This function is shared between rendering (render.go) and mouse handling (mouse.go)
// to ensure consistent positioning.
func (m *OS) CalculateDockLayout() DockLayout {
	layout := DockLayout{}

	// Build left side text (compact format)
	layout.ModeLabel, layout.TrailText, layout.LeftWidth, layout.ModeInfo = m.buildDockLeftText()

	// The session controls hold the bar's right-hand end and never give any of it
	// up, so everything else is laid out against what they leave rather than
	// against the screen.
	barWidth := max(m.GetRenderWidth()-m.dockSessionStripWidth(), 0)

	// The workspace strip rides in the left region, so the dock items are laid
	// out against the room it leaves. Half the bar is the most it may take: past
	// that a session of named workspaces would push the minimized entries and
	// the meters off the dock, and a strip that scrolls costs one click to read
	// where a missing entry costs a search.
	budget := min(max(barWidth-layout.LeftWidth, 0), barWidth/2)
	layout.WorkspaceStrip = m.planDockWorkspaceStrip(budget)
	layout.LeftWidth += layout.WorkspaceStrip.Width

	// Calculate right side width. The estimate below is what the right block
	// would like; on a narrow screen it is capped at what the left block leaves,
	// otherwise the two together are wider than the dock and the right-hand end
	// (the system stats, or the copy-mode help) is drawn off the screen.
	layout.RightWidth = min(m.calculateDockRightWidth(), max(barWidth-layout.LeftWidth, 0))

	// Get all dock items
	allItems := m.getDockItems()

	// Calculate how many items fit and their positions
	layout.calculateItemPositions(barWidth, allItems)

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

// buildDockLeftText builds the dock's left region: the mode pill's label, the
// passive badges trailing it, the width the two claim, and the mode info the
// renderer styles them with.
func (m *OS) buildDockLeftText() (modeLabel, trail string, width int, modeInfo ModeInfo) {
	focusedWindow := m.GetFocusedWindow()

	// Build mode info (will be styled with colors in render.go)
	modeInfo = ModeInfo{
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

	switch {
	case m.SidebarFocused:
		// The rail owns the keyboard: the mode pill is the authoritative "who has
		// input" indicator, so it says so in accent, outranking the pane mode.
		modeInfo.Color = theme.ColorToString(theme.UI().Accent)
		modeLabel = "SIDEBAR"
	case m.Mode == TerminalMode:
		if focusedWindow.CopyModeVisible() {
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
	default:
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
	if focusedWindow != nil && focusedWindow.Zoomed && !m.SidebarFocused {
		modeLabel += " Z"
	}

	// The chip is a filled pill, so its background has to end the way it
	// starts. The icons above carry their own padding, and every suffix (the
	// split direction, the zoom flag) landed after the trailing space, leaving
	// the fill flush against the last glyph; the rail's label had none at all.
	// One cell either side, applied once, whatever the label ended up being.
	modeLabel = " " + strings.TrimSpace(modeLabel) + " "

	// What is left of the "2:3 • 5  3 " stats blob. The totals went: the strip
	// two cells to the right names every occupied workspace and marks the
	// current one, so "5 terminals across 3 workspaces" drove no decision and
	// cost two icons and a separator to say.
	//
	// The "<workspace>:<windows here>" readout stays, quiet and unbolded. It is
	// the one number the strip does not carry, and it is the dock's live count
	// of the current workspace, which the e2e harness reads as its source of
	// truth for how many panes exist. Cutting it too is a separate change that
	// needs that harness to grow another way to ask.
	trail = fmt.Sprintf(" %d:%d ", m.CurrentWorkspace, m.GetWorkspaceWindowCount(m.CurrentWorkspace))

	// Passive project-tape badge: when the focused window is inside a directory
	// carrying a .tuios.tape, a small status marker rides in the dock. It is
	// informational only; it opens no dialog and runs nothing.
	if badge := m.tapeDockBadge(); badge != "" {
		trail += badge + " "
	}

	// Rendered width, not byte length: Nerd Font glyphs and the caps are wider
	// than their bytes. +4 for margins/padding.
	width = lipgloss.Width(config.GetDockPillLeftChar()) +
		lipgloss.Width(modeLabel) +
		lipgloss.Width(config.GetDockPillRightChar()) +
		lipgloss.Width(trail) + 4

	return modeLabel, trail, width, modeInfo
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

	if focusedWindow.CopyModeVisible() {
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

		// Get window name (only custom names), laundered for the dock as chrome.
		windowName := printableTitle(window.CustomName)

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

	// All items fit.
	layout.VisibleItems = allItems
	layout.TruncatedCount = 0
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
}
