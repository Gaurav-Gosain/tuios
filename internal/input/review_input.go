package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/textedit"
)

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
	key = commandKey(msg)
	if o.ReviewConfirming() {
		return o, o.ReviewCompareConfirm(key == "y" || key == "Y")
	}
	if o.ReviewCompareShown() {
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
