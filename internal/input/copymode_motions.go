package input

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

type copyMotion func(cm *terminal.CopyMode, grid terminal.CopyGrid, count int)

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

var fixedCopyMotions = map[string]string{
	"0":      config.ActionCopyModeLineStart,
	"^":      config.ActionCopyModeFirstNonBlank,
	"$":      config.ActionCopyModeLineEnd,
	"w":      config.ActionCopyModeWordForward,
	"b":      config.ActionCopyModeWordBackward,
	"ctrl+b": config.ActionCopyModePageUp,
}

func repeated(move func(*terminal.CopyMode, terminal.CopyGrid)) copyMotion {
	return func(cm *terminal.CopyMode, grid terminal.CopyGrid, count int) {
		repeatMotion(max(count, 1), cm, func() { move(cm, grid) })
	}
}

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
