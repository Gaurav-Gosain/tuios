// Package input implements vim-style copy mode for TUIOS.
//
// Copy mode is a unified interface that replaces both ScrollbackMode and SelectionMode,
// providing vim-like navigation, search, and visual selection capabilities.
package input

import (
	"fmt"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	vt "github.com/Gaurav-Gosain/tuios/internal/vt"
	tea "github.com/charmbracelet/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// HandleCopyModeKey is the main dispatcher for copy mode input
func HandleCopyModeKey(msg tea.KeyPressMsg, o *app.OS, window *terminal.Window) (*app.OS, tea.Cmd) {
	if window.CopyMode == nil || !window.CopyMode.Active {
		return o, nil
	}

	cm := window.CopyMode

	// Handle by state
	switch cm.State {
	case terminal.CopyModeSearch:
		return handleSearchInput(msg, cm, window, o)
	case terminal.CopyModeVisualChar, terminal.CopyModeVisualLine:
		return handleVisualInput(msg, cm, window, o)
	case terminal.CopyModeNormal:
		return handleNormalInput(msg, cm, window, o)
	}

	return o, nil
}

// handleNormalInput handles keys in normal navigation mode
func handleNormalInput(msg tea.KeyPressMsg, cm *terminal.CopyMode, window *terminal.Window, o *app.OS) (*app.OS, tea.Cmd) {
	keyStr := msg.String()

	// Handle pending character search (f/F/t/T followed by character)
	if cm.PendingCharSearch {
		// Check for escape to cancel
		if keyStr == "esc" {
			cm.PendingCharSearch = false
			o.ShowNotification("", "info", 0)
			return o, nil
		}

		cm.PendingCharSearch = false
		// Get the character from the key press
		if len(keyStr) == 1 && keyStr[0] >= 32 && keyStr[0] <= 126 {
			// Only accept printable ASCII characters
			char := rune(keyStr[0])
			cm.LastCharSearch = char
			findCharOnLine(cm, window, char, cm.LastCharSearchDir, cm.LastCharSearchTill)
			window.InvalidateCache()
			o.ShowNotification("", "info", 0) // Clear notification
		} else {
			// Invalid character, cancel search
			o.ShowNotification("", "info", 0)
		}
		return o, nil
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
			o.ShowNotification(fmt.Sprintf("%d", cm.PendingCount), "info", 0)
			return o, nil
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
		if o != nil {
			o.ShowNotification("", "info", 0) // Clear count display
		}
	}()

	switch keyStr {
	case "q", "esc":
		window.ExitCopyMode()
		o.ShowNotification("Copy Mode Exited", "info", config.NotificationDuration)
		return o, nil
	case "i":
		// Exit copy mode and enter terminal mode
		window.ExitCopyMode()
		o.Mode = app.TerminalMode
		o.ShowNotification("Terminal Mode", "info", config.NotificationDuration)
		return o, nil

	// Navigation - basic movement
	case "h", "left":
		for i := 0; i < count; i++ {
			moveLeft(cm, window)
		}
	case "l", "right":
		for i := 0; i < count; i++ {
			moveRight(cm, window)
		}
	case "j", "down":
		for i := 0; i < count; i++ {
			moveDown(cm, window)
		}
	case "k", "up":
		for i := 0; i < count; i++ {
			moveUp(cm, window)
		}

	// Navigation - word movement
	case "w":
		for i := 0; i < count; i++ {
			moveWordForward(cm, window)
		}
	case "b":
		for i := 0; i < count; i++ {
			moveWordBackward(cm, window)
		}
	case "e":
		for i := 0; i < count; i++ {
			moveWordEnd(cm, window)
		}
	case "W":
		for i := 0; i < count; i++ {
			moveWordForwardBig(cm, window)
		}
	case "B":
		for i := 0; i < count; i++ {
			moveWordBackwardBig(cm, window)
		}
	case "E":
		for i := 0; i < count; i++ {
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
		for i := 0; i < count; i++ {
			moveHalfPageUp(cm, window)
		}
	case "ctrl+d":
		for i := 0; i < count; i++ {
			moveHalfPageDown(cm, window)
		}
	case "ctrl+b", "pgup":
		for i := 0; i < count; i++ {
			movePageUp(cm, window)
		}
	case "ctrl+f", "pgdown":
		for i := 0; i < count; i++ {
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
				for i := 0; i < diff; i++ {
					moveDown(cm, window)
				}
			} else if diff < 0 {
				for i := 0; i < -diff; i++ {
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
		for i := 0; i < count; i++ {
			moveParagraphUp(cm, window)
		}
	case "}":
		for i := 0; i < count; i++ {
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
		o.ShowNotification("f", "info", 0)
		return o, nil
	case "F":
		// Find character backward on current line
		cm.PendingCharSearch = true
		cm.LastCharSearchDir = -1
		cm.LastCharSearchTill = false
		o.ShowNotification("F", "info", 0)
		return o, nil
	case "t":
		// Till character forward (stop before)
		cm.PendingCharSearch = true
		cm.LastCharSearchDir = 1
		cm.LastCharSearchTill = true
		o.ShowNotification("t", "info", 0)
		return o, nil
	case "T":
		// Till character backward (stop before)
		cm.PendingCharSearch = true
		cm.LastCharSearchDir = -1
		cm.LastCharSearchTill = true
		o.ShowNotification("T", "info", 0)
		return o, nil
	case ";":
		// Repeat last character search
		for i := 0; i < count; i++ {
			repeatCharSearch(cm, window, false)
		}
	case ",":
		// Repeat last character search in opposite direction
		for i := 0; i < count; i++ {
			repeatCharSearch(cm, window, true)
		}

	// Search
	case "/":
		cm.State = terminal.CopyModeSearch
		cm.SearchQuery = ""
		cm.SearchBackward = false
		o.ShowNotification("/", "info", 0) // Persistent until search complete
		return o, nil
	case "?":
		cm.State = terminal.CopyModeSearch
		cm.SearchQuery = ""
		cm.SearchBackward = true
		o.ShowNotification("?", "info", 0) // Persistent until search complete
		return o, nil
	case "n":
		// n goes forward for /, backward for ?
		for i := 0; i < count; i++ {
			if cm.SearchBackward {
				prevMatch(cm, window)
			} else {
				nextMatch(cm, window)
			}
		}
	case "N":
		// N goes backward for /, forward for ?
		for i := 0; i < count; i++ {
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
		o.ShowNotification("Search cleared", "info", config.NotificationDuration)
		window.InvalidateCache()
		return o, nil

	// Visual mode
	case "v":
		enterVisualChar(cm, window)
		o.ShowNotification("VISUAL", "info", 0)
		return o, nil
	case "V":
		enterVisualLine(cm, window)
		o.ShowNotification("VISUAL LINE", "info", 0)
		return o, nil
	}

	window.InvalidateCache()
	return o, nil
}

// handleSearchInput handles keys in search mode
func handleSearchInput(msg tea.KeyPressMsg, cm *terminal.CopyMode, window *terminal.Window, o *app.OS) (*app.OS, tea.Cmd) {
	key := msg.Key()

	// Determine search prefix based on direction
	searchPrefix := "/"
	if cm.SearchBackward {
		searchPrefix = "?"
	}

	switch {
	case key.Code == tea.KeyEnter:
		cm.State = terminal.CopyModeNormal
		matchInfo := ""
		if len(cm.SearchMatches) > 0 {
			matchInfo = fmt.Sprintf(" (%d matches)", len(cm.SearchMatches))
		}
		o.ShowNotification(fmt.Sprintf("%s%s%s", searchPrefix, cm.SearchQuery, matchInfo), "info", config.NotificationDuration)
	case key.Code == tea.KeyEscape:
		cm.State = terminal.CopyModeNormal
		cm.SearchQuery = ""
		cm.SearchMatches = nil
		o.ShowNotification("", "info", 0)
	case key.Code == tea.KeyBackspace:
		if len(cm.SearchQuery) > 0 {
			cm.SearchQuery = cm.SearchQuery[:len(cm.SearchQuery)-1]
			executeSearch(cm, window)
		}
		o.ShowNotification(searchPrefix+cm.SearchQuery, "info", 0)
	default:
		if key.Text != "" {
			cm.SearchQuery += key.Text
			executeSearch(cm, window)
			o.ShowNotification(searchPrefix+cm.SearchQuery, "info", 0)
		}
	}

	window.InvalidateCache()
	return o, nil
}

// handleVisualInput handles keys in visual selection mode
func handleVisualInput(msg tea.KeyPressMsg, cm *terminal.CopyMode, window *terminal.Window, o *app.OS) (*app.OS, tea.Cmd) {
	keyStr := msg.String()

	// Handle pending character search (f/F/t/T followed by character)
	if cm.PendingCharSearch {
		// Check for escape to cancel
		if keyStr == "esc" {
			cm.PendingCharSearch = false
			o.ShowNotification("", "info", 0)
			return o, nil
		}

		cm.PendingCharSearch = false
		// Get the character from the key press
		if len(keyStr) == 1 && keyStr[0] >= 32 && keyStr[0] <= 126 {
			// Only accept printable ASCII characters
			char := rune(keyStr[0])
			cm.LastCharSearch = char
			findCharOnLine(cm, window, char, cm.LastCharSearchDir, cm.LastCharSearchTill)
			updateVisualEnd(cm, window)
			window.InvalidateCache()
			o.ShowNotification("", "info", 0) // Clear notification
		} else {
			// Invalid character, cancel search
			o.ShowNotification("", "info", 0)
		}
		return o, nil
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
			o.ShowNotification(fmt.Sprintf("%d", cm.PendingCount), "info", 0)
			return o, nil
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
		if o != nil {
			o.ShowNotification("", "info", 0)
		}
	}()

	switch keyStr {
	case "esc":
		cm.State = terminal.CopyModeNormal
		o.ShowNotification("", "info", 0)
	case "y", "c":
		text := extractVisualText(cm, window)
		cm.State = terminal.CopyModeNormal
		o.ShowNotification(fmt.Sprintf("Yanked %d chars", len(text)), "success", config.NotificationDuration)
		window.InvalidateCache()
		return o, tea.SetClipboard(text)

	// Movement in visual mode extends selection - basic
	case "h", "left":
		for i := 0; i < count; i++ {
			moveLeft(cm, window)
		}
		updateVisualEnd(cm, window)
	case "l", "right":
		for i := 0; i < count; i++ {
			moveRight(cm, window)
		}
		updateVisualEnd(cm, window)
	case "j", "down":
		for i := 0; i < count; i++ {
			moveDown(cm, window)
		}
		updateVisualEnd(cm, window)
	case "k", "up":
		for i := 0; i < count; i++ {
			moveUp(cm, window)
		}
		updateVisualEnd(cm, window)

	// Word movement
	case "w":
		for i := 0; i < count; i++ {
			moveWordForward(cm, window)
		}
		updateVisualEnd(cm, window)
	case "b":
		for i := 0; i < count; i++ {
			moveWordBackward(cm, window)
		}
		updateVisualEnd(cm, window)
	case "e":
		for i := 0; i < count; i++ {
			moveWordEnd(cm, window)
		}
		updateVisualEnd(cm, window)
	case "W":
		for i := 0; i < count; i++ {
			moveWordForwardBig(cm, window)
		}
		updateVisualEnd(cm, window)
	case "B":
		for i := 0; i < count; i++ {
			moveWordBackwardBig(cm, window)
		}
		updateVisualEnd(cm, window)
	case "E":
		for i := 0; i < count; i++ {
			moveWordEndBig(cm, window)
		}
		updateVisualEnd(cm, window)

	// Character search (f/F/t/T)
	case "f":
		cm.PendingCharSearch = true
		cm.LastCharSearchDir = 1
		cm.LastCharSearchTill = false
		o.ShowNotification("f", "info", 0)
		return o, nil
	case "F":
		cm.PendingCharSearch = true
		cm.LastCharSearchDir = -1
		cm.LastCharSearchTill = false
		o.ShowNotification("F", "info", 0)
		return o, nil
	case "t":
		cm.PendingCharSearch = true
		cm.LastCharSearchDir = 1
		cm.LastCharSearchTill = true
		o.ShowNotification("t", "info", 0)
		return o, nil
	case "T":
		cm.PendingCharSearch = true
		cm.LastCharSearchDir = -1
		cm.LastCharSearchTill = true
		o.ShowNotification("T", "info", 0)
		return o, nil
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
	case "gg":
		moveToTop(cm, window)
		updateVisualEnd(cm, window)
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
		o.ShowNotification("", "info", 0)
	case "V":
		// Pressing V in visual char mode switches to visual line mode
		// Pressing V in visual line mode exits to normal mode
		if cm.State == terminal.CopyModeVisualLine {
			cm.State = terminal.CopyModeNormal
			o.ShowNotification("", "info", 0)
		} else {
			enterVisualLine(cm, window)
			o.ShowNotification("VISUAL LINE", "info", 0)
		}
	}

	window.InvalidateCache()
	return o, nil
}

// Movement functions (exported for mouse handler)

// MoveLeft moves cursor left
func MoveLeft(cm *terminal.CopyMode, window *terminal.Window) {
	moveLeft(cm, window)
}

// MoveRight moves cursor right
func MoveRight(cm *terminal.CopyMode, window *terminal.Window) {
	moveRight(cm, window)
}

// MoveUp moves cursor up
func MoveUp(cm *terminal.CopyMode, window *terminal.Window) {
	moveUp(cm, window)
}

// MoveDown moves cursor down
func MoveDown(cm *terminal.CopyMode, window *terminal.Window) {
	moveDown(cm, window)
}

// Internal movement functions

func moveLeft(cm *terminal.CopyMode, window *terminal.Window) {
	cm.CursorX = max(0, cm.CursorX-1)
}

func moveRight(cm *terminal.CopyMode, window *terminal.Window) {
	cm.CursorX = min(window.Width-3, cm.CursorX+1)
}

// moveUp moves cursor up (k key) - keeps cursor in middle of viewport when possible
func moveUp(cm *terminal.CopyMode, window *terminal.Window) {
	midPoint := window.Height / 2

	if cm.CursorY > midPoint {
		// Cursor below middle - just move it up
		cm.CursorY--
	} else if cm.ScrollOffset < window.ScrollbackLen() {
		// Cursor at/above middle - scroll content instead (cursor stays in place)
		cm.ScrollOffset++
		window.ScrollbackOffset = cm.ScrollOffset
	} else if cm.CursorY > 0 {
		// At top of scrollback, cursor can still move
		cm.CursorY--
	}
}

// moveDown moves cursor down (j key) - keeps cursor in middle of viewport when possible
func moveDown(cm *terminal.CopyMode, window *terminal.Window) {
	midPoint := window.Height / 2

	if cm.CursorY < midPoint {
		// Cursor above middle - just move it down
		cm.CursorY++
	} else if cm.ScrollOffset > 0 {
		// Cursor at/below middle - scroll content instead (cursor stays in place)
		cm.ScrollOffset--
		window.ScrollbackOffset = cm.ScrollOffset
	} else if cm.CursorY < window.Height-3 {
		// At live content, cursor can move to bottom
		cm.CursorY++
	}
}

func moveWordForward(cm *terminal.CopyMode, window *terminal.Window) {
	maxWidth := window.Width - 3
	maxIterations := 1000 // Prevent infinite loops

	// Get current character type
	cell := getCellAtCursor(cm, window)
	var currentContent string
	if cell != nil {
		currentContent = cell.Content
	}
	currentType := getCharType(currentContent)

	// Phase 1: Skip current word/punctuation group
	for i := 0; i < maxIterations; i++ {
		cell := getCellAtCursor(cm, window)
		var content string
		if cell != nil {
			content = cell.Content
		}
		charType := getCharType(content)

		// Stop if we hit a different type (but continue through same type)
		if charType != currentType || charType == 0 {
			break
		}

		// Move right, potentially wrapping to next line
		if cm.CursorX >= maxWidth {
			// Wrap to next line
			cm.CursorX = 0
			moveDown(cm, window)
		} else {
			cm.CursorX++
		}
	}

	// Phase 2: Skip whitespace to next word/punctuation
	for i := 0; i < maxIterations; i++ {
		cell := getCellAtCursor(cm, window)
		var content string
		if cell != nil {
			content = cell.Content
		}
		charType := getCharType(content)

		// Found a non-whitespace character - we're at start of next word
		if charType != 0 {
			break
		}

		// Move right, potentially wrapping to next line
		if cm.CursorX >= maxWidth {
			// Wrap to next line
			cm.CursorX = 0
			moveDown(cm, window)
		} else {
			cm.CursorX++
		}
	}
}

func moveWordBackward(cm *terminal.CopyMode, window *terminal.Window) {
	maxWidth := window.Width - 3
	maxIterations := 1000

	// Move left at least once to leave current position
	if cm.CursorX > 0 {
		cm.CursorX--
	} else if cm.CursorY > 0 || cm.ScrollOffset > 0 {
		// Wrap to end of previous line
		moveUp(cm, window)
		cm.CursorX = maxWidth
	} else {
		return // Already at top-left
	}

	// Phase 1: Skip whitespace backward
	for i := 0; i < maxIterations; i++ {
		cell := getCellAtCursor(cm, window)
		var content string
		if cell != nil {
			content = cell.Content
		}
		charType := getCharType(content)

		// Found non-whitespace - move to phase 2
		if charType != 0 {
			break
		}

		// Move left, potentially wrapping
		if cm.CursorX > 0 {
			cm.CursorX--
		} else if cm.CursorY > 0 || cm.ScrollOffset > 0 {
			moveUp(cm, window)
			cm.CursorX = maxWidth
		} else {
			return // At top-left
		}
	}

	// Phase 2: Move to start of current word/punctuation group
	// Get the type of the current (non-whitespace) character
	cell := getCellAtCursor(cm, window)
	var currentContent string
	if cell != nil {
		currentContent = cell.Content
	}
	currentType := getCharType(currentContent)

	for i := 0; i < maxIterations; i++ {
		if cm.CursorX == 0 {
			// At start of line - this is the word start
			break
		}

		// Peek at previous character
		prevX := cm.CursorX - 1
		absY := getAbsoluteY(cm, window)
		var prevCell *uv.Cell

		scrollbackLen := window.ScrollbackLen()
		if absY < scrollbackLen {
			line := window.ScrollbackLine(absY)
			if line != nil && prevX < len(line) {
				prevCell = &line[prevX]
			}
		} else {
			screenY := absY - scrollbackLen
			prevCell = window.Terminal.CellAt(prevX, screenY)
		}

		// Get previous character type
		var prevContent string
		if prevCell != nil {
			prevContent = prevCell.Content
		}
		prevType := getCharType(prevContent)

		// If previous char is different type, we're at word start
		if prevType != currentType {
			break
		}

		// Previous char is same type, move back
		cm.CursorX--
	}
}

func moveWordEnd(cm *terminal.CopyMode, window *terminal.Window) {
	maxWidth := window.Width - 3
	maxIterations := 1000

	// Move right at least once to leave current position
	if cm.CursorX < maxWidth {
		cm.CursorX++
	} else {
		// Wrap to next line
		cm.CursorX = 0
		moveDown(cm, window)
		return
	}

	// Phase 1: Skip whitespace
	for i := 0; i < maxIterations; i++ {
		cell := getCellAtCursor(cm, window)

		// Found non-whitespace - move to phase 2
		if cell != nil && cell.Content != "" && cell.Content != " " && cell.Content != "\t" {
			break
		}

		// Move right, potentially wrapping
		if cm.CursorX >= maxWidth {
			cm.CursorX = 0
			moveDown(cm, window)
		} else {
			cm.CursorX++
		}
	}

	// Phase 2: Move to end of word (last non-whitespace character)
	for i := 0; i < maxIterations; i++ {
		// Peek at next character
		nextX := cm.CursorX + 1
		if nextX > maxWidth {
			// At end of line
			break
		}

		absY := getAbsoluteY(cm, window)
		var nextCell *uv.Cell

		scrollbackLen := window.ScrollbackLen()
		if absY < scrollbackLen {
			line := window.ScrollbackLine(absY)
			if line != nil && nextX < len(line) {
				nextCell = &line[nextX]
			}
		} else {
			screenY := absY - scrollbackLen
			nextCell = window.Terminal.CellAt(nextX, screenY)
		}

		// If next char is whitespace/empty, we're at word end
		if nextCell == nil || nextCell.Content == "" || nextCell.Content == " " || nextCell.Content == "\t" {
			break
		}

		// Next char is part of word, move forward
		cm.CursorX++
	}
}

func moveWordForwardBig(cm *terminal.CopyMode, window *terminal.Window) {
	// Like 'w' but treats any whitespace-delimited sequence as a word
	maxWidth := window.Width - 3
	maxIterations := 1000

	// Phase 1: Skip current WORD (any non-whitespace)
	for i := 0; i < maxIterations; i++ {
		cell := getCellAtCursor(cm, window)
		if cell == nil || cell.Content == " " || cell.Content == "\t" {
			break
		}

		if cm.CursorX >= maxWidth {
			cm.CursorX = 0
			moveDown(cm, window)
		} else {
			cm.CursorX++
		}
	}

	// Phase 2: Skip whitespace to next WORD
	for i := 0; i < maxIterations; i++ {
		cell := getCellAtCursor(cm, window)

		if cell != nil && cell.Content != " " && cell.Content != "\t" {
			break
		}

		if cm.CursorX >= maxWidth {
			cm.CursorX = 0
			moveDown(cm, window)
		} else {
			cm.CursorX++
		}
	}
}

func moveWordBackwardBig(cm *terminal.CopyMode, window *terminal.Window) {
	// Like 'b' but for WORDs
	maxWidth := window.Width - 3
	maxIterations := 1000

	// Move left at least once
	if cm.CursorX > 0 {
		cm.CursorX--
	} else if cm.CursorY > 0 || cm.ScrollOffset > 0 {
		moveUp(cm, window)
		cm.CursorX = maxWidth
	} else {
		return
	}

	// Phase 1: Skip whitespace backward
	for i := 0; i < maxIterations; i++ {
		cell := getCellAtCursor(cm, window)

		if cell != nil && cell.Content != " " && cell.Content != "\t" {
			break
		}

		if cm.CursorX > 0 {
			cm.CursorX--
		} else if cm.CursorY > 0 || cm.ScrollOffset > 0 {
			moveUp(cm, window)
			cm.CursorX = maxWidth
		} else {
			return
		}
	}

	// Phase 2: Move to start of WORD
	for i := 0; i < maxIterations; i++ {
		if cm.CursorX == 0 {
			break
		}

		// Peek at previous character
		prevX := cm.CursorX - 1
		absY := getAbsoluteY(cm, window)
		var prevCell *uv.Cell

		scrollbackLen := window.ScrollbackLen()
		if absY < scrollbackLen {
			line := window.ScrollbackLine(absY)
			if line != nil && prevX < len(line) {
				prevCell = &line[prevX]
			}
		} else {
			screenY := absY - scrollbackLen
			prevCell = window.Terminal.CellAt(prevX, screenY)
		}

		if prevCell == nil || prevCell.Content == " " || prevCell.Content == "\t" {
			break
		}

		cm.CursorX--
	}
}

func moveWordEndBig(cm *terminal.CopyMode, window *terminal.Window) {
	// Like 'e' but for WORDs
	maxWidth := window.Width - 3
	maxIterations := 1000

	// Move right at least once
	if cm.CursorX < maxWidth {
		cm.CursorX++
	} else {
		cm.CursorX = 0
		moveDown(cm, window)
		return
	}

	// Phase 1: Skip whitespace
	for i := 0; i < maxIterations; i++ {
		cell := getCellAtCursor(cm, window)

		if cell != nil && cell.Content != " " && cell.Content != "\t" {
			break
		}

		if cm.CursorX >= maxWidth {
			cm.CursorX = 0
			moveDown(cm, window)
		} else {
			cm.CursorX++
		}
	}

	// Phase 2: Move to end of WORD
	for i := 0; i < maxIterations; i++ {
		nextX := cm.CursorX + 1
		if nextX > maxWidth {
			break
		}

		absY := getAbsoluteY(cm, window)
		var nextCell *uv.Cell

		scrollbackLen := window.ScrollbackLen()
		if absY < scrollbackLen {
			line := window.ScrollbackLine(absY)
			if line != nil && nextX < len(line) {
				nextCell = &line[nextX]
			}
		} else {
			screenY := absY - scrollbackLen
			nextCell = window.Terminal.CellAt(nextX, screenY)
		}

		if nextCell == nil || nextCell.Content == " " || nextCell.Content == "\t" {
			break
		}

		cm.CursorX++
	}
}

func moveHalfPageUp(cm *terminal.CopyMode, window *terminal.Window) {
	lines := max(1, window.Height/2)
	for i := 0; i < lines; i++ {
		moveUp(cm, window)
	}
}

func moveHalfPageDown(cm *terminal.CopyMode, window *terminal.Window) {
	lines := max(1, window.Height/2)
	for i := 0; i < lines; i++ {
		moveDown(cm, window)
	}
}

func movePageUp(cm *terminal.CopyMode, window *terminal.Window) {
	lines := max(1, window.Height-2)
	for i := 0; i < lines; i++ {
		moveUp(cm, window)
	}
}

func movePageDown(cm *terminal.CopyMode, window *terminal.Window) {
	lines := max(1, window.Height-2)
	for i := 0; i < lines; i++ {
		moveDown(cm, window)
	}
}

func moveToTop(cm *terminal.CopyMode, window *terminal.Window) {
	cm.ScrollOffset = window.ScrollbackLen()
	window.ScrollbackOffset = cm.ScrollOffset // Sync for rendering
	cm.CursorY = 0
	cm.CursorX = 0
}

func moveToBottom(cm *terminal.CopyMode, window *terminal.Window) {
	cm.ScrollOffset = 0
	window.ScrollbackOffset = cm.ScrollOffset // Sync for rendering
	cm.CursorY = window.Height - 3
	cm.CursorX = 0
}

func moveParagraphUp(cm *terminal.CopyMode, window *terminal.Window) {
	// Move up until we find a blank line, then skip blank lines
	maxIterations := 1000
	foundNonBlank := false

	for i := 0; i < maxIterations; i++ {
		// Check if current line is blank
		absY := getAbsoluteY(cm, window)
		lineText := getLineText(cm, window, absY)
		isBlank := strings.TrimSpace(lineText) == ""

		if foundNonBlank && isBlank {
			// Found the blank line separating paragraphs
			break
		}
		if !isBlank {
			foundNonBlank = true
		}

		// Move up
		if cm.CursorY > 0 {
			cm.CursorY--
		} else if cm.ScrollOffset < window.ScrollbackLen() {
			cm.ScrollOffset++
			window.ScrollbackOffset = cm.ScrollOffset
		} else {
			break // At top
		}
	}

	// Skip any additional blank lines
	for i := 0; i < maxIterations; i++ {
		absY := getAbsoluteY(cm, window)
		lineText := getLineText(cm, window, absY)
		if strings.TrimSpace(lineText) != "" {
			break
		}

		// Move up
		if cm.CursorY > 0 {
			cm.CursorY--
		} else if cm.ScrollOffset < window.ScrollbackLen() {
			cm.ScrollOffset++
			window.ScrollbackOffset = cm.ScrollOffset
		} else {
			break
		}
	}
}

func moveParagraphDown(cm *terminal.CopyMode, window *terminal.Window) {
	// Move down until we find a blank line, then skip blank lines
	maxIterations := 1000
	foundNonBlank := false

	for i := 0; i < maxIterations; i++ {
		// Check if current line is blank
		absY := getAbsoluteY(cm, window)
		lineText := getLineText(cm, window, absY)
		isBlank := strings.TrimSpace(lineText) == ""

		if foundNonBlank && isBlank {
			// Found the blank line separating paragraphs
			break
		}
		if !isBlank {
			foundNonBlank = true
		}

		// Move down
		if cm.CursorY < window.Height-3 {
			cm.CursorY++
		} else if cm.ScrollOffset > 0 {
			cm.ScrollOffset--
			window.ScrollbackOffset = cm.ScrollOffset
		} else {
			break // At bottom
		}
	}

	// Skip any additional blank lines
	for i := 0; i < maxIterations; i++ {
		absY := getAbsoluteY(cm, window)
		lineText := getLineText(cm, window, absY)
		if strings.TrimSpace(lineText) != "" {
			break
		}

		// Move down
		if cm.CursorY < window.Height-3 {
			cm.CursorY++
		} else if cm.ScrollOffset > 0 {
			cm.ScrollOffset--
			window.ScrollbackOffset = cm.ScrollOffset
		} else {
			break
		}
	}
}

func moveToMatchingBracket(cm *terminal.CopyMode, window *terminal.Window) {
	// Get character at cursor
	cell := getCellAtCursor(cm, window)
	if cell == nil || cell.Content == "" {
		return
	}

	char := cell.Content
	var matchChar string
	var direction int // 1 for forward, -1 for backward

	// Determine matching bracket and search direction
	switch char {
	case "(":
		matchChar = ")"
		direction = 1
	case ")":
		matchChar = "("
		direction = -1
	case "[":
		matchChar = "]"
		direction = 1
	case "]":
		matchChar = "["
		direction = -1
	case "{":
		matchChar = "}"
		direction = 1
	case "}":
		matchChar = "{"
		direction = -1
	case "<":
		matchChar = ">"
		direction = 1
	case ">":
		matchChar = "<"
		direction = -1
	default:
		return // Not on a bracket
	}

	// Search for matching bracket
	depth := 1
	maxIterations := 10000

	for i := 0; i < maxIterations && depth > 0; i++ {
		// Move in search direction
		if direction > 0 {
			// Moving forward
			if cm.CursorX < window.Width-3 {
				cm.CursorX++
			} else {
				// Wrap to next line
				cm.CursorX = 0
				if cm.CursorY < window.Height-3 {
					cm.CursorY++
				} else if cm.ScrollOffset > 0 {
					cm.ScrollOffset--
					window.ScrollbackOffset = cm.ScrollOffset
				} else {
					break // At end
				}
			}
		} else {
			// Moving backward
			if cm.CursorX > 0 {
				cm.CursorX--
			} else {
				// Wrap to previous line
				cm.CursorX = window.Width - 3
				if cm.CursorY > 0 {
					cm.CursorY--
				} else if cm.ScrollOffset < window.ScrollbackLen() {
					cm.ScrollOffset++
					window.ScrollbackOffset = cm.ScrollOffset
				} else {
					break // At start
				}
			}
		}

		// Check current character
		currentCell := getCellAtCursor(cm, window)
		if currentCell != nil && currentCell.Content != "" {
			currentChar := currentCell.Content
			if currentChar == char {
				depth++
			} else if currentChar == matchChar {
				depth--
			}
		}
	}
}

func getLineText(cm *terminal.CopyMode, window *terminal.Window, absY int) string {
	scrollbackLen := window.ScrollbackLen()

	if absY < scrollbackLen {
		line := window.ScrollbackLine(absY)
		if line != nil {
			return extractLineTextFromCells(line)
		}
	} else {
		screenY := absY - scrollbackLen
		return extractScreenLineText(window.Terminal, screenY)
	}

	return ""
}

// Search functions

func executeSearch(cm *terminal.CopyMode, window *terminal.Window) {
	// Check cache
	if cm.SearchQuery != "" && cm.SearchQuery == cm.SearchCache.Query && cm.SearchCache.Valid {
		cm.SearchMatches = cm.SearchCache.Matches
		if len(cm.SearchMatches) > 0 {
			cm.CurrentMatch = 0
			jumpToMatch(cm, window, 0)
		}
		return
	}

	cm.SearchMatches = nil
	if cm.SearchQuery == "" {
		return
	}

	query := cm.SearchQuery
	if !cm.CaseSensitive {
		query = strings.ToLower(query)
	}

	scrollbackLen := window.ScrollbackLen()
	screenHeight := window.Terminal.Height()

	// Search scrollback
	for i := 0; i < scrollbackLen; i++ {
		line := window.ScrollbackLine(i)
		if line == nil {
			continue
		}
		lineText := extractLineTextFromCells(line)

		if !cm.CaseSensitive {
			lineText = strings.ToLower(lineText)
		}

		// Find all occurrences
		// Note: strings.Index returns BYTE positions, not character positions
		byteIdx := 0
		queryCharLen := len([]rune(query)) // Character length, not byte length

		for {
			idx := strings.Index(lineText[byteIdx:], query)
			if idx == -1 {
				break
			}

			// Convert byte positions to character positions
			bytePos := byteIdx + idx
			charStart := byteIndexToCharIndex(lineText, bytePos)
			charEnd := charStart + queryCharLen

			// Convert character indices to column positions
			colStart := charIndexToColumn(line, charStart)
			colEnd := charIndexToColumn(line, charEnd)

			match := terminal.SearchMatch{
				Line:   i,
				StartX: colStart,
				EndX:   colEnd,
			}
			cm.SearchMatches = append(cm.SearchMatches, match)

			// Move to next position (in bytes)
			byteIdx = bytePos + len(query)

			// Limit matches
			if len(cm.SearchMatches) >= 1000 {
				break
			}
		}
		if len(cm.SearchMatches) >= 1000 {
			break
		}
	}

	// Search current screen
	if len(cm.SearchMatches) < 1000 {
		for y := 0; y < screenHeight; y++ {
			lineText := extractScreenLineText(window.Terminal, y)

			if !cm.CaseSensitive {
				lineText = strings.ToLower(lineText)
			}

			// Note: strings.Index returns BYTE positions, not character positions
			byteIdx := 0
			queryCharLen := len([]rune(query)) // Character length, not byte length

			for {
				idx := strings.Index(lineText[byteIdx:], query)
				if idx == -1 {
					break
				}

				// Convert byte positions to character positions
				bytePos := byteIdx + idx
				charStart := byteIndexToCharIndex(lineText, bytePos)
				charEnd := charStart + queryCharLen

				// Get cells for this screen line to calculate columns
				cells := getScreenLineCells(window.Terminal, y)
				colStart := charIndexToColumn(cells, charStart)
				colEnd := charIndexToColumn(cells, charEnd)

				match := terminal.SearchMatch{
					Line:   scrollbackLen + y,
					StartX: colStart,
					EndX:   colEnd,
				}
				cm.SearchMatches = append(cm.SearchMatches, match)

				// Move to next position (in bytes)
				byteIdx = bytePos + len(query)

				if len(cm.SearchMatches) >= 1000 {
					break
				}
			}
			if len(cm.SearchMatches) >= 1000 {
				break
			}
		}
	}

	// Update cache
	cm.SearchCache.Query = cm.SearchQuery
	cm.SearchCache.Matches = cm.SearchMatches
	cm.SearchCache.CacheTime = time.Now()
	cm.SearchCache.Valid = true

	// Jump to appropriate match based on search direction and current position
	if len(cm.SearchMatches) > 0 {
		currentAbsY := getAbsoluteY(cm, window)

		if cm.SearchBackward {
			// For backward search (?), find the closest match before current position
			// Start from the end and work backwards
			foundMatch := -1
			for i := len(cm.SearchMatches) - 1; i >= 0; i-- {
				match := cm.SearchMatches[i]
				if match.Line < currentAbsY || (match.Line == currentAbsY && match.StartX < cm.CursorX) {
					foundMatch = i
					break
				}
			}

			// If no match before cursor, wrap to last match
			if foundMatch == -1 {
				foundMatch = len(cm.SearchMatches) - 1
			}

			cm.CurrentMatch = foundMatch
			jumpToMatch(cm, window, foundMatch)
		} else {
			// For forward search (/), find the closest match after current position
			foundMatch := -1
			for i := 0; i < len(cm.SearchMatches); i++ {
				match := cm.SearchMatches[i]
				if match.Line > currentAbsY || (match.Line == currentAbsY && match.StartX > cm.CursorX) {
					foundMatch = i
					break
				}
			}

			// If no match after cursor, wrap to first match
			if foundMatch == -1 {
				foundMatch = 0
			}

			cm.CurrentMatch = foundMatch
			jumpToMatch(cm, window, foundMatch)
		}
	}
}

func nextMatch(cm *terminal.CopyMode, window *terminal.Window) {
	if len(cm.SearchMatches) == 0 {
		return
	}

	cm.CurrentMatch = (cm.CurrentMatch + 1) % len(cm.SearchMatches)
	jumpToMatch(cm, window, cm.CurrentMatch)
}

func prevMatch(cm *terminal.CopyMode, window *terminal.Window) {
	if len(cm.SearchMatches) == 0 {
		return
	}

	cm.CurrentMatch--
	if cm.CurrentMatch < 0 {
		cm.CurrentMatch = len(cm.SearchMatches) - 1
	}
	jumpToMatch(cm, window, cm.CurrentMatch)
}

func jumpToMatch(cm *terminal.CopyMode, window *terminal.Window, matchIdx int) {
	if matchIdx < 0 || matchIdx >= len(cm.SearchMatches) {
		return
	}

	match := cm.SearchMatches[matchIdx]
	scrollbackLen := window.ScrollbackLen()

	if match.Line < scrollbackLen {
		// Match is in scrollback
		cm.ScrollOffset = scrollbackLen - match.Line
		window.ScrollbackOffset = cm.ScrollOffset // Sync for rendering
		cm.CursorY = 0
	} else {
		// Match is in current screen
		screenLine := match.Line - scrollbackLen
		cm.ScrollOffset = 0
		window.ScrollbackOffset = cm.ScrollOffset // Sync for rendering
		cm.CursorY = min(screenLine, window.Height-3)
	}

	cm.CursorX = match.StartX
}

// Visual selection functions

// getLineContentBounds returns the X positions of the first and last non-empty characters on a line
func getLineContentBounds(cm *terminal.CopyMode, window *terminal.Window, absY int) (int, int) {
	scrollbackLen := window.ScrollbackLen()

	// Get cells for this line
	var cells []uv.Cell
	if absY < scrollbackLen {
		cells = window.ScrollbackLine(absY)
	} else {
		screenY := absY - scrollbackLen
		cells = getScreenLineCells(window.Terminal, screenY)
	}

	if len(cells) == 0 {
		return 0, 0
	}

	// Find first non-empty, non-continuation cell
	startX := 0
	for i, cell := range cells {
		if cell.Width > 0 && cell.Content != "" && cell.Content != " " {
			startX = i
			break
		}
	}

	// Find last non-empty, non-continuation cell
	endX := len(cells) - 1
	for i := len(cells) - 1; i >= 0; i-- {
		if cells[i].Width > 0 && cells[i].Content != "" && cells[i].Content != " " {
			endX = i
			break
		}
	}

	// If entire line is empty, just return 0, 0
	if endX < startX {
		return 0, 0
	}

	return startX, endX
}

func enterVisualChar(cm *terminal.CopyMode, window *terminal.Window) {
	cm.State = terminal.CopyModeVisualChar
	absY := getAbsoluteY(cm, window)
	cm.VisualStart = terminal.Position{X: cm.CursorX, Y: absY}
	cm.VisualEnd = cm.VisualStart
}

func enterVisualLine(cm *terminal.CopyMode, window *terminal.Window) {
	cm.State = terminal.CopyModeVisualLine
	absY := getAbsoluteY(cm, window)

	// Get line content bounds (first to last non-empty character)
	startX, endX := getLineContentBounds(cm, window, absY)

	cm.VisualStart = terminal.Position{X: startX, Y: absY}
	cm.VisualEnd = terminal.Position{X: endX, Y: absY}
}

func updateVisualEnd(cm *terminal.CopyMode, window *terminal.Window) {
	absY := getAbsoluteY(cm, window)

	if cm.State == terminal.CopyModeVisualChar {
		cm.VisualEnd = terminal.Position{X: cm.CursorX, Y: absY}
	} else if cm.State == terminal.CopyModeVisualLine {
		// For visual line mode, we need to select entire lines
		// Start Y stays fixed, we only update end Y
		cm.VisualEnd.Y = absY

		// Determine which line is earlier and which is later
		startY := cm.VisualStart.Y
		endY := cm.VisualEnd.Y

		// Normalize: make sure startY <= endY for bounds calculation
		if startY > endY {
			startY, endY = endY, startY
		}

		// Get line content bounds for both lines
		startLineStartX, _ := getLineContentBounds(cm, window, startY)
		_, endLineEndX := getLineContentBounds(cm, window, endY)

		// If moving upwards (current Y < original start Y), we want:
		// - Start to be at beginning of the upper line (current position)
		// - End to be at end of the lower line (original start)
		if absY < cm.VisualStart.Y {
			// Moving upwards
			cm.VisualEnd.X = startLineStartX
			cm.VisualStart.X = endLineEndX
		} else {
			// Moving downwards or same line
			cm.VisualStart.X = startLineStartX
			cm.VisualEnd.X = endLineEndX
		}
	}
}

func extractVisualText(cm *terminal.CopyMode, window *terminal.Window) string {
	start, end := cm.VisualStart, cm.VisualEnd

	// Normalize selection
	if start.Y > end.Y || (start.Y == end.Y && start.X > end.X) {
		start, end = end, start
	}

	var text strings.Builder
	scrollbackLen := window.ScrollbackLen()

	// Single line
	if start.Y == end.Y {
		if start.Y < scrollbackLen {
			line := window.ScrollbackLine(start.Y)
			for x := start.X; x <= end.X && line != nil && x < len(line); x++ {
				if line[x].Content != "" {
					text.WriteString(line[x].Content)
				} else {
					text.WriteRune(' ')
				}
			}
		} else {
			screenY := start.Y - scrollbackLen
			for x := start.X; x <= end.X && x < window.Width; x++ {
				cell := window.Terminal.CellAt(x, screenY)
				if cell != nil && cell.Content != "" {
					text.WriteString(cell.Content)
				} else {
					text.WriteRune(' ')
				}
			}
		}
		return strings.TrimSpace(text.String())
	}

	// Multi-line
	for y := start.Y; y <= end.Y; y++ {
		startX, endX := 0, window.Width-1

		if y == start.Y {
			startX = start.X
		}
		if y == end.Y {
			endX = end.X
		}

		// Extract line content
		var lineCells []uv.Cell
		if y < scrollbackLen {
			lineCells = window.ScrollbackLine(y)
		} else {
			screenY := y - scrollbackLen
			// Build cells array from screen
			for x := 0; x < window.Width; x++ {
				cell := window.Terminal.CellAt(x, screenY)
				if cell != nil {
					lineCells = append(lineCells, *cell)
				} else {
					lineCells = append(lineCells, uv.Cell{})
				}
			}
		}

		// Append line content
		if lineCells != nil {
			for x := startX; x <= endX && x < len(lineCells); x++ {
				if lineCells[x].Content != "" {
					text.WriteString(lineCells[x].Content)
				} else {
					text.WriteRune(' ')
				}
			}
		}

		// Add newline only if this is NOT a soft-wrapped line
		if y < end.Y {
			// Check if this line is soft-wrapped (continues on next line)
			// Heuristic: if line content extends to terminal width and doesn't end with whitespace,
			// it's likely wrapped
			isSoftWrapped := false
			if lineCells != nil && len(lineCells) > 0 {
				// Find last non-empty cell
				lastNonEmptyX := -1
				for x := len(lineCells) - 1; x >= 0; x-- {
					if lineCells[x].Content != "" && lineCells[x].Content != " " {
						lastNonEmptyX = x
						break
					}
				}
				// If line extends close to terminal width, it's probably wrapped
				if lastNonEmptyX >= window.Width-5 {
					isSoftWrapped = true
				}
			}

			if isSoftWrapped {
				// Remove trailing whitespace since this line continues on the next
				currentText := text.String()
				text.Reset()
				text.WriteString(strings.TrimRight(currentText, " "))
			} else {
				text.WriteRune('\n')
			}
		}
	}

	return strings.TrimSpace(text.String())
}

// Helper functions

func getAbsoluteY(cm *terminal.CopyMode, window *terminal.Window) int {
	scrollbackLen := window.ScrollbackLen()
	if cm.ScrollOffset > 0 {
		return scrollbackLen - cm.ScrollOffset + cm.CursorY
	}
	return scrollbackLen + cm.CursorY
}

// isVimWordChar returns true if the rune is part of a vim "word" (alphanumeric or underscore)
func isVimWordChar(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '_'
}

// getCharType returns the type of character: 0=whitespace, 1=word char, 2=punctuation
func getCharType(content string) int {
	if content == "" || content == " " || content == "\t" {
		return 0 // whitespace
	}
	r := []rune(content)[0]
	if isVimWordChar(r) {
		return 1 // word character
	}
	return 2 // punctuation/special
}

func getCellAtCursor(cm *terminal.CopyMode, window *terminal.Window) *uv.Cell {
	absY := getAbsoluteY(cm, window)
	scrollbackLen := window.ScrollbackLen()

	if absY < scrollbackLen {
		line := window.ScrollbackLine(absY)
		if line != nil && cm.CursorX < len(line) {
			return &line[cm.CursorX]
		}
		return nil
	}

	screenY := absY - scrollbackLen
	return window.Terminal.CellAt(cm.CursorX, screenY)
}

// byteIndexToCharIndex converts a byte index in a UTF-8 string to a character (rune) index
// This is needed because strings.Index returns byte positions, not character positions
func byteIndexToCharIndex(s string, byteIdx int) int {
	if byteIdx <= 0 {
		return 0
	}
	if byteIdx >= len(s) {
		return len([]rune(s))
	}

	// Count runes up to the byte index
	charIdx := 0
	byteCount := 0
	for _, r := range s {
		if byteCount >= byteIdx {
			break
		}
		byteCount += len(string(r))
		charIdx++
	}
	return charIdx
}

func extractLineTextFromCells(cells []uv.Cell) string {
	var text strings.Builder
	for _, cell := range cells {
		// Skip continuation cells (Width=0) of wide characters
		// These are placeholder cells for emoji, CJK, nerd fonts, etc.
		if cell.Width == 0 {
			continue
		}
		if cell.Content != "" {
			text.WriteString(cell.Content)
		} else {
			text.WriteRune(' ')
		}
	}
	return text.String()
}

func extractScreenLineText(term *vt.Emulator, y int) string {
	var text strings.Builder
	width := term.Width()
	for x := 0; x < width; x++ {
		cell := term.CellAt(x, y)
		// Skip continuation cells (Width=0) of wide characters
		// These are placeholder cells for emoji, CJK, nerd fonts, etc.
		if cell != nil && cell.Width == 0 {
			continue
		}
		if cell != nil && cell.Content != "" {
			text.WriteString(cell.Content)
		} else {
			text.WriteRune(' ')
		}
	}
	return text.String()
}

// getScreenLineCells returns all cells for a screen line
func getScreenLineCells(term *vt.Emulator, y int) []uv.Cell {
	width := term.Width()
	cells := make([]uv.Cell, width)

	for x := 0; x < width; x++ {
		cell := term.CellAt(x, y)
		if cell != nil {
			cells[x] = *cell
		} else {
			// Empty cell
			cells[x] = uv.Cell{Content: " ", Width: 1}
		}
	}

	return cells
}

// charIndexToColumn converts a character index in the text string to a column position
// accounting for wide characters (emoji, nerd fonts, CJK, etc.)
//
// The cells array is structured so that each cell index IS the column position.
// For wide characters (Width=2), the next cell is a continuation (Width=0).
// Example:
//   Columns:  0  1  2  3  4  5
//   Cells:   [🎨][] [f][i][l][e]
//   Width:    2  0  1  1  1  1
//   Text (skipping Width=0): "🎨file"
//   Character index 1 ('f') → Column 2
func charIndexToColumn(cells []uv.Cell, charIndex int) int {
	if charIndex <= 0 {
		return 0
	}

	if len(cells) == 0 {
		return 0
	}

	charsProcessed := 0

	for col, cell := range cells {
		// Skip continuation cells (Width=0) when counting characters
		if cell.Width == 0 {
			continue
		}

		// If we've reached the target character index, return the column
		// (which is the cell index)
		if charsProcessed == charIndex {
			return col
		}

		charsProcessed++
	}

	// Past the end - return the last column
	return len(cells)
}

// findCharOnLine searches for a character across multiple lines
// direction: 1 for forward, -1 for backward
// till: true to stop before the character, false to land on it
// findCharOnLine searches for a character across multiple lines
// direction: 1 for forward, -1 for backward
// till: true to stop before the character, false to land on it
func findCharOnLine(cm *terminal.CopyMode, window *terminal.Window, char rune, direction int, till bool) {
	startAbsY := getAbsoluteY(cm, window)
	scrollbackLen := window.ScrollbackLen()
	screenHeight := window.Terminal.Height()
	totalLines := scrollbackLen + screenHeight

	maxIterations := 1000 // Prevent infinite loops

	if direction > 0 {
		// Search forward across lines
		for lineOffset := 0; lineOffset < maxIterations; lineOffset++ {
			absY := startAbsY + lineOffset
			if absY >= totalLines {
				break
			}

			lineText := getLineText(cm, window, absY)
			if lineText == "" {
				continue
			}

			// Get cells for this line
			var cells []uv.Cell
			if absY < scrollbackLen {
				cells = window.ScrollbackLine(absY)
			} else {
				screenY := absY - scrollbackLen
				cells = getScreenLineCells(window.Terminal, screenY)
			}

			if len(cells) == 0 {
				continue
			}

			// Convert lineText to runes
			runes := []rune(lineText)

			// Determine starting position
			startCharIdx := 0
			if lineOffset == 0 {
				// On current line, start from cursor + 1
				for col := 0; col < cm.CursorX && col < len(cells); col++ {
					if cells[col].Width > 0 {
						startCharIdx++
					}
				}
				startCharIdx++ // Start searching from next character
			}

			// Search this line
			for charIdx := startCharIdx; charIdx < len(runes); charIdx++ {
				if runes[charIdx] == char {
					// Found it!
					targetCharIdx := charIdx
					if till {
						targetCharIdx = charIdx - 1
						if targetCharIdx < 0 {
							continue // Can't stop before first character
						}
					}

					// Move to the target line using step-by-step movement
					// This ensures scroll offset is handled correctly
					linesToMove := absY - startAbsY
					for i := 0; i < linesToMove; i++ {
						moveDown(cm, window)
					}

					// Set cursor X position
					cm.CursorX = charIndexToColumn(cells, targetCharIdx)
					return
				}
			}
		}
	} else {
		// Search backward across lines
		for lineOffset := 0; lineOffset < maxIterations; lineOffset++ {
			absY := startAbsY - lineOffset
			if absY < 0 {
				break
			}

			lineText := getLineText(cm, window, absY)
			if lineText == "" {
				continue
			}

			// Get cells for this line
			var cells []uv.Cell
			if absY < scrollbackLen {
				cells = window.ScrollbackLine(absY)
			} else {
				screenY := absY - scrollbackLen
				cells = getScreenLineCells(window.Terminal, screenY)
			}

			if len(cells) == 0 {
				continue
			}

			// Convert lineText to runes
			runes := []rune(lineText)

			// Determine starting position
			endCharIdx := len(runes) - 1
			if lineOffset == 0 {
				// On current line, convert cursor column to character index
				currentCharIdx := 0
				for col := 0; col < cm.CursorX && col < len(cells); col++ {
					if cells[col].Width > 0 {
						currentCharIdx++
					}
				}
				endCharIdx = currentCharIdx - 1 // Start searching from previous character
			}

			// Search this line backward
			for charIdx := endCharIdx; charIdx >= 0; charIdx-- {
				if runes[charIdx] == char {
					// Found it!
					targetCharIdx := charIdx
					if till {
						targetCharIdx = charIdx + 1
						if targetCharIdx >= len(runes) {
							continue // Can't stop after last character
						}
					}

					// Move to the target line using step-by-step movement
					// This ensures scroll offset is handled correctly
					linesToMove := startAbsY - absY
					for i := 0; i < linesToMove; i++ {
						moveUp(cm, window)
					}

					// Set cursor X position
					cm.CursorX = charIndexToColumn(cells, targetCharIdx)
					return
				}
			}
		}
	}
	// Character not found - no movement
}
func repeatCharSearch(cm *terminal.CopyMode, window *terminal.Window, reverse bool) {
	if cm.LastCharSearch == 0 {
		return // No previous character search
	}

	direction := cm.LastCharSearchDir
	if reverse {
		direction = -direction
	}

	findCharOnLine(cm, window, cm.LastCharSearch, direction, cm.LastCharSearchTill)
}

// HandleCopyModeMouseClick handles mouse clicks in copy mode
func HandleCopyModeMouseClick(cm *terminal.CopyMode, window *terminal.Window, clickX, clickY int) {
	// Convert window-relative coordinates (with border) to terminal coordinates
	terminalX := clickX - window.X - 1 // Account for left border
	terminalY := clickY - window.Y - 1 // Account for top border

	// Check bounds
	if terminalX < 0 || terminalY < 0 || terminalX >= window.Width-2 || terminalY >= window.Height-2 {
		return // Click outside terminal content area
	}

	// Move cursor to clicked position
	cm.CursorX = terminalX
	cm.CursorY = terminalY

	// If in visual mode, update selection end
	if cm.State == terminal.CopyModeVisualChar || cm.State == terminal.CopyModeVisualLine {
		updateVisualEnd(cm, window)
	}

	window.InvalidateCache()
}

// HandleCopyModeMouseDrag handles mouse drag start in copy mode (initiates visual selection)
func HandleCopyModeMouseDrag(cm *terminal.CopyMode, window *terminal.Window, startX, startY int) {
	// Convert window-relative coordinates to terminal coordinates
	terminalX := startX - window.X - 1
	terminalY := startY - window.Y - 1

	// Check bounds
	if terminalX < 0 || terminalY < 0 || terminalX >= window.Width-2 || terminalY >= window.Height-2 {
		return
	}

	// Always exit visual mode first if we're in it, then start fresh
	// This ensures each click-and-drag creates a new selection
	if cm.State == terminal.CopyModeVisualChar || cm.State == terminal.CopyModeVisualLine {
		cm.State = terminal.CopyModeNormal
	}

	// Move cursor to drag start position
	cm.CursorX = terminalX
	cm.CursorY = terminalY

	// Enter visual character mode for new selection
	enterVisualChar(cm, window)

	window.InvalidateCache()
}

// HandleCopyModeMouseMotion handles mouse motion during drag in copy mode
func HandleCopyModeMouseMotion(cm *terminal.CopyMode, window *terminal.Window, mouseX, mouseY int) {
	// Only handle if in visual mode
	if cm.State != terminal.CopyModeVisualChar && cm.State != terminal.CopyModeVisualLine {
		return
	}

	// Convert window-relative coordinates to terminal coordinates
	terminalX := mouseX - window.X - 1
	terminalY := mouseY - window.Y - 1

	// Check bounds
	if terminalX < 0 || terminalY < 0 || terminalX >= window.Width-2 || terminalY >= window.Height-2 {
		return
	}

	// Update cursor position
	cm.CursorX = terminalX
	cm.CursorY = terminalY

	// Update visual selection end
	updateVisualEnd(cm, window)

	window.InvalidateCache()
}
