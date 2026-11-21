package app

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// DockItem represents a single item in the dock
type DockItem struct {
	WindowIndex int
	Label       string
	Width       int // Total width including circles
	// Number and Name are what the label was built from, so the layout can
	// rebuild it at a shorter length when the bar runs out of room.
	Number int
	Name   string
}

// DockLayout contains calculated layout information for the dock
type DockLayout struct {
	// ModeLabel is the mode pill's text without its caps; the renderer decides
	// whether it wears any. TrailText is the workspace readout and TapeText the
	// project-tape badge, both styled as passive information. They are separate
	// because they are separate components now: the badge used to be glued onto
	// the readout, so neither could be moved or dropped without the other.
	ModeLabel  string
	TrailText  string
	TapeText   string
	LeftWidth  int
	RightWidth int
	// CenterRightReserve is the room a custom center keeps on its right: the
	// right block's resting width, and at least a notification's mark and
	// opening gap, so a message never resizes or moves the center.
	CenterRightReserve int
	// CustomLeft, CustomCenter and CustomRight are the room the custom cells on each side asked for, so the
	// layout pass and the draw pass reserve against the same number.
	CustomLeft     int
	CustomCenter   int
	CustomRight    int
	TruncatedCount int        // Number of items that don't fit
	VisibleItems   []DockItem // Items that fit and should be displayed
	ModeInfo       ModeInfo   // Mode display information for styling
	WorkspaceStrip dockWorkspaceStrip
}

type dockWorkspaceTab struct {
	Workspace int
	Label     string
	Active    bool
	Dragged   bool
	Width     int
	Gap       int
	Add       bool
	Attention bool
}

type dockWorkspaceHit struct {
	X0, X1, Y, Workspace int
}

type dockWorkspaceArrowHit struct {
	X0, X1, Y, Delta int
}

type dockItemHit struct {
	X0, X1, Y, WindowIndex int
}

type dockOverflowHit struct {
	Active     bool
	X0, X1, Y  int
	Overflowed int
}

const dockWorkspacePillGap = 1

const dockWorkspaceArrowWidth = 2

func workspacePillWidth(label string, s *config.Settings) int {
	lc, rc := s.GetDockWorkspaceCapLeft(), s.GetDockWorkspaceCapRight()
	return lipgloss.Width(lc) + lipgloss.Width(rc) + lipgloss.Width(label) + 2
}

func (m *OS) dockTabs() config.DockTabsConfig {
	if m.UserConfig == nil {
		return config.DockTabsConfig{}
	}
	return m.UserConfig.Dock.Tabs
}

func (m *OS) dockTabWidth(t *config.DockTabsConfig, label string) int {
	if t.Flat() && !t.Caps {
		return lipgloss.Width(label) + 2
	}
	return workspacePillWidth(label, &m.Settings)
}

func dockTabGap(t *config.DockTabsConfig) int {
	if t.Flat() && t.Separator != "" {
		return lipgloss.Width(t.Separator)
	}
	return dockWorkspacePillGap
}

func (m *OS) workspaceTabText(tabs *config.DockTabsConfig, n int) string {
	zoom := ""
	if n == m.dockWorkspace() && tabs.ZoomMarker != "" && m.zoomedWindow() != nil {
		zoom = tabs.ZoomMarker
	}
	return m.Settings.FormatWorkspaceTab(printableTitle(m.workspaceGivenName(n)), n, tabs.Icon(n), zoom)
}

func (m *OS) workspacePillName(n int) string {
	label := printableTitle(m.WorkspaceLabel(n))
	if label == "" {
		label = strconv.Itoa(m.workspaceDisplayNumber(n))
	}
	return label
}

func (m *OS) workspacePillLabel(n int) string {
	tabs := m.dockTabs()
	return m.workspacePillLabelWith(&tabs, n)
}

func (m *OS) workspacePillLabelWith(tabs *config.DockTabsConfig, n int) string {
	label := m.workspaceTabText(tabs, n)
	if limit := m.Settings.DockWorkspaceLabelMax; limit > 0 {
		label = overlay.Truncate(label, limit)
	}
	if mark := m.workspaceMarks(tabs, n); mark != "" {
		label += mark
	}
	return label
}

func (m *OS) workspacePillClipped(n int) bool {
	limit := m.Settings.DockWorkspaceLabelMax
	if limit <= 0 {
		return false
	}
	tabs := m.dockTabs()
	return lipgloss.Width(m.workspaceTabText(&tabs, n)) > limit
}

