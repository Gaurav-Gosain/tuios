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
)

// multiClickInterval is how long after a click a second one still counts as
// part of the same gesture, and therefore how long a multi-click selection's
// clipboard write waits before it can be trusted.
//
// It was 500ms, matching the Windows and macOS defaults, back when it only
// decided how a click sequence was read. Now that it also gates the clipboard
// it is a delay the user waits through, and half a second is past the point
// where a response stops feeling immediate: the usual figure for that is
// around 300ms, beyond which an interface reads as reacting rather than
// responding.
//
// 300ms is chosen against both ends. Below it: measured inter-click intervals
// for an unhurried double-click sit around 100-250ms, and xterm has shipped a
// 250ms multiClickTime for decades without being thought broken, so 300ms
// still admits a comfortably slow double-click. Above it: GTK and Qt both
// default to 400ms, which would work but spends another tenth of a second on
// every copy. The cost of being wrong is asymmetric but small either way, a
// deliberate triple-click read as a double plus a single, and the user can see
// what got selected before it lands.
const multiClickInterval = 300 * time.Millisecond

// multiClickSlop is how far the pointer may drift between clicks of one
// gesture, in cells. Requiring the exact same cell reads as an intermittent
// double-click on a real mouse: one cell is a handful of pixels, and the button
// going down twice moves the pointer more than that often enough to notice.
const multiClickSlop = 1

// registerClick advances the multi-click counter for a press at a terminal cell
// and returns how many clicks this one makes: 1, 2, or 3. A fourth click starts
// a new gesture rather than continuing to climb, so click-click-click-click is
// character, word, line, character.
func registerClick(win *terminal.Window, termX, termY int) int {
	near := termY == win.LastClickY && abs(termX-win.LastClickX) <= multiClickSlop
	if !near || time.Since(win.LastClickTime) > multiClickInterval || win.ClickCount >= 3 {
		win.ClickCount = 1
	} else {
		win.ClickCount++
	}
	win.LastClickTime = time.Now()
	win.LastClickX = termX
	win.LastClickY = termY
	return win.ClickCount
}

