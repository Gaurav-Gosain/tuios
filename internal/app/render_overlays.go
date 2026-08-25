package app

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

func (m *OS) renderOverlays() []*lipgloss.Layer {
	var layers []*lipgloss.Layer

	// Clear last frame's hit geometry; each panel that renders below re-records
	// itself. The ASCII glyph set is synced at the top of GetCanvas, ahead of
	// every layer that reads it.
	m.OverlayHits = m.OverlayHits[:0]
	m.reconcileOverlayZOrder()

	isRecording := m.TapeRecorder != nil && m.TapeRecorder.IsRecording()

	// Show clock/status unless hidden (but always show if recording or prefix active)
	if (config.ShowClock && !config.HideClock) || isRecording || m.PrefixActive {
		// The reading comes from the clock component, so the badge and a clock
		// cell on the dock can never disagree, and so the format is the one
		// [dock.clock] asked for rather than the one that used to be spelled
		// here in a string literal.
		currentTime := m.DockClockText()
		var statusText string

		if isRecording {
			statusText = config.TapeRecordingIndicator + " | " + currentTime
		} else if m.PrefixActive {
			statusText = "PREFIX | " + currentTime
		} else {
			statusText = currentTime
		}

		// The badge rests on the same Panel step the rest of the chrome does and
		// only lights up for a state the user is holding: the prefix is a mode
		// waiting for a key, and recording is the one that writes to disk.
		pal := theme.UI()
		timeStyle := lipgloss.NewStyle().
			Foreground(pal.FgDim).
			Background(pal.Panel).
			Bold(true).
			Padding(0, 1)

		switch {
		case isRecording:
			timeStyle = timeStyle.Background(pal.Warn).Foreground(theme.ContrastText(pal.Warn))
		case m.PrefixActive:
			timeStyle = timeStyle.Background(pal.Warning).Foreground(theme.ContrastText(pal.Warning))
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

	if len(m.GetVisibleWindows()) == 0 && m.GetContentWidth() > 0 && m.GetUsableHeight() > 0 {
		asciiArt := `████████╗██╗   ██╗██╗ ██████╗ ███████╗
╚══██╔══╝██║   ██║██║██╔═══██╗██╔════╝
   ██║   ██║   ██║██║██║   ██║███████╗
   ██║   ██║   ██║██║██║   ██║╚════██║
   ██║   ╚██████╔╝██║╚██████╔╝███████║
   ╚═╝    ╚═════╝ ╚═╝ ╚═════╝ ╚══════╝`

		// The splash is the first thing anyone sees, at whatever width. Its
		// three parts have fixed widths - 38 columns of block letters, a 28
		// column subtitle and a 44 column hint line - and the box adds a border
		// and two columns of padding on each side. Asking for all of it needs 74
		// columns, so on anything narrower it used to run off the right edge
		// with the border cut away. Drop to what fits instead.
		const (
			artCols      = 38
			subtitleCols = 28
			hintCols     = 44 // three key chips and their labels, spaced
			boxCols      = 6  // both borders, both paddings
			boxRows      = 4  // border and padding, top and bottom
		)
		// The splash belongs to the content region: the sidebar's reserved
		// columns and the dock's rows are drawn by someone else, and centering
		// on the whole screen both overdrew the rail and put the box off-centre
		// in the part of the screen the user can actually see.
		contentW, contentH := m.GetContentWidth(), m.GetUsableHeight()
		avail := contentW - boxCols
		// The same argument applies to the height: the block letters are six
		// rows of a box that also carries a border, padding, a subtitle and up
		// to three stacked hints, which is more than a short terminal has.
		availRows := max(contentH-boxRows, 1)

		ui := theme.UI()
		titleText := asciiArt
		if avail < artCols || availRows < 12 {
			titleText = "TUIOS"
		}
		title := lipgloss.NewStyle().
			Foreground(ui.Accent).
			Bold(true).
			Render(titleText)

		parts := []string{title}

		if avail >= subtitleCols && availRows >= 8 {
			parts = append(parts, "", lipgloss.NewStyle().
				Foreground(ui.AccentBright).
				Render("Terminal UI Operating System"))
		}

		// The hints read as one line when there is room for one, and stack when
		// there is not, so no width loses a hint entirely. They are key chips and
		// lowercase labels, the same shape every overlay footer uses: the quoted
		// Title-case prose was the only surface speaking that way.
		hints := make([]string, 0, 3)
		for _, h := range []overlay.Hint{
			{Key: "n", Label: "new window"},
			{Key: "?", Label: "help"},
			{Key: ",", Label: "settings"},
		} {
			hints = append(hints, overlay.KeyBadge(h.Key, ui)+
				lipgloss.NewStyle().Foreground(ui.FgDim).Render(" "+h.Label))
		}
		sep := "\n"
		if avail >= hintCols {
			sep = "   "
		}
		parts = append(parts, "", lipgloss.NewStyle().
			Align(lipgloss.Center).
			Render(strings.Join(hints, sep)))

		content := lipgloss.JoinVertical(lipgloss.Center, squeezeLines(parts, availRows)...)

		boxStyle := lipgloss.NewStyle().
			Border(getNormalBorder()).
			BorderForeground(ui.Accent).
			Padding(1, 2).
			MaxWidth(contentW)

		box := boxStyle.Render(content)
		if lipgloss.Width(box) > contentW || lipgloss.Height(box) > contentH {
			// A wide enough rail, or a short enough screen, leaves no room for
			// the box even after squeezing. One clipped hint line still says
			// what to press, which beats drawing a border over the rail or the
			// dock.
			box = lipgloss.NewStyle().
				Foreground(ui.FgDim).
				MaxWidth(contentW).
				Render("n new window")
		}

		centeredContent := lipgloss.Place(
			contentW, contentH,
			lipgloss.Center, lipgloss.Center,
			box,
		)

		welcomeLayer := lipgloss.NewLayer(centeredContent).
			X(m.GetLeftMargin()).Y(m.GetTopMargin()).Z(1).ID("welcome")

		layers = append(layers, welcomeLayer)
	}

	if m.ShowCommandPalette {
		content, geo, rows := m.renderCommandPalette()
		layers = m.placeOverlayPanel(layers, "palette", content, geo, rows)
	}

	if m.ShowLauncher {
		content, geo, rows := m.renderLauncher()
		layers = m.placeOverlayPanel(layers, "launcher", content, geo, rows)
	}

	if m.ShowSessionSwitcher {
		content, geo, rows := m.renderSessionSwitcher()
		layers = m.placeOverlayPanel(layers, "session", content, geo, rows)
	}

	if m.ShowWorkspaceSwitcher {
		content, geo, rows := m.renderWorkspaceSwitcher()
		layers = m.placeOverlayPanel(layers, "workspace", content, geo, rows)
	}

	if m.ShowLayoutPicker {
		content, geo, rows := m.renderLayoutPicker()
		layers = m.placeOverlayPanel(layers, "layout", content, geo, rows)
	}

	if m.ShowSettings {
		content, geo, rows := m.renderSettings()
		layers = m.placeOverlayPanel(layers, "settings", content, geo, rows)
	}

	if m.ShowKeybindManager {
		content, geo, rows := m.renderKeybindManager()
		layers = m.placeOverlayPanel(layers, "keybinds", content, geo, rows)
	}

	if m.ShowThemePicker {
		content, geo, rows := m.renderThemePicker()
		layers = m.placeOverlayPanel(layers, "themepicker", content, geo, rows)
	}

	if m.ShowDockEditor {
		content, geo, rows := m.renderDockEditor()
		layers = m.placeOverlayPanel(layers, "dockeditor", content, geo, rows)
	}

	if m.ShowGlyphPicker {
		content, geo, rows := m.renderGlyphPicker()
		layers = m.placeOverlayPanel(layers, "glyphpicker", content, geo, rows)
	}

	if m.ShowAccentPicker {
		content, geo, rows := m.renderAccentPicker()
		layers = m.placeOverlayPanel(layers, "accent", content, geo, rows)
	}

	// The rename dialog is modal and not draggable, so it is placed directly
	// rather than through the overlay panel stack. It is centred like the rest.
	if content, geo, x, y, ok := m.renderRenameDialog(); ok {
		m.renameHit = overlay.Rect{X0: x, Y0: y, X1: x + geo.Width, Y1: y + geo.Height}
		layers = append(layers, lipgloss.NewLayer(content).
			X(x).Y(y).Z(config.ZIndexOverlayBase).ID("rename"))
	}

	if m.ShowAggregateView {
		content, geo, rows := m.renderAggregateView()
		layers = m.placeOverlayPanel(layers, "aggregate", content, geo, rows)
	}

	if m.ShowScrollbackBrowser {
		browserContent := m.renderScrollbackBrowser()
		if browserContent != "" {
			browserLayer := lipgloss.NewLayer(browserContent).
				X(0).Y(0).Z(config.ZIndexScrollbackBrowser).ID("scrollback-browser")
			layers = append(layers, browserLayer)
		}
	}

	if m.ShotPreview.Open {
		content, geo, rows := m.renderScreenshotPreview()
		layers = m.placeOverlayPanel(layers, overlayKindShot, content, geo, rows)
	}

	// Capture mode draws above every panel: it is a gesture over the whole
	// screen, and its marquee has to be visible on top of whatever is open.
	layers = append(layers, m.renderCaptureMode()...)

	if m.ShowQuitMenu {
		content, geo, rows := m.renderQuitMenu()
		layers = m.placeOverlayPanel(layers, "quit", content, geo, rows)
	}

	if m.ShowSessionClose {
		content, geo, rows := m.renderSessionClose()
		layers = m.placeOverlayPanel(layers, "sessionclose", content, geo, rows)
	}

	if m.ShowHelp {
		content, geo := m.RenderHelpMenu()
		layers = m.placeOverlayPanel(layers, "help", content, geo, nil)
	}

	if m.ShowTapeManager {
		tapeContent := m.RenderTapeManager()
		layers = append(layers, m.centeredBoxLayer(tapeContent, config.ZIndexHelp, "tape-manager"))
	}

	if m.ShowTapeReview {
		reviewContent := m.RenderTapeReview()
		layers = append(layers, m.centeredBoxLayer(reviewContent, config.ZIndexHelp+1, "tape-review"))
	}

	if m.ShowCacheStats {
		stats := GetGlobalStyleCache().GetStats()

		pal := theme.UI()
		bg := pal.Surface
		labelStyle := overlay.Style(bg).Foreground(pal.FgDim).Render
		valueStyle := overlay.Style(bg).Foreground(pal.Fg).Bold(true).Render

		var statsLines []string
		statsLines = append(statsLines, labelStyle("Hit Rate:      ")+valueStyle(fmt.Sprintf("%.2f%%", stats.HitRate)))
		statsLines = append(statsLines, labelStyle("Cache Hits:    ")+valueStyle(fmt.Sprintf("%d", stats.Hits)))
		statsLines = append(statsLines, labelStyle("Cache Misses:  ")+valueStyle(fmt.Sprintf("%d", stats.Misses)))
		statsLines = append(statsLines, labelStyle("Total Lookups: ")+valueStyle(fmt.Sprintf("%d", stats.Hits+stats.Misses)))
		statsLines = append(statsLines, labelStyle("Evictions:     ")+valueStyle(fmt.Sprintf("%d", stats.Evicts)))
		statsLines = append(statsLines, "")
		statsLines = append(statsLines, labelStyle("Cache Size:    ")+valueStyle(fmt.Sprintf("%d / %d entries", stats.Size, stats.Capacity)))
		statsLines = append(statsLines, labelStyle("Fill Rate:     ")+valueStyle(fmt.Sprintf("%.1f%%", float64(stats.Size)/float64(stats.Capacity)*100.0)))
		statsLines = append(statsLines, "")

		// The verdict is a status word, so it takes a status token: the same
		// three the logs and the message block are read by.
		perfText, perfColor := "Poor", pal.Warn
		switch {
		case stats.HitRate >= 95.0:
			perfText, perfColor = "Excellent", pal.Success
		case stats.HitRate >= 85.0:
			perfText, perfColor = "Good", pal.Success
		case stats.HitRate >= 70.0:
			perfText, perfColor = "Fair", pal.Warning
		}
		statsLines = append(statsLines,
			labelStyle("Performance:   ")+overlay.Style(bg).Foreground(perfColor).Bold(true).Render(perfText))

		width := m.panelWidth(60)
		hints := []overlay.Hint{{Key: "r", Label: "reset"}, {Key: "esc", Label: "close"}}
		rows, hints := m.panelBody(len(statsLines), 0, width, nil, hints)
		panel := overlay.Panel{
			Title: "cache stats",
			Width: width,
			Body:  clipStyledLines(strings.Join(squeezeLines(statsLines, rows), "\n"), width),
			Hints: hints,
		}
		content, _ := panel.Render(pal)

		layers = append(layers, m.centeredBoxLayer(content, config.ZIndexLogs, "cache-stats"))
	}

	if m.ShowLogs {
		pal := theme.UI()
		bg := pal.Surface

		// The viewer prefers 80 columns; a narrower screen gets a narrower
		// viewer rather than a viewer with its right-hand side off the edge.
		logTextWidth := m.panelWidth(80)
		totalLogs := len(m.LogMessages)

		hints := []overlay.Hint{
			{Key: "j/k", Label: "scroll"},
			{Key: "E", Label: "copy errors"},
			{Key: "A", Label: "copy all"},
			{Key: "q", Label: "close"},
		}
		// A viewer that scrolls spends two more body lines saying where in the
		// log it is, so it is measured again with them once it knows it does.
		logsPerPage, hints := m.panelBody(totalLogs, 0, logTextWidth, nil, hints)
		if totalLogs > logsPerPage {
			logsPerPage, hints = m.panelBody(totalLogs, 2, logTextWidth, nil, hints)
		}

		maxScroll := max(totalLogs-logsPerPage, 0)
		m.LogScrollOffset = max(0, min(m.LogScrollOffset, maxScroll))

		var logLines []string
		startIdx := m.LogScrollOffset

		displayCount := 0
		for i := startIdx; i < len(m.LogMessages) && displayCount < logsPerPage; i++ {
			msg := m.LogMessages[i]

			// The severity tokens the rest of the app reads by, so a log line
			// says the same thing a message in the dock does.
			levelColor := pal.Success
			switch msg.Level {
			case "ERROR":
				levelColor = pal.Warn
			case "WARN":
				levelColor = pal.Warning
			}

			line := overlay.Style(bg).Foreground(pal.FgDim).Render(msg.Time.Format("15:04:05")+" ") +
				overlay.Style(bg).Foreground(levelColor).Render(fmt.Sprintf("[%s] ", msg.Level)) +
				overlay.Style(bg).Foreground(pal.Fg).Render(msg.Message)
			logLines = append(logLines, clipStyled(line, logTextWidth))
			displayCount++
		}

		if maxScroll > 0 {
			logLines = append(logLines, "", overlay.Style(bg).Foreground(pal.FgDim).Italic(true).
				Render(fmt.Sprintf("  %d-%d of %d logs", startIdx+1, startIdx+displayCount, len(m.LogMessages))))
		}

		panel := overlay.Panel{
			Title: "logs",
			Width: logTextWidth,
			Body:  strings.Join(logLines, "\n"),
			Hints: hints,
		}
		content, _ := panel.Render(pal)

		layers = append(layers, m.centeredBoxLayer(content, config.ZIndexLogs, "logs"))
	}

	showScriptIndicator := true
	if m.ScriptMode && !m.ScriptFinishedTime.IsZero() {
		elapsed := time.Since(m.ScriptFinishedTime)
		if elapsed > scriptDoneLinger {
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
		} else if player := m.ScriptPlayer; player != nil {
			// Local script playback (tape play)
			progress = player.Progress()
			currentCmd = player.CurrentIndex()
			totalCmds = player.TotalCommands()
			isFinished = player.IsFinished()
		}

		if totalCmds > 0 {
			if isFinished {
				scriptStatus = fmt.Sprintf("DONE • %d/%d commands", totalCmds, totalCmds)
			} else {
				barWidth := 15
				filledWidth := (progress * barWidth) / 100
				full, empty := "█", "░"
				if config.UseASCIIOnly {
					full, empty = "#", "-"
				}
				var bar strings.Builder
				for i := range barWidth {
					if i < filledWidth {
						bar.WriteString(full)
					} else {
						bar.WriteString(empty)
					}
				}

				// Display 1-based index for human readability (command 1 of N, not 0 of N)
				displayCmd := min(currentCmd+1, totalCmds)

				if m.ScriptPaused {
					scriptStatus = fmt.Sprintf("PAUSED • %s %d%% • %d/%d", bar.String(), progress, displayCmd, totalCmds)
				} else {
					scriptStatus = fmt.Sprintf("RUNNING • %s %d%% • %d/%d", bar.String(), progress, displayCmd, totalCmds)
				}
			}
		} else {
			scriptStatus = "TAPE"
		}

		pal := theme.UI()
		scriptStyle := lipgloss.NewStyle().
			Foreground(theme.ContrastText(pal.Accent)).
			Background(pal.Accent).
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
		} else if m.LayoutPrefixActive {
			title = "Layout"
			bindings = config.GetPrefixKeybindings("layout")
		} else {
			title = "Prefix"
			bindings = config.GetPrefixKeybindings("", m.IsDaemonSession)
		}

		// The overlay spends four rows on a blank pad above and below, the title
		// and its rule. A prefix with more bindings than the rest of the screen
		// can hold says how many it left out rather than running off the bottom
		// where they cannot be read.
		moreCount := 0
		if rh := m.GetRenderHeight(); rh > 0 {
			maxRows := max(rh-5, 1)
			if len(bindings) > maxRows {
				moreCount = len(bindings) - (maxRows - 1)
				bindings = bindings[:maxRows-1]
			}
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
		// The overlay carries two cells of padding on each side and sits two
		// cells in from the screen edge, so it can ask for at most that much
		// less than the screen. Descriptions are cut to whatever is left; the
		// key column is what the overlay is for and keeps its width.
		if maxWidth := m.GetRenderWidth() - 8; maxWidth > 0 && contentWidth > maxWidth {
			contentWidth = max(maxWidth, maxKeyLen+2)
		}
		descWidth := max(contentWidth-maxKeyLen-2, 1)

		// The panel's own ground and tokens. This overlay was the last one on a
		// palette of its own, which is why it was the only amber on the screen.
		pal := theme.UI()
		bg := pal.Surface

		var styledLines []string

		padLine := func(s string, targetWidth int) string {
			return overlay.Fill(s, targetWidth, bg)
		}

		titleStyled := overlay.Style(bg).Foreground(pal.Fg).Bold(true).
			Render(truncateString(strings.ToLower(title), contentWidth))
		styledLines = append(styledLines, padLine(titleStyled, contentWidth))
		styledLines = append(styledLines, overlay.Rule(contentWidth, bg, pal))

		for _, binding := range bindings {
			line := overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(binding.Key) +
				overlay.Style(bg).Render(strings.Repeat(" ", maxKeyLen-len(binding.Key)+2)) +
				overlay.Style(bg).Foreground(pal.FgDim).Render(truncateString(binding.Description, descWidth))
			styledLines = append(styledLines, padLine(line, contentWidth))
		}

		if moreCount > 0 {
			more := overlay.Style(bg).Foreground(pal.FgMute).
				Render(truncateString(fmt.Sprintf("+%d more", moreCount), contentWidth))
			styledLines = append(styledLines, padLine(more, contentWidth))
		}

		paddingH := overlay.Style(bg).Render("  ")
		emptyLine := overlay.Style(bg).Render(strings.Repeat(" ", contentWidth+4))

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
		// A binding list taller than the screen would otherwise be positioned
		// off the top, hiding the first entries with no way to reach them.
		overlayX = max(min(overlayX, renderWidth-overlayWidth), 0)
		overlayY = max(min(overlayY, renderHeight-overlayHeight), 0)

		whichKeyLayer := lipgloss.NewLayer(renderedOverlay).
			X(overlayX).
			Y(overlayY).
			Z(config.ZIndexWhichKey).
			ID("whichkey")

		layers = append(layers, whichKeyLayer)
	}

	// Notifications are no longer drawn here. They live in the dock's right-hand
	// block (see renderNotificationBlock), which is the placement decision: a
	// message never covers a pane, and it is never retired by a frame being
	// composed. Retiring one used to happen right here, inside render
	// composition, which is why a toast could sit on screen for seventeen seconds
	// after it expired whenever the session went quiet enough that no further
	// frame was drawn. Expiry belongs to the tick now.

	focusedWindow := m.GetFocusedWindow()
	if focusedWindow != nil && focusedWindow.CopyMode != nil &&
		focusedWindow.CopyMode.Active &&
		focusedWindow.CopyMode.State == terminal.CopyModeSearch {

		searchQuery := focusedWindow.CopyMode.SearchQuery
		matchCount := len(focusedWindow.CopyMode.SearchMatches)
		currentMatch := focusedWindow.CopyMode.CurrentMatch

		// The rename dialog's canon: text on Surface, a reverse-video cell for
		// the cursor rather than a block glyph drawn in the foreground colour,
		// and the match count in the accent because it is the answer the search
		// is for.
		pal := theme.UI()
		bg := pal.Surface
		body := overlay.Style(bg).Foreground(pal.Fg).Render("/"+searchQuery) +
			overlay.Cursor(" ", bg, pal.Fg)
		switch {
		case matchCount > 0:
			body += overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).
				Render(fmt.Sprintf(" [%d/%d]", currentMatch+1, matchCount))
		case searchQuery != "":
			body += overlay.Style(bg).Foreground(pal.FgMute).Render(" [0]")
		}

		pad := overlay.Style(bg).Render(" ")
		renderedSearch := pad + body + pad

		searchOff := focusedWindow.BorderOffset()
		searchX := focusedWindow.X + searchOff + 1
		searchY := focusedWindow.Y + focusedWindow.Height - searchOff - 1

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
			rightMargin := 2
			// The strip grows to the left from the right edge, so on a narrow
			// screen it would start off the left edge; drop the oldest keys
			// until what is left fits instead.
			showkeysContent := m.renderShowkeysFitted(max(m.GetRenderWidth()-rightMargin, 1))
			contentWidth := lipgloss.Width(showkeysContent)
			contentHeight := lipgloss.Height(showkeysContent)

			dockOffset := 0
			if config.DockbarPosition == "bottom" {
				dockOffset = config.DockHeight
			}

			x := max(m.GetRenderWidth()-contentWidth-rightMargin, 0)
			y := max(m.GetRenderHeight()-contentHeight-dockOffset, 0)

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

	// The context menu is placed last so it sits above everything else it may
	// have been opened on top of, and so its recorded bounds are from the frame
	// the user is actually looking at.
	layers = m.placeContextMenu(layers)

	return layers
}
