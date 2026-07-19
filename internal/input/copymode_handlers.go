// Package input implements vim-style copy mode for TUIOS.
package input

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
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
	// want - notifications, cache invalidation, leaving copy mode, entering
	// terminal mode, clipboard writes - is recorded in fx and applied below,
	// after the lock is dropped. Do not reintroduce direct o.* / window.* calls
	// inside this region: the handler would then be one PTY write or one nested
	// RLockIO away from the recursive read-lock deadlock, because a queued
	// LockIO writer starves any later reader on a sync.RWMutex and the handler
	// would be waiting on a lock it is itself holding.
	cm := window.CopyMode
	fx := &copyModeEffects{}

	func() {
		window.RLockIO()
		defer window.RUnlockIO()

		switch cm.State {
		case terminal.CopyModeSearch:
			handleSearchInput(msg, cm, window, fx)
		case terminal.CopyModeVisualChar, terminal.CopyModeVisualLine:
			handleVisualInput(msg, cm, window, fx)
		case terminal.CopyModeNormal:
			handleNormalInput(msg, cm, window, fx)
		}
	}()

	return fx.apply(o, window)
}

// handleNormalInput handles keys in normal navigation mode
func handleNormalInput(msg tea.KeyPressMsg, cm *terminal.CopyMode, window *terminal.Window, fx *copyModeEffects) {
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
			findCharOnLine(cm, window, char, cm.LastCharSearchDir, cm.LastCharSearchTill)
			fx.InvalidateCache()
			fx.ShowNotification("", "info", 0) // Clear notification
		} else {
			// Invalid character, cancel search
			fx.ShowNotification("", "info", 0)
		}
		return
	}

	// Handle digit keys for count prefix (1-9, 0 only if already has count)
	if len(keyStr) == 1 && keyStr[0] >= '0' && keyStr[0] <= '9' {
		digit := int(keyStr[0] - '0')
		// 0 is only part of count if we already have a count started (e.g., 10, 20)
		if digit == 0 && cm.PendingCount == 0 {
			// Fall through to handle '0' as "start of line" command
		} else {
			// Accumulate count
			cm.PendingCount = cm.PendingCount*10 + digit
			cm.CountStartTime = time.Now()
			fx.ShowNotification(fmt.Sprintf("%d", cm.PendingCount), "info", 0)
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
		fx.ShowNotification("Copy Mode Exited", "info", config.NotificationDuration)
		return
	case "i":
		// Exit copy mode and enter terminal mode
		fx.ExitCopyMode()
		fx.ShowNotification("Terminal Mode", "info", config.NotificationDuration)
		// Enter terminal mode and start raw input reader
		fx.EnterTerminalMode()
		return

	// Navigation - basic movement
	case "h", "left":
		for range count {
			moveLeft(cm, window)
		}
	case "l", "right":
		for range count {
			moveRight(cm, window)
		}
	case "j", "down":
		for range count {
			moveDown(cm, window)
		}
	case "k", "up":
		for range count {
			moveUp(cm, window)
		}

	// Navigation - word movement
	case "w":
		for range count {
			moveWordForward(cm, window)
		}
	case "b":
		for range count {
			moveWordBackward(cm, window)
		}
	case "e":
		for range count {
			moveWordEnd(cm, window)
		}
	case "W":
		for range count {
			moveWordForwardBig(cm, window)
		}
	case "B":
		for range count {
			moveWordBackwardBig(cm, window)
		}
	case "E":
		for range count {
			moveWordEndBig(cm, window)
		}

	// Navigation - line movement
	case "0":
		cm.CursorX = 0
	case "^":
		cm.CursorX = 0 // Could be enhanced to skip leading whitespace
	case "$":
		cm.CursorX = max(0, window.Width-3) // Account for borders

	// Navigation - page movement
	case "ctrl+u":
		for range count {
			moveHalfPageUp(cm, window)
		}
	case "ctrl+d":
		for range count {
			moveHalfPageDown(cm, window)
		}
	case "ctrl+b", "pgup":
		for range count {
			movePageUp(cm, window)
		}
	case "ctrl+f", "pgdown":
		for range count {
			movePageDown(cm, window)
		}

	// Navigation - jump to top/bottom
	case "g":
		// Handle 'gg' sequence
		if cm.PendingGCount && time.Since(cm.LastCommandTime) < 500*time.Millisecond {
			moveToTop(cm, window)
			cm.PendingGCount = false
		} else {
			cm.PendingGCount = true
			cm.LastCommandTime = time.Now()
		}
	case "G":
		// count + G goes to specific line (e.g., 10G goes to line 10)
		if count > 1 {
			// Go to specific line number (count is the line number)
			scrollbackLen := window.ScrollbackLen()
			targetAbsY := count - 1 // Convert from 1-indexed to 0-indexed
			totalLines := scrollbackLen + window.Terminal.Height()
			if targetAbsY >= totalLines {
				targetAbsY = totalLines - 1
			}

			// Move to target line using step-by-step movement
			currentAbsY := getAbsoluteY(cm, window)
			diff := targetAbsY - currentAbsY
			if diff > 0 {
				for range diff {
					moveDown(cm, window)
				}
			} else if diff < 0 {
				for range -diff {
					moveUp(cm, window)
				}
			}
		} else {
			moveToBottom(cm, window)
		}

	// Navigation - screen position
	case "H":
		// Move to top of screen
		cm.CursorY = 0
	case "M":
		// Move to middle of screen
		cm.CursorY = window.Height / 2
	case "L":
		// Move to bottom of screen
		cm.CursorY = window.Height - 3

	// Navigation - paragraph movement
	case "{":
		for range count {
			moveParagraphUp(cm, window)
		}
	case "}":
		for range count {
			moveParagraphDown(cm, window)
		}

	// Navigation - matching bracket
	case "%":
		moveToMatchingBracket(cm, window)

	// Character search (f/F/t/T)
	case "f":
		// Find character forward on current line
		cm.PendingCharSearch = true
		cm.LastCharSearchDir = 1
		cm.LastCharSearchTill = false
		fx.ShowNotification("f", "info", 0)
		return
	case "F":
		// Find character backward on current line
		cm.PendingCharSearch = true
		cm.LastCharSearchDir = -1
		cm.LastCharSearchTill = false
		fx.ShowNotification("F", "info", 0)
		return
	case "t":
		// Till character forward (stop before)
		cm.PendingCharSearch = true
		cm.LastCharSearchDir = 1
		cm.LastCharSearchTill = true
		fx.ShowNotification("t", "info", 0)
		return
	case "T":
		// Till character backward (stop before)
		cm.PendingCharSearch = true
		cm.LastCharSearchDir = -1
		cm.LastCharSearchTill = true
		fx.ShowNotification("T", "info", 0)
		return
	case ";":
		// Repeat last character search
		for range count {
			repeatCharSearch(cm, window, false)
		}
	case ",":
		// Repeat last character search in opposite direction
		for range count {
			repeatCharSearch(cm, window, true)
		}

	// Search
	case "/":
		cm.State = terminal.CopyModeSearch
		cm.SearchQuery = ""
		cm.SearchBackward = false
		fx.ShowNotification("/", "info", 0) // Persistent until search complete
		return
	case "?":
		cm.State = terminal.CopyModeSearch
		cm.SearchQuery = ""
		cm.SearchBackward = true
		fx.ShowNotification("?", "info", 0) // Persistent until search complete
		return
	case "n":
		// n goes forward for /, backward for ?
		for range count {
			if cm.SearchBackward {
				prevMatch(cm, window)
			} else {
				nextMatch(cm, window)
			}
		}
	case "N":
		// N goes backward for /, forward for ?
		for range count {
			if cm.SearchBackward {
				nextMatch(cm, window)
			} else {
				prevMatch(cm, window)
			}
		}
	case "ctrl+l":
		// Clear search highlighting (like vim's :noh)
		cm.SearchQuery = ""
		cm.SearchMatches = nil
		cm.CurrentMatch = 0
		cm.SearchCache.Valid = false
		fx.ShowNotification("Search cleared", "info", config.NotificationDuration)
		fx.InvalidateCache()
		return

	// Visual mode
	case "v":
		enterVisualChar(cm, window)
		fx.InvalidateCache()
		fx.ShowNotification("VISUAL", "info", 0)
		return
	case "V":
		enterVisualLine(cm, window)
		fx.InvalidateCache()
		fx.ShowNotification("VISUAL LINE", "info", 0)
		return
	}

	fx.InvalidateCache()
}

