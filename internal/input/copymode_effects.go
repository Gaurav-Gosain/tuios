package input

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"tuios.dev/vt"
)

type copyModeEffects struct {
	notifications        []copyModeNotification
	invalidate           bool
	exitCopyMode         bool
	enterTerminal        bool
	clipboard            string
	setClipboard         bool
	flash                bool
	flashBlock           bool
	flashStart, flashEnd terminal.Position
	action               string
	editScrollback       bool
	openHints            bool
}

type copyModeNotification struct {
	message  string
	notyType string
	duration time.Duration
}

//go:noinline
func (fx *copyModeEffects) ShowNotification(message, notifType string, duration time.Duration) {
	fx.notifications = append(fx.notifications, copyModeNotification{
		message:  message,
		notyType: notifType,
		duration: duration,
	})
}

func (fx *copyModeEffects) InvalidateCache() { fx.invalidate = true }

func (fx *copyModeEffects) ExitCopyMode() { fx.exitCopyMode = true }

func (fx *copyModeEffects) EnterTerminalMode() { fx.enterTerminal = true }

func (fx *copyModeEffects) SetClipboard(text string) {
	fx.clipboard = vt.StripSixelMarkers(text)
	fx.setClipboard = true
}

func (fx *copyModeEffects) Flash(start, end terminal.Position) {
	fx.flash = true
	fx.flashStart, fx.flashEnd = start, end
}

func (fx *copyModeEffects) FlashBlock(start, end terminal.Position) {
	fx.Flash(start, end)
	fx.flashBlock = true
}

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

func (fx *copyModeEffects) clipboardCmd(o *app.OS) tea.Cmd {
	if o != nil && o.Settings.CopyCommand != "" {
		return o.Yank(fx.clipboard)
	}
	cmd := tea.SetClipboard(fx.clipboard)
	if o != nil {
		cmd = tea.Batch(cmd, o.SaveToPasteBuffers(fx.clipboard))
	}
	return cmd
}

func (fx *copyModeEffects) applyQuickLook(o *app.OS) (*app.OS, tea.Cmd) {
	return fx.applyOver(o, o.QuickLookCopyEnd)
}

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
