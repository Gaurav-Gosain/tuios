package input

import (
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
	"tuios.dev/vt"
)

const multiClickSlop = 1

func registerClick(win *terminal.Window, termX, termY int, interval time.Duration) int {
	near := termY == win.LastClickY && abs(termX-win.LastClickX) <= multiClickSlop
	if !near || time.Since(win.LastClickTime) > interval || win.ClickCount >= 3 {
		win.ClickCount = 1
	} else {
		win.ClickCount++
	}
	win.LastClickTime = time.Now()
	win.LastClickX = termX
	win.LastClickY = termY
	return win.ClickCount
}

func beginMouseSelection(cm *terminal.CopyMode, window *terminal.Window, screenX, screenY, clicks int, s *config.Settings) {
	switch clicks {
	case 2:
		HandleCopyModeMouseDrag(cm, window, screenX, screenY)
		func() {
			window.RLockIO()
			defer window.RUnlockIO()
			if !selectWordUnderCursor(cm, window, s) {
				cm.State = terminal.CopyModeNormal
			}
		}()
		window.InvalidateCache()
	case 3:
		HandleCopyModeMouseDrag(cm, window, screenX, screenY)
		func() {
			window.RLockIO()
			defer window.RUnlockIO()
			enterVisualLine(cm, window)
		}()
		window.InvalidateCache()
	default:
		HandleCopyModeMouseDrag(cm, window, screenX, screenY)
	}
}

func selectWordUnderCursor(cm *terminal.CopyMode, window *terminal.Window, s *config.Settings) bool {
	absY := getAbsoluteY(cm, window)
	cells := copyModeLineCells(window, absY)
	if cm.CursorX < 0 || cm.CursorX >= len(cells) {
		return false
	}
	if !isSelectionWordChar(cells[cm.CursorX], s) {
		return false
	}

	startX := cm.CursorX
	for startX > 0 && isSelectionWordChar(cells[startX-1], s) {
		startX--
	}
	endX := cm.CursorX
	for endX < len(cells)-1 && isSelectionWordChar(cells[endX+1], s) {
		endX++
	}

	cm.State = terminal.CopyModeVisualChar
	cm.VisualStart = terminal.Position{X: startX, Y: absY}
	cm.VisualEnd = terminal.Position{X: endX, Y: absY}
	cm.CursorX = endX
	return true
}

func isSelectionWordChar(cell uv.Cell, s *config.Settings) bool {
	if cell.Content == "" || cell.Content == " " {
		return false
	}
	r := []rune(cell.Content)[0]
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return true
	}
	if unicode.IsSpace(r) {
		return false
	}
	return strings.ContainsRune(s.WordCharacters, r)
}

func copyModeLineCells(window *terminal.Window, absY int) []uv.Cell {
	scrollbackLen := window.ScrollbackLen()
	if absY < scrollbackLen {
		return window.ScrollbackLine(absY)
	}
	if window.Terminal == nil {
		return nil
	}
	return getScreenLineCells(window.Terminal, absY-scrollbackLen)
}

func finishMouseSelection(o *app.OS, window *terminal.Window) tea.Cmd {
	cm := window.CopyMode
	if cm == nil || !cm.Active {
		return nil
	}

	inVisual := cm.State.InVisual()
	moved := cm.VisualStart != cm.VisualEnd
	deliberate := moved || window.ClickCount >= 2

	if !inVisual || !deliberate {
		cm.State = terminal.CopyModeNormal
		endImplicitSelection(window)
		return nil
	}

	if !o.Settings.CopyOnSelect {
		return nil
	}

	text := selectionText(window)
	if text == "" {
		return nil
	}

	if o.SelectionDragged || window.ClickCount < 2 {
		return tea.Batch(o.CopyToClipboard(text), o.SaveToPasteBuffers(text))
	}
	return o.DeferCopyToClipboard(text, remainingClickWindow(window, o.DoubleClickWindow()))
}

func selectionText(window *terminal.Window) string {
	if !window.HasSelection() {
		return ""
	}
	window.RLockIO()
	defer window.RUnlockIO()
	if window.Terminal == nil {
		return ""
	}
	return vt.StripSixelMarkers(extractVisualText(window.CopyMode, window))
}

func remainingClickWindow(window *terminal.Window, interval time.Duration) time.Duration {
	return interval - time.Since(window.LastClickTime)
}

func endImplicitSelection(window *terminal.Window) {
	cm := window.CopyMode
	if cm == nil || !cm.Active || !cm.Implicit {
		return
	}
	if cm.ScrollOffset == 0 && cm.State == terminal.CopyModeNormal {
		window.ExitCopyMode()
	}
}
