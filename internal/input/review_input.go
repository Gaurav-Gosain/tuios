package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/textedit"
)

// handleReviewInput handles a key while the review overlay is open. It owns
// every key, in either mode, wherever it was opened from: a key meant for the
// diff must not reach a pane, the Inbox or the rail underneath.
//
// The keys are the overlay's own, not bindings, like the scrollback browser's,
// except the hunk and file motions and the stage keys, which
// [keybindings.review] binds:
//
//	j k, arrows      move by line (through the files with the list focused)
//	] [              next and previous hunk (review_next_hunk, review_prev_hunk)
//	} {              next and previous file (review_next_file, review_prev_file)
//	a d              stage or unstage the hunk (review_stage_hunk, review_unstage_hunk)
//	A D              stage or unstage the file (review_stage_file, review_unstage_file)
//	z                undo the last stage or unstage (review_undo_stage)
//	tab              the file list or the diff
//	enter            in the file list: open that file
//	c, C             a note on the line, or on the whole hunk
//	e, x             edit or resolve the note under the cursor
//	S                send the unsent notes to the pane's agent
//	s                one column, or the two sides next to each other
//	h l, left right  scroll the focused panel sideways: the code, or the paths
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
		if textedit.IsWordDeleteKey(key) {
			o.ReviewEditorDeleteWord()
			return o, nil
		}
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
	// Past the editor the keys are commands. See commandKey.
	key = commandKey(msg)
	if o.ReviewConfirming() {
		return o, o.ReviewCompareConfirm(key == "y" || key == "Y")
	}
	if o.ReviewCompareShown() {
		// The attempts list moves on the list keys, letters included. Its
		// own K (keep) is not a list key by default.
		if listKey(o, key, true, listPage, o.ReviewCompareMove) {
			return o, nil
		}
		switch key {
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
	if action := tableAction(o, msg, overlayKeys(o), config.TableReview); action != "" {
		var cmd tea.Cmd
		switch action {
		case config.ActionReviewNextHunk:
			o.ReviewHunk(1)
		case config.ActionReviewPrevHunk:
			o.ReviewHunk(-1)
		case config.ActionReviewNextFile:
			o.ReviewFile(1)
		case config.ActionReviewPrevFile:
			o.ReviewFile(-1)
		case config.ActionReviewStageHunk:
			cmd = o.ReviewStage(false, false)
		case config.ActionReviewUnstageHunk:
			cmd = o.ReviewStage(true, false)
		case config.ActionReviewStageFile:
			cmd = o.ReviewStage(false, true)
		case config.ActionReviewUnstageFile:
			cmd = o.ReviewStage(true, true)
		case config.ActionReviewUndoStage:
			cmd = o.ReviewUndoStage()
		default:
			return o, nil
		}
		o.NoteAction(action)
		return o, cmd
	}
	// The diff moves on the list keys, letters included; a page is the rows
	// the panel shows.
	switch o.ListKeys.Keys(key, true) {
	case listnav.Up:
		o.ReviewMove(-1)
		return o, nil
	case listnav.Down:
		o.ReviewMove(1)
		return o, nil
	case listnav.PageUp:
		o.ReviewPage(-1)
		return o, nil
	case listnav.PageDown:
		o.ReviewPage(1)
		return o, nil
	case listnav.Home:
		o.ReviewEdge(false)
		return o, nil
	case listnav.End:
		o.ReviewEdge(true)
		return o, nil
	}
	switch key {
	// Half a page, as in vim and less, besides the list's page keys.
	case "ctrl+d", "space":
		o.ReviewPage(1)
	case "ctrl+u":
		o.ReviewPage(-1)
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
	case "s":
		o.ReviewToggleSplit()
	case "h", "left":
		o.ReviewScrollX(-1)
	case "l", "right":
		o.ReviewScrollX(1)
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