// handleSearchInput handles keys in search mode
func handleSearchInput(msg tea.KeyPressMsg, cm *terminal.CopyMode, window *terminal.Window, fx *copyModeEffects) {
	key := msg.Key()

	// Determine search prefix based on direction
	searchPrefix := "/"
	if cm.SearchBackward {
		searchPrefix = "?"
	}

	switch key.Code {
	case tea.KeyEnter:
		cm.State = terminal.CopyModeNormal
		matchInfo := ""
		if len(cm.SearchMatches) > 0 {
			matchInfo = fmt.Sprintf(" (%d matches)", len(cm.SearchMatches))
		}
		fx.ShowNotification(fmt.Sprintf("%s%s%s", searchPrefix, cm.SearchQuery, matchInfo), "info", config.NotificationDuration)
	case tea.KeyEscape:
		cm.State = terminal.CopyModeNormal
		cm.SearchQuery = ""
		cm.SearchMatches = nil
		fx.ShowNotification("", "info", 0)
	case tea.KeyBackspace:
		if len(cm.SearchQuery) > 0 {
			cm.SearchQuery = cm.SearchQuery[:len(cm.SearchQuery)-1]
			executeSearch(cm, window)
		}
		fx.ShowNotification(searchPrefix+cm.SearchQuery, "info", 0)
	default:
		if key.Text != "" {
			cm.SearchQuery += key.Text
			executeSearch(cm, window)
			fx.ShowNotification(searchPrefix+cm.SearchQuery, "info", 0)
		}
	}

	fx.InvalidateCache()
}

