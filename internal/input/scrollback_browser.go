package input

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/scrollback"
)

// HandleScrollbackBrowserKey handles keyboard input when the scrollback browser is open.
func HandleScrollbackBrowserKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	browser, ok := o.ScrollbackBrowser.(*scrollback.Browser)
	if browser == nil || !ok {
		o.ShowScrollbackBrowser = false
		return o, nil
	}

	keyStr := msg.String()

	// Search mode: capture text input
	if browser.SearchActive {
		switch keyStr {
		case "esc":
			browser.SearchActive = false
			return o, nil
		case "enter":
			browser.SearchActive = false
			return o, nil
		case "backspace":
			if len(browser.SearchQuery) > 0 {
				browser.SetSearch(browser.SearchQuery[:len(browser.SearchQuery)-1])
			}
			return o, nil
		default:
			// Accept printable characters
			if len(keyStr) == 1 && keyStr[0] >= 32 && keyStr[0] <= 126 {
				browser.SetSearch(browser.SearchQuery + keyStr)
				return o, nil
			}
			return o, nil
		}
	}

	// Help overlay: toggle with ?, dismiss with esc/q/?
	if browser.ShowHelp {
		if keyStr == "?" || keyStr == "esc" || keyStr == "q" {
			browser.ShowHelp = false
		}
		return o, nil
	}
	if keyStr == "?" {
		browser.ShowHelp = true
		return o, nil
	}

	// Output mode: navigate lines in right pane
	if browser.OutputMode {
		return handleBrowserOutputModeKey(keyStr, browser, o)
	}

	// Normal mode keybindings
	switch keyStr {
	// Close
	case "q", "esc":
		o.ShowScrollbackBrowser = false
		o.ScrollbackBrowser = nil
		return o, nil

	// Navigation
	case "j", "down":
		browser.Next()
	case "k", "up":
		browser.Prev()
	case "ctrl+d":
		browser.PageDown(10)
	case "ctrl+u":
		browser.PageUp(10)
	case "g":
		// Go to top (gg pattern  - single g goes to top in this context)
		browser.SelectedIdx = 0
		browser.PreviewScroll = 0
	case "G":
		// Go to bottom
		var count int
		switch browser.Mode {
		case scrollback.ModeJSON:
			count = len(browser.FilteredJSON)
		case scrollback.ModePaths:
			count = len(browser.FilteredPaths)
		default:
			count = len(browser.FilteredIdx)
		}
		if count > 0 {
			browser.SelectedIdx = count - 1
		}
		browser.PreviewScroll = 0

	// Enter output mode
	case "l", "right":
		browser.EnterOutputMode()

	// Preview scroll
	case "J", "ctrl+e":
		browser.ScrollPreviewDown()
	case "K", "ctrl+y":
		browser.ScrollPreviewUp()

	// Multi-select
	case "space":
		browser.ToggleSelect()
		browser.Next()

	// Search
	case "/":
		browser.SearchActive = true
		browser.SearchQuery = ""

	// Mode switching
	case "tab":
		browser.CycleMode()
	case "1":
		browser.SetMode(scrollback.ModeCommands)
	case "2":
		browser.SetMode(scrollback.ModeJSON)
	case "3":
		browser.SetMode(scrollback.ModePaths)

	// Copy output to clipboard
	case "y":
		text := browser.SelectedText()
		if text != "" {
			o.ShowNotification(
				fmt.Sprintf("Copied %d chars", len(text)),
				"success", config.NotificationDuration,
			)
			return o, tea.SetClipboard(text)
		}
		o.ShowNotification("Nothing to copy", "warning", config.NotificationDuration)

	// Copy command text to clipboard
	case "c":
		text := browser.SelectedCommandText()
		if text != "" {
			o.ShowNotification(
				fmt.Sprintf("Copied: %s", truncateForNotif(text, 30)),
				"success", config.NotificationDuration,
			)
			return o, tea.SetClipboard(text)
		}

	// Paste selected command back to terminal
	case "enter":
		text := browser.SelectedCommandText()
		if text == "" {
			return o, nil
		}
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow == nil {
			return o, nil
		}

		// Close browser first
		o.ShowScrollbackBrowser = false
		o.ScrollbackBrowser = nil

		// Send text to terminal (with bracketed paste if supported)
		data := []byte(text)
		if focusedWindow.Terminal != nil && focusedWindow.Terminal.BracketedPasteEnabled() {
			data = append([]byte("\x1b[200~"), data...)
			data = append(data, []byte("\x1b[201~")...)
		}
		_ = focusedWindow.SendInput(data)
		o.ShowNotification(
			fmt.Sprintf("Pasted: %s", truncateForNotif(text, 30)),
			"info", config.NotificationDuration,
		)
	}

	return o, nil
}

