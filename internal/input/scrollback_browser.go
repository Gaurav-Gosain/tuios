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

const (
	maxVimCount   = 100000
	maxHorizCount = 1000
)

// HandleScrollbackBrowserKey handles keyboard input when the scrollback browser is open.
func HandleScrollbackBrowserKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	browser := o.ScrollbackBrowser
	if browser == nil {
		o.ShowScrollbackBrowser = false
		return o, nil
	}

	keyStr := msg.String()

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

	if browser.OutputMode {
		return handleBrowserOutputModeKey(msg, keyStr, browser, o)
	}

	if browserListKey(o, keyStr, browser) {
		return o, nil
	}

	switch keyStr {
	case "q", "esc":
		o.ShowScrollbackBrowser = false
		o.ScrollbackBrowser = nil
		return o, nil

	case "ctrl+d":
		browser.PageDown(listPage)
	case "ctrl+u":
		browser.PageUp(listPage)

	case "l", "right":
		browser.EnterOutputMode()

	case "J", "ctrl+e":
		browser.ScrollPreviewDown()
	case "K", "ctrl+y":
		browser.ScrollPreviewUp()

	case "space":
		browser.ToggleSelect()
		browser.Next()

	case "/":
		browser.SearchActive = true
		browser.Search.Set("")

	case "tab":
		browser.CycleMode()
	case "1":
		browser.SetMode(scrollback.ModeCommands)
	case "2":
		browser.SetMode(scrollback.ModeJSON)
	case "3":
		browser.SetMode(scrollback.ModePaths)

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

	case "c":
		text := browser.SelectedCommandText()
		if text != "" {
			o.ShowNotification(
				fmt.Sprintf("Copied: %s", truncateForNotif(text, 30)),
				"success", o.Settings.NotificationDuration,
			)
			return o, tea.SetClipboard(vt.StripSixelMarkers(text))
		}

	case "enter":
		text := browser.SelectedCommandText()
		if text == "" {
			return o, nil
		}
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow == nil {
			return o, nil
		}

		o.ShowScrollbackBrowser = false
		o.ScrollbackBrowser = nil

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

	markers := term.SemanticMarkers()
	if markers != nil {
		o.Log("info", "Scrollback browser: %d OSC 133 markers found", markers.Len())
	} else {
		o.Log("info", "Scrollback browser: no semantic marker list")
	}

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

	if len(keyStr) == 1 && keyStr[0] >= '1' && keyStr[0] <= '9' {
		vim.PendingCount = min(vim.PendingCount*10+int(keyStr[0]-'0'), maxVimCount)
		return o, nil
	}
	if keyStr == "0" && vim.PendingCount > 0 {
		vim.PendingCount = min(vim.PendingCount*10, maxVimCount)
		return o, nil
	}

	count := vim.ConsumeCount()
	vCount := count
	if n := len(vim.Lines); n > 0 && vCount > n {
		vCount = n
	}
	hCount := min(count, maxHorizCount)
	center := false
	isVisual := vim.Mode == scrollback.VimVisualChar || vim.Mode == scrollback.VimVisualLine

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

func handleScrollbackBrowserMouseWheel(msg tea.MouseWheelMsg, o *app.OS) (*app.OS, tea.Cmd) {
	browser := o.ScrollbackBrowser
	if browser == nil {
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

func handleScrollbackBrowserMouseClick(msg tea.MouseClickMsg, o *app.OS) (*app.OS, tea.Cmd) {
	browser := o.ScrollbackBrowser
	if browser == nil {
		return o, nil
	}

	if msg.Button != tea.MouseLeft {
		return o, nil
	}

	rightPaneX := browser.LayoutLeftW + 6
	rightPaneY := 5
	rightPaneW := browser.LayoutRightW
	rightPaneH := browser.LayoutPaneH - 1

	leftPaneX := 3
	leftPaneY := 4
	leftPaneH := browser.LayoutPaneH

	x, y := msg.X, msg.Y

	if browser.OutputMode && browser.Vim != nil {
		vim := browser.Vim

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

				if vim.Mode == scrollback.VimVisualChar || vim.Mode == scrollback.VimVisualLine {
					vim.ExitVisual()
				}

				browser.DragActive = true
				browser.DragOriginY = vim.CursorY
				browser.DragOriginX = vim.CursorX

				vim.EnsureVisible()
			}
		}
	} else {
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
			browser.EnterOutputMode()
		}
	}

	return o, nil
}

func handleScrollbackBrowserMouseMotion(msg tea.MouseMotionMsg, o *app.OS) (*app.OS, tea.Cmd) {
	browser := o.ScrollbackBrowser
	if browser == nil || !browser.DragActive {
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

	if vim.Mode != scrollback.VimVisualChar && vim.Mode != scrollback.VimVisualLine {
		vim.CursorY = browser.DragOriginY
		vim.CursorX = browser.DragOriginX
		vim.EnterVisualChar()
	}

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

func handleScrollbackBrowserMouseRelease(o *app.OS) (*app.OS, tea.Cmd) {
	browser := o.ScrollbackBrowser
	if browser == nil {
		return o, nil
	}
	browser.DragActive = false
	return o, nil
}

func truncateForNotif(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

type browserMotion struct {
	move    func(*scrollback.VimState)
	byCount func(v *scrollback.VimState, count int)
	steps   motionSteps
	center  bool
}

type motionSteps uint8

const (
	stepsOnce motionSteps = iota
	stepsVertical
	stepsHorizontal
)

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

func browserListLen(browser *scrollback.Browser) int {
	switch browser.Mode {
	case scrollback.ModeJSON:
		return len(browser.FilteredJSON)
	case scrollback.ModePaths:
		return len(browser.FilteredPaths)
	}
	return len(browser.FilteredIdx)
}
