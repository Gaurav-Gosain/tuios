package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

func (kp *KittyPassthrough) OnWindowMove(windowID string, newX, newY, contentOffsetX, contentOffsetY int, scrollbackLen, scrollOffset, viewportHeight int) {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	if !kp.enabled {
		return
	}
	// In web mode, RefreshAllPlacements handles repositioning via the
	// overlay. OnWindowMove's delete-then-reposition pattern sends d=i
	// which wipes image data from the overlay's storage.
	if kp.inlineGraphics {
		return
	}

	placements := kp.placements[windowID]
	if placements == nil {
		return
	}

	viewportTop := scrollbackLen - scrollOffset

	for _, p := range placements {
		if !p.Hidden {
			kp.deleteOnePlacement(p)
		}

		relativeY := p.AbsoluteLine - viewportTop
		p.HostX = newX + contentOffsetX + p.GuestX
		p.HostY = newY + contentOffsetY + relativeY

		// Check if in viewport
		if relativeY >= 0 && relativeY < viewportHeight {
			kp.placeOne(p)
			p.Hidden = false
		} else {
			p.Hidden = true
		}
	}
}

func (kp *KittyPassthrough) OnWindowClose(windowID string) {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	if !kp.enabled {
		return
	}

	placements := kp.placements[windowID]
	for _, p := range placements {
		kp.deleteOnePlacement(p)
	}
	delete(kp.placements, windowID)
	delete(kp.imageIDMap, windowID)
}

func (kp *KittyPassthrough) ClearWindow(windowID string) {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	kittyPassthroughLog("ClearWindow: winID=%s enabled=%v, %d placements to delete",
		windowID[:min(8, len(windowID))], kp.enabled, len(kp.placements[windowID]))

	if !kp.enabled {
		return
	}

	placements := kp.placements[windowID]
	for _, p := range placements {
		kp.deleteOnePlacement(p)
	}
	kp.placements[windowID] = nil
}

func (m *OS) setupKittyPassthrough(window *terminal.Window) {
	if m.KittyPassthrough == nil || window == nil || window.Terminal == nil {
		return
	}

	win := window
	kp := m.KittyPassthrough

	// Set up callback for when placements are cleared (e.g., clear screen, ED sequences).
	// The VT keeps separate KittyState objects for the main and alt screens; the
	// active one depends on IsAltScreen() at call time. Register on BOTH so the
	// clear callback fires regardless of which screen the app is on (youterm,
	// yazi, etc. run on alt screen and rely on ED 2 clearing their thumbnails).
	clearCallback := func() {
		kittyPassthroughLog("CALLBACK FIRED: winID=%s", win.ID[:min(8, len(win.ID))])
		kp.ClearWindow(win.ID)
	}
	window.Terminal.KittyMainState().SetClearCallback(clearCallback)
	window.Terminal.KittyAltState().SetClearCallback(clearCallback)
	kittyPassthroughLog("setupKittyPassthrough: registered clear callback on BOTH main/alt for winID=%s",
		win.ID[:min(8, len(win.ID))])

	window.Terminal.SetKittyPassthroughFunc(func(cmd *vt.KittyCommand, rawData []byte) {
		// In daemon mode, the daemon's VT emulator responds to queries directly
		// with low latency. Skip here to avoid sending a duplicate response.
		if win.DaemonMode && cmd.Action == vt.KittyActionQuery {
			return
		}

		cursorPos := win.Terminal.CursorPosition()
		scrollbackLen := win.Terminal.ScrollbackLen()
		borderOff := win.BorderOffset()
		result := kp.ForwardCommand(
			cmd, rawData, win.ID,
			win.X, win.Y,
			win.Width, win.Height,
			borderOff, borderOff,
			cursorPos.X, cursorPos.Y,
			scrollbackLen,
			win.IsAltScreen,
			func(response []byte) {
				kittyPassthroughLog("ptyInput callback: Pty=%v, DaemonWriteFunc=%v, response=%q", win.Pty != nil, win.DaemonWriteFunc != nil, response)
				if win.Pty != nil {
					_, _ = win.Pty.Write(response)
				} else if win.DaemonWriteFunc != nil {
					_ = win.DaemonWriteFunc(response)
				} else {
					kittyPassthroughLog("ptyInput callback: WARNING - both Pty and DaemonWriteFunc are nil, response dropped!")
				}
			},
		)
		// Reserve space in guest terminal for the image placement
		// Only move cursor when C=0 (default behavior), not when C=1 (no cursor move)
		if result != nil && result.Rows > 0 && result.CursorMove == 0 {
			win.Terminal.ReserveImageSpace(result.Rows, result.Cols)
		}
	})
}