// beginMouseSelection starts a selection gesture in an active copy-mode session
// at a screen position. clicks is what registerClick returned: one click starts
// a character selection the caller can drag out, two selects the word under the
// pointer, three selects the line.
//
// A line rather than a sentence for three clicks: terminal content is
// line-oriented, and sentence detection over shell output, log lines and code
// would be guesswork.
func beginMouseSelection(cm *terminal.CopyMode, window *terminal.Window, screenX, screenY, clicks int) {
	switch clicks {
	case 2:
		HandleCopyModeMouseDrag(cm, window, screenX, screenY)
		func() {
			window.RLockIO()
			defer window.RUnlockIO()
			if !selectWordUnderCursor(cm, window) {
				// Nothing word-shaped under the pointer. Leave the one-cell
				// selection the drag started rather than inventing one.
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

// selectWordUnderCursor puts a visual-character selection around the word the
// copy-mode cursor sits in. It reports false when the cursor is not on a word
// character, which is the caller's cue to leave the selection alone rather than
// select a lone space.
//
// Callers must hold the window's I/O read lock.
func selectWordUnderCursor(cm *terminal.CopyMode, window *terminal.Window) bool {
	absY := getAbsoluteY(cm, window)
	cells := copyModeLineCells(window, absY)
	if cm.CursorX < 0 || cm.CursorX >= len(cells) {
		return false
	}
	if !isSelectionWordChar(cells[cm.CursorX]) {
		return false
	}

	startX := cm.CursorX
	for startX > 0 && isSelectionWordChar(cells[startX-1]) {
		startX--
	}
	endX := cm.CursorX
	for endX < len(cells)-1 && isSelectionWordChar(cells[endX+1]) {
		endX++
	}

	cm.State = terminal.CopyModeVisualChar
	cm.VisualStart = terminal.Position{X: startX, Y: absY}
	cm.VisualEnd = terminal.Position{X: endX, Y: absY}
	cm.CursorX = endX
	return true
}

// isSelectionWordChar reports whether a cell belongs to a double-click word.
// Letters and digits always do; the punctuation that also does is
// appearance.word_characters, so a path, a URL or a flag like --no-vm comes out
// as one word instead of a handful of fragments.
func isSelectionWordChar(cell uv.Cell) bool {
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
	return strings.ContainsRune(config.WordCharacters, r)
}

// copyModeLineCells returns the cells of an absolute line, from the scrollback
// ring or from the live screen. Callers must hold the window's I/O read lock.
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

// finishMouseSelection ends a mouse selection gesture and, when
// appearance.copy_on_select is on, puts the selected text on the clipboard.
//
// Copying on release is what a graphical terminal does (X11's primary
// selection, kitty's copy_on_select), and it is why a selection here no longer
// needs a separate keystroke to be useful. It is a setting because a stray drag
// overwriting the clipboard is a real annoyance for some people.
//
// A drag writes at once: the button coming up ended the gesture and nothing can
// reinterpret it. A multi-click waits out the rest of its window first, because
// a double-click is also the first two thirds of a triple-click; see
// app/clipboard_copy.go.
//
// Two things it deliberately does not do. It does not copy a selection that
// never moved, so an ordinary click cannot clobber the clipboard. And it leaves
// the highlight up rather than clearing it, so the user can see what they got;
// the next press clears it, as does typing.
//
// The clipboard path is the same tea.SetClipboard (OSC 52) that copy mode's y
// has always used, so it reaches exactly the environments that already did,
// including over SSH, and no more.
func finishMouseSelection(o *app.OS, window *terminal.Window) tea.Cmd {
	cm := window.CopyMode
	if cm == nil || !cm.Active {
		return nil
	}

	inVisual := cm.State == terminal.CopyModeVisualChar || cm.State == terminal.CopyModeVisualLine
	moved := cm.VisualStart != cm.VisualEnd
	// A double or triple click is a selection even when it lands on a
	// one-character word or an empty line, so the counter speaks for it.
	deliberate := moved || window.ClickCount >= 2

	if !inVisual || !deliberate {
		cm.State = terminal.CopyModeNormal
		endImplicitSelection(window)
		return nil
	}

	if !config.CopyOnSelect {
		return nil
	}

	var text string
	func() {
		window.RLockIO()
		defer window.RUnlockIO()
		if window.Terminal != nil {
			text = extractVisualText(cm, window)
		}
	}()
	if text == "" {
		return nil
	}

	// Deliberately not stored on window.SelectedText: that field drives the
	// older, coordinate-based selection highlight, whose bounds this path never
	// sets, so filling it would paint a stray highlight at the origin.
	if o.SelectionDragged || window.ClickCount < 2 {
		return o.CopyToClipboard(text)
	}
	return o.DeferCopyToClipboard(text, remainingClickWindow(window))
}

// remainingClickWindow is how much of the multi-click window is left, measured
// from the last press rather than from this release.
//
// Anchoring it to the press is what makes the wait exactly as long as the
// period in which another click could still join the gesture, whatever the
// button was held for. Anchoring it to the release would add the hold time on
// top, so a deliberate press-and-hold would sit there with a stale clipboard
// for as long as the user leaned on the button.
func remainingClickWindow(window *terminal.Window) time.Duration {
	return multiClickInterval - time.Since(window.LastClickTime)
}

// endImplicitSelection drops a copy-mode session that only existed to carry a
// mouse gesture and has nothing left to show: no selection and nothing scrolled
// back. Without it an ordinary click inside a pane would leave the session
// hanging around until the next keystroke.
func endImplicitSelection(window *terminal.Window) {
	cm := window.CopyMode
	if cm == nil || !cm.Active || !cm.Implicit {
		return
	}
	if cm.ScrollOffset == 0 && cm.State == terminal.CopyModeNormal {
		window.ExitCopyMode()
	}
}
