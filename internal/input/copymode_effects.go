package input

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// copyModeEffects records the side effects a copy-mode key handler wants to
// perform, so they can be applied AFTER the window's I/O read lock is dropped.
//
// The handlers walk the emulator cell buffer (CellAt/Width/Height/scrollback)
// and therefore have to run under RLockIO. Everything else they used to do
// inline - notifications, cache invalidation, leaving copy mode, entering
// terminal mode, setting the clipboard - touches OS/Window state that has
// nothing to do with the cell buffer. Running those inside the lock is what
// made the handler "one SendInput call away" from the recursive read-lock
// deadlock: any effect that grows a PTY write or a second RLockIO would park
// the handler behind a queued writer while it still holds the read lock that
// writer is waiting on.
//
// Routing effects through this struct makes that impossible by construction:
// nothing reachable from the locked region can take the lock again, because
// the locked region only mutates CopyMode fields and reads the buffer.
type copyModeEffects struct {
	notifications []copyModeNotification
	invalidate    bool
	exitCopyMode  bool
	enterTerminal bool
	clipboard     string
	setClipboard  bool
}

type copyModeNotification struct {
	message  string
	notyType string
	duration time.Duration
}

// ShowNotification queues a notification. Notifications are applied in call
// order, matching the append semantics of OS.ShowNotification, so a handler
// that notifies and then clears still ends up with both entries.
func (fx *copyModeEffects) ShowNotification(message, notifType string, duration time.Duration) {
	fx.notifications = append(fx.notifications, copyModeNotification{
		message:  message,
		notyType: notifType,
		duration: duration,
	})
}

// InvalidateCache marks the window's render cache for invalidation. It is
// idempotent, so handlers may call it on several paths.
func (fx *copyModeEffects) InvalidateCache() { fx.invalidate = true }

// ExitCopyMode marks copy mode for exit.
func (fx *copyModeEffects) ExitCopyMode() { fx.exitCopyMode = true }

// EnterTerminalMode marks the OS for a switch into terminal mode.
func (fx *copyModeEffects) EnterTerminalMode() { fx.enterTerminal = true }

// SetClipboard queues an OSC 52 clipboard write for the yanked text.
func (fx *copyModeEffects) SetClipboard(text string) {
	fx.clipboard = text
	fx.setClipboard = true
}

// apply runs the queued effects against the real OS and Window. It must be
// called with the window's I/O lock NOT held.
//
// The order mirrors what the handlers used to do inline: leave copy mode
// first, then invalidate the cache, then notify, then produce the tea.Cmd.
func (fx *copyModeEffects) apply(o *app.OS, window *terminal.Window) (*app.OS, tea.Cmd) {
	if fx.exitCopyMode {
		window.ExitCopyMode()
	}
	if fx.invalidate {
		window.InvalidateCache()
	}
	if o != nil {
		for _, n := range fx.notifications {
			o.ShowNotification(n.message, n.notyType, n.duration)
		}
	}

	var cmd tea.Cmd
	if fx.enterTerminal && o != nil {
		cmd = o.EnterTerminalMode()
	}
	if fx.setClipboard {
		cmd = tea.SetClipboard(fx.clipboard)
	}
	return o, cmd
}