// handleVisualInput handles keys in visual selection mode
func handleVisualInput(msg tea.KeyPressMsg, cm *terminal.CopyMode, window *terminal.Window, fx *copyModeEffects) {
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
			findCharOnLine(cm, window, char, cm.LastCharSearchDir, cm.LastCharSearchTill)
			updateVisualEnd(cm, window)
			fx.InvalidateCache()
			fx.ShowNotification("", "info", 0) // Clear notification
		} else {
			// Invalid character, cancel search
			fx.ShowNotification("", "info", 0)
		}
		return
	}

	// Handle digit keys for count prefix in visual mode
	if len(keyStr) == 1 && keyStr[0] >= '0' && keyStr[0] <= '9' {
		digit := int(keyStr[0] - '0')
		// 0 is only part of count if we already have a count started
		if digit == 0 && cm.PendingCount == 0 {
			// Fall through to handle '0' as "start of line" command
		} else {
			cm.PendingCount = cm.PendingCount*10 + digit
			cm.CountStartTime = time.Now()
			fx.ShowNotification(fmt.Sprintf("%d", cm.PendingCount), "info", 0)
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
		text := extractVisualText(cm, window)
		cm.State = terminal.CopyModeNormal
		fx.ShowNotification(fmt.Sprintf("Yanked %d chars", len(text)), "success", config.NotificationDuration)
		fx.InvalidateCache()
		fx.SetClipboard(text)
		return

	// Movement in visual mode extends selection - basic
	case "h", "left":
		for range count {
			moveLeft(cm, window)
		}
		updateVisualEnd(cm, window)
	case "l", "right":
		for range count {
			moveRight(cm, window)
		}
		updateVisualEnd(cm, window)
	case "j", "down":
		for range count {
			moveDown(cm, window)
		}
		updateVisualEnd(cm, window)
	case "k", "up":
		for range count {
			moveUp(cm, window)
		}
		updateVisualEnd(cm, window)

	// Word movement
	case "w":
		for range count {
			moveWordForward(cm, window)
		}
		updateVisualEnd(cm, window)
	case "b":
		for range count {
			moveWordBackward(cm, window)
		}
		updateVisualEnd(cm, window)
	case "e":
		for range count {
			moveWordEnd(cm, window)
		}
		updateVisualEnd(cm, window)
	case "W":
		for range count {
			moveWordForwardBig(cm, window)
		}
		updateVisualEnd(cm, window)
	case "B":
		for range count {
			moveWordBackwardBig(cm, window)
		}
		updateVisualEnd(cm, window)
	case "E":
		for range count {
			moveWordEndBig(cm, window)
		}
		updateVisualEnd(cm, window)

	// Character search (f/F/t/T)
	case "f":
		cm.PendingCharSearch = true
		cm.LastCharSearchDir = 1
		cm.LastCharSearchTill = false
		fx.ShowNotification("f", "info", 0)
		return
	case "F":
		cm.PendingCharSearch = true
		cm.LastCharSearchDir = -1
		cm.LastCharSearchTill = false
		fx.ShowNotification("F", "info", 0)
		return
	case "t":
		cm.PendingCharSearch = true
		cm.LastCharSearchDir = 1
		cm.LastCharSearchTill = true
		fx.ShowNotification("t", "info", 0)
		return
	case "T":
		cm.PendingCharSearch = true
		cm.LastCharSearchDir = -1
		cm.LastCharSearchTill = true
		fx.ShowNotification("T", "info", 0)
		return
	case ";":
		repeatCharSearch(cm, window, false)
		updateVisualEnd(cm, window)
	case ",":
		repeatCharSearch(cm, window, true)
		updateVisualEnd(cm, window)

	// Line movement
	case "0", "^":
		cm.CursorX = 0
		updateVisualEnd(cm, window)
	case "$":
		cm.CursorX = max(0, window.Width-3)
		updateVisualEnd(cm, window)

	// Page movement
	case "ctrl+u":
		moveHalfPageUp(cm, window)
		updateVisualEnd(cm, window)
	case "ctrl+d":
		moveHalfPageDown(cm, window)
		updateVisualEnd(cm, window)
	case "ctrl+b", "pgup":
		movePageUp(cm, window)
		updateVisualEnd(cm, window)
	case "ctrl+f", "pgdown":
		movePageDown(cm, window)
		updateVisualEnd(cm, window)

	// Jump movement
	case "g":
		// Detect the 'gg' sequence. Keys arrive singly, so a literal "gg" case
		// never matches; mirror the pending-g state used in normal mode.
		if cm.PendingGCount && time.Since(cm.LastCommandTime) < 500*time.Millisecond {
			moveToTop(cm, window)
			cm.PendingGCount = false
			updateVisualEnd(cm, window)
		} else {
			cm.PendingGCount = true
			cm.LastCommandTime = time.Now()
		}
	case "G":
		moveToBottom(cm, window)
		updateVisualEnd(cm, window)

	// Screen position
	case "H":
		cm.CursorY = 0
		updateVisualEnd(cm, window)
	case "M":
		cm.CursorY = window.Height / 2
		updateVisualEnd(cm, window)
	case "L":
		cm.CursorY = window.Height - 3
		updateVisualEnd(cm, window)

	// Paragraph movement
	case "{":
		moveParagraphUp(cm, window)
		updateVisualEnd(cm, window)
	case "}":
		moveParagraphDown(cm, window)
		updateVisualEnd(cm, window)

	// Bracket matching
	case "%":
		moveToMatchingBracket(cm, window)
		updateVisualEnd(cm, window)

	// Toggle visual mode (pressing v/V again exits visual mode)
	case "v":
		// Exit visual mode and return to normal mode
		cm.State = terminal.CopyModeNormal
		fx.ShowNotification("", "info", 0)
	case "V":
		// Pressing V in visual char mode switches to visual line mode
		// Pressing V in visual line mode exits to normal mode
		if cm.State == terminal.CopyModeVisualLine {
			cm.State = terminal.CopyModeNormal
			fx.ShowNotification("", "info", 0)
		} else {
			enterVisualLine(cm, window)
			fx.ShowNotification("VISUAL LINE", "info", 0)
		}
	}

	fx.InvalidateCache()
}

