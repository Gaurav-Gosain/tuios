package input

import (
	"unicode/utf8"

	"fmt"
	"time"

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

	// Copy mode navigates the cell buffer (CellAt/Width/Height/scrollback) from
	// the input goroutine while the PTY reader mutates it under the write lock,
	// so the traversal needs the shared lock.
	//
	// The lock is scoped to the traversal ONLY. Every side effect the handlers
	// want (notifications, cache invalidation, leaving copy mode, entering
	// terminal mode, clipboard writes) is recorded in fx and applied below,
	// after the lock is dropped. Do not reintroduce direct o.* / window.* calls
	// inside this region: the handler would then be one PTY write or one nested
	// RLockIO away from the recursive read-lock deadlock, because a queued
	// LockIO writer starves any later reader on a sync.RWMutex and the handler
	// would be waiting on a lock it is itself holding.
	// A key bound to a copy mode search action opens the prompt from copy
	// mode too. It is checked first, because copy mode otherwise takes every
	// key. Neither action has a default key, so no copy-mode key is shadowed
	// unless the person binds one of them to it.
	if m, cmd, ok := copyModeSearchActionKey(msg, o, window); ok {
		return m, cmd
	}

	if cmd, ok := copyModeFocusKey(msg, o, window); ok {
		return o, cmd
	}

	// Multi copy mode drives every pane of the multifocus set with this key.
	// See copymode_multi.go.
	if o.MultiCopy.Has(window.ID) {
		return handleMultiCopyKey(msg, o, window)
	}

	// A [[keybindings.copy_pipe]] key. See copymode_pipe.go.
	if pipe, ok := copyPipeKey(msg, o, window.CopyMode); ok {
		return copyPipeYank(o, window, pipe)
	}

	fx := &copyModeEffects{}
	dispatchCopyModeKey(msg, copyModeAction(msg, o), window, fx, &o.Settings)
	return fx.apply(o, window)
}

