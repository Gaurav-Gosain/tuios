package app

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

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

	modeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#a0a0b0")).
		Bold(true).
		MarginRight(2)

	if m.workspaceActiveStyle == nil {
		activeStyle := lipgloss.NewStyle().
			Background(lipgloss.Color("#4865f2")).
			Foreground(lipgloss.Color("#ffffff")).
			Bold(true)
		m.workspaceActiveStyle = &activeStyle
	}

	leftText := layout.LeftText

	leftCircle := config.GetDockPillLeftChar()
	rightCircle := config.GetDockPillRightChar()

	var styledModeText, styledWorkspaceText string

	if leftCircle != "" && rightCircle != "" {
		startIdx := strings.Index(leftText, leftCircle)
		endIdx := strings.Index(leftText, rightCircle)

		if startIdx != -1 && endIdx > startIdx {
			workspacePart := leftText[endIdx+len(rightCircle):]

			modeColor := layout.ModeInfo.Color
			modeLabel := leftText[startIdx+len(leftCircle) : endIdx]

			styledLeftCircle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(modeColor)).
				Render(leftCircle)

			styledLabel := lipgloss.NewStyle().
				Background(lipgloss.Color(modeColor)).
				Foreground(lipgloss.Color("#ffffff")).
				Bold(true).
				Render(modeLabel)

			styledRightCircle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(modeColor)).
				Render(rightCircle)

			styledModeText = styledLeftCircle + styledLabel + styledRightCircle

			styledWorkspaceText = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#b0b0c0")).
				Bold(true).
				Render(workspacePart)
		} else {
			styledModeText = modeStyle.Render(leftText)
			styledWorkspaceText = ""
		}
	} else {
		modeColor := layout.ModeInfo.Color

		var modeLabel, workspacePart string
		if strings.Contains(leftText, " ") {
			for i := 1; i < len(leftText); i++ {
				if leftText[i] >= '0' && leftText[i] <= '9' {
					modeLabel = leftText[:i]
					workspacePart = leftText[i:]
					break
				}
			}
		}

		if modeLabel == "" {
			modeLabel = leftText
		}

		styledModeText = lipgloss.NewStyle().
			Background(lipgloss.Color(modeColor)).
			Foreground(lipgloss.Color("#ffffff")).
			Bold(true).
			Render(modeLabel)

		styledWorkspaceText = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#b0b0c0")).
			Bold(true).
			Render(workspacePart)
	}

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

		labelText := dockItem.Label

		leftCircle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(bgColor)).
			Render(config.GetDockPillLeftChar())

		nameLabel := lipgloss.NewStyle().
			Background(lipgloss.Color(bgColor)).
			Foreground(lipgloss.Color(fgColor)).
			Bold(isHighlighted || windowIndex == m.FocusedWindow).
			Render(labelText)

		rightCircle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(bgColor)).
			Render(config.GetDockPillRightChar())

		if itemNumber > 1 {
			dockItemsStr.WriteString(" ")
		}
		dockItemsStr.WriteString(leftCircle + nameLabel + rightCircle)

		itemNumber++
	}

	if layout.TruncatedCount > 0 {
		truncStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#808090"))
		dockItemsStr.WriteString(truncStyle.Render(" ..."))
	}

	leftInfo := lipgloss.JoinHorizontal(lipgloss.Top,
		styledModeText,
		styledWorkspaceText,
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
