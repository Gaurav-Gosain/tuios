package input

import (
	"strconv"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

const copyCountTimeout = 5 * time.Second

func addCountDigit(cm *terminal.CopyMode, digit int, fx *copyModeEffects) {
	dropStaleCount(cm, fx)
	n := cm.PendingCount*10 + digit
	n = min(n, maxVimCount)
	cm.PendingCount = n
	cm.CountStartTime = time.Now()
	fx.ShowNotification(strconv.Itoa(n), "info", 0)
}

func dropStaleCount(cm *terminal.CopyMode, fx *copyModeEffects) {
	if cm.PendingCount != 0 && time.Since(cm.CountStartTime) > copyCountTimeout {
		cm.PendingCount = 0
		fx.ShowNotification("", "info", 0)
	}
}

func repeatMotion(count int, cm *terminal.CopyMode, move func()) {
	for range count {
		x, y, off := cm.CursorX, cm.CursorY, cm.ScrollOffset
		move()
		if cm.CursorX == x && cm.CursorY == y && cm.ScrollOffset == off {
			return
		}
	}
}

func findCharCount(cm *terminal.CopyMode, grid terminal.CopyGrid, char rune, count int) {
	x, y, off := cm.CursorX, cm.CursorY, cm.ScrollOffset
	for i := range count {
		bx, by, boff := cm.CursorX, cm.CursorY, cm.ScrollOffset
		till := cm.LastCharSearchTill && i == count-1
		findCharOnLine(cm, grid, char, cm.LastCharSearchDir, till)
		if cm.CursorX == bx && cm.CursorY == by && cm.ScrollOffset == boff {
			if i > 0 {
				cm.CursorX, cm.CursorY, cm.ScrollOffset = x, y, off
			}
			return
		}
	}
}
