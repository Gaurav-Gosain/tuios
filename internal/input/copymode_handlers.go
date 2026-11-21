package input

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/textedit"
)

// HandleCopyModeKey is the main dispatcher for copy mode input
func HandleCopyModeKey(msg tea.KeyPressMsg, o *app.OS, window *terminal.Window) (*app.OS, tea.Cmd) {
	if window.CopyMode == nil || !window.CopyMode.Active {
		return o, nil
	}

	if m, cmd, ok := copyModeSearchActionKey(msg, o, window); ok {
		return m, cmd
	}

	if cmd, ok := copyModeFocusKey(msg, o, window); ok {
		return o, cmd
	}

	if o.MultiCopy.Has(window.ID) {
		return handleMultiCopyKey(msg, o, window)
	}

	if pipe, ok := copyPipeKey(msg, o, window.CopyMode); ok {
		return copyPipeYank(o, window, pipe)
	}

	fx := &copyModeEffects{}
	dispatchCopyModeKey(msg, copyModeAction(msg, o), window, fx, &o.Settings)
	return fx.apply(o, window)
}

func copyModeFocusKey(msg tea.KeyPressMsg, o *app.OS, window *terminal.Window) (tea.Cmd, bool) {
	cm := window.CopyMode
	if o.KeybindRegistry == nil || cm.State == terminal.CopyModeSearch || waitingForChar(cm) {
		return nil, false
	}
	if o.MultiCopy.Has(window.ID) && o.MultiCopy.Save != nil {
		return nil, false
	}
	if config.CopyModeFixedKey(msg.String()) || config.CopyModeFixedKey(commandKey(msg)) {
		return nil, false
	}
	if _, ok := copyPipeKey(msg, o, cm); ok || copyModeAction(msg, o) != "" {
		return nil, false
	}
	var action string
	if o.Mode == app.TerminalMode {
		action = tableAction(o, msg, o.KeybindRegistry, config.TableTerminalMode)
		if !isTerminalFocusAction(action) {
			return nil, false
		}
	} else {
		action = lookupAction(o, msg, o.KeybindRegistry.GetAction)
		if !isTerminalFocusAction(action) && action != "focus_up" && action != "focus_down" {
			return nil, false
		}
	}
	_, cmd, ok := dispatchAction(action, msg, o)
	return cmd, ok
}

func copyModeAction(msg tea.KeyPressMsg, o *app.OS) string {
	return tableAction(o, msg, overlayKeys(o), config.TableCopyMode)
}

func dispatchCopyModeKey(msg tea.KeyPressMsg, action string, window *terminal.Window, fx *copyModeEffects, s *config.Settings) {
	window.RLockIO()
	defer window.RUnlockIO()
	runCopyModeKey(msg, action, window.CopyMode, window, fx, s)
}

func runCopyModeKey(msg tea.KeyPressMsg, action string, cm *terminal.CopyMode, grid terminal.CopyGrid, fx *copyModeEffects, s *config.Settings) {
	if action == config.ActionCopyModeExit && cm.State != terminal.CopyModeSearch && !waitingForChar(cm) {
		cm.PendingCount = 0
		msg := "Left copy mode"
		if cm.State.InVisual() {
			text := extractVisualText(cm, grid)
			cm.State = terminal.CopyModeNormal
			fx.SetClipboard(text)
			msg = fmt.Sprintf("Yanked %d chars and left copy mode", len(text))
		}
		fx.ExitCopyMode()
		fx.ShowNotification(msg, "info", s.NotificationDuration)
		fx.action = action
		return
	}
	if action != "" && runCopyModeAction(action, cm, grid, fx) {
		return
	}

	if commandKey(msg) == "ctrl+c" {
		cm.Pending = terminal.CopyPendingNone
		fx.ExitCopyMode()
		fx.ShowNotification("Left copy mode", "info", s.NotificationDuration)
		return
	}

	switch cm.State {
	case terminal.CopyModeSearch:
		handleSearchInput(msg, cm, grid, fx, s)
	case terminal.CopyModeVisualChar, terminal.CopyModeVisualLine, terminal.CopyModeVisualBlock:
		handleVisualInput(msg, cm, grid, fx, s)
	case terminal.CopyModeNormal:
		handleNormalInput(msg, cm, grid, fx, s)
	}
}

func runCopyModeAction(action string, cm *terminal.CopyMode, grid terminal.CopyGrid, fx *copyModeEffects) bool {
	if cm.State == terminal.CopyModeSearch || waitingForChar(cm) {
		return false
	}
	if action == config.ActionHints {
		fx.openHints = true
		fx.action = action
		return true
	}
	if action == config.ActionEditScrollback {
		fx.editScrollback = true
		fx.action = action
		return true
	}
	if !runCopyMotion(action, cm, grid, fx) {
		return false
	}
	fx.action = action
	return true
}

