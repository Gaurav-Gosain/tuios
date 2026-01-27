package app

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func (m *OS) renderDock() *lipgloss.Layer {
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

	var dockItemsStr string
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
			dockItemsStr += " "
		}
		dockItemsStr += leftCircle + nameLabel + rightCircle

		itemNumber++
	}

	if layout.TruncatedCount > 0 {
		truncStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#808090"))
		dockItemsStr += truncStyle.Render(" ...")
	}

	leftInfo := lipgloss.JoinHorizontal(lipgloss.Top,
		styledModeText,
		styledWorkspaceText,
	)

	var rightInfo string
	focusedWindow := m.GetFocusedWindow()

	inCopyMode := focusedWindow != nil && focusedWindow.CopyMode != nil && focusedWindow.CopyMode.Active
	if inCopyMode {
		var helpText string
		switch focusedWindow.CopyMode.State {
		case terminal.CopyModeNormal:
			helpText = "hjkl:move w/b/e:word f/F/t/T:char /:search n/N:next/prev C-l:clear ;,:repeat v:visual y:yank i:term q:quit"
		case terminal.CopyModeSearch:
			helpText = "Type to search  n/N:next/prev  Enter:done  Esc:cancel"
		case terminal.CopyModeVisualChar:
			helpText = "hjkl:extend w/b/e:word f/F/t/T:char ;,:repeat {/}:para %:bracket y:yank Esc:cancel"
		case terminal.CopyModeVisualLine:
			helpText = "jk:extend  y:yank  Esc:cancel"
		}

		helpStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#a0a0b0")).
			Background(lipgloss.Color("#1a1a2e")).
			Padding(0, 1)
		rightInfo = helpStyle.Render(helpText)
	} else {
		cpuGraph := m.GetCPUGraph()
		ramUsage := m.GetRAMUsage()
		rightInfo = sysInfoStyle.Render(cpuGraph + " " + ramUsage)
	}

	actualLeftWidth := lipgloss.Width(leftInfo)
	centerWidth := lipgloss.Width(dockItemsStr)
	rightWidth := layout.RightWidth

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
		lipgloss.NewStyle().Render(dockItemsStr),
		lipgloss.NewStyle().Width(rightSpacer).Render(""),
		paddedRightInfo,
	)

	renderWidth := m.GetRenderWidth()
	if m.cachedSeparatorWidth != renderWidth {
		m.cachedSeparator = strings.Repeat(config.GetWindowSeparatorChar(), renderWidth)
		m.cachedSeparatorWidth = renderWidth
	}

	separator := lipgloss.NewStyle().
		Width(renderWidth).
		Foreground(lipgloss.Color("#303040")).
		Render(m.cachedSeparator)

	dockbarYPos := m.GetRenderHeight() - config.DockHeight
	dockbarParts := []string{separator, dockBar}
	if config.DockbarPosition == "top" {
		dockbarYPos = 0
		dockbarParts[0], dockbarParts[1] = dockbarParts[1], dockbarParts[0]
	}

	fullDock := lipgloss.JoinVertical(lipgloss.Left, dockbarParts...)
	return lipgloss.NewLayer(fullDock).X(0).Y(dockbarYPos).Z(config.ZIndexDock).ID("dock")
}
