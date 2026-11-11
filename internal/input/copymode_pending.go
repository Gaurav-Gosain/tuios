package input

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// waitingForChar reports whether f, F, t or T waits for its character, which
// takes the next key as text.
func waitingForChar(cm *terminal.CopyMode) bool {
	return cm.Pending == terminal.CopyPendingChar
}

// takePendingG reports whether a g pressed now finishes gg, and ends a
// pending g either way: it is for the very next key only, so g j g is not gg.
func takePendingG(cm *terminal.CopyMode, s *config.Settings) bool {
	if cm.Pending != terminal.CopyPendingG {
		return false
	}
	cm.Pending = terminal.CopyPendingNone
	return time.Since(cm.PendingSince) < s.ChordTimeout()
}

// pressG runs g: the second g of gg goes to the top, and a first g waits for
// the second. finishes is takePendingG's answer for this key.
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

// armCharSearch starts f, F, t or T (key), which waits for its character.
// count is the count typed before it.
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

// takeSearchChar ends a pending f, F, t or T with keyStr: a printable ASCII
// character runs the search, esc or anything else cancels it. In a visual
// selection the selection's end follows the cursor.
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