// OpenScrollbackBrowser opens the scrollback browser for the focused terminal window.
func OpenScrollbackBrowser(o *app.OS) {
	focusedWindow := o.GetFocusedWindow()
	if focusedWindow == nil || focusedWindow.Terminal == nil {
		o.ShowNotification("No terminal to browse", "warning", 2*time.Second)
		return
	}

	term := focusedWindow.Terminal

	// Log marker diagnostics
	markers := term.SemanticMarkers()
	if markers != nil {
		o.Log("info", "Scrollback browser: %d OSC 133 markers found", markers.Len())
	} else {
		o.Log("info", "Scrollback browser: no semantic marker list")
	}

	// Wire up debug logging so parser diagnostics appear in the log viewer
	scrollback.DebugLogFunc = func(format string, args ...any) {
		o.Log("debug", "[scrollback] "+format, args...)
	}
	blocks := scrollback.ParseBlocks(term)
	scrollback.DebugLogFunc = nil
	if len(blocks) == 0 {
		o.ShowNotification("No commands found in scrollback", "info", 2*time.Second)
		return
	}

	o.Log("info", "Scrollback browser: %d command blocks parsed (method=%s)", len(blocks), blocks[0].Method)

	browser := scrollback.NewBrowser(blocks)
	browser.ParseMethod = blocks[0].Method
	if markers != nil {
		browser.MarkerCount = markers.Len()
	}
	o.ScrollbackBrowser = browser
	o.ShowScrollbackBrowser = true
}

func handleBrowserOutputModeKey(keyStr string, browser *scrollback.Browser, o *app.OS) (*app.OS, tea.Cmd) {
	vim := browser.Vim
	if vim == nil {
		browser.ExitOutputMode()
		return o, nil
	}

	// Search sub-mode: capture typed characters
	if vim.Mode == scrollback.VimSearch {
		switch keyStr {
		case "esc":
			vim.Mode = scrollback.VimNormal
			vim.SearchQuery = ""
		case "enter":
			vim.Mode = scrollback.VimNormal
			vim.SearchExecute()
		case "backspace":
			if len(vim.SearchQuery) > 0 {
				vim.SearchQuery = vim.SearchQuery[:len(vim.SearchQuery)-1]
			}
		default:
			if len(keyStr) == 1 && keyStr[0] >= 32 && keyStr[0] <= 126 {
				vim.SearchQuery += keyStr
			}
		}
		vim.EnsureVisible()
		return o, nil
	}

	// Pending character search (f/F/t/T waiting for char)
	if vim.PendingCharSearch {
		vim.PendingCharSearch = false
		if keyStr == "esc" {
			return o, nil
		}
		if len(keyStr) == 1 && keyStr[0] >= 32 && keyStr[0] <= 126 {
			vim.FindChar(rune(keyStr[0]), vim.LastCharSearchDir, vim.LastCharSearchTill)
		}
		vim.EnsureVisible()
		return o, nil
	}

	// Count prefix: accumulate digits
	if len(keyStr) == 1 && keyStr[0] >= '1' && keyStr[0] <= '9' {
		vim.PendingCount = vim.PendingCount*10 + int(keyStr[0]-'0')
		return o, nil
	}
	if keyStr == "0" && vim.PendingCount > 0 {
		vim.PendingCount = vim.PendingCount * 10
		return o, nil
	}

	count := vim.ConsumeCount()
	center := false
	isVisual := vim.Mode == scrollback.VimVisualChar || vim.Mode == scrollback.VimVisualLine

	switch keyStr {
	// Basic movement
	case "j", "down":
		for range count {
			vim.MoveDown()
		}
	case "k", "up":
		for range count {
			vim.MoveUp()
		}
	case "h":
		for range count {
			vim.MoveLeft()
		}
	case "l":
		for range count {
			vim.MoveRight()
		}

	// Line position
	case "0":
		vim.MoveToLineStart()
	case "^":
		vim.MoveToFirstNonBlank()
	case "$":
		vim.MoveToLineEnd()

	// Word movement
	case "w":
		for range count {
			vim.WordForward()
		}
	case "b":
		for range count {
			vim.WordBackward()
		}
	case "e":
		for range count {
			vim.WordEnd()
		}
	case "W":
		for range count {
			vim.WORDForward()
		}
	case "B":
		for range count {
			vim.WORDBackward()
		}
	case "E":
		for range count {
			vim.WORDEnd()
		}

	// Character search
	case "f":
		vim.PendingCharSearch = true
		vim.LastCharSearchDir = 1
		vim.LastCharSearchTill = false
		return o, nil
	case "F":
		vim.PendingCharSearch = true
		vim.LastCharSearchDir = -1
		vim.LastCharSearchTill = false
		return o, nil
	case "t":
		vim.PendingCharSearch = true
		vim.LastCharSearchDir = 1
		vim.LastCharSearchTill = true
		return o, nil
	case "T":
		vim.PendingCharSearch = true
		vim.LastCharSearchDir = -1
		vim.LastCharSearchTill = true
		return o, nil
	case ";":
		for range count {
			vim.RepeatCharSearch(false)
		}
	case ",":
		for range count {
			vim.RepeatCharSearch(true)
		}

	// Jump
	case "g":
		if vim.PendingG && time.Since(vim.LastGTime) < 500*time.Millisecond {
			vim.MoveToTop()
			vim.PendingG = false
		} else {
			vim.PendingG = true
			vim.LastGTime = time.Now()
			return o, nil
		}
	case "G":
		if count > 1 {
			vim.MoveToLine(count)
		} else {
			vim.MoveToBottom()
		}

	// Screen position
	case "H":
		vim.MoveToScreenTop()
	case "M":
		vim.MoveToScreenMiddle()
	case "L":
		vim.MoveToScreenBottom()

	// Page movement
	case "ctrl+d":
		vim.HalfPageDown()
		center = true
	case "ctrl+u":
		vim.HalfPageUp()
		center = true
	case "ctrl+f":
		vim.PageDown()
		center = true
	case "ctrl+b":
		vim.PageUp()
		center = true

	// Paragraph
	case "{":
		for range count {
			vim.ParagraphUp()
		}
	case "}":
		for range count {
			vim.ParagraphDown()
		}

	// Bracket matching
	case "%":
		vim.MatchBracket()

	// Search
	case "/":
		vim.Mode = scrollback.VimSearch
		vim.SearchQuery = ""
		return o, nil
	case "n":
		for range count {
			vim.SearchNext()
		}
	case "N":
		for range count {
			vim.SearchPrev()
		}

	// Visual modes
	case "v":
		if vim.Mode == scrollback.VimVisualChar {
			vim.ExitVisual()
		} else {
			vim.EnterVisualChar()
		}
	case "V":
		if vim.Mode == scrollback.VimVisualLine {
			vim.ExitVisual()
		} else {
			vim.EnterVisualLine()
		}

	// Yank
	case "y":
		if isVisual {
			text := vim.SelectedText()
			if text != "" {
				vim.ExitVisual()
				o.ShowNotification(
					fmt.Sprintf("Copied %d chars", len(text)),
					"success", config.NotificationDuration,
				)
				vim.EnsureVisible()
				return o, tea.SetClipboard(text)
			}
		} else {
			text := vim.SelectedText()
			if text != "" {
				o.ShowNotification(
					fmt.Sprintf("Copied %d chars", len(text)),
					"success", config.NotificationDuration,
				)
				vim.EnsureVisible()
				return o, tea.SetClipboard(text)
			}
		}
		o.ShowNotification("Nothing to copy", "warning", config.NotificationDuration)

	// Exit
	case "esc", "q":
		if isVisual {
			vim.ExitVisual()
		} else {
			browser.ExitOutputMode()
			return o, nil
		}
	case "left":
		for range count {
			vim.MoveLeft()
		}
	case "right":
		for range count {
			vim.MoveRight()
		}
	}

	if center {
		vim.CenterView()
	} else {
		vim.EnsureVisible()
	}
	return o, nil
}

