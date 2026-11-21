package input

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func scrollCopyModeUp(win *terminal.Window, s *config.Settings) {
	scrollCopyModeUpBy(win, scrollStep(s))
}

func scrollCopyModeDown(win *terminal.Window, s *config.Settings) {
	scrollCopyModeDownBy(win, scrollStep(s))
}

func scrollCopyModeUpBy(win *terminal.Window, lines int) {
	cm := win.CopyMode
	if cm == nil || !cm.Active {
		return
	}
	limit := win.ScrollbackLen()
	moved := min(cm.ScrollOffset+lines, limit) - cm.ScrollOffset
	if moved <= 0 {
		return
	}
	cm.ScrollOffset += moved
	win.ScrollbackOffset = cm.ScrollOffset
	setCopyCursorRow(cm, win, cm.CursorY+moved)
	win.InvalidateCache()
}

func scrollCopyModeDownBy(win *terminal.Window, lines int) {
	cm := win.CopyMode
	if cm == nil || !cm.Active {
		return
	}
	moved := cm.ScrollOffset - max(cm.ScrollOffset-lines, 0)
	if moved <= 0 {
		return
	}
	cm.ScrollOffset -= moved
	win.ScrollbackOffset = cm.ScrollOffset
	setCopyCursorRow(cm, win, cm.CursorY-moved)
	win.InvalidateCache()
}

func setCopyCursorRow(cm *terminal.CopyMode, win *terminal.Window, row int) {
	cm.CursorY = max(min(row, win.ContentHeight()-1), 0)
}

func scrollStep(s *config.Settings) int {
	return max(s.ScrollLines, 1)
}

func leaveCopyModeAtBottom(win *terminal.Window) {
	cm := win.CopyMode
	if cm == nil || !cm.Active || !cm.Implicit {
		return
	}
	if cm.ScrollOffset != 0 || cm.State != terminal.CopyModeNormal {
		return
	}
	win.ExitCopyMode()
}
