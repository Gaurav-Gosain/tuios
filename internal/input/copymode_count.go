package input

import (
	"strconv"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// copyCountTimeout is how long a typed count waits for its key. A count left
// longer is dropped when the next key comes, and that key acts once.
const copyCountTimeout = 5 * time.Second

// addCountDigit adds a typed digit to the pending count and shows the
// count. The count stops at maxVimCount, the scrollback browser's bound:
// every motion runs once per count on the Update goroutine, so a count with
// no bound froze the client (9999999999w is ten billion word motions).
func addCountDigit(cm *terminal.CopyMode, digit int, fx *copyModeEffects) {
	dropStaleCount(cm, fx)
	n := cm.PendingCount*10 + digit
	n = min(n, maxVimCount)
	cm.PendingCount = n
	cm.CountStartTime = time.Now()
	fx.ShowNotification(strconv.Itoa(n), "info", 0)
}

// dropStaleCount drops a count typed more than copyCountTimeout ago.
func dropStaleCount(cm *terminal.CopyMode, fx *copyModeEffects) {
	if cm.PendingCount != 0 && time.Since(cm.CountStartTime) > copyCountTimeout {
		cm.PendingCount = 0
		fx.ShowNotification("", "info", 0)
	}
}

// repeatMotion runs move count times, and stops at the first step that did
// not move the cursor or the view: at the end of the buffer every further
// step would do nothing.
func repeatMotion(count int, cm *terminal.CopyMode, move func()) {
	for range count {
		x, y, off := cm.CursorX, cm.CursorY, cm.ScrollOffset
		move()
		if cm.CursorX == x && cm.CursorY == y && cm.ScrollOffset == off {
			return
		}
	}
}

// findCharCount runs a pending f, F, t or T count times, as vim does: 3fx
// goes to the third x. When there are fewer than count matches the cursor
// does not move. A till search goes to each match but the last with a find,
// so it does not stop again before the same character.
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