// handleScrollbackBrowserMouseWheel handles mouse wheel events when the scrollback browser is open.
func handleScrollbackBrowserMouseWheel(msg tea.MouseWheelMsg, o *app.OS) (*app.OS, tea.Cmd) {
	browser, ok := o.ScrollbackBrowser.(*scrollback.Browser)
	if browser == nil || !ok {
		return o, nil
	}

	const scrollAmount = 3

	if browser.OutputMode && browser.Vim != nil {
		vim := browser.Vim
		switch msg.Button {
		case tea.MouseWheelUp:
			for range scrollAmount {
				vim.MoveUp()
			}
		case tea.MouseWheelDown:
			for range scrollAmount {
				vim.MoveDown()
			}
		}
		vim.EnsureVisible()
	} else {
		switch msg.Button {
		case tea.MouseWheelUp:
			for range scrollAmount {
				browser.Prev()
			}
		case tea.MouseWheelDown:
			for range scrollAmount {
				browser.Next()
			}
		}
	}

	return o, nil
}

// handleScrollbackBrowserMouseClick handles mouse click events when the scrollback browser is open.
func handleScrollbackBrowserMouseClick(msg tea.MouseClickMsg, o *app.OS) (*app.OS, tea.Cmd) {
	browser, ok := o.ScrollbackBrowser.(*scrollback.Browser)
	if browser == nil || !ok {
		return o, nil
	}

	if msg.Button != tea.MouseLeft {
		return o, nil
	}

	// Compute right pane screen bounds.
	// Layout: border(1) + padding(2) + leftW + " │ "(3) + rightPane
	rightPaneX := browser.LayoutLeftW + 6
	// First output line: border(1) + padding(1) + header(2) + paneHeader(1) = 5
	rightPaneY := 5
	rightPaneW := browser.LayoutRightW
	// Output rows = paneH - 1 (minus right pane header), minus 1 for scroll indicator if shown
	rightPaneH := browser.LayoutPaneH - 1

	// Left pane bounds.
	leftPaneX := 3 // border(1) + padding(2)
	leftPaneY := 4 // border(1) + padding(1) + header(2)
	leftPaneH := browser.LayoutPaneH

	x, y := msg.X, msg.Y

	if browser.OutputMode && browser.Vim != nil {
		vim := browser.Vim

		// Click in right pane output area
		if x >= rightPaneX && x < rightPaneX+rightPaneW && y >= rightPaneY && y < rightPaneY+rightPaneH {
			relX := x - rightPaneX
			relY := y - rightPaneY
			lineIdx := vim.ScrollY + relY

			if lineIdx >= 0 && lineIdx < len(vim.Lines) {
				vim.CursorY = lineIdx
				runes := []rune(vim.Lines[lineIdx])
				if relX >= len(runes) {
					vim.CursorX = max(len(runes)-1, 0)
				} else {
					vim.CursorX = relX
				}

				// Exit visual mode on plain click
				if vim.Mode == scrollback.VimVisualChar || vim.Mode == scrollback.VimVisualLine {
					vim.ExitVisual()
				}

				// Set up drag tracking for visual selection
				browser.DragActive = true
				browser.DragOriginY = vim.CursorY
				browser.DragOriginX = vim.CursorX

				vim.EnsureVisible()
			}
		}
	} else {
		// Not in output mode: click left pane to select, right pane to enter output
		if x >= leftPaneX && x < leftPaneX+browser.LayoutLeftW && y >= leftPaneY && y < leftPaneY+leftPaneH {
			relY := y - leftPaneY
			idx := browser.ScrollOffset + relY
			count := len(browser.FilteredIdx)
			switch browser.Mode {
			case scrollback.ModeJSON:
				count = len(browser.FilteredJSON)
			case scrollback.ModePaths:
				count = len(browser.FilteredPaths)
			}
			if idx >= 0 && idx < count {
				browser.SelectedIdx = idx
				browser.PreviewScroll = 0
			}
		} else if x >= rightPaneX && x < rightPaneX+rightPaneW && y >= rightPaneY {
			// Click in right pane: enter output mode
			browser.EnterOutputMode()
		}
	}

	return o, nil
}

