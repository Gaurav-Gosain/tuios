package input

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// copyMotion moves the copy-mode cursor. count is the count typed before the
// key, 0 when none.
type copyMotion func(cm *terminal.CopyMode, grid terminal.CopyGrid, count int)

// copyMotions are copy mode's cursor motions, by their [keybindings.copy_mode]
// action. Normal mode and a visual selection run the same motion (see
// runCopyMotion), so a key moves the same way in both.
var copyMotions = map[string]copyMotion{
	config.ActionCopyModeLeft:            repeated(moveLeft),
	config.ActionCopyModeRight:           repeated(moveRight),
	config.ActionCopyModeUp:              repeated(moveUp),
	config.ActionCopyModeDown:            repeated(moveDown),
	config.ActionCopyModeWordForward:     repeated(moveWordForward),
	config.ActionCopyModeWordBackward:    repeated(moveWordBackward),
	config.ActionCopyModeWordEnd:         repeated(moveWordEnd),
	config.ActionCopyModeBigWordForward:  repeated(moveWordForwardBig),
	config.ActionCopyModeBigWordBackward: repeated(moveWordBackwardBig),
	config.ActionCopyModeBigWordEnd:      repeated(moveWordEndBig),
	config.ActionCopyModeHalfPageUp:      repeated(moveHalfPageUp),
	config.ActionCopyModeHalfPageDown:    repeated(moveHalfPageDown),
	config.ActionCopyModePageUp:          repeated(movePageUp),
	config.ActionCopyModePageDown:        repeated(movePageDown),
	config.ActionCopyModeParagraphUp:     repeated(moveParagraphUp),
	config.ActionCopyModeParagraphDown:   repeated(moveParagraphDown),
	config.ActionCopyModeRepeatCharSearch: repeated(func(cm *terminal.CopyMode, grid terminal.CopyGrid) {
		repeatCharSearch(cm, grid, false)
	}),
	config.ActionCopyModeRepeatCharSearchBack: repeated(func(cm *terminal.CopyMode, grid terminal.CopyGrid) {
		repeatCharSearch(cm, grid, true)
	}),
	config.ActionCopyModeLineStart: func(cm *terminal.CopyMode, _ terminal.CopyGrid, _ int) {
		cm.CursorX = 0
	},
	config.ActionCopyModeFirstNonBlank: func(cm *terminal.CopyMode, grid terminal.CopyGrid, _ int) {
		cm.CursorX = firstNonBlankCol(cm, grid)
	},
	config.ActionCopyModeLineEnd: func(cm *terminal.CopyMode, grid terminal.CopyGrid, _ int) {
		cm.CursorX = lastTextCol(cm, grid)
	},
	config.ActionCopyModeScreenTop: func(cm *terminal.CopyMode, _ terminal.CopyGrid, _ int) {
		cm.CursorY = 0
	},
	config.ActionCopyModeScreenMiddle: func(cm *terminal.CopyMode, grid terminal.CopyGrid, _ int) {
		cm.CursorY = grid.ViewHeight() / 2
	},
	config.ActionCopyModeScreenBottom: func(cm *terminal.CopyMode, grid terminal.CopyGrid, _ int) {
		cm.CursorY = grid.LastContentRow()
	},
	config.ActionCopyModeMatchingBracket: func(cm *terminal.CopyMode, grid terminal.CopyGrid, _ int) {
		moveToMatchingBracket(cm, grid)
	},
	config.ActionCopyModeBottom: moveToLineOrBottom,
}

// fixedCopyMotions are the motions of the keys copy mode reads itself rather
// than through [keybindings.copy_mode]. See config.ActionCopyModeLeft.
var fixedCopyMotions = map[string]string{
	"0":      config.ActionCopyModeLineStart,
	"^":      config.ActionCopyModeFirstNonBlank,
	"$":      config.ActionCopyModeLineEnd,
	"w":      config.ActionCopyModeWordForward,
	"b":      config.ActionCopyModeWordBackward,
	"ctrl+b": config.ActionCopyModePageUp,
}

// repeated is a motion that steps count times, once when no count was typed.
func repeated(move func(*terminal.CopyMode, terminal.CopyGrid)) copyMotion {
	return func(cm *terminal.CopyMode, grid terminal.CopyGrid, count int) {
		repeatMotion(max(count, 1), cm, func() { move(cm, grid) })
	}
}

// moveToLineOrBottom is G: the last line, or line count after a count above 1
// (10G goes to line 10).
func moveToLineOrBottom(cm *terminal.CopyMode, grid terminal.CopyGrid, count int) {
	if count <= 1 {
		moveToBottom(cm, grid)
		return
	}
	target := min(count-1, grid.ScrollbackLen()+grid.Text().Height()-1)
	diff := target - getAbsoluteY(cm, grid)
	for range diff {
		moveDown(cm, grid)
	}
	for range -diff {
		moveUp(cm, grid)
	}
}

// runCopyMotion runs the motion bound to action, and reports whether action is
// a motion. In a visual selection the selection's end follows the cursor. A
// pending g is dropped: this key is not the second g of gg. The count typed
// before the key is used and cleared.
func runCopyMotion(action string, cm *terminal.CopyMode, grid terminal.CopyGrid, fx *copyModeEffects) bool {
	motion, ok := copyMotions[action]
	if !ok {
		return false
	}
	dropStaleCount(cm, fx)
	if cm.Pending == terminal.CopyPendingG {
		cm.Pending = terminal.CopyPendingNone
	}
	motion(cm, grid, cm.PendingCount)
	if cm.PendingCount != 0 {
		cm.PendingCount = 0
		fx.ShowNotification("", "info", 0)
	}
	if cm.State.InVisual() {
		updateVisualEnd(cm, grid)
	}
	fx.InvalidateCache()
	return true
}
