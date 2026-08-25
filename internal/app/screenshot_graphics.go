//go:build unix

package app

import (
	"os/exec"
	"runtime"
)

// The preview's pixel tier.
//
// Kitty graphics buy exactly one thing here: seeing the pixel frame, the wash
// and the shadow before leaving the terminal. Everything else the panel does
// is text, works on a plain xterm, and is not a fallback: the cells in the
// body are the same cells the content was drawn from.
//
// The transmit and place helpers are the launcher's, generalised. The preview
// takes its own reserved image id rather than sharing the launcher's counter,
// so a panel that is open at the same time cannot delete the other's picture.
//
// Sixel gets no pixel tier. Sixel has no way to delete a placement, which is
// why the launcher icons already skip it, and a preview panel that can be
// dragged and closed needs exactly that.

// screenshotImageID is the reserved kitty image id for the preview. The
// launcher's icons count up from 0xF000_0000, so this sits sixteen million ids
// above the base it starts at: the two cannot meet without a session drawing
// that many distinct icons, and neither can meet a pane's own images, which a
// guest is handed counting up from 1.
const screenshotImageID = 0xF100_0000

// screenshotPlacement is the placement id under that image.
const screenshotPlacement = 1

// screenshotGraphicsReady reports whether this host can show the pixel tier.
func (m *OS) screenshotGraphicsReady() bool {
	if m.PostRenderWriter == nil {
		return false
	}
	if !GetHostCapabilities().KittyGraphics {
		return false
	}
	w, h := iconCellSize()
	return w > 0 && h > 0
}

// flushScreenshotGraphicsForFrame puts the preview picture on the host for the
// frame just drawn, or takes it down when the panel is not up.
//
// The picture is laid over the cells the text tier already drew, rather than
// over reserved blanks the way the launcher's icons are. That is deliberate: a
// host that claims kitty graphics and does not deliver would otherwise leave
// an empty panel, and the text tier is complete on its own.
//
// Where the panel landed is only known after it is placed as a layer, so the
// origin comes from the hit geometry that placement recorded. If the panel was
// not placed this frame there is nowhere to put the picture, and nothing is
// drawn rather than something drawn in the wrong place.
func (m *OS) flushScreenshotGraphicsForFrame() {
	if !m.ShotPreview.Open || len(m.ShotPreview.PNG) == 0 || !m.screenshotGraphicsReady() {
		m.clearScreenshotGraphics()
		return
	}
	h, ok := m.overlayHitByKind(overlayKindShot)
	if !ok {
		return
	}
	cols, rows := m.screenshotPreviewBody()
	x := h.OriginX + h.Geo.BodyX + shotPreviewImageInset
	y := h.OriginY + h.Geo.BodyY + m.shotPreviewImageRow()
	want := screenshotPlacementState{x: x, y: y, cols: cols, rows: rows, path: m.ShotPreview.Path}
	if m.shotImagePlaced && m.shotPlacement == want {
		return
	}

	var buf []byte
	if m.shotImagePlaced {
		buf = appendKittyUnplace(buf, screenshotImageID, screenshotPlacement)
	}
	if m.shotPlacement.path != want.path || !m.shotImageSent {
		buf = appendKittyTransmitPNG(buf, screenshotImageID, m.ShotPreview.PNG)
		m.shotImageSent = true
	}
	buf = appendKittyPlaceBox(buf, screenshotImageID, screenshotPlacement, x, y, cols, rows)
	m.shotImagePlaced = true
	m.shotPlacement = want
	if m.PostRenderWriter != nil {
		m.PostRenderWriter.QueuePostRender(wrapSync(buf))
	}
}

// shotPreviewImageInset is the body gutter the text tier indents by, so the
// picture lands on the same column the cells would have.
const shotPreviewImageInset = 2

// clearScreenshotGraphics takes down the preview placement. A drawn image
// outlives the panel that placed it, so closing the panel has to say so.
func (m *OS) clearScreenshotGraphics() {
	if !m.shotImagePlaced {
		return
	}
	m.shotImagePlaced = false
	m.shotImageSent = false
	m.shotPlacement = screenshotPlacementState{}
	if m.PostRenderWriter == nil {
		return
	}
	buf := appendKittyUnplace(nil, screenshotImageID, screenshotPlacement)
	m.PostRenderWriter.QueuePostRender(wrapSync(buf))
}

// openInOSViewer hands a path to the desktop's own file handler. It is only
// ever called from a local client; on a remote one the viewer would open on
// the server, in front of nobody.
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
	// The viewer outlives this call; nothing waits on it and nothing reads
	// back from it.
	go func() { _ = cmd.Wait() }()
	return nil
}