func (m *OS) workspacePillHidesName(n int) bool {
	if m.workspacePillClipped(n) {
		return true
	}
	format := m.Settings.DockWorkspaceTabFormat
	if format == "" || strings.Contains(format, "{name}") {
		return false
	}
	return m.workspacePillName(n) != strconv.Itoa(m.workspaceDisplayNumber(n)) || !strings.Contains(format, "{index}")
}

func (m *OS) occupiedWorkspaceNumbers() []int {
	ws := make([]int, 0, m.NumWorkspaces)
	for i := 1; i <= m.NumWorkspaces; i++ {
		if i == m.dockWorkspace() || m.GetWorkspaceWindowCount(i) > 0 {
			ws = append(ws, i)
		}
	}
	return ws
}

func (m *OS) occupiedWorkspaces() []int {
	return m.workspaceDisplayOrder(m.occupiedWorkspaceNumbers())
}

func (m *OS) buildDockWorkspaceTabs() []dockWorkspaceTab {
	if !m.Settings.DockWorkspaceTabs {
		return nil
	}
	style := m.dockTabs()
	tabs := make([]dockWorkspaceTab, 0, m.NumWorkspaces)
	gap := dockTabGap(&style)
	for _, n := range m.occupiedWorkspaces() {
		label := m.workspacePillLabelWith(&style, n)
		tabs = append(tabs, dockWorkspaceTab{
			Workspace: n,
			Label:     label,
			Active:    n == m.dockWorkspace(),
			Dragged:   m.dockWorkspaceDrag.Dragging && n == m.dockWorkspaceDrag.Workspace,
			Width:     m.dockTabWidth(&style, label),
			Gap:       gap,
			Attention: style.AttentionFg != "" && n != m.dockWorkspace() && m.workspaceWantsAttention(n),
		})
	}
	flat := style.Flat()
	if next := m.nextFreeWorkspace(); next > 0 && (!flat || style.ShowAdd) {
		tabs = append(tabs, dockWorkspaceTab{Add: true, Label: "+", Width: m.dockTabWidth(&style, "+"), Gap: gap})
	}
	if len(tabs) < 2 && (!flat || len(tabs) == 0) {
		return nil
	}
	return tabs
}

func (m *OS) nextFreeWorkspace() int {
	for i := 1; i <= m.NumWorkspaces; i++ {
		if i != m.dockWorkspace() && m.GetWorkspaceWindowCount(i) == 0 {
			return i
		}
	}
	return 0
}

func dockWorkspaceTabsWidth(tabs []dockWorkspaceTab) int {
	if len(tabs) == 0 {
		return 0
	}
	w := 1
	for i, t := range tabs {
		if i > 0 {
			w += t.Gap
		}
		w += t.Width
	}
	return w
}

type dockWorkspaceStrip struct {
	Pills     []dockWorkspaceTab
	Add       *dockWorkspaceTab
	MoreLeft  bool
	MoreRight bool
	Scrolls   bool
	Inner     int
	Width     int
}

func pillsSpan(tabs []dockWorkspaceTab, from, to int) int {
	w := 0
	for i := from; i < to; i++ {
		if i > from {
			w += tabs[i].Gap
		}
		w += tabs[i].Width
	}
	return w
}

func pillsFitting(tabs []dockWorkspaceTab, first, width int) int {
	n := 0
	for i := first; i < len(tabs); i++ {
		if pillsSpan(tabs, first, i+1) > width {
			break
		}
		n++
	}
	return n
}

