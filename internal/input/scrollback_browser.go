package input

import (
	"unicode/utf8"

	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/scrollback"
	"github.com/Gaurav-Gosain/tuios/internal/textedit"
	"tuios.dev/vt"
)

// Bounds on the vim-style count prefix in the scrollback browser. maxVimCount
// caps digit accumulation; maxHorizCount bounds horizontal and search motions,
// which saturate at the line edge after a handful of steps.
const (
	maxVimCount   = 100000
	maxHorizCount = 1000
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
		default:
			browser.SearchKey(keyStr, msg.Text)
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
		return handleBrowserOutputModeKey(msg, keyStr, browser, o)
	}

	// The list keys of [keybindings.lists] move the command list, letters
	// included: the list has no filter on its keys, / opens the search.
	if browserListKey(o, keyStr, browser) {
		return o, nil
	}

	// Normal mode keybindings
	switch keyStr {
	// Close
	case "q", "esc":
		o.ShowScrollbackBrowser = false
		o.ScrollbackBrowser = nil
		return o, nil

	// Half a page, as in vim and less, besides the list's page keys.
	case "ctrl+d":
		browser.PageDown(listPage)
	case "ctrl+u":
		browser.PageUp(listPage)

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
		browser.Search.Set("")

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
				"success", o.Settings.NotificationDuration,
			)
			return o, tea.SetClipboard(vt.StripSixelMarkers(text))
		}
		o.ShowNotification("Nothing to copy", "warning", o.Settings.NotificationDuration)

	// Copy command text to clipboard
	case "c":
		text := browser.SelectedCommandText()
		if text != "" {
			o.ShowNotification(
				fmt.Sprintf("Copied: %s", truncateForNotif(text, 30)),
				"success", o.Settings.NotificationDuration,
			)
			return o, tea.SetClipboard(vt.StripSixelMarkers(text))
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

		// Send the text as a paste. The line came from pane output, so it
		// goes through the same sanitizing as any other paste.
		_ = focusedWindow.Paste(text)
		o.ShowNotification(
			fmt.Sprintf("Pasted: %s", truncateForNotif(text, 30)),
			"info", o.Settings.NotificationDuration,
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

func handleBrowserOutputModeKey(msg tea.KeyPressMsg, keyStr string, browser *scrollback.Browser, o *app.OS) (*app.OS, tea.Cmd) {
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
			vim.Search.Set("")
		case "enter":
			vim.Mode = scrollback.VimNormal
			vim.SearchExecute()
		default:
			vim.Search.Edit(keyStr, msg.Text, textedit.LineOpts{AllowClear: true})
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

	// Count prefix: accumulate digits, capped so a long digit run cannot build
	// an enormous multiplier that drives a multi-second motion loop below.
	if len(keyStr) == 1 && keyStr[0] >= '1' && keyStr[0] <= '9' {
		vim.PendingCount = min(vim.PendingCount*10+int(keyStr[0]-'0'), maxVimCount)
		return o, nil
	}
	if keyStr == "0" && vim.PendingCount > 0 {
		vim.PendingCount = min(vim.PendingCount*10, maxVimCount)
		return o, nil
	}

	count := vim.ConsumeCount()
	// Clamp the motion count before the per-motion loops. Every motion saturates
	// at the buffer edge after a few steps, so a huge count only wastes work (and
	// the word motions allocate a []rune per iteration). Vertical/line-spanning
	// motions can repeat up to the line count; horizontal/search motions saturate
	// far sooner, so a small constant bounds them.
	vCount := count
	if n := len(vim.Lines); n > 0 && vCount > n {
		vCount = n
	}
	hCount := min(count, maxHorizCount)
	center := false
	isVisual := vim.Mode == scrollback.VimVisualChar || vim.Mode == scrollback.VimVisualLine

	// The g of gg. A pending g is for the very next key only, so g j g is
	// not gg, as in copy mode.
	finishesGG := vim.PendingG && time.Since(vim.LastGTime) < o.Settings.ChordTimeout()
	vim.PendingG = false
	if keyStr == "g" {
		if !finishesGG {
			vim.PendingG, vim.LastGTime = true, time.Now()
			return o, nil
		}
		vim.MoveToTop()
		vim.EnsureVisible()
		return o, nil
	}

	// The motions take copy mode's keys: the [keybindings.copy_mode] actions
	// and the keys copy mode reads itself, so a key rebound there moves here
	// too.
	action := fixedCopyMotions[keyStr]
	if action == "" {
		action = copyModeAction(msg, o)
	}
	if m, ok := browserMotions[action]; ok {
		n := 1
		switch m.steps {
		case stepsVertical:
			n = vCount
		case stepsHorizontal:
			n = hCount
		}
		if m.byCount != nil {
			m.byCount(vim, count)
		} else {
			for range n {
				m.move(vim)
			}
		}
		if m.center {
			vim.CenterView()
		} else {
			vim.EnsureVisible()
		}
		return o, nil
	}

	switch keyStr {
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

	// Search
	case "/":
		vim.Mode = scrollback.VimSearch
		vim.Search.Set("")
		return o, nil
	case "n":
		for range vCount {
			vim.SearchNext()
		}
	case "N":
		for range vCount {
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
					"success", o.Settings.NotificationDuration,
				)
				vim.EnsureVisible()
				return o, tea.SetClipboard(vt.StripSixelMarkers(text))
			}
		} else {
			text := vim.SelectedText()
			if text != "" {
				o.ShowNotification(
					fmt.Sprintf("Copied %d chars", len(text)),
					"success", o.Settings.NotificationDuration,
				)
				vim.EnsureVisible()
				return o, tea.SetClipboard(vt.StripSixelMarkers(text))
			}
		}
		o.ShowNotification("Nothing to copy", "warning", o.Settings.NotificationDuration)

	// Exit
	case "esc", "q":
		if isVisual {
			vim.ExitVisual()
		} else {
			browser.ExitOutputMode()
			return o, nil
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

	scrollAmount := o.Settings.ScrollLines

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

// truncateForNotif shortens s to at most maxLen runes for a notification,
// ending it with "..." when it was cut. It counts and cuts by rune, so a
// command holding multi-byte text is never split inside a character.
func truncateForNotif(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	// Too small to fit an ellipsis: hard-truncate on a rune boundary.
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

// browserMotion is a copy-mode motion as the scrollback browser runs it on its
// own text. steps says which count repeats it; byCount takes the count itself.
type browserMotion struct {
	move    func(*scrollback.VimState)
	byCount func(v *scrollback.VimState, count int)
	steps   motionSteps
	center  bool
}

// motionSteps is how a count repeats a motion: by lines, which a count may
// repeat up to the line count, by columns, capped at maxHorizCount, or not
// at all.
type motionSteps uint8

const (
	stepsOnce motionSteps = iota
	stepsVertical
	stepsHorizontal
)

// browserMotions are the scrollback browser's motions, by the copy-mode
// action whose keys run them.
var browserMotions = map[string]browserMotion{
	config.ActionCopyModeLeft:            {move: (*scrollback.VimState).MoveLeft, steps: stepsHorizontal},
	config.ActionCopyModeRight:           {move: (*scrollback.VimState).MoveRight, steps: stepsHorizontal},
	config.ActionCopyModeUp:              {move: (*scrollback.VimState).MoveUp, steps: stepsVertical},
	config.ActionCopyModeDown:            {move: (*scrollback.VimState).MoveDown, steps: stepsVertical},
	config.ActionCopyModeLineStart:       {move: (*scrollback.VimState).MoveToLineStart},
	config.ActionCopyModeFirstNonBlank:   {move: (*scrollback.VimState).MoveToFirstNonBlank},
	config.ActionCopyModeLineEnd:         {move: (*scrollback.VimState).MoveToLineEnd},
	config.ActionCopyModeWordForward:     {move: (*scrollback.VimState).WordForward, steps: stepsVertical},
	config.ActionCopyModeWordBackward:    {move: (*scrollback.VimState).WordBackward, steps: stepsVertical},
	config.ActionCopyModeWordEnd:         {move: (*scrollback.VimState).WordEnd, steps: stepsVertical},
	config.ActionCopyModeBigWordForward:  {move: (*scrollback.VimState).WORDForward, steps: stepsVertical},
	config.ActionCopyModeBigWordBackward: {move: (*scrollback.VimState).WORDBackward, steps: stepsVertical},
	config.ActionCopyModeBigWordEnd:      {move: (*scrollback.VimState).WORDEnd, steps: stepsVertical},
	config.ActionCopyModeHalfPageUp:      {move: (*scrollback.VimState).HalfPageUp, center: true},
	config.ActionCopyModeHalfPageDown:    {move: (*scrollback.VimState).HalfPageDown, center: true},
	config.ActionCopyModePageUp:          {move: (*scrollback.VimState).PageUp, center: true},
	config.ActionCopyModePageDown:        {move: (*scrollback.VimState).PageDown, center: true},
	config.ActionCopyModeScreenTop:       {move: (*scrollback.VimState).MoveToScreenTop},
	config.ActionCopyModeScreenMiddle:    {move: (*scrollback.VimState).MoveToScreenMiddle},
	config.ActionCopyModeScreenBottom:    {move: (*scrollback.VimState).MoveToScreenBottom},
	config.ActionCopyModeParagraphUp:     {move: (*scrollback.VimState).ParagraphUp, steps: stepsVertical},
	config.ActionCopyModeParagraphDown:   {move: (*scrollback.VimState).ParagraphDown, steps: stepsVertical},
	config.ActionCopyModeMatchingBracket: {move: (*scrollback.VimState).MatchBracket},
	config.ActionCopyModeRepeatCharSearch: {steps: stepsHorizontal, move: func(v *scrollback.VimState) {
		v.RepeatCharSearch(false)
	}},
	config.ActionCopyModeRepeatCharSearchBack: {steps: stepsHorizontal, move: func(v *scrollback.VimState) {
		v.RepeatCharSearch(true)
	}},
	config.ActionCopyModeBottom: {byCount: func(v *scrollback.VimState, count int) {
		if count > 1 {
			v.MoveToLine(count)
			return
		}
		v.MoveToBottom()
	}},
}

// browserListKey moves the scrollback browser's list for a key of
// [keybindings.lists], and reports whether the key was one.
func browserListKey(o *app.OS, key string, browser *scrollback.Browser) bool {
	switch o.ListKeys.Keys(key, true) {
	case listnav.None:
		return false
	case listnav.Up:
		browser.Prev()
	case listnav.Down:
		browser.Next()
	case listnav.PageUp:
		browser.PageUp(listPage)
	case listnav.PageDown:
		browser.PageDown(listPage)
	case listnav.Home:
		browser.SelectedIdx = 0
		browser.PreviewScroll = 0
	case listnav.End:
		if n := browserListLen(browser); n > 0 {
			browser.SelectedIdx = n - 1
		}
		browser.PreviewScroll = 0
	}
	return true
}

// browserListLen is how many rows the browser's list holds in its mode now.
func browserListLen(browser *scrollback.Browser) int {
	switch browser.Mode {
	case scrollback.ModeJSON:
		return len(browser.FilteredJSON)
	case scrollback.ModePaths:
		return len(browser.FilteredPaths)
	}
	return len(browser.FilteredIdx)
}
