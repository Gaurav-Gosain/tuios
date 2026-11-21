package input

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func waitingForChar(cm *terminal.CopyMode) bool {
	return cm.Pending == terminal.CopyPendingChar
}

func takePendingG(cm *terminal.CopyMode, s *config.Settings) bool {
	if cm.Pending != terminal.CopyPendingG {
		return false
	}
	cm.Pending = terminal.CopyPendingNone
	return time.Since(cm.PendingSince) < s.ChordTimeout()
}

func pressG(cm *terminal.CopyMode, grid terminal.CopyGrid, finishes bool) {
	if !finishes {
		cm.Pending, cm.PendingSince = terminal.CopyPendingG, time.Now()
		return
	}
	moveToTop(cm, grid)
	if cm.State.InVisual() {
		updateVisualEnd(cm, grid)
	}
}

func armCharSearch(cm *terminal.CopyMode, key string, count int, fx *copyModeEffects) {
	cm.Pending, cm.PendingSince = terminal.CopyPendingChar, time.Now()
	cm.PendingCharCount = count
	cm.LastCharSearchDir = 1
	if key == "F" || key == "T" {
		cm.LastCharSearchDir = -1
	}
	cm.LastCharSearchTill = key == "t" || key == "T"
	fx.ShowNotification(key, "info", 0)
}

func takeSearchChar(keyStr string, cm *terminal.CopyMode, grid terminal.CopyGrid, fx *copyModeEffects) {
	cm.Pending = terminal.CopyPendingNone
	fx.ShowNotification("", "info", 0)
	if len(keyStr) != 1 || keyStr[0] < 32 || keyStr[0] > 126 {
		return
	}
	cm.LastCharSearch = rune(keyStr[0])
	findCharCount(cm, grid, cm.LastCharSearch, max(cm.PendingCharCount, 1))
	if cm.State.InVisual() {
		updateVisualEnd(cm, grid)
	}
	fx.InvalidateCache()
}
