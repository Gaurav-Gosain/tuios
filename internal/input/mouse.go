package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
)

func sendMouseToWindow(o *app.OS, win *terminal.Window, event uv.MouseEvent) {
	if o.DrawsTML(win) {
		o.TMLMouse(win, event)
		return
	}
	if win.Terminal == nil {
		return
	}
	mouse := event.Mouse()
	at := o.PointerPixelIn(win.CellPixelWidth, win.CellPixelHeight, mouse.X, mouse.Y)
	if win.DaemonMode {
		seq := win.Terminal.EncodeMouseEventAt(event, at)
		if seq != "" {
			_ = win.SendInput([]byte(seq))
		}
	} else {
		win.Terminal.SendMouseAt(event, at)
	}
}

func paneTakesMouse(o *app.OS, w *terminal.Window) bool {
	return w != nil && w.Terminal != nil && (o.DrawsTML(w) || w.Terminal.HasMouseMode())
}

func paneWantsMotion(o *app.OS, w *terminal.Window, button tea.MouseButton) bool {
	if w == nil || w.Terminal == nil {
		return false
	}
	if o.DrawsTML(w) && button != tea.MouseNone {
		return true
	}
	return guestWantsMotion(w.Terminal, button)
}

func pipOwns(x, y int, o *app.OS) bool {
	if !o.PiPAt(x, y) {
		return false
	}
	idx := findClickedWindow(x, y, o)
	return idx < 0 || app.HitZ(o.Windows[idx]) < config.ZIndexPiP
}

func findClickedWindow(x, y int, o *app.OS) int {
	topWindow := -1
	topZ := -1

	for i, window := range o.Windows {
		if window.Workspace != o.CurrentWorkspace {
			continue
		}
		if window.Minimized {
			continue
		}
		if x >= window.X && x < window.X+window.Width &&
			y >= window.Y && y < window.Y+window.Height {
			if z := app.HitZ(window); z >= topZ {
				topZ = z
				topWindow = i
			}
		}
	}

	return topWindow
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func scrollbarGrab(win *terminal.Window, rect app.ScrollbarRect, mouseY int, s *config.Settings) int {
	if rect.OnThumb(mouseY) {
		return mouseY - rect.ThumbY
	}
	scrollToThumbRow(win, mouseY-rect.ThumbH/2, s)
	return mouseY - app.ScrollbarThumbRow(win, s)
}

func scrollToThumbRow(win *terminal.Window, row int, s *config.Settings) {
	if win.Terminal == nil {
		return
	}
	win.RLockIO()
	scrollbackLen := win.Terminal.ScrollbackLen()
	win.RUnlockIO()
	if scrollbackLen <= 0 {
		return
	}

	if !win.InCopyMode() {
		win.EnterCopyModeImplicit()
	}
	if win.CopyMode == nil {
		return
	}

	scrollOffset := app.ScrollbarOffsetForThumbRow(win, row, s)

	win.CopyMode.ScrollOffset = scrollOffset
	win.ScrollbackOffset = scrollOffset
	win.InvalidateCache()

	leaveCopyModeAtBottom(win)
}
