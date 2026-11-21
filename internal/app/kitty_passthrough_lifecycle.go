package app

import (
	"bytes"
	"fmt"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"tuios.dev/vt"
)

func (kp *KittyPassthrough) OnWindowMove(windowID string, newX, newY, contentOffsetX, contentOffsetY int, scrollbackLen, scrollOffset, viewportHeight int) {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	if !kp.enabled {
		return
	}
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

	kp.releaseHeld(windowID)
	delete(kp.held, windowID)
	delete(kp.syncProbes, windowID)
	delete(kp.heldFrames, windowID)
	delete(kp.heldGeom, windowID)

	if !kp.enabled {
		return
	}

	kp.deleteWindowImages(windowID, false)
	kp.dropWindowPlacements(windowID)
	for _, hostID := range kp.imageIDMap[windowID] {
		kp.releaseHostID(windowID, hostID)
	}
	delete(kp.imageIDMap, windowID)
	kp.forgetKept(windowID, 0)
	delete(kp.rawChunks, windowID)
	kp.forgetImagePixels(windowID, 0)
	kp.forgetFrameHashes(windowID)
	kp.deleteRemoteVideoImages(windowID)
	kp.deleteVirtualImages(windowID)
}

func (kp *KittyPassthrough) deleteVirtualImages(windowID string) {
	ids := kp.virtualImages[windowID]
	if len(ids) == 0 {
		delete(kp.virtualImages, windowID)
		return
	}
	var buf bytes.Buffer
	for hostID := range ids {
		fmt.Fprintf(&buf, "\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", hostID)
		kp.forgetKept(windowID, hostID)
	}
	kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
	delete(kp.virtualImages, windowID)
}

func (kp *KittyPassthrough) deleteRemoteVideoImages(windowID string) {
	ids := kp.remoteVideo[windowID]
	delete(kp.lastFrameHash, windowID)
	if len(ids) == 0 {
		delete(kp.remoteVideo, windowID)
		return
	}
	var buf bytes.Buffer
	for hostID := range ids {
		fmt.Fprintf(&buf, "\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", hostID)
	}
	kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
	delete(kp.remoteVideo, windowID)
}

func (kp *KittyPassthrough) ClearWindow(windowID string) {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	kp.beginGuestCapture()
	defer kp.endGuestCapture(windowID)

	kittyPassthroughLog("ClearWindow: winID=%s enabled=%v, %d placements to delete",
		windowID[:min(8, len(windowID))], kp.enabled, len(kp.placements[windowID]))

	if !kp.enabled {
		return
	}

	placements := kp.placements[windowID]
	for _, p := range placements {
		kp.deleteOnePlacement(p)
	}
	kp.dropWindowPlacements(windowID)
	kp.deleteRemoteVideoImages(windowID)
	kp.deleteVirtualImages(windowID)
}

func (m *OS) setupKittyPassthrough(window *terminal.Window) {
	if m.KittyPassthrough == nil || window == nil || window.Terminal == nil {
		return
	}

	win := window
	kp := m.KittyPassthrough

	clearCallback := func() {
		kittyPassthroughLog("CALLBACK FIRED: winID=%s", win.ID[:min(8, len(win.ID))])
		kp.ClearWindow(win.ID)
	}
	window.Terminal.KittyMainState().SetClearCallback(clearCallback)
	window.Terminal.KittyAltState().SetClearCallback(clearCallback)
	kittyPassthroughLog("setupKittyPassthrough: registered clear callback on BOTH main/alt for winID=%s",
		win.ID[:min(8, len(win.ID))])

	window.Terminal.SetKittyImageIDTranslator(func(guestID uint32) (uint32, bool) {
		return kp.HostImageIDForPlaceholder(win.ID, guestID)
	})
	mode := vt.KittyPlaceholdersDrop
	if m.placeholdersEnabled() {
		mode = vt.KittyPlaceholdersKeep
	}
	window.Terminal.SetKittyPlaceholderMode(mode)

	kp.SetGuestSyncProbe(win.ID, func() (bool, uint64) {
		if t := win.Terminal; t != nil {
			return t.SyncUpdate()
		}
		return false, 0
	})

	window.Terminal.SetKittyPassthroughFunc(func(cmd *vt.KittyCommand, rawData []byte) {
		if win.DaemonMode && cmd.Action == vt.KittyActionQuery {
			return
		}
		if win.ProcessElsewhere() && cmd.Medium.IsFile() {
			kp.DropRemoteFileMedium(win.ID, cmd)
			return
		}

		answer := clientAnswersKittyCommand(win.DaemonMode, cmd.Action)

		if !kp.IsEnabled() && m.SixelPassthrough.DrawsSymbols() {
			m.drawKittySymbols(win, kp, cmd)
		}

		cursorPos := win.Terminal.CursorPosition()
		scrollbackLen := win.Terminal.ScrollbackLen()
		geo := win.LastGeometry()
		result := kp.ForwardCommand(
			cmd, rawData, win.ID,
			geo.X, geo.Y,
			geo.ContentW, geo.ContentH,
			geo.BorderOffset, geo.BorderOffset,
			cursorPos.X, cursorPos.Y,
			scrollbackLen,
			win.IsAltScreen(),
			func(response []byte) {
				if !answer {
					return
				}
				kittyPassthroughLog("ptyInput callback: Pty=%v, DaemonWriteFunc=%v, response=%q", win.Pty != nil, win.DaemonWriteFunc != nil, response)
				if win.Pty != nil {
					_, _ = win.Pty.Write(response)
				} else if win.DaemonWriteFunc != nil {
					_ = win.DaemonWriteFunc(response)
				} else {
					kittyPassthroughLog("ptyInput callback: WARNING: both Pty and DaemonWriteFunc are nil, response dropped!")
				}
			},
		)
		if result != nil && result.Rows > 0 && result.CursorMove == 0 {
			win.Terminal.ReserveImageSpace(result.Rows, result.Cols)
		}
	})
}

func clientAnswersKittyCommand(daemonPane bool, action vt.KittyGraphicsAction) bool {
	return !daemonPane || !vt.IsKittyAnimationAction(action)
}