func (m *OS) planDockWorkspaceStrip(room, barWidth int) dockWorkspaceStrip {
	m.ensureDockPlan()
	if !m.dockPlan.Has(config.DockComponentWorkspaces) {
		return dockWorkspaceStrip{}
	}
	tabs := m.buildDockWorkspaceTabs()
	if len(tabs) == 0 {
		return dockWorkspaceStrip{}
	}

	budget := min(dockWorkspaceTabsWidth(tabs), room)
	if budget > barWidth*2/3 {
		budget = min(barWidth/2, room)
	}

	strip := dockWorkspaceStrip{Pills: tabs}
	if last := tabs[len(tabs)-1]; last.Add {
		strip.Add = &last
		strip.Pills = tabs[:len(tabs)-1]
	}

	addSpan := 0
	if strip.Add != nil {
		addSpan = strip.Add.Gap + strip.Add.Width
	}
	avail := budget - 1 - addSpan
	if avail <= 0 || len(strip.Pills) == 0 {
		return m.addOnlyStrip(strip, budget)
	}

	if natural := pillsSpan(strip.Pills, 0, len(strip.Pills)); natural <= avail {
		m.dockWorkspaceScroll, m.dockWorkspaceScrollFor, m.dockWorkspaceScrollAt = 0, m.dockWorkspace(), 0
		strip.Width = 1 + natural + addSpan
		return strip
	}

	inner := avail - 2*dockWorkspaceArrowWidth
	if inner < 1 {
		return m.addOnlyStrip(strip, budget)
	}
	strip.Scrolls, strip.Inner = true, inner

	all := strip.Pills
	first := m.dockWorkspaceScroll
	if m.dockWorkspace() != m.dockWorkspaceScrollFor || inner != m.dockWorkspaceScrollAt {
		m.dockWorkspaceScrollFor, m.dockWorkspaceScrollAt = m.dockWorkspace(), inner
		first = scrollToShow(all, m.activePillIndex(all), first, inner)
	}
	first = min(max(first, 0), lastScrollOffset(all, inner))
	m.dockWorkspaceScroll = first

	count := pillsFitting(all, first, inner)
	if count == 0 {
		return m.pinCurrentPill(strip, inner, addSpan, budget)
	}
	strip.Pills = all[first : first+count]
	strip.MoreLeft = first > 0
	strip.MoreRight = first+count < len(all)
	strip.Width = 1 + 2*dockWorkspaceArrowWidth + inner + addSpan
	return strip
}

func (m *OS) addOnlyStrip(strip dockWorkspaceStrip, budget int) dockWorkspaceStrip {
	strip.Pills, strip.Scrolls, strip.Width = nil, false, 0
	switch {
	case strip.Add == nil:
	case budget >= 1+strip.Add.Width:
		strip.Width = 1 + strip.Add.Width
	default:
		strip.Add = nil
	}
	return strip
}

func (m *OS) pinCurrentPill(strip dockWorkspaceStrip, inner, addSpan, budget int) dockWorkspaceStrip {
	style := m.dockTabs()
	labelBudget := inner - m.dockTabWidth(&style, "")
	if labelBudget < 1 || len(strip.Pills) == 0 {
		return m.addOnlyStrip(strip, budget)
	}
	active := m.activePillIndex(strip.Pills)
	pill := strip.Pills[active]
	pill.Label = overlay.Truncate(pill.Label, labelBudget)
	pill.Width = m.dockTabWidth(&style, pill.Label)
	strip.Pills = []dockWorkspaceTab{pill}
	strip.MoreLeft, strip.MoreRight = false, false
	strip.Width = 1 + 2*dockWorkspaceArrowWidth + inner + addSpan
	return strip
}

func (m *OS) activePillIndex(pills []dockWorkspaceTab) int {
	for i, p := range pills {
		if p.Active {
			return i
		}
	}
	return 0
}

func scrollToShow(pills []dockWorkspaceTab, active, first, inner int) int {
	if active < first {
		return active
	}
	for first < active && first+pillsFitting(pills, first, inner) <= active {
		first++
	}
	return first
}

func lastScrollOffset(pills []dockWorkspaceTab, inner int) int {
	for i := range pills {
		if pillsSpan(pills, i, len(pills)) <= inner {
			return i
		}
	}
	return max(len(pills)-1, 0)
}

// DockWorkspacePillAt returns the workspace of the pill covering the absolute
// cell (x, y), or 0. Unlike DockWorkspaceAt it does not resolve the trailing "+"
// tab: that tab stands for a workspace that does not exist yet, and there is
// nothing there to rename.
func (m *OS) DockWorkspacePillAt(x, y int) int {
	for _, h := range m.dockWorkspaceHits {
		if y == h.Y && x >= h.X0 && x < h.X1 {
			return h.Workspace
		}
	}
	return 0
}