func handleNormalInput(msg tea.KeyPressMsg, cm *terminal.CopyMode, grid terminal.CopyGrid, fx *copyModeEffects, s *config.Settings) {
	keyStr := msg.String()

	if waitingForChar(cm) {
		takeSearchChar(keyStr, cm, grid, fx)
		return
	}
	keyStr = commandKey(msg)

	finishesGG := takePendingG(cm, s)
	dropStaleCount(cm, fx)

	if len(keyStr) == 1 && keyStr[0] >= '0' && keyStr[0] <= '9' {
		digit := int(keyStr[0] - '0')
		if digit == 0 && cm.PendingCount == 0 {
		} else {
			addCountDigit(cm, digit, fx)
			return
		}
	}

	count := cm.PendingCount
	if count == 0 {
		count = 1
	}

	defer func() {
		cm.PendingCount = 0
		if fx != nil {
			fx.ShowNotification("", "info", 0)
		}
	}()

	switch keyStr {
	case "q", "esc":
		fx.ExitCopyMode()
		fx.ShowNotification("Left copy mode", "info", s.NotificationDuration)
		return
	case "i":
		fx.ExitCopyMode()
		fx.ShowNotification("Terminal mode", "info", s.NotificationDuration)
		fx.EnterTerminalMode()
		return

	case "0":
		runCopyMotion(fixedCopyMotions["0"], cm, grid, fx)
	case "ctrl+b", "^", "$", "w", "b":
		runCopyMotion(fixedCopyMotions[keyStr], cm, grid, fx)

	case "g":
		pressG(cm, grid, finishesGG)

	case "f", "F", "t", "T":
		armCharSearch(cm, keyStr, count, fx)
		return

	case "/", "?":
		backward := keyStr == "?"
		cm.BeginSearch(backward, grid.ScrollbackLen())
		fx.ShowNotification(searchPrompt(backward), "info", 0)
		return
	case "n", "N":
		if len(cm.SearchMatches) == 0 {
			break
		}
		backward := cm.SearchBackward
		if keyStr == "N" {
			backward = !backward
		}
		wrapped := false
		for range count {
			if stepMatch(cm, grid, backward) {
				wrapped = true
			}
		}
		if wrapped {
			fx.ShowNotification(searchWrapMessage(backward), "info", s.NotificationDuration)
		}
	case "ctrl+l":
		cm.Search.Set("")
		cm.SearchMatches = nil
		cm.CurrentMatch = 0
		cm.SearchCache.Valid = false
		fx.ShowNotification("Search cleared", "info", s.NotificationDuration)
		fx.InvalidateCache()
		return

	case "v":
		enterVisualChar(cm, grid)
		fx.InvalidateCache()
		fx.ShowNotification("VISUAL", "info", 0)
		return
	case "V":
		enterVisualLine(cm, grid)
		fx.InvalidateCache()
		fx.ShowNotification("VISUAL LINE", "info", 0)
		return
	case "ctrl+v":
		enterVisualBlock(cm, grid)
		fx.InvalidateCache()
		fx.ShowNotification(visualBlockMessage, "info", 0)
		return
	}

	fx.InvalidateCache()
}

func handleSearchInput(msg tea.KeyPressMsg, cm *terminal.CopyMode, grid terminal.CopyGrid, fx *copyModeEffects, s *config.Settings) {
	key := msg.Key()
	searchPrefix := searchPrompt(cm.SearchBackward)
	switch key.Code {
	case tea.KeyEnter, tea.KeyKpEnter:
		cm.State = terminal.CopyModeNormal
		matchInfo := ""
		if len(cm.SearchMatches) > 0 {
			matchInfo = fmt.Sprintf(" (%s matches)", terminal.SearchMatchCount(len(cm.SearchMatches), s.SearchMaxMatches))
		}
		fx.ShowNotification(fmt.Sprintf("%s%s%s", searchPrefix, cm.SearchQuery(), matchInfo), "info", s.NotificationDuration)
	case tea.KeyEscape:
		cm.State = terminal.CopyModeNormal
		cm.Search.Set("")
		cm.SearchMatches = nil
		restoreSearchOrigin(cm, grid)
		fx.ShowNotification("", "info", 0)
	default:
		changed, consumed := cm.Search.Edit(msg.String(), key.Text, textedit.LineOpts{AllowClear: true})
		if changed {
			executeSearch(cm, grid, s.SearchMaxMatches)
		}
		if changed || consumed {
			fx.ShowNotification(searchPrefix+searchLineText(&cm.Search), "info", 0)
		}
	}
	fx.InvalidateCache()
}

func searchLineText(l *textedit.Line) string {
	before, at, after := l.Parts()
	if at == "" {
		return before
	}
	return before + "▏" + at + after
}

const visualBlockMessage = "VISUAL BLOCK"

