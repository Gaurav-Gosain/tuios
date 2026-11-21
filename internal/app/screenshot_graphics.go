//go:build unix

package app

import (
	"os/exec"
	"runtime"
)

const screenshotImageID = 0xF100_0000

const screenshotPlacement = 1

func (m *OS) screenshotGraphicsReady() bool {
	if m.PostRenderWriter == nil {
		return false
	}
	if !m.hostCaps().KittyGraphics {
		return false
	}
	w, h := m.hostCellSize()
	return w > 0 && h > 0
}

func (m *OS) screenshotPreviewPixelBudget() (maxW, maxH int) {
	cellW, cellH := m.hostCellSize()
	if cellW <= 0 || cellH <= 0 {
		return 0, 0
	}
	bodyCols, bodyRows := m.screenshotPreviewBody()
	return max(1, bodyCols*cellW*2), max(1, bodyRows*cellH*2)
}

func buildScreenshotTransmit(png []byte) []byte {
	if len(png) == 0 {
		return nil
	}
	return appendKittyTransmitPNG(nil, screenshotImageID, png)
}

func (m *OS) screenshotPreviewPictureBox() (inset, cols, rows int, ok bool) {
	if !m.ShotPreview.Open || len(m.ShotPreview.PNG) == 0 || !m.screenshotGraphicsReady() {
		return 0, 0, 0, false
	}
	cellW, cellH := m.hostCellSize()
	bodyCols, bodyRows := m.screenshotPreviewBody()
	cols, rows = fitBoxToPicture(bodyCols, bodyRows, m.ShotPreview.PixelW, m.ShotPreview.PixelH, cellW, cellH)
	return max(0, (bodyCols-cols-shotPreviewImageInset)/2), cols, rows, true
}

func (m *OS) flushScreenshotGraphicsForFrame() {
	inset, cols, rows, ok := m.screenshotPreviewPictureBox()
	if !ok {
		m.clearScreenshotGraphics()
		return
	}
	h, hit := m.overlayHitByKind(overlayKindShot)
	if !hit {
		return
	}
	x := h.OriginX + h.Geo.BodyX + shotPreviewImageInset + inset
	y := h.OriginY + h.Geo.BodyY + m.shotPreviewImageRow()
	want := screenshotPlacementState{
		x: x, y: y, cols: cols, rows: rows, capture: m.ShotPreview.Capture,
		hostW: m.Width, hostH: m.Height,
	}
	if m.shotImagePlaced && m.shotPlacement == want {
		return
	}
	if m.shotPlacement.hostW != want.hostW || m.shotPlacement.hostH != want.hostH {
		m.shotImageSent = false
	}

	var buf []byte
	if m.shotImagePlaced {
		buf = appendKittyUnplace(buf, screenshotImageID, screenshotPlacement)
	}
	if m.shotPlacement.capture != want.capture || !m.shotImageSent {
		buf = append(buf, m.ShotPreview.Transmit...)
		m.shotImageSent = true
	}
	buf = appendKittyPlaceBox(buf, screenshotImageID, screenshotPlacement, x, y, cols, rows)
	m.shotImagePlaced = true
	m.shotPlacement = want
	if m.PostRenderWriter != nil {
		m.PostRenderWriter.QueuePostRender(wrapSync(buf))
	}
}

const shotPreviewImageInset = 2

func (m *OS) clearScreenshotGraphics() {
	placed := m.shotImagePlaced
	m.shotImagePlaced = false
	m.shotImageSent = false
	m.shotPlacement = screenshotPlacementState{}
	if !placed || m.PostRenderWriter == nil {
		return
	}
	buf := appendKittyUnplace(nil, screenshotImageID, screenshotPlacement)
	m.PostRenderWriter.QueuePostRender(wrapSync(buf))
}

func openInOSViewer(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func fitBoxToPicture(cols, rows, pixelW, pixelH, cellW, cellH int) (int, int) {
	if pixelW <= 0 || pixelH <= 0 || cellW <= 0 || cellH <= 0 || cols <= 0 || rows <= 0 {
		return cols, rows
	}
	boxW, boxH := cols*cellW, rows*cellH
	if pixelW*boxH < pixelH*boxW {
		return clampInt(roundDiv(pixelW*boxH/pixelH, cellW), 1, cols), rows
	}
	return cols, clampInt(roundDiv(pixelH*boxW/pixelW, cellH), 1, rows)
}
