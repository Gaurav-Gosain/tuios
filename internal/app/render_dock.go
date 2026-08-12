package app

import (
	"image/color"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// workspaceChip renders one workspace digit for the dock strip or the rail
// band. Inactive is a bare dim digit; active is an accent digit, bold and
// underlined, on no fill of its own. An inverse fill is the loudest mark the
// grammar has and it was being spent on the thing the user already knows; the
// underline says the same in one attribute, survives ASCII mode and monochrome
// (both are attributes, not glyphs), and leaves the mode chip as the bar's only
// filled element. Both states pad to the caps' widths, so every chip is exactly
// workspaceChipWidth wide whatever its state. rowBg is what sits behind the
// chip (nil for the bare terminal background), fg the resting digit color.
func workspaceChip(n int, active bool, fg, rowBg color.Color) string {
	lc, rc := workspaceChipCaps()
	digit := strconv.Itoa(n)
	pad := func(s string) string { return sidebarStyle(rowBg, nil).Render(strings.Repeat(" ", lipgloss.Width(s))) }
	if !active {
		return sidebarStyle(rowBg, fg).Render(
			strings.Repeat(" ", lipgloss.Width(lc)) + digit + strings.Repeat(" ", lipgloss.Width(rc)))
	}
	// The pill caps go with the fill they capped: a half-circle around bare
	// canvas is a glyph with nothing behind it. They keep their columns so the
	// strip does not reflow when the current workspace moves along it.
	body := sidebarStyle(rowBg, theme.UI().Accent).Bold(true).Underline(true)
	return pad(lc) + body.Render(digit) + pad(rc)
}

// renderDockWorkspaceTabs styles the workspace strip starting at column startX
// and records each tab's screen span into m.dockWorkspaceHits.
func (m *OS) renderDockWorkspaceTabs(tabs []dockWorkspaceTab, startX int) string {
	m.dockWorkspaceHits = m.dockWorkspaceHits[:0]
	if len(tabs) == 0 {
		return ""
	}

	pal := theme.UI()
	y := m.GetDockbarContentYPosition()

	var b strings.Builder
	b.WriteString(" ")
	x := startX + 1
	for _, t := range tabs {
		if t.Add {
			b.WriteString(workspaceAddChip(pal.FgMute, nil))
		} else {
			b.WriteString(workspaceChip(t.Workspace, t.Active, pal.FgMute, nil))
		}
		m.dockWorkspaceHits = append(m.dockWorkspaceHits, dockWorkspaceHit{
			X0: x, X1: x + t.Width, Y: y, Workspace: t.Workspace,
		})
		x += t.Width
	}
	return b.String()
}

// workspaceAddChip renders the strip's trailing "+", padded to the same caps as
// a digit chip so the row stays evenly spaced.
func workspaceAddChip(fg, rowBg color.Color) string {
	lc, rc := workspaceChipCaps()
	return sidebarStyle(rowBg, fg).Render(
		strings.Repeat(" ", lipgloss.Width(lc)) + "+" + strings.Repeat(" ", lipgloss.Width(rc)))
}

func (m *OS) renderDock() *lipgloss.Layer {
	fullDock, dockbarYPos := m.renderDockString()
	return lipgloss.NewLayer(fullDock).X(0).Y(dockbarYPos).Z(config.ZIndexDock).ID("dock")
}

// renderDockString returns the dock content and its top row, used both by the
// layer path and the fullscreen fast path.
func (m *OS) renderDockString() (string, int) {
	layout := m.CalculateDockLayout()
	pal := theme.UI()

	sysInfoStyle := lipgloss.NewStyle().
		Foreground(pal.FgMute).
		MarginRight(2)

	// The mode label arrives without its caps, so the pill is assembled here
	// rather than found again by searching the string for the cap glyphs. It is
	// the dock's only filled element, so it is the only one that has to earn its
	// contrast: the foreground is picked against whatever colour the mode is,
	// and bold buys the last of the legibility a saturated fill costs.
	modeColor := lipgloss.Color(layout.ModeInfo.Color)
	fill := lipgloss.NewStyle().Background(modeColor).Foreground(theme.ContrastText(modeColor)).Bold(true)
	styledModeText := fill.Render(layout.ModeLabel)
	if lc, rc := config.GetDockPillLeftChar(), config.GetDockPillRightChar(); lc != "" && rc != "" {
		caps := lipgloss.NewStyle().Foreground(modeColor)
		styledModeText = caps.Render(lc) + fill.Render(layout.ModeLabel) + caps.Render(rc)
	}

	styledTrailText := lipgloss.NewStyle().Foreground(pal.FgMute).Render(layout.TrailText)

	var dockItemsStr strings.Builder
	itemNumber := 1

	// Where each entry lands inside the items block, measured off the styled
	// string as it is built. Turned into screen columns below, once the centring
	// spacer this block is placed against is known.
	type itemSpan struct {
		windowIndex, x0, x1 int
	}
	var itemSpans []itemSpan
	relX := 0

	for _, dockItem := range layout.VisibleItems {
		windowIndex := dockItem.WindowIndex
		window := m.Windows[windowIndex]

		// A minimized entry rests on the same Panel step the rest of the chrome
		// uses; only the two states worth a saturated fill get one.
		bgColor, fgColor := color.Color(pal.Panel), color.Color(pal.FgDim)
		emphasis := false

		isHighlighted := time.Now().Before(window.MinimizeHighlightUntil)

		switch {
		case isHighlighted:
			bgColor, emphasis = pal.Success, true
		case windowIndex == m.FocusedWindow && !window.Minimizing:
			bgColor, emphasis = pal.Accent, true
		}
		if emphasis {
			fgColor = theme.ContrastText(bgColor)
		}

		// Flat by default: the caps repeated on every minimized window turned
		// the row into beads. getDockItems pads the label, so the fill alone
		// still reads as a cell.
		caps := lipgloss.NewStyle().Foreground(bgColor)
		nameLabel := lipgloss.NewStyle().
			Background(bgColor).
			Foreground(fgColor).
			Bold(emphasis).
			Render(dockItem.Label)

		if itemNumber > 1 {
			dockItemsStr.WriteString(" ")
			relX++
		}
		chunk := caps.Render(config.GetDockPillLeftChar()) +
			nameLabel + caps.Render(config.GetDockPillRightChar())
		dockItemsStr.WriteString(chunk)

		w := lipgloss.Width(chunk)
		itemSpans = append(itemSpans, itemSpan{windowIndex, relX, relX + w})
		relX += w

		itemNumber++
	}

	if layout.TruncatedCount > 0 {
		truncStyle := lipgloss.NewStyle().Foreground(pal.FgMute)
		dockItemsStr.WriteString(truncStyle.Render(" ..."))
	}

	// The strip sits between the mode pill and the stats, and records where each
	// tab landed as it goes: both dock paths render through here, so the hit
	// rects are the drawn geometry rather than a second guess at it.
	styledTabs := m.renderDockWorkspaceTabs(layout.WorkspaceTabs, lipgloss.Width(styledModeText))

	leftInfo := lipgloss.JoinHorizontal(lipgloss.Top,
		styledModeText,
		styledTabs,
		styledTrailText,
	)

	renderWidth := m.GetRenderWidth()

	// The session controls take the bar's right-hand end before anything else is
	// measured, and barWidth is the bar every other block is fitted into. They
	// are built first because whether the leave control is there at all depends
	// on the run path, not on the width, so their span is not something the rest
	// of the layout can infer.
	sessionStrip, sessionCells := m.buildDockSessionStrip()
	barWidth := max(renderWidth-lipgloss.Width(sessionStrip), 0)

	actualLeftWidth := lipgloss.Width(leftInfo)
	centerWidth := lipgloss.Width(dockItemsStr.String())
	// The right block never takes more room than the left block and the dock
	// items leave, so the bar as a whole stays inside the screen.
	rightWidth := max(min(layout.RightWidth, barWidth-actualLeftWidth-centerWidth), 0)

	var rightInfo string
	// notifRule is the run of hairline the message burns down over, drawn into
	// the right-hand end of the separator row below. Empty when nothing is live.
	var notifRule string
	focusedWindow := m.GetFocusedWindow()

	// The message is built against the room the left block and the dock items
	// have actually left, not against an estimate, so its width needs no
	// correction afterwards. Correcting it afterwards is what the generic
	// truncation below would do, and the first thing that would cut is the
	// closing cap: the block would lose the shape that makes it part of the bar.
	notif, hasNotif := m.renderNotificationBlock(barWidth, max(barWidth-actualLeftWidth-centerWidth, 0))

	// Recorded here rather than in the builder because this is the pass that
	// places the block; the layout pass measures it against a different budget.
	m.notifHit = notifHitZones{
		Active:    hasNotif,
		X0:        barWidth - notif.Width,
		DismissX0: barWidth - notif.DismissW,
		Y:         m.GetDockbarContentYPosition(),
	}

	inCopyMode := focusedWindow.CopyModeVisible()
	switch {
	case hasNotif:
		// The message outranks the help line for its duration. Copy mode is a
		// mode the user is holding and can read the keys for again in a moment;
		// a message is a thing that just happened and will not be repeated.
		rightInfo = notif.Text
		notifRule = notif.Rule
		rightWidth = notif.Width
	case inCopyMode:
		helpTexts := copyModeHelpTexts(focusedWindow.CopyMode.State)

		helpStyle := lipgloss.NewStyle().
			Foreground(pal.FgDim).
			Background(pal.Panel).
			Padding(0, 1)
		// Take the longest help line that fits; the copy-mode keys are worth a
		// dock's width but not worth spilling off the end of it.
		for i, text := range helpTexts {
			rightInfo = helpStyle.Render(text)
			if lipgloss.Width(rightInfo) <= rightWidth || i == len(helpTexts)-1 {
				break
			}
		}
	default:
		var sysInfoParts []string
		if config.ShowCPU {
			sysInfoParts = append(sysInfoParts, m.GetCPUGraph())
		}
		if config.ShowRAM {
			sysInfoParts = append(sysInfoParts, m.GetRAMUsage())
		}
		// The CPU graph is the first thing dropped on a dock too narrow for
		// both readouts, then the RAM figure; a clipped graph reads as noise.
		for len(sysInfoParts) > 0 {
			rightInfo = sysInfoStyle.Render(strings.Join(sysInfoParts, " "))
			if lipgloss.Width(rightInfo) <= rightWidth {
				break
			}
			sysInfoParts = sysInfoParts[1:]
			rightInfo = ""
		}
	}
	if w := lipgloss.Width(rightInfo); w > rightWidth {
		rightInfo = truncateToWidth(rightInfo, rightWidth)
	}

	availableSpace := barWidth - actualLeftWidth - rightWidth - centerWidth
	leftSpacer := availableSpace / 2
	rightSpacer := availableSpace - leftSpacer

	if leftSpacer < 0 {
		leftSpacer = 0
		rightSpacer = 0
	}
	if rightSpacer < 0 {
		rightSpacer = 0
	}

	// The entries' screen columns, now that the spacer in front of them is known.
	// Recorded from the geometry this pass drew, so a click hit-tests the bar the
	// user is looking at rather than the one a later state would produce.
	m.dockItemHits = m.dockItemHits[:0]
	itemsX, itemY := actualLeftWidth+leftSpacer, m.GetDockbarContentYPosition()
	for _, s := range itemSpans {
		m.dockItemHits = append(m.dockItemHits, dockItemHit{
			X0: itemsX + s.x0, X1: itemsX + s.x1, Y: itemY, WindowIndex: s.windowIndex,
		})
	}

	paddedRightInfo := lipgloss.NewStyle().Width(rightWidth).Align(lipgloss.Right).Render(rightInfo)

	dockBar := lipgloss.JoinHorizontal(
		lipgloss.Top,
		leftInfo,
		lipgloss.NewStyle().Width(leftSpacer).Render(""),
		lipgloss.NewStyle().Render(dockItemsStr.String()),
		lipgloss.NewStyle().Width(rightSpacer).Render(""),
		paddedRightInfo,
	)

	// Backstop: whatever the parts add up to, the bar stops where the session
	// controls begin. The controls are appended after it, so an overfull bar
	// loses its own right-hand end and never the strip.
	if lipgloss.Width(dockBar) > barWidth {
		dockBar = truncateToWidth(dockBar, barWidth)
	}

	// The strip's screen columns, now that the bar in front of it is drawn.
	// Recorded from this pass for the reason the minimized entries are: whether
	// the leave control exists at all comes from the run path, so a handler that
	// recomputed the columns would need to re-derive that too and could disagree
	// with the frame the user clicked.
	m.dockSessionHits = m.dockSessionHits[:0]
	sessionX := barWidth + 1 // the strip opens with a bare column
	for _, c := range sessionCells {
		m.dockSessionHits = append(m.dockSessionHits, dockSessionHit{
			X0: sessionX, X1: sessionX + c.Width, Y: itemY, Action: c.Action,
		})
		sessionX += c.Width
	}
	dockBar += sessionStrip

	// Keyed on the glyph as well as the width: the separator character follows
	// the border style, which is switchable from the settings menu, and a
	// width-only key served the old hairline until the next resize.
	if sepChar := config.GetWindowSeparatorChar(); m.cachedSeparatorWidth != renderWidth || m.cachedSeparatorChar != sepChar {
		m.cachedSeparator = strings.Repeat(sepChar, renderWidth)
		m.cachedSeparatorWidth = renderWidth
		m.cachedSeparatorChar = sepChar
	}

	separator := lipgloss.NewStyle().
		Width(renderWidth).
		Foreground(theme.NotificationRule()).
		Render(m.cachedSeparator)

	// The message burns down over the hairline directly above it. The lit run
	// replaces the right-hand end of the separator rather than being drawn on
	// top of it, so the row is still exactly one screen wide and the burn is
	// aligned with the block by construction: they are the same span.
	if ruleWidth := lipgloss.Width(notifRule); ruleWidth > 0 && ruleWidth <= renderWidth {
		separator = lipgloss.NewStyle().
			Foreground(theme.NotificationRule()).
			Render(strings.Repeat(config.GetWindowSeparatorChar(), renderWidth-ruleWidth)) + notifRule
	}

	dockbarYPos := m.GetRenderHeight() - config.DockHeight
	dockbarParts := []string{separator, dockBar}
	if config.DockbarPosition == "top" {
		dockbarYPos = 0
		dockbarParts[0], dockbarParts[1] = dockbarParts[1], dockbarParts[0]
	}

	fullDock := lipgloss.JoinVertical(lipgloss.Left, dockbarParts...)
	return fullDock, dockbarYPos
}