// DockWorkspaceAt returns the workspace whose dock tab covers the absolute cell
// (x, y), or 0 when none does.
func (m *OS) DockWorkspaceAt(x, y int) int {
	for _, h := range m.dockWorkspaceHits {
		if y == h.Y && x >= h.X0 && x < h.X1 {
			if h.Workspace == 0 {
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
	m.ensureDockPlan()
	layout := DockLayout{}

	layout.ModeLabel, layout.TrailText, layout.TapeText, layout.LeftWidth, layout.ModeInfo = m.buildDockLeftText()

	layout.CustomLeft = m.dockCustomWidth(m.dockPlan.Left)
	layout.CustomCenter = m.dockCustomWidth(m.dockPlan.Center)
	layout.CustomRight = m.dockCustomWidth(m.dockPlan.Right)
	layout.LeftWidth += layout.CustomLeft

	barWidth := max(m.GetRenderWidth()-m.dockSessionStripWidth(), 0)

	layout.WorkspaceStrip = m.planDockWorkspaceStrip(max(barWidth-layout.LeftWidth, 0), barWidth)
	layout.LeftWidth += layout.WorkspaceStrip.Width

	allItems := m.getDockItems()

	room := max(barWidth-layout.LeftWidth, 0)
	want, yields := m.dockRightWidth()
	if layout.CustomCenter > 0 {
		persistent := want
		if !yields {
			persistent = m.calculateDockRightWidth()
		}
		if m.dockPlan.Has(config.DockComponentNotifications) {
			persistent = max(persistent, notifChromeWidth+1)
		}
		layout.CenterRightReserve = min(persistent, room)
		items := layout
		items.LeftWidth += layout.CustomCenter
		items.RightWidth = layout.CenterRightReserve
		items.calculateItemPositions(barWidth, allItems)
		layout.VisibleItems, layout.TruncatedCount = items.VisibleItems, items.TruncatedCount
		layout.RightWidth = min(want, room)
		return layout
	}
	if yields {
		if dockItemsWidth(allItems) > max(room-want, 0) &&
			!shortenDockItemNames(max(room-want, 0), allItems) {
			want = m.dockButtonsWidth(dockButtonsFit{compact: true})
		}
		room = max(room-dockItemsWidth(allItems), 0)
	}
	layout.RightWidth = min(want, room)

	layout.calculateItemPositions(barWidth, allItems)

	return layout
}

// ModeInfo contains mode display information
type ModeInfo struct {
	Block     string      // The character to display (e.g., "█")
	Color     color.Color // The pill's fill: a slot at 16 colours, so its ink is picked by slot
	CursorPos string      // Cursor position for copy mode (empty otherwise)
	IsTiling  bool        // Whether tiling mode is active
	NextSplit string      // Next split direction when tiling ("V" or "H")
}

func (m *OS) prefixArmed() bool {
	return m.PrefixActive || m.SubMenu != SubMenuNone || m.Menu != ""
}

func (m *OS) tiledModeLabel(modeIcon, nextSplit string) string {
	if !m.AutoTiling {
		return modeIcon
	}
	icon := m.Settings.GetDockModeIconTiling()
	if m.Settings.DockModePill == "input" {
		icon = modeIcon
	}
	if nextSplit != "" && icon != "" && !strings.HasSuffix(icon, " ") {
		icon += " "
	}
	return icon + nextSplit
}

func (m *OS) prefixOnPill() bool {
	return m.prefixArmed() && m.prefixPillShown()
}

func (m *OS) prefixPillShown() bool {
	if m.Settings.DockPrefixIndicator == "badge" || m.Settings.DockbarPosition == "hidden" {
		return false
	}
	m.ensureDockPlan()
	return m.dockPlan.Has(config.DockComponentMode)
}

func withLockIcon(label, icon string) string {
	return strings.TrimRight(label, " ") + " " + icon
}

func (m *OS) buildDockLeftText() (modeLabel, trail, tape string, width int, modeInfo ModeInfo) {
	m.ensureDockPlan()
	focusedWindow := m.GetFocusedWindow()

	modeInfo = ModeInfo{
		Block:    "█",
		IsTiling: m.AutoTiling,
	}

	if m.AutoTiling && m.Settings.DockModeSplitLetter {
		modeInfo.NextSplit = m.nextSplitIndicator(focusedWindow)
	}

	switch {
	case m.prefixOnPill():
		modeInfo.Color = theme.UI().Warning
		modeLabel = m.Settings.GetDockModeIconPrefix()
		if m.lockPillShown() {
			modeLabel = withLockIcon(modeLabel, m.Settings.GetDockLockIcon(false))
		}
	case m.Nested() && (focusedWindow == nil || !focusedWindow.CopyModeVisible()):
		modeInfo.Color = theme.UI().Info
		modeLabel = m.nestLabel()
	case m.SidebarFocused:
		modeInfo.Color = theme.UI().Accent
		modeLabel = "SIDEBAR"
	case m.HoldModeActive():
		modeInfo.Color = theme.DockColorWindow()
		modeLabel = m.Settings.GetDockModeIconWindow() + " HOLD"
	case m.hints != nil:
		modeInfo.Color = theme.DockColorCopy()
		modeLabel = "HINTS"
	case focusedWindow != nil && m.MultiCopy.Has(focusedWindow.ID):
		modeInfo.Color = theme.DockColorMultifocus()
		modeLabel, _ = m.multiCopyPill()
	case m.Mode == TerminalMode:
		if focusedWindow.CopyModeVisible() {
			modeInfo.Color = theme.DockColorCopy()
			modeInfo.CursorPos = fmt.Sprintf("%d:%d", focusedWindow.CopyMode.CursorY, focusedWindow.CopyMode.CursorX)
			modeLabel = " " + modeInfo.CursorPos + " "

		} else {
			modeInfo.Color = theme.DockColorTerminal()
			modeLabel = m.tiledModeLabel(m.Settings.GetDockModeIconTerminal(), modeInfo.NextSplit)
			if m.Locked() {
				modeLabel = withLockIcon(modeLabel, m.Settings.GetDockLockIcon(true))
			}
		}
	default:
		modeInfo.Color = theme.DockColorWindow()
		modeLabel = m.tiledModeLabel(m.Settings.GetDockModeIconWindow(), modeInfo.NextSplit)
	}

	if m.zoomedWindow() != nil && !m.SidebarFocused {
		modeLabel += " Z"
	}

	if trimmed := strings.TrimSpace(modeLabel); trimmed != "" {
		modeLabel = " " + trimmed + " "
	} else {
		modeLabel = ""
	}

	ws := m.dockWorkspace()
	trail = fmt.Sprintf(" %d:%d ", m.workspaceDisplayNumber(ws), m.GetWorkspaceWindowCount(ws))

	if badge := m.tapeDockBadge(); badge != "" {
		tape = badge + " "
	}

	if m.dockPlan.Has(config.DockComponentMode) && modeLabel != "" {
		width += lipgloss.Width(m.Settings.GetDockModeCapLeft()) +
			lipgloss.Width(modeLabel) +
			lipgloss.Width(m.Settings.GetDockModeCapRight())
	} else {
		modeLabel = ""
	}
	if m.dockPlan.Has(config.DockComponentTrail) {
		width += lipgloss.Width(trail)
	} else {
		trail = ""
	}
	if note := m.hostLinkNote(); note != "" {
		width -= lipgloss.Width(trail)
		trail = note
		width += lipgloss.Width(trail)
	}
	if m.dockPlan.Has(config.DockComponentTape) {
		width += lipgloss.Width(tape)
	} else {
		tape = ""
	}
	width += m.spotlightChipWidth()
	width += lipgloss.Width(m.calmChip())
	width += lipgloss.Width(m.keyModeChip())
	width += 4

	return modeLabel, trail, tape, width, modeInfo
}

func dockItemsWidth(items []DockItem) int {
	w := 0
	for i, it := range items {
		if i > 0 {
			w++
		}
		w += it.Width
	}
	return w
}

func (m *OS) dockRightWidth() (width int, yields bool) {
	if block, ok := m.dockNotificationBlock(m.GetRenderWidth(), 0); ok {
		return block.Width, false
	}
	if legend := m.dockModeLegend(); len(legend) > 0 {
		return modeLegendWidth(legend), false
	}
	return m.calculateDockRightWidth(), true
}

func (m *OS) dockModeLegend() []overlay.Hint {
	m.ensureDockPlan()
	if !m.dockPlan.Has(config.DockComponentCopyHelp) {
		return nil
	}
	return m.modeLegend()
}

func (m *OS) calculateDockRightWidth() int {
	m.ensureDockPlan()

	parts := m.dockMeterParts()
	custom := m.dockCustomWidth(m.dockPlan.Right) + m.dockButtonsWidth(dockButtonsFit{shown: len(m.dockButtonRow())})
	if len(parts) == 0 {
		return custom
	}
	return custom + lipgloss.Width(strings.Join(parts, " ")) + dockSysInfoMargin
}

func (m *OS) dockMeterParts() []string {
	m.ensureDockPlan()
	var parts []string
	if m.Settings.ShowCPU && m.dockPlan.Has(config.DockComponentCPU) {
		parts = append(parts, m.GetCPUGraph())
	}
	if m.Settings.ShowRAM && m.dockPlan.Has(config.DockComponentRAM) {
		parts = append(parts, m.GetRAMUsage())
	}
	return parts
}

func (m *OS) dockNotificationBlock(barWidth, room int) (notifBlock, bool) {
	m.ensureDockPlan()
	if !m.dockPlan.Has(config.DockComponentNotifications) {
		return notifBlock{}, false
	}
	return m.renderNotificationBlock(barWidth, room)
}

const dockSysInfoMargin = 2

const dockItemMinNameCells = 6

func dockItemLabel(number int, name string) string {
	if name = printableTitle(name); name != "" {
		return fmt.Sprintf(" %d:%s ", number, name)
	}
	return fmt.Sprintf(" %d ", number)
}

func (m *OS) getDockItems() []DockItem {
	m.ensureDockPlan()
	if !m.dockPlan.Has(config.DockComponentWindows) {
		return nil
	}
	dockWindows := m.MinimizedInOrder()

	items := make([]DockItem, 0, len(dockWindows))
	itemNumber := 1

	for _, windowIndex := range dockWindows {
		window := m.Windows[windowIndex]
		labelText := dockItemLabel(itemNumber, window.CustomName)

		itemWidth := lipgloss.Width(m.Settings.GetDockPillLeftChar()) +
			lipgloss.Width(labelText) +
			lipgloss.Width(m.Settings.GetDockPillRightChar())

		items = append(items, DockItem{
			WindowIndex: windowIndex,
			Label:       labelText,
			Width:       itemWidth,
			Number:      itemNumber,
			Name:        printableTitle(window.CustomName),
		})

		itemNumber++
	}

	return items
}

func (layout *DockLayout) calculateItemPositions(screenWidth int, allItems []DockItem) {
	if dockItemsWidth(allItems) <= max(screenWidth-layout.LeftWidth-layout.RightWidth, 0) {
		layout.VisibleItems = allItems
		layout.TruncatedCount = 0
		return
	}
	if shortenDockItemNames(max(screenWidth-layout.LeftWidth-layout.RightWidth, 0), allItems) {
		layout.VisibleItems = allItems
		layout.TruncatedCount = 0
		return
	}

	layout.truncateItems(screenWidth, allItems)
}

func shortenDockItemNames(available int, allItems []DockItem) bool {
	entryWidthAt := func(item DockItem, b int) int {
		name := overlay.Truncate(item.Name, b)
		return item.Width - lipgloss.Width(item.Label) + lipgloss.Width(dockItemLabel(item.Number, name))
	}
	named := 0
	for _, item := range allItems {
		if item.Name != "" {
			named++
		}
	}
	if named == 0 {
		return false
	}
	longest := 0
	for _, item := range allItems {
		longest = max(longest, lipgloss.Width(item.Name))
	}
	totalAt := func(b int) int {
		total := 0
		for i, item := range allItems {
			if i > 0 {
				total++
			}
			total += entryWidthAt(item, b)
		}
		return total
	}
	lo, hi := dockItemMinNameCells, longest
	if hi < lo || totalAt(lo) > available {
		return false
	}
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if totalAt(mid) <= available {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	budget := lo
	for i := range allItems {
		item := &allItems[i]
		label := dockItemLabel(item.Number, overlay.Truncate(item.Name, budget))
		item.Width += lipgloss.Width(label) - lipgloss.Width(item.Label)
		item.Label = label
	}
	return true
}

func (layout *DockLayout) truncateItems(screenWidth int, allItems []DockItem) {
	const truncationIndicatorWidth = 4

	maxItemsWidth := max(screenWidth-layout.LeftWidth-layout.RightWidth-truncationIndicatorWidth-4, 0)

	currentWidth := 0
	visibleCount := 0

	for i, item := range allItems {
		itemWidthWithSpace := item.Width
		if i > 0 {
			itemWidthWithSpace++
		}

		if currentWidth+itemWidthWithSpace <= maxItemsWidth {
			currentWidth += itemWidthWithSpace
			visibleCount++
		} else {
			break
		}
	}

	if visibleCount > 0 {
		layout.VisibleItems = allItems[:visibleCount]
	} else {
		layout.VisibleItems = []DockItem{}
	}
	layout.TruncatedCount = len(allItems) - visibleCount
}

func (m *OS) nextSplitIndicator(focused *terminal.Window) string {
	switch m.PreselectionDir {
	case layout.PreselectionLeft, layout.PreselectionRight:
		return "V"
	case layout.PreselectionUp, layout.PreselectionDown:
		return "H"
	}
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil {
		return "V"
	}
	focusedIntID := 0
	if focused != nil {
		focusedIntID = m.WindowToBSPID[focused.ID]
	}
	return tree.NextSplitDirection(focusedIntID, m.GetBSPBounds(), m.separatorGap())
}
