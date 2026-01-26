package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"charm.land/lipgloss/v2"
)

func (m *OS) renderOverlays() []*lipgloss.Layer {
	var layers []*lipgloss.Layer

	isRecording := m.TapeRecorder != nil && m.TapeRecorder.IsRecording()

	// Show clock/status unless hidden (but always show if recording or prefix active)
	if !config.HideClock || isRecording || m.PrefixActive {
		currentTime := time.Now().Format("15:04:05")
		var statusText string

		if isRecording {
			statusText = config.TapeRecordingIndicator + " | " + currentTime
		} else if m.PrefixActive {
			statusText = "PREFIX | " + currentTime
		} else {
			statusText = currentTime
		}

		timeStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#a0a0b0")).
			Bold(true).
			Padding(0, 1)

		if isRecording {
			timeStyle = timeStyle.
				Background(lipgloss.Color("#cc0000")).
				Foreground(lipgloss.Color("#ffffff"))
		} else if m.PrefixActive {
			timeStyle = timeStyle.
				Background(lipgloss.Color("#ff6b6b")).
				Foreground(lipgloss.Color("#ffffff"))
		} else {
			timeStyle = timeStyle.
				Background(lipgloss.Color("#1a1a2e"))
		}

		renderedTime := timeStyle.Render(statusText)

		timeX := 1
		timeLayer := lipgloss.NewLayer(renderedTime).
			X(timeX).
			Y(m.GetTimeYPosition()).
			Z(config.ZIndexTime).
			ID("time")

		layers = append(layers, timeLayer)
	}

	if len(m.GetVisibleWindows()) == 0 {
		asciiArt := `████████╗██╗   ██╗██╗ ██████╗ ███████╗
╚══██╔══╝██║   ██║██║██╔═══██╗██╔════╝
   ██║   ██║   ██║██║██║   ██║███████╗
   ██║   ██║   ██║██║██║   ██║╚════██║
   ██║   ╚██████╔╝██║╚██████╔╝███████║
   ╚═╝    ╚═════╝ ╚═╝ ╚═════╝ ╚══════╝`

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

		boxStyle := lipgloss.NewStyle().
			Border(getNormalBorder()).
			BorderForeground(lipgloss.Color("6")).
			Padding(1, 2)

		centeredContent := lipgloss.Place(
			m.GetRenderWidth(), m.GetRenderHeight(),
			lipgloss.Center, lipgloss.Center,
			boxStyle.Render(content),
		)

		welcomeLayer := lipgloss.NewLayer(centeredContent).
			X(0).Y(0).Z(1).ID("welcome")

		layers = append(layers, welcomeLayer)
	}

	if m.ShowQuitConfirm {
		quitContent, width, height := m.renderQuitConfirmDialog()
		x := (m.GetRenderWidth() - width) / 2
		y := (m.GetRenderHeight() - height) / 2
		quitLayer := lipgloss.NewLayer(quitContent).
			X(x).Y(y).Z(config.ZIndexHelp + 1).ID("quit-confirm")
		layers = append(layers, quitLayer)
	}

	if m.ShowHelp {
		helpContent := m.RenderHelpMenu(m.GetRenderWidth(), m.GetRenderHeight())

		helpLayer := lipgloss.NewLayer(helpContent).
			X(0).Y(0).Z(config.ZIndexHelp).ID("help")

		layers = append(layers, helpLayer)
	}

	if m.ShowTapeManager {
		tapeContent := m.RenderTapeManager(m.GetRenderWidth(), m.GetRenderHeight())
		tapeLayer := lipgloss.NewLayer(tapeContent).
			X(0).Y(0).Z(config.ZIndexHelp).ID("tape-manager")
		layers = append(layers, tapeLayer)
	}

	if m.ShowCacheStats {
		stats := GetGlobalStyleCache().GetStats()

		statsTitle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("14")).
			Bold(true).
			Render("Style Cache Statistics")

		labelStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("11")).
			Render

		valueStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")).
			Bold(true).
			Render

		var statsLines []string
		statsLines = append(statsLines, statsTitle)
		statsLines = append(statsLines, "")
		statsLines = append(statsLines, labelStyle("Hit Rate:      ")+valueStyle(fmt.Sprintf("%.2f%%", stats.HitRate)))
		statsLines = append(statsLines, labelStyle("Cache Hits:    ")+valueStyle(fmt.Sprintf("%d", stats.Hits)))
		statsLines = append(statsLines, labelStyle("Cache Misses:  ")+valueStyle(fmt.Sprintf("%d", stats.Misses)))
		statsLines = append(statsLines, labelStyle("Total Lookups: ")+valueStyle(fmt.Sprintf("%d", stats.Hits+stats.Misses)))
		statsLines = append(statsLines, labelStyle("Evictions:     ")+valueStyle(fmt.Sprintf("%d", stats.Evicts)))
		statsLines = append(statsLines, "")
		statsLines = append(statsLines, labelStyle("Cache Size:    ")+valueStyle(fmt.Sprintf("%d / %d entries", stats.Size, stats.Capacity)))
		statsLines = append(statsLines, labelStyle("Fill Rate:     ")+valueStyle(fmt.Sprintf("%.1f%%", float64(stats.Size)/float64(stats.Capacity)*100.0)))
		statsLines = append(statsLines, "")

		perfLabel := "Performance: "
		var perfText, perfColor string
		if stats.HitRate >= 95.0 {
			perfText = "Excellent"
			perfColor = "10"
		} else if stats.HitRate >= 85.0 {
			perfText = "Good"
			perfColor = "11"
		} else if stats.HitRate >= 70.0 {
			perfText = "Fair"
			perfColor = "214"
		} else {
			perfText = "Poor"
			perfColor = "9"
		}

		statsLines = append(statsLines, labelStyle(perfLabel)+lipgloss.NewStyle().
			Foreground(lipgloss.Color(perfColor)).
			Bold(true).
			Render(perfText))

		statsLines = append(statsLines, "")
		statsLines = append(statsLines, lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			Render("Press 'q'/'esc' to exit, 'r' to reset stats"))

		statsContent := strings.Join(statsLines, "\n")

		statsBox := lipgloss.NewStyle().
			Border(getBorder()).
			BorderForeground(lipgloss.Color("13")).
			Padding(1, 2).
			Background(lipgloss.Color("#1a1a2a")).
			Render(statsContent)

		centeredStats := lipgloss.Place(m.GetRenderWidth(), m.GetRenderHeight(),
			lipgloss.Center, lipgloss.Center, statsBox)

		statsLayer := lipgloss.NewLayer(centeredStats).
			X(0).Y(0).Z(config.ZIndexLogs).ID("cache-stats")

		layers = append(layers, statsLayer)
	}

	if m.ShowLogs {
		logTitle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("14")).
			Bold(true).
			Render("System Logs")

		maxDisplayHeight := max(m.GetRenderHeight()-8, 8)
		totalLogs := len(m.LogMessages)

		fixedLines := 4

		if totalLogs > maxDisplayHeight-fixedLines {
			fixedLines = 6
		}

		logsPerPage := max(maxDisplayHeight-fixedLines, 1)

		maxScroll := max(totalLogs-logsPerPage, 0)
		m.LogScrollOffset = max(0, min(m.LogScrollOffset, maxScroll))

		var logLines []string
		logLines = append(logLines, logTitle)
		logLines = append(logLines, "")

		startIdx := m.LogScrollOffset

		displayCount := 0
		for i := startIdx; i < len(m.LogMessages) && displayCount < logsPerPage; i++ {
			msg := m.LogMessages[i]

			var levelColor string
			switch msg.Level {
			case "ERROR":
				levelColor = "9"
			case "WARN":
				levelColor = "11"
			default:
				levelColor = "10"
			}

			timeStr := msg.Time.Format("15:04:05")
			levelStr := lipgloss.NewStyle().
				Foreground(lipgloss.Color(levelColor)).
				Render(fmt.Sprintf("[%s]", msg.Level))

			logLine := fmt.Sprintf("%s %s %s", timeStr, levelStr, msg.Message)
			logLines = append(logLines, logLine)
			displayCount++
		}

		if maxScroll > 0 {
			scrollInfo := fmt.Sprintf("Showing %d-%d of %d logs (↑/↓ to scroll)",
				startIdx+1, startIdx+displayCount, len(m.LogMessages))
			logLines = append(logLines, "")
			logLines = append(logLines, lipgloss.NewStyle().
				Foreground(lipgloss.Color("8")).
				Render(scrollInfo))
		}

		logLines = append(logLines, "")
		logLines = append(logLines, lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			Render("Press 'q'/'esc' to exit, j/k or ↑/↓ to scroll"))

		logContent := strings.Join(logLines, "\n")

		logBox := lipgloss.NewStyle().
			Border(getBorder()).
			BorderForeground(lipgloss.Color("12")).
			Padding(1, 2).
			Width(80).
			Background(lipgloss.Color("#1a1a2a")).
			Render(logContent)

		centeredLogs := lipgloss.Place(m.GetRenderWidth(), m.GetRenderHeight(),
			lipgloss.Center, lipgloss.Center, logBox)

		logLayer := lipgloss.NewLayer(centeredLogs).
			X(0).Y(0).Z(config.ZIndexLogs).ID("logs")

		layers = append(layers, logLayer)
	}

	showScriptIndicator := true
	if m.ScriptMode && !m.ScriptFinishedTime.IsZero() {
		elapsed := time.Since(m.ScriptFinishedTime)
		if elapsed > 2*time.Second {
			showScriptIndicator = false
		}
	}

	if m.ScriptMode && showScriptIndicator {
		var scriptStatus string

		// Check for remote script progress first (tape exec), then local player (tape play)
		var currentCmd, totalCmds, progress int
		var isFinished bool

		if m.RemoteScriptTotal > 0 {
			// Remote script execution (tape exec)
			currentCmd = m.RemoteScriptIndex
			totalCmds = m.RemoteScriptTotal
			if totalCmds > 0 {
				progress = (currentCmd * 100) / totalCmds
			}
			isFinished = !m.ScriptFinishedTime.IsZero()
		} else if m.ScriptPlayer != nil {
			// Local script playback (tape play)
			if player, ok := m.ScriptPlayer.(*tape.Player); ok {
				progress = player.Progress()
				currentCmd = player.CurrentIndex()
				totalCmds = player.TotalCommands()
				isFinished = player.IsFinished()
			}
		}

		if totalCmds > 0 {
			if isFinished {
				scriptStatus = fmt.Sprintf("DONE • %d/%d commands", totalCmds, totalCmds)
			} else {
				barWidth := 15
				filledWidth := (progress * barWidth) / 100
				bar := ""
				for i := range barWidth {
					if i < filledWidth {
						bar += "█"
					} else {
						bar += "░"
					}
				}

				// Display 1-based index for human readability (command 1 of N, not 0 of N)
				displayCmd := min(currentCmd+1, totalCmds)

				if m.ScriptPaused {
					scriptStatus = fmt.Sprintf("PAUSED • %s %d%% • %d/%d", bar, progress, displayCmd, totalCmds)
				} else {
					scriptStatus = fmt.Sprintf("RUNNING • %s %d%% • %d/%d", bar, progress, displayCmd, totalCmds)
				}
			}
		} else {
			scriptStatus = "TAPE"
		}

		scriptStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("255")).
			Background(lipgloss.Color("55")).
			Padding(0, 1)

		scriptIndicator := scriptStyle.Render(scriptStatus)
		scriptLayer := lipgloss.NewLayer(scriptIndicator).
			X(m.GetRenderWidth() - lipgloss.Width(scriptIndicator) - 2).
			Y(1).
			Z(config.ZIndexNotifications).
			ID("script-mode")

		layers = append(layers, scriptLayer)
	}

	if m.PrefixActive && !m.ShowHelp && config.WhichKeyEnabled && time.Since(m.LastPrefixTime) > config.WhichKeyDelay {
		var title string
		var bindings []config.Keybinding

		if m.WorkspacePrefixActive {
			title = "Workspace"
			bindings = config.GetPrefixKeybindings("workspace")
		} else if m.MinimizePrefixActive {
			title = "Minimize"
			bindings = config.GetPrefixKeybindings("minimize")
			minimizedCount := 0
			for _, win := range m.Windows {
				if win.Minimized && win.Workspace == m.CurrentWorkspace {
					minimizedCount++
				}
			}
			for i := range bindings {
				if bindings[i].Key == "1-9" {
					bindings[i].Description = fmt.Sprintf("Restore window (%d minimized)", minimizedCount)
					break
				}
			}
		} else if m.TilingPrefixActive {
			title = "Window"
			bindings = config.GetPrefixKeybindings("window")
		} else if m.DebugPrefixActive {
			title = "Debug"
			bindings = config.GetPrefixKeybindings("debug")
		} else if m.TapePrefixActive {
			title = "Tape"
			bindings = config.GetPrefixKeybindings("tape")
		} else {
			title = "Prefix"
			bindings = config.GetPrefixKeybindings("", m.IsDaemonSession)
		}

		maxKeyLen := 0
		maxDescLen := 0
		for _, binding := range bindings {
			if len(binding.Key) > maxKeyLen {
				maxKeyLen = len(binding.Key)
			}
			if len(binding.Description) > maxDescLen {
				maxDescLen = len(binding.Description)
			}
		}
		contentWidth := max(maxKeyLen+2+maxDescLen, len(title))

		bg := lipgloss.Color("#1f2937")

		var styledLines []string

		padLine := func(s string, targetWidth int) string {
			currentWidth := lipgloss.Width(s)
			if currentWidth < targetWidth {
				s += lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", targetWidth-currentWidth))
			}
			return s
		}

		titleStyled := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#ffffff")).
			Bold(true).
			Background(bg).
			Render(title)
		styledLines = append(styledLines, padLine(titleStyled, contentWidth))

		sepStyled := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#4b5563")).
			Background(bg).
			Render(strings.Repeat("─", contentWidth))
		styledLines = append(styledLines, sepStyled)

		for _, binding := range bindings {
			keyStyled := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#fbbf24")).
				Bold(true).
				Background(bg).
				Render(binding.Key)
			paddingStyled := lipgloss.NewStyle().
				Background(bg).
				Render(strings.Repeat(" ", maxKeyLen-len(binding.Key)+2))
			descStyled := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#d1d5db")).
				Background(bg).
				Render(binding.Description)
			line := keyStyled + paddingStyled + descStyled
			styledLines = append(styledLines, padLine(line, contentWidth))
		}

		paddingH := lipgloss.NewStyle().Background(bg).Render("  ")
		emptyLine := lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", contentWidth+4))

		var finalLines []string
		finalLines = append(finalLines, emptyLine)
		for _, line := range styledLines {
			finalLines = append(finalLines, paddingH+line+paddingH)
		}
		finalLines = append(finalLines, emptyLine)

		renderedOverlay := strings.Join(finalLines, "\n")

		overlayWidth := lipgloss.Width(renderedOverlay)
		overlayHeight := lipgloss.Height(renderedOverlay)
		var overlayX, overlayY int

		renderWidth := m.GetRenderWidth()
		renderHeight := m.GetRenderHeight()
		switch config.WhichKeyPosition {
		case "top-left":
			overlayX = 2
			overlayY = 1
		case "top-right":
			overlayX = renderWidth - overlayWidth - 2
			overlayY = 1
		case "bottom-left":
			overlayX = 2
			overlayY = renderHeight - overlayHeight - 2
		case "center":
			overlayX = (renderWidth - overlayWidth) / 2
			overlayY = (renderHeight - overlayHeight) / 2
		default:
			overlayX = renderWidth - overlayWidth - 2
			overlayY = renderHeight - overlayHeight - 2
		}

		whichKeyLayer := lipgloss.NewLayer(renderedOverlay).
			X(overlayX).
			Y(overlayY).
			Z(config.ZIndexWhichKey).
			ID("whichkey")

		layers = append(layers, whichKeyLayer)
	}

	if len(m.Notifications) > 0 {
		m.CleanupNotifications()

		notifY := 1
		notifSpacing := 4
		for i, notif := range m.Notifications {
			if i >= 3 {
				break
			}

			opacity := 1.0
			if notif.Animation != nil {
				elapsed := time.Since(notif.Animation.StartTime)
				if elapsed < notif.Animation.Duration {
					opacity = float64(elapsed) / float64(notif.Animation.Duration)
				}
			}

			timeLeft := notif.Duration - time.Since(notif.StartTime)
			if timeLeft < config.NotificationFadeOutDuration {
				opacity *= float64(timeLeft) / float64(config.NotificationFadeOutDuration)
			}

			if opacity <= 0 {
				continue
			}

			var bgColor, fgColor, icon string
			switch notif.Type {
			case "error":
				bgColor = "#dc2626"
				fgColor = "#ffffff"
				icon = config.NotificationIconError
			case "warning":
				bgColor = "#d97706"
				fgColor = "#ffffff"
				icon = config.NotificationIconWarning
			case "success":
				bgColor = "#16a34a"
				fgColor = "#ffffff"
				icon = config.NotificationIconSuccess
			default:
				bgColor = "#2563eb"
				fgColor = "#ffffff"
				icon = config.NotificationIconInfo
			}

			maxNotifWidth := min(max(m.GetRenderWidth()-8, 20), 60)

			message := notif.Message
			maxMessageLen := maxNotifWidth - 10
			if len(message) > maxMessageLen {
				message = message[:maxMessageLen-3] + "..."
			}

			notifContent := fmt.Sprintf(" %s  %s ", icon, message)

			notifBox := lipgloss.NewStyle().
				Background(lipgloss.Color(bgColor)).
				Foreground(lipgloss.Color(fgColor)).
				Padding(1, 2).
				Bold(true).
				MaxWidth(maxNotifWidth).
				Render(notifContent)

			notifX := max(m.GetRenderWidth()-lipgloss.Width(notifBox)-2, 0)
			currentY := notifY + (i * notifSpacing)

			notifLayer := lipgloss.NewLayer(notifBox).
				X(notifX).Y(currentY).Z(config.ZIndexNotifications).
				ID(fmt.Sprintf("notif-%s", notif.ID))

			layers = append(layers, notifLayer)
		}
	}

	focusedWindow := m.GetFocusedWindow()
	if focusedWindow != nil && focusedWindow.CopyMode != nil &&
		focusedWindow.CopyMode.Active &&
		focusedWindow.CopyMode.State == terminal.CopyModeSearch {

		searchQuery := focusedWindow.CopyMode.SearchQuery
		matchCount := len(focusedWindow.CopyMode.SearchMatches)
		currentMatch := focusedWindow.CopyMode.CurrentMatch

		searchText := "/" + searchQuery + "█"
		if matchCount > 0 {
			searchText += fmt.Sprintf(" [%d/%d]", currentMatch+1, matchCount)
		} else if searchQuery != "" {
			searchText += " [0]"
		}

		searchStyle := lipgloss.NewStyle().
			Background(lipgloss.Color("#000000")).
			Foreground(lipgloss.Color("#FFFF00")).
			Bold(true).
			Padding(0, 1)

		renderedSearch := searchStyle.Render(searchText)

		searchX := focusedWindow.X + 2
		searchY := focusedWindow.Y + focusedWindow.Height - 2

		searchLayer := lipgloss.NewLayer(renderedSearch).
			X(searchX).
			Y(searchY).
			Z(config.ZIndexHelp + 1).
			ID("copy-mode-search")

		layers = append(layers, searchLayer)
	}

	if m.ShowKeys && len(m.RecentKeys) > 0 {
		m.CleanupExpiredKeys(3 * time.Second)
		if len(m.RecentKeys) > 0 {
			showkeysContent := m.renderShowkeys()
			contentWidth := lipgloss.Width(showkeysContent)
			contentHeight := lipgloss.Height(showkeysContent)

			rightMargin := 2
			dockOffset := 0
			if config.DockbarPosition == "bottom" {
				dockOffset = config.DockHeight
			}

			x := m.GetRenderWidth() - contentWidth - rightMargin
			y := m.GetRenderHeight() - contentHeight - dockOffset

			zIndex := config.ZIndexNotifications + 1
			if m.ShowHelp {
				zIndex = config.ZIndexHelp + 1
			}

			showkeysLayer := lipgloss.NewLayer(showkeysContent).
				X(x).
				Y(y).
				Z(zIndex).
				ID("showkeys")

			layers = append(layers, showkeysLayer)
		}
	}

	return layers
}