func handleVisualInput(msg tea.KeyPressMsg, cm *terminal.CopyMode, grid terminal.CopyGrid, fx *copyModeEffects, s *config.Settings) {
	keyStr := msg.String()

	if waitingForChar(cm) {
		takeSearchChar(keyStr, cm, grid, fx)
		return
	}
	keyStr = commandKey(msg)

	finishesGG := takePendingG(cm, s)
	dropStaleCount(cm, fx)

	if len(keyStr) == 1 && keyStr[0] >= '0' && keyStr[0] <= '9' {
		digit := int(keyStr[0] - '0')
		if digit == 0 && cm.PendingCount == 0 {
		} else {
			addCountDigit(cm, digit, fx)
			return
		}
	}

	count := cm.PendingCount
	if count == 0 {
		count = 1
	}

	defer func() {
		cm.PendingCount = 0
		if fx != nil {
			fx.ShowNotification("", "info", 0)
		}
	}()

	switch keyStr {
	case "esc", "q":
		cm.State = terminal.CopyModeNormal
		fx.ShowNotification("", "info", 0)
	case "y", "c":
		text := extractVisualText(cm, grid)
		if cm.State == terminal.CopyModeVisualBlock {
			fx.FlashBlock(cm.VisualStart, cm.VisualEnd)
		} else {
			fx.Flash(cm.VisualStart, cm.VisualEnd)
		}
		cm.State = terminal.CopyModeNormal
		fx.ShowNotification(fmt.Sprintf("Yanked %d chars", len(text)), "success", s.NotificationDuration)
		fx.InvalidateCache()
		fx.SetClipboard(text)
		return

	case "f", "F", "t", "T":
		armCharSearch(cm, keyStr, count, fx)
		return

	case "0":
		runCopyMotion(fixedCopyMotions["0"], cm, grid, fx)
	case "ctrl+b", "^", "$", "w", "b":
		runCopyMotion(fixedCopyMotions[keyStr], cm, grid, fx)

	case "g":
		pressG(cm, grid, finishesGG)

	case "o":
		swapVisualEnds(cm, grid)

	case "v":
		if cm.State == terminal.CopyModeVisualBlock {
			cm.State = terminal.CopyModeVisualChar
			fx.ShowNotification("VISUAL", "info", 0)
		} else {
			cm.State = terminal.CopyModeNormal
			fx.ShowNotification("", "info", 0)
		}
	case "ctrl+v":
		switch cm.State {
		case terminal.CopyModeVisualBlock:
			cm.State = terminal.CopyModeNormal
			fx.ShowNotification("", "info", 0)
		case terminal.CopyModeVisualChar:
			cm.State = terminal.CopyModeVisualBlock
			fx.ShowNotification(visualBlockMessage, "info", 0)
		default:
			enterVisualBlock(cm, grid)
			fx.ShowNotification(visualBlockMessage, "info", 0)
		}
	case "V":
		if cm.State == terminal.CopyModeVisualLine {
			cm.State = terminal.CopyModeNormal
			fx.ShowNotification("", "info", 0)
		} else {
			enterVisualLine(cm, grid)
			fx.ShowNotification("VISUAL LINE", "info", 0)
		}
	}

	fx.InvalidateCache()
}

// HandleCopyModeMouseDrag handles mouse drag start in copy mode (initiates visual selection)
func HandleCopyModeMouseDrag(cm *terminal.CopyMode, window *terminal.Window, startX, startY int) {
	terminalX, terminalY, inContent := window.ScreenToTerminal(startX, startY)

	if !inContent {
		return
	}

	func() {
		window.RLockIO()
		defer window.RUnlockIO()

		if cm.State.InVisual() {
			cm.State = terminal.CopyModeNormal
		}

		cm.CursorX = terminalX
		cm.CursorY = terminalY

		for cm.CursorX > 0 {
			cell := getCellAtCursor(cm, window)
			if cell == nil || cell.Width > 0 {
				break
			}
			cm.CursorX--
		}

		enterVisualChar(cm, window)
	}()

	window.InvalidateCache()
}

// HandleCopyModeMouseMotion handles mouse motion during drag in copy mode.
// Returns the auto-scroll direction: -1 (up), 0 (none), 1 (down).
func HandleCopyModeMouseMotion(cm *terminal.CopyMode, window *terminal.Window, mouseX, mouseY int) int {
	if !cm.State.InVisual() {
		return 0
	}

	scrollDir := func() int {
		window.RLockIO()
		defer window.RUnlockIO()

		terminalX, terminalY, inContent := window.ScreenToTerminal(mouseX, mouseY)

		if !inContent {
			borderOff := window.BorderOffset()
			contentTop := window.Y + borderOff
			contentBottom := window.Y + borderOff + window.ContentHeight()

			dir := 0
			if mouseY < contentTop {
				dir = -1
				for range 3 {
					moveUp(cm, window)
				}
			} else if mouseY >= contentBottom {
				dir = 1
				for range 3 {
					moveDown(cm, window)
				}
			}
			updateVisualEnd(cm, window)
			return dir
		}

		cm.CursorX = terminalX
		cm.CursorY = terminalY

		for cm.CursorX > 0 {
			cell := getCellAtCursor(cm, window)
			if cell == nil || cell.Width > 0 {
				break
			}
			cm.CursorX--
		}

		updateVisualEnd(cm, window)
		return 0
	}()

	window.InvalidateCache()
	return scrollDir
}