// copyModeFocusKey moves focus for a key bound to a focus action, and
// reports whether it did. The pane stays in copy mode, as a pane does in tmux
// when select-pane leaves it. In terminal mode the terminal_mode focus keys
// count, and in window mode the window-mode ones, as they do outside copy
// mode. A key copy mode reads itself, a copy pipe key and a copy_mode binding
// keep their copy-mode meaning, and the search prompt, the multi copy save
// prompt and a pending f/t character take every key as text. The leader is not
// checked: Ctrl+B is copy mode's page up.
func copyModeFocusKey(msg tea.KeyPressMsg, o *app.OS, window *terminal.Window) (tea.Cmd, bool) {
	cm := window.CopyMode
	if o.KeybindRegistry == nil || cm.State == terminal.CopyModeSearch || cm.PendingCharSearch {
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

// copyModeAction is the [keybindings.copy_mode] action msg is bound to, or "".
func copyModeAction(msg tea.KeyPressMsg, o *app.OS) string {
	return tableAction(o, msg, overlayKeys(o), config.TableCopyMode)
}

// dispatchCopyModeKey runs one copy-mode key against one pane, under that
// pane's I/O read lock, and records what it wants done in fx. action is the
// [keybindings.copy_mode] action the key is bound to, or "".
func dispatchCopyModeKey(msg tea.KeyPressMsg, action string, window *terminal.Window, fx *copyModeEffects, s *config.Settings) {
	window.RLockIO()
	defer window.RUnlockIO()
	runCopyModeKey(msg, action, window.CopyMode, window, fx, s)
}

// runCopyModeKey runs one copy-mode key against grid and records what it
// wants done in fx. The caller holds whatever lock the grid needs.
func runCopyModeKey(msg tea.KeyPressMsg, action string, cm *terminal.CopyMode, grid terminal.CopyGrid, fx *copyModeEffects, s *config.Settings) {
	if action != "" && runCopyModeAction(action, cm, grid, fx) {
		return
	}

	// Ctrl+C leaves copy mode from every state, the search prompt and a
	// pending f/t character too, and copies nothing, as in tmux's vi mode.
	if commandKey(msg) == "ctrl+c" {
		cm.PendingCharSearch = false
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

// runCopyModeAction runs a bound copy-mode motion in normal mode or in a
// visual selection, and reports whether it ran. The search prompt and a
// pending f/t character take the key as text, so the action does not run
// there. A count typed before the key is dropped: the line motions ignore it,
// as 0 and $ do.
func runCopyModeAction(action string, cm *terminal.CopyMode, grid terminal.CopyGrid, fx *copyModeEffects) bool {
	if cm.State == terminal.CopyModeSearch || cm.PendingCharSearch {
		return false
	}
	if action == config.ActionHints {
		// The labels go on the view as it is scrolled now. A label that
		// copies ends copy mode; esc from hints keeps it.
		fx.openHints = true
		fx.action = action
		return true
	}
	if action == config.ActionEditScrollback {
		// The editor opens at the cursor, so the count and the selection
		// stay as they are for when it closes.
		fx.editScrollback = true
		fx.action = action
		return true
	}
	switch action {
	case config.ActionCopyModeLineStart:
		cm.CursorX = 0
	case config.ActionCopyModeFirstNonBlank:
		cm.CursorX = firstNonBlankCol(cm, grid)
	case config.ActionCopyModeLineEnd:
		cm.CursorX = lastTextCol(cm, grid)
	default:
		return false
	}
	if cm.PendingCount != 0 {
		cm.PendingCount = 0
		fx.ShowNotification("", "info", 0)
	}
	if cm.State.InVisual() {
		updateVisualEnd(cm, grid)
	}
	fx.InvalidateCache()
	fx.action = action
	return true
}

// handleNormalInput handles keys in normal navigation mode
func handleNormalInput(msg tea.KeyPressMsg, cm *terminal.CopyMode, grid terminal.CopyGrid, fx *copyModeEffects, s *config.Settings) {
	keyStr := msg.String()

	// Handle pending character search (f/F/t/T followed by character)
	if cm.PendingCharSearch {
		// Check for escape to cancel
		if keyStr == "esc" {
			cm.PendingCharSearch = false
			fx.ShowNotification("", "info", 0)
			return
		}

		cm.PendingCharSearch = false
		// Get the character from the key press
		if len(keyStr) == 1 && keyStr[0] >= 32 && keyStr[0] <= 126 {
			// Only accept printable ASCII characters
			char := rune(keyStr[0])
			cm.LastCharSearch = char
			findCharCount(cm, grid, char, max(cm.PendingCharCount, 1))
			fx.InvalidateCache()
			fx.ShowNotification("", "info", 0) // Clear notification
		} else {
			// Invalid character, cancel search
			fx.ShowNotification("", "info", 0)
		}
		return
	}
	// The pending search above takes the typed character. A command key falls
	// back to the base-layout key. See commandKey.
	keyStr = commandKey(msg)

	// A pending g is for the very next key: g j g is not gg.
	pendingG := cm.PendingGCount
	cm.PendingGCount = false
	dropStaleCount(cm, fx)

	// Handle digit keys for count prefix (1-9, 0 only if already has count)
	if len(keyStr) == 1 && keyStr[0] >= '0' && keyStr[0] <= '9' {
		digit := int(keyStr[0] - '0')
		// 0 is only part of count if we already have a count started (e.g., 10, 20)
		if digit == 0 && cm.PendingCount == 0 {
			// Fall through to handle '0' as "start of line" command
		} else {
			addCountDigit(cm, digit, fx)
			return
		}
	}

	// Get count (default to 1 if no count specified)
	count := cm.PendingCount
	if count == 0 {
		count = 1
	}

	// Clear count after reading it (will be reset after command execution)
	defer func() {
		cm.PendingCount = 0
		if fx != nil {
			fx.ShowNotification("", "info", 0) // Clear count display
		}
	}()

	switch keyStr {
	case "q", "esc":
		fx.ExitCopyMode()
		fx.ShowNotification("Left copy mode", "info", s.NotificationDuration)
		return
	case "i":
		// Exit copy mode and enter terminal mode
		fx.ExitCopyMode()
		fx.ShowNotification("Terminal mode", "info", s.NotificationDuration)
		// Enter terminal mode and start raw input reader
		fx.EnterTerminalMode()
		return

	// Navigation: basic movement
	case "h", "left":
		repeatMotion(count, cm, func() { moveLeft(cm, grid) })
	case "l", "right":
		repeatMotion(count, cm, func() { moveRight(cm, grid) })
	case "j", "down":
		repeatMotion(count, cm, func() { moveDown(cm, grid) })
	case "k", "up":
		repeatMotion(count, cm, func() { moveUp(cm, grid) })

	// Navigation: word movement
	case "w":
		repeatMotion(count, cm, func() { moveWordForward(cm, grid) })
	case "b":
		repeatMotion(count, cm, func() { moveWordBackward(cm, grid) })
	case "e":
		repeatMotion(count, cm, func() { moveWordEnd(cm, grid) })
	case "W":
		repeatMotion(count, cm, func() { moveWordForwardBig(cm, grid) })
	case "B":
		repeatMotion(count, cm, func() { moveWordBackwardBig(cm, grid) })
	case "E":
		repeatMotion(count, cm, func() { moveWordEndBig(cm, grid) })

	// Navigation: line movement
	case "0":
		cm.CursorX = 0
	case "^":
		cm.CursorX = firstNonBlankCol(cm, grid)
	case "$":
		cm.CursorX = lastTextCol(cm, grid)

	// Navigation: page movement
	case "ctrl+u":
		repeatMotion(count, cm, func() { moveHalfPageUp(cm, grid) })
	case "ctrl+d":
		repeatMotion(count, cm, func() { moveHalfPageDown(cm, grid) })
	case "ctrl+b", "pgup":
		repeatMotion(count, cm, func() { movePageUp(cm, grid) })
	case "ctrl+f", "pgdown":
		repeatMotion(count, cm, func() { movePageDown(cm, grid) })

	// Navigation: jump to top/bottom
	case "g":
		// Handle 'gg' sequence
		if pendingG && time.Since(cm.LastCommandTime) < 500*time.Millisecond {
			moveToTop(cm, grid)
		} else {
			cm.PendingGCount = true
			cm.LastCommandTime = time.Now()
		}
	case "G":
		// count + G goes to specific line (e.g., 10G goes to line 10)
		if count > 1 {
			// Go to specific line number (count is the line number)
			scrollbackLen := grid.ScrollbackLen()
			targetAbsY := count - 1 // Convert from 1-indexed to 0-indexed
			totalLines := scrollbackLen + grid.Text().Height()
			if targetAbsY >= totalLines {
				targetAbsY = totalLines - 1
			}

			// Move to target line using step-by-step movement
			currentAbsY := getAbsoluteY(cm, grid)
			diff := targetAbsY - currentAbsY
			if diff > 0 {
				for range diff {
					moveDown(cm, grid)
				}
			} else if diff < 0 {
				for range -diff {
					moveUp(cm, grid)
				}
			}
		} else {
			moveToBottom(cm, grid)
		}

	// Navigation: screen position
	case "H":
		// Move to top of screen
		cm.CursorY = 0
	case "M":
		// Move to middle of screen
		cm.CursorY = grid.ViewHeight() / 2
	case "L":
		// Move to bottom of screen
		cm.CursorY = grid.LastContentRow()

	// Navigation: paragraph movement
	case "{":
		repeatMotion(count, cm, func() { moveParagraphUp(cm, grid) })
	case "}":
		repeatMotion(count, cm, func() { moveParagraphDown(cm, grid) })

	// Navigation: matching bracket
	case "%":
		moveToMatchingBracket(cm, grid)

	// Character search (f/F/t/T)
	case "f":
		// Find character forward on current line
		cm.PendingCharSearch = true
		cm.PendingCharCount = count
		cm.LastCharSearchDir = 1
		cm.LastCharSearchTill = false
		fx.ShowNotification("f", "info", 0)
		return
	case "F":
		// Find character backward on current line
		cm.PendingCharSearch = true
		cm.PendingCharCount = count
		cm.LastCharSearchDir = -1
		cm.LastCharSearchTill = false
		fx.ShowNotification("F", "info", 0)
		return
	case "t":
		// Till character forward (stop before)
		cm.PendingCharSearch = true
		cm.PendingCharCount = count
		cm.LastCharSearchDir = 1
		cm.LastCharSearchTill = true
		fx.ShowNotification("t", "info", 0)
		return
	case "T":
		// Till character backward (stop before)
		cm.PendingCharSearch = true
		cm.PendingCharCount = count
		cm.LastCharSearchDir = -1
		cm.LastCharSearchTill = true
		fx.ShowNotification("T", "info", 0)
		return
	case ";":
		// Repeat last character search
		repeatMotion(count, cm, func() { repeatCharSearch(cm, grid, false) })
	case ",":
		// Repeat last character search in opposite direction
		repeatMotion(count, cm, func() { repeatCharSearch(cm, grid, true) })

	// Search
	case "/", "?":
		backward := keyStr == "?"
		cm.BeginSearch(backward, grid.ScrollbackLen())
		fx.ShowNotification(searchPrompt(backward), "info", 0) // Persistent until search complete
		return
	case "n", "N":
		// n repeats the last search in its own direction, N in the opposite
		// one: after ?, n goes up and N goes down, as in vim and tmux.
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
		// Clear search highlighting (like vim's :noh)
		cm.SearchQuery = ""
		cm.SearchMatches = nil
		cm.CurrentMatch = 0
		cm.SearchCache.Valid = false
		fx.ShowNotification("Search cleared", "info", s.NotificationDuration)
		fx.InvalidateCache()
		return

	// Visual mode
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

// handleSearchInput handles keys in search mode
func handleSearchInput(msg tea.KeyPressMsg, cm *terminal.CopyMode, grid terminal.CopyGrid, fx *copyModeEffects, s *config.Settings) {
	key := msg.Key()

	searchPrefix := searchPrompt(cm.SearchBackward)

	// The word chords come first: Option+Backspace has the code of backspace.
	if textedit.IsWordDeleteKey(msg.String()) {
		if cm.SearchQuery != "" {
			cm.SearchQuery = textedit.DropLastWord(cm.SearchQuery)
			executeSearch(cm, grid)
		}
		fx.ShowNotification(searchPrefix+cm.SearchQuery, "info", 0)
		fx.InvalidateCache()
		return
	}

	switch key.Code {
	case tea.KeyEnter, tea.KeyKpEnter:
		cm.State = terminal.CopyModeNormal
		matchInfo := ""
		if len(cm.SearchMatches) > 0 {
			matchInfo = fmt.Sprintf(" (%s matches)", terminal.SearchMatchCount(len(cm.SearchMatches)))
		}
		fx.ShowNotification(fmt.Sprintf("%s%s%s", searchPrefix, cm.SearchQuery, matchInfo), "info", s.NotificationDuration)
	case tea.KeyEscape:
		// Esc gives up the search: the cursor goes back to where the prompt
		// opened, as it does in vim.
		cm.State = terminal.CopyModeNormal
		cm.SearchQuery = ""
		cm.SearchMatches = nil
		restoreSearchOrigin(cm, grid)
		fx.ShowNotification("", "info", 0)
	case tea.KeyBackspace:
		if len(cm.SearchQuery) > 0 {
			_, size := utf8.DecodeLastRuneInString(cm.SearchQuery)
			cm.SearchQuery = cm.SearchQuery[:len(cm.SearchQuery)-size]
			executeSearch(cm, grid)
		}
		fx.ShowNotification(searchPrefix+cm.SearchQuery, "info", 0)
	default:
		if key.Text != "" {
			cm.SearchQuery += key.Text
			executeSearch(cm, grid)
			fx.ShowNotification(searchPrefix+cm.SearchQuery, "info", 0)
		}
	}

	fx.InvalidateCache()
}

// visualBlockMessage names a rectangular selection in the dock.
const visualBlockMessage = "VISUAL BLOCK"

// handleVisualInput handles keys in visual selection mode
func handleVisualInput(msg tea.KeyPressMsg, cm *terminal.CopyMode, grid terminal.CopyGrid, fx *copyModeEffects, s *config.Settings) {
	keyStr := msg.String()

	// Handle pending character search (f/F/t/T followed by character)
	if cm.PendingCharSearch {
		// Check for escape to cancel
		if keyStr == "esc" {
			cm.PendingCharSearch = false
			fx.ShowNotification("", "info", 0)
			return
		}

		cm.PendingCharSearch = false
		// Get the character from the key press
		if len(keyStr) == 1 && keyStr[0] >= 32 && keyStr[0] <= 126 {
			// Only accept printable ASCII characters
			char := rune(keyStr[0])
			cm.LastCharSearch = char
			findCharCount(cm, grid, char, max(cm.PendingCharCount, 1))
			updateVisualEnd(cm, grid)
			fx.InvalidateCache()
			fx.ShowNotification("", "info", 0) // Clear notification
		} else {
			// Invalid character, cancel search
			fx.ShowNotification("", "info", 0)
		}
		return
	}
	keyStr = commandKey(msg)

	// A pending g is for the very next key: g j g is not gg.
	pendingG := cm.PendingGCount
	cm.PendingGCount = false
	dropStaleCount(cm, fx)

	// Handle digit keys for count prefix in visual mode
	if len(keyStr) == 1 && keyStr[0] >= '0' && keyStr[0] <= '9' {
		digit := int(keyStr[0] - '0')
		// 0 is only part of count if we already have a count started
		if digit == 0 && cm.PendingCount == 0 {
			// Fall through to handle '0' as "start of line" command
		} else {
			addCountDigit(cm, digit, fx)
			return
		}
	}

	// Get count (default to 1 if no count specified)
	count := cm.PendingCount
	if count == 0 {
		count = 1
	}

	// Clear count after reading it
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
		// The region is taken before the selection ends, because the sweep
		// is drawn over it after the selection has gone.
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

	// Movement in visual mode extends selection: basic
	case "h", "left":
		repeatMotion(count, cm, func() { moveLeft(cm, grid) })
		updateVisualEnd(cm, grid)
	case "l", "right":
		repeatMotion(count, cm, func() { moveRight(cm, grid) })
		updateVisualEnd(cm, grid)
	case "j", "down":
		repeatMotion(count, cm, func() { moveDown(cm, grid) })
		updateVisualEnd(cm, grid)
	case "k", "up":
		repeatMotion(count, cm, func() { moveUp(cm, grid) })
		updateVisualEnd(cm, grid)

	// Word movement
	case "w":
		repeatMotion(count, cm, func() { moveWordForward(cm, grid) })
		updateVisualEnd(cm, grid)
	case "b":
		repeatMotion(count, cm, func() { moveWordBackward(cm, grid) })
		updateVisualEnd(cm, grid)
	case "e":
		repeatMotion(count, cm, func() { moveWordEnd(cm, grid) })
		updateVisualEnd(cm, grid)
	case "W":
		repeatMotion(count, cm, func() { moveWordForwardBig(cm, grid) })
		updateVisualEnd(cm, grid)
	case "B":
		repeatMotion(count, cm, func() { moveWordBackwardBig(cm, grid) })
		updateVisualEnd(cm, grid)
	case "E":
		repeatMotion(count, cm, func() { moveWordEndBig(cm, grid) })
		updateVisualEnd(cm, grid)

	// Character search (f/F/t/T)
	case "f":
		cm.PendingCharSearch = true
		cm.PendingCharCount = count
		cm.LastCharSearchDir = 1
		cm.LastCharSearchTill = false
		fx.ShowNotification("f", "info", 0)
		return
	case "F":
		cm.PendingCharSearch = true
		cm.PendingCharCount = count
		cm.LastCharSearchDir = -1
		cm.LastCharSearchTill = false
		fx.ShowNotification("F", "info", 0)
		return
	case "t":
		cm.PendingCharSearch = true
		cm.PendingCharCount = count
		cm.LastCharSearchDir = 1
		cm.LastCharSearchTill = true
		fx.ShowNotification("t", "info", 0)
		return
	case "T":
		cm.PendingCharSearch = true
		cm.PendingCharCount = count
		cm.LastCharSearchDir = -1
		cm.LastCharSearchTill = true
		fx.ShowNotification("T", "info", 0)
		return
	case ";":
		repeatCharSearch(cm, grid, false)
		updateVisualEnd(cm, grid)
	case ",":
		repeatCharSearch(cm, grid, true)
		updateVisualEnd(cm, grid)

	// Line movement
	case "0":
		cm.CursorX = 0
		updateVisualEnd(cm, grid)
	case "^":
		cm.CursorX = firstNonBlankCol(cm, grid)
		updateVisualEnd(cm, grid)
	case "$":
		cm.CursorX = lastTextCol(cm, grid)
		updateVisualEnd(cm, grid)

	// Page movement
	case "ctrl+u":
		moveHalfPageUp(cm, grid)
		updateVisualEnd(cm, grid)
	case "ctrl+d":
		moveHalfPageDown(cm, grid)
		updateVisualEnd(cm, grid)
	case "ctrl+b", "pgup":
		movePageUp(cm, grid)
		updateVisualEnd(cm, grid)
	case "ctrl+f", "pgdown":
		movePageDown(cm, grid)
		updateVisualEnd(cm, grid)

	// Jump movement
	case "g":
		// Detect the 'gg' sequence. Keys arrive singly, so a literal "gg" case
		// never matches; mirror the pending-g state used in normal mode.
		if pendingG && time.Since(cm.LastCommandTime) < 500*time.Millisecond {
			moveToTop(cm, grid)
			updateVisualEnd(cm, grid)
		} else {
			cm.PendingGCount = true
			cm.LastCommandTime = time.Now()
		}
	case "G":
		moveToBottom(cm, grid)
		updateVisualEnd(cm, grid)

	// Screen position
	case "H":
		cm.CursorY = 0
		updateVisualEnd(cm, grid)
	case "M":
		cm.CursorY = grid.ViewHeight() / 2
		updateVisualEnd(cm, grid)
	case "L":
		cm.CursorY = grid.LastContentRow()
		updateVisualEnd(cm, grid)

	// Paragraph movement
	case "{":
		moveParagraphUp(cm, grid)
		updateVisualEnd(cm, grid)
	case "}":
		moveParagraphDown(cm, grid)
		updateVisualEnd(cm, grid)

	// Bracket matching
	case "%":
		moveToMatchingBracket(cm, grid)
		updateVisualEnd(cm, grid)

	// Go to the other end of the selection
	case "o":
		swapVisualEnds(cm, grid)

	// Toggle visual mode (pressing v/V again exits visual mode)
	case "v":
		// Pressing v in a block keeps the two ends as a character selection.
		// Otherwise it exits visual mode and returns to normal mode.
		if cm.State == terminal.CopyModeVisualBlock {
			cm.State = terminal.CopyModeVisualChar
			fx.ShowNotification("VISUAL", "info", 0)
		} else {
			cm.State = terminal.CopyModeNormal
			fx.ShowNotification("", "info", 0)
		}
	case "ctrl+v":
		// Pressing ctrl+v in a block exits. A character selection keeps its two
		// ends as corners. A line selection has no columns, so a new block starts
		// at the cursor.
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
		// Pressing V in visual char mode switches to visual line mode
		// Pressing V in visual line mode exits to normal mode
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
	// Convert window-relative coordinates to terminal coordinates
	terminalX, terminalY, inContent := window.ScreenToTerminal(startX, startY)

	// Check bounds
	if !inContent {
		return
	}

	// Lock scoped to the cell-buffer traversal; InvalidateCache runs outside it.
	func() {
		window.RLockIO()
		defer window.RUnlockIO()

		// Always exit visual mode first if we're in it, then start fresh
		// This ensures each click-and-drag creates a new selection
		if cm.State.InVisual() {
			cm.State = terminal.CopyModeNormal
		}

		// Move cursor to drag start position
		cm.CursorX = terminalX
		cm.CursorY = terminalY

		// Adjust cursor to avoid landing on continuation cells of wide characters
		// Move left until we find a cell with Width > 0
		for cm.CursorX > 0 {
			cell := getCellAtCursor(cm, window)
			if cell == nil || cell.Width > 0 {
				break
			}
			cm.CursorX--
		}

		// Enter visual character mode for new selection
		enterVisualChar(cm, window)
	}()

	window.InvalidateCache()
}

// HandleCopyModeMouseMotion handles mouse motion during drag in copy mode.
// Returns the auto-scroll direction: -1 (up), 0 (none), 1 (down).
func HandleCopyModeMouseMotion(cm *terminal.CopyMode, window *terminal.Window, mouseX, mouseY int) int {
	// Only handle if in visual mode
	if !cm.State.InVisual() {
		return 0
	}

	// Lock scoped to the cell-buffer traversal; InvalidateCache runs outside it.
	scrollDir := func() int {
		window.RLockIO()
		defer window.RUnlockIO()

		// Convert window-relative coordinates to terminal coordinates
		terminalX, terminalY, inContent := window.ScreenToTerminal(mouseX, mouseY)

		// Auto-scroll when dragging outside content area
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

		// Update cursor position
		cm.CursorX = terminalX
		cm.CursorY = terminalY

		// Adjust cursor to avoid landing on continuation cells of wide characters
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