func (m *OS) renderQuitConfirmDialog() (string, int, int) {
	borderColor := theme.HelpBorder()
	selectedColor := theme.HelpTabActive()
	unselectedColor := theme.HelpGray()

	title := lipgloss.NewStyle().
		Foreground(selectedColor).
		Bold(true).
		Render("Quit TUIOS?")

	yesButtonContent := "yes"
	noButtonContent := "no"

	var yesButton, noButton string

	if m.QuitConfirmSelection == 0 {
		yesButton = lipgloss.NewStyle().
			Foreground(selectedColor).
			Bold(true).
			Border(lipgloss.NormalBorder()).
			BorderForeground(selectedColor).
			Padding(0, 1).
			Render(yesButtonContent)

		noButton = lipgloss.NewStyle().
			Foreground(unselectedColor).
			Border(lipgloss.NormalBorder()).
			BorderForeground(unselectedColor).
			Padding(0, 1).
			Render(noButtonContent)
	} else {
		yesButton = lipgloss.NewStyle().
			Foreground(unselectedColor).
			Border(lipgloss.NormalBorder()).
			BorderForeground(unselectedColor).
			Padding(0, 1).
			Render(yesButtonContent)

		noButton = lipgloss.NewStyle().
			Foreground(selectedColor).
			Bold(true).
			Border(lipgloss.NormalBorder()).
			BorderForeground(selectedColor).
			Padding(0, 1).
			Render(noButtonContent)
	}

	buttonRow := lipgloss.JoinHorizontal(lipgloss.Center, yesButton, "   ", noButton)

	dialogContent := lipgloss.JoinVertical(
		lipgloss.Center,
		title,
		"",
		buttonRow,
	)

	dialogBox := lipgloss.NewStyle().
		Border(getBorder()).
		BorderForeground(borderColor).
		Padding(1, 3).
		Render(dialogContent)

	width := lipgloss.Width(dialogBox)
	height := lipgloss.Height(dialogBox)

	return dialogBox, width, height
}