// HandleCopyModeMouseClick handles mouse clicks in copy mode
func HandleCopyModeMouseClick(cm *terminal.CopyMode, window *terminal.Window, clickX, clickY int) {
	// Convert window-relative coordinates (with border) to terminal coordinates
	terminalX, terminalY, inContent := window.ScreenToTerminal(clickX, clickY)

	// Check bounds
	if !inContent {
		return // Click outside terminal content area
	}

	// The lock covers the cell-buffer traversal only; InvalidateCache runs
	// after the unlock so nothing reachable from the locked region can take
	// the I/O lock a second time.
	func() {
		window.RLockIO()
		defer window.RUnlockIO()

		// Move cursor to clicked position
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

		// If in visual mode, update selection end
		if cm.State == terminal.CopyModeVisualChar || cm.State == terminal.CopyModeVisualLine {
			updateVisualEnd(cm, window)
		}
	}()

	window.InvalidateCache()
}

// HandleCopyModeMouseDrag handles mouse drag start in copy mode (initiates visual selection)
func HandleCopyModeMouseDrag(cm *terminal.CopyMode, window *terminal.Window, startX, startY int) {
	// Convert window-relative coordinates to terminal coordinates
	terminalX, terminalY, inContent := window.ScreenToTerminal(startX, startY)

	// Check bounds
	if !inContent {
		return
	}

	// Lock scoped to the traversal; see HandleCopyModeMouseClick.
	func() {
		window.RLockIO()
		defer window.RUnlockIO()

		// Always exit visual mode first if we're in it, then start fresh
		// This ensures each click-and-drag creates a new selection
		if cm.State == terminal.CopyModeVisualChar || cm.State == terminal.CopyModeVisualLine {
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
	if cm.State != terminal.CopyModeVisualChar && cm.State != terminal.CopyModeVisualLine {
		return 0
	}

	// Lock scoped to the traversal; see HandleCopyModeMouseClick.
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
					MoveUp(cm, window)
				}
			} else if mouseY >= contentBottom {
				dir = 1
				for range 3 {
					MoveDown(cm, window)
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
