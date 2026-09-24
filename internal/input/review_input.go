package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleReviewInput handles a key while the review overlay is open. It owns
// every key, in either mode, wherever it was opened from: a key meant for the
// diff must not reach a pane, the Inbox or the rail underneath.
//
// The keys are the overlay's own, not bindings, like the scrollback browser's:
//
//	j k, arrows      move by line (through the files with the list focused)
//	] [              next and previous hunk
//	} {              next and previous file
//	tab              the file list or the diff
//	enter            in the file list: open that file
//	c, C             a note on the line, or on the whole hunk
//	e, x             edit or resolve the note under the cursor
//	S                send the unsent notes to the pane's agent
//	u                since the base, or the uncommitted changes only
//	b                change the base
//	w                the compare view, for a pane in a fan
//	r                read the diff again
//	esc, q           close (or back to the compare view)
//
// In the compare view: j k move, enter reviews an attempt, m marks one, d
// diffs the two marked, V runs a command in every attempt, K keeps one and
// esc goes back. While a line is being typed every printable key is text,
// enter saves it and esc drops it.
func handleReviewInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	key := msg.String()
	if o.ReviewEditing() {
		switch key {
		case "enter":
			return o, o.ReviewEditorSubmit()
		case "esc":
			o.ReviewEditorCancel()
		case "backspace", "ctrl+h":
			o.ReviewEditorBackspace()
		case "space":
			o.ReviewEditorType(" ")
		default:
			if msg.Text != "" {
				o.ReviewEditorType(msg.Text)
			}
		}
		return o, nil
	}
	if o.ReviewConfirming() {
		return o, o.ReviewCompareConfirm(key == "y" || key == "Y")
	}
	if o.ReviewCompareShown() {
		switch key {
		case "j", "down":
			o.ReviewCompareMove(1)
		case "k", "up":
			o.ReviewCompareMove(-1)
		case "enter":
			return o, o.ReviewCompareOpen()
		case "m":
			o.ReviewCompareMark()
		case "d":
			return o, o.ReviewCompareDiff()
		case "V":
			o.ReviewCompareVerifyPrompt()
		case "K":
			o.ReviewCompareKeep()
		case "esc", "q", "w":
			return o, o.ReviewCompareBack()
		}
		return o, nil
	}
	switch key {
	case "j", "down":
		o.ReviewMove(1)
	case "k", "up":
		o.ReviewMove(-1)
	case "pgdown", "ctrl+d", "space":
		o.ReviewPage(1)
	case "pgup", "ctrl+u":
		o.ReviewPage(-1)
	case "g", "home":
		o.ReviewEdge(false)
	case "G", "end":
		o.ReviewEdge(true)
	case "]":
		o.ReviewHunk(1)
	case "[":
		o.ReviewHunk(-1)
	case "}":
		o.ReviewFile(1)
	case "{":
		o.ReviewFile(-1)
	case "tab":
		o.ReviewToggleFocus()
	case "enter":
		o.ReviewEnter()
	case "c":
		o.ReviewNote(false)
	case "C":
		o.ReviewNote(true)
	case "e":
		o.ReviewEditNote()
	case "x":
		return o, o.ReviewResolveNote()
	case "S":
		return o, o.ReviewSend()
	case "u":
		return o, o.ReviewToggleUncommitted()
	case "b":
		o.ReviewBasePrompt()
	case "w":
		return o, o.ReviewCompare()
	case "r":
		return o, o.ReviewReload()
	case "esc", "q":
		return o, o.ReviewClose()
	}
	return o, nil
}