// handleScrollbackBrowserMouseMotion handles mouse motion (drag) events.
func handleScrollbackBrowserMouseMotion(msg tea.MouseMotionMsg, o *app.OS) (*app.OS, tea.Cmd) {
	browser, ok := o.ScrollbackBrowser.(*scrollback.Browser)
	if browser == nil || !ok || !browser.DragActive {
		return o, nil
	}

	vim := browser.Vim
	if vim == nil || !browser.OutputMode {
		browser.DragActive = false
		return o, nil
	}

	rightPaneX := browser.LayoutLeftW + 6
	rightPaneY := 5
	rightPaneW := browser.LayoutRightW
	rightPaneH := browser.LayoutPaneH - 1

	x, y := msg.X, msg.Y
	if x < rightPaneX || x >= rightPaneX+rightPaneW || y < rightPaneY || y >= rightPaneY+rightPaneH {
		return o, nil
	}

	relX := x - rightPaneX
	relY := y - rightPaneY
	lineIdx := vim.ScrollY + relY

	if lineIdx < 0 || lineIdx >= len(vim.Lines) {
		return o, nil
	}

	// Enter visual char mode on first drag motion (if not already visual)
	if vim.Mode != scrollback.VimVisualChar && vim.Mode != scrollback.VimVisualLine {
		// Set anchor at drag origin
		vim.CursorY = browser.DragOriginY
		vim.CursorX = browser.DragOriginX
		vim.EnterVisualChar()
	}

	// Move cursor to current mouse position (extends visual selection)
	vim.CursorY = lineIdx
	runes := []rune(vim.Lines[lineIdx])
	if relX >= len(runes) {
		vim.CursorX = max(len(runes)-1, 0)
	} else {
		vim.CursorX = relX
	}

	vim.EnsureVisible()
	return o, nil
}

// handleScrollbackBrowserMouseRelease handles mouse release events.
func handleScrollbackBrowserMouseRelease(o *app.OS) (*app.OS, tea.Cmd) {
	browser, ok := o.ScrollbackBrowser.(*scrollback.Browser)
	if browser == nil || !ok {
		return o, nil
	}
	browser.DragActive = false
	return o, nil
}

func truncateForNotif(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen-3] + "..."
	}
	return s
}
