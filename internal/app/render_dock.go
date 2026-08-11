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
// band. Inactive is a bare dim digit; active is a flat filled cell, where the
// background is the tab and there is no cap glyph to read as one. Both pad to
// the caps' widths, so every chip is exactly workspaceChipWidth wide whatever
// its state. rowBg is what sits behind the chip (nil for the bare terminal
// background), fg the resting digit color.
func workspaceChip(n int, active bool, fg, rowBg color.Color) string {
	lc, rc := workspaceChipCaps()
	digit := strconv.Itoa(n)
	if !active {
		return sidebarStyle(rowBg, fg).Render(
			strings.Repeat(" ", lipgloss.Width(lc)) + digit + strings.Repeat(" ", lipgloss.Width(rc)))
	}
	fill := lipgloss.Color(sidebarFocusColor)
	body := lipgloss.NewStyle().Background(fill).Foreground(lipgloss.Color("#ffffff"))
	if !config.DockPillCaps {
		return body.Render(
			strings.Repeat(" ", lipgloss.Width(lc)) + digit + strings.Repeat(" ", lipgloss.Width(rc)))
	}
	caps := sidebarStyle(rowBg, fill)
	return caps.Render(lc) + body.Bold(true).Render(digit) + caps.Render(rc)
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

	sysInfoStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#808090")).
		MarginRight(2)

	// The mode label arrives without its caps, so the pill is assembled here
	// rather than found again by searching the string for the cap glyphs.
	modeColor := lipgloss.Color(layout.ModeInfo.Color)
	fill := lipgloss.NewStyle().Background(modeColor).Foreground(lipgloss.Color("#ffffff"))
	styledModeText := fill.Render(layout.ModeLabel)
	if lc, rc := config.GetDockPillLeftChar(), config.GetDockPillRightChar(); lc != "" && rc != "" {
		caps := lipgloss.NewStyle().Foreground(modeColor)
		styledModeText = caps.Render(lc) + fill.Bold(true).Render(layout.ModeLabel) + caps.Render(rc)
	}

	styledTrailText := lipgloss.NewStyle().Foreground(theme.UI().FgDim).Render(layout.TrailText)

	var dockItemsStr strings.Builder
	itemNumber := 1

	for _, dockItem := range layout.VisibleItems {
		windowIndex := dockItem.WindowIndex
		window := m.Windows[windowIndex]

		bgColor := "#2a2a3e"
		fgColor := "#a0a0a8"

		isHighlighted := time.Now().Before(window.MinimizeHighlightUntil)

		if isHighlighted {
			bgColor = "#66ff66"
			fgColor = "#000000"
		} else if windowIndex == m.FocusedWindow && !window.Minimizing {
			bgColor = "#4865f2"
			fgColor = "#ffffff"
		}

		// Flat by default: the caps repeated on every minimized window turned
		// the row into beads. getDockItems pads the label, so the fill alone
		// still reads as a cell.
		caps := lipgloss.NewStyle().Foreground(lipgloss.Color(bgColor))
		nameLabel := lipgloss.NewStyle().
			Background(lipgloss.Color(bgColor)).
			Foreground(lipgloss.Color(fgColor)).
			Bold(config.DockPillCaps && (isHighlighted || windowIndex == m.FocusedWindow)).
			Render(dockItem.Label)

		if itemNumber > 1 {
			dockItemsStr.WriteString(" ")
		}
		dockItemsStr.WriteString(caps.Render(config.GetDockPillLeftChar()) +
			nameLabel + caps.Render(config.GetDockPillRightChar()))

		itemNumber++
	}

	if layout.TruncatedCount > 0 {
		truncStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#808090"))
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
	actualLeftWidth := lipgloss.Width(leftInfo)
	centerWidth := lipgloss.Width(dockItemsStr.String())
	// The right block never takes more room than the left block and the dock
	// items leave, so the bar as a whole stays inside the screen.
	rightWidth := max(min(layout.RightWidth, renderWidth-actualLeftWidth-centerWidth), 0)

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
	notif, hasNotif := m.renderNotificationBlock(renderWidth, max(renderWidth-actualLeftWidth-centerWidth, 0))

	// Recorded here rather than in the builder because this is the pass that
	// places the block; the layout pass measures it against a different budget.
	m.notifHit = notifHitZones{
		Active:    hasNotif,
		X0:        renderWidth - notif.Width,
		DismissX0: renderWidth - notif.DismissW,
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
			Foreground(lipgloss.Color("#a0a0b0")).
			Background(lipgloss.Color("#1a1a2e")).
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

	availableSpace := m.GetRenderWidth() - actualLeftWidth - rightWidth - centerWidth
	leftSpacer := availableSpace / 2
	rightSpacer := availableSpace - leftSpacer

	if leftSpacer < 0 {
		leftSpacer = 0
		rightSpacer = 0
	}
	if rightSpacer < 0 {
		rightSpacer = 0
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

	// Backstop: whatever the parts add up to, the bar is one screen wide.
	if lipgloss.Width(dockBar) > renderWidth {
		dockBar = truncateToWidth(dockBar, renderWidth)
	}

	if m.cachedSeparatorWidth != renderWidth {
		m.cachedSeparator = strings.Repeat(config.GetWindowSeparatorChar(), renderWidth)
		m.cachedSeparatorWidth = renderWidth
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
