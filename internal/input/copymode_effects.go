package input

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"tuios.dev/vt"
)

// copyModeEffects records the side effects a copy-mode key handler wants to
// perform, so they can be applied AFTER the window's I/O read lock is dropped.
//
// The handlers walk the emulator cell buffer (CellAt/Width/Height/scrollback)
// and therefore have to run under RLockIO. Everything else they do
// (notifications, cache invalidation, leaving copy mode, entering terminal
// mode, setting the clipboard) touches OS/Window state that has nothing to do
// with the cell buffer. Running those inside the lock would leave the handler
// one SendInput call away from a recursive read-lock deadlock: any effect that grows a PTY write or a second RLockIO would park
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
	// flash is the region a yank took, written down before the yank ends the
	// visual selection it came from. See Flash.
	flash                bool
	flashBlock           bool
	flashStart, flashEnd terminal.Position
	// action is the [keybindings.copy_mode] action the key ran, for the
	// recent actions list.
	action string
	// editScrollback opens the scrollback in the editor. It reads the
	// window and writes a file, so it runs after the lock is dropped.
	editScrollback bool
	// openHints opens hints mode on the pane, over the copy-mode view.
	openHints bool
}

type copyModeNotification struct {
	message  string
	notyType string
	duration time.Duration
}

// ShowNotification queues a notification. Notifications are applied in call
// order, matching the append semantics of OS.ShowNotification, so a handler
// that notifies and then clears still ends up with both entries.
//
//go:noinline
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

// SetClipboard queues the yank of text: through the copy command when one is
// set, else an OSC 52 clipboard write.
func (fx *copyModeEffects) SetClipboard(text string) {
	// Image cells are markers in the grid; a copy gets blanks for them.
	fx.clipboard = vt.StripSixelMarkers(text)
	fx.setClipboard = true
}

// Flash queues the copy sweep over the region a yank took, in the pane's
// absolute coordinates. A yank ends the visual selection inside the locked
// region, so by the time the effects apply there is no selection left to read
// the region from.
func (fx *copyModeEffects) Flash(start, end terminal.Position) {
	fx.flash = true
	fx.flashStart, fx.flashEnd = start, end
}

// FlashBlock is Flash for a rectangular selection.
func (fx *copyModeEffects) FlashBlock(start, end terminal.Position) {
	fx.Flash(start, end)
	fx.flashBlock = true
}

// apply runs the queued effects against the real OS and Window. It must be
// called with the window's I/O lock NOT held.
//
// The order is fixed: leave copy mode
// first, then invalidate the cache, then notify, then produce the tea.Cmd.
func (fx *copyModeEffects) apply(o *app.OS, window *terminal.Window) (*app.OS, tea.Cmd) {
	if fx.exitCopyMode {
		window.ExitCopyMode()
	}
	if fx.invalidate {
		window.InvalidateCache()
	}
	if o != nil {
		o.NoteAction(fx.action)
		for _, n := range fx.notifications {
			o.ShowNotification(n.message, n.notyType, n.duration)
		}
	}

	var cmd tea.Cmd
	if fx.enterTerminal && o != nil {
		cmd = o.EnterTerminalMode()
	}
	if fx.flash && o != nil {
		if fx.flashBlock {
			o.NoteCopyFlashBlock(window, fx.flashStart, fx.flashEnd)
		} else {
			o.NoteCopyFlashRegion(window, fx.flashStart, fx.flashEnd)
		}
	}
	if fx.setClipboard {
		cmd = fx.clipboardCmd(o)
	}
	if fx.editScrollback && o != nil {
		cmd = tea.Batch(cmd, o.EditScrollbackInEditor())
	}
	if fx.openHints && o != nil {
		o.OpenHints()
	}
	return o, cmd
}

// clipboardCmd writes the yank: through the copy command when one is set,
// else an OSC 52 clipboard write, and into the paste buffers either way.
func (fx *copyModeEffects) clipboardCmd(o *app.OS) tea.Cmd {
	if o != nil && o.Settings.CopyCommand != "" {
		// Yank keeps the paste buffer itself.
		return o.Yank(fx.clipboard)
	}
	cmd := tea.SetClipboard(fx.clipboard)
	if o != nil {
		cmd = tea.Batch(cmd, o.SaveToPasteBuffers(fx.clipboard))
	}
	return cmd
}

// applyQuickLook runs the queued effects of a key in copy mode over the quick
// look preview. Leaving copy mode, to the pane too (i), goes back to the
// preview. The editor and hints work on a pane, so they do nothing here.
func (fx *copyModeEffects) applyQuickLook(o *app.OS) (*app.OS, tea.Cmd) {
	return fx.applyOver(o, o.QuickLookCopyEnd)
}

// applyOver runs the queued effects of a key in copy mode over text that is
// not a pane, the quick look preview or the agents view's thread. Leaving
// copy mode, to the pane too (i), calls end. The editor and hints work on a
// pane, so they do nothing here.
func (fx *copyModeEffects) applyOver(o *app.OS, end func()) (*app.OS, tea.Cmd) {
	if fx.exitCopyMode || fx.enterTerminal {
		end()
	}
	o.NoteAction(fx.action)
	for _, n := range fx.notifications {
		o.ShowNotification(n.message, n.notyType, n.duration)
	}
	if fx.setClipboard {
		return o, fx.clipboardCmd(o)
	}
	return o, nil
}
