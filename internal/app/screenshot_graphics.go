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
	w, h := hostCellSize()
	return w > 0 && h > 0
}

// hostCellSize is one host cell in pixels.
//
// It exists because iconCellSize is not it: that is the launcher's icon box,
// which is two cells wide by one tall, and reading it as a cell told the
// preview every cell was twice as wide as it is. The picture was then placed
// into a box half the width it needed and drawn squeezed to fit, which is the
// "horizontally stretched" report. Anything that has to reason in pixels about
// a cell asks this.
func hostCellSize() (w, h int) {
	caps := GetHostCapabilities()
	if caps.CellWidth <= 0 || caps.CellHeight <= 0 {
		return 0, 0
	}
	return caps.CellWidth, caps.CellHeight
}

// screenshotPreviewPictureBox is the cell box the preview's picture is drawn
// into, and whether there is a picture to draw at all.
//
// The renderer and the graphics flush both ask this. The renderer needs it to
// know which body rows the picture will cover, and the flush needs it to place
// the picture; if the two worked it out separately they could disagree and the
// panel would show cells and pixels of the same capture side by side.
func (m *OS) screenshotPreviewPictureBox() (cols, rows int, ok bool) {
	if !m.ShotPreview.Open || len(m.ShotPreview.PNG) == 0 || !m.screenshotGraphicsReady() {
		return 0, 0, false
	}
	cellW, cellH := hostCellSize()
	bodyCols, bodyRows := m.screenshotPreviewBody()
	cols, rows = fitBoxToPicture(bodyCols, bodyRows, m.ShotPreview.PixelW, m.ShotPreview.PixelH, cellW, cellH)
	return cols, rows, true
}

// flushScreenshotGraphicsForFrame puts the preview picture on the host for the
// frame just drawn, or takes it down when the panel is not up.
//
// The panel blanks the rows this is about to draw over, so the two tiers do not
// show at once; see blankPictureRows. Both sides read the box from
// screenshotPreviewPictureBox, so they cannot disagree about which rows those
// are.
//
// Where the panel landed is only known after it is placed as a layer, so the
// origin comes from the hit geometry that placement recorded. If the panel was
// not placed this frame there is nowhere to put the picture, and nothing is
// drawn rather than something drawn in the wrong place.
func (m *OS) flushScreenshotGraphicsForFrame() {
	cols, rows, ok := m.screenshotPreviewPictureBox()
	if !ok {
		m.clearScreenshotGraphics()
		return
	}
	h, hit := m.overlayHitByKind(overlayKindShot)
	if !hit {
		return
	}
	x := h.OriginX + h.Geo.BodyX + shotPreviewImageInset
	y := h.OriginY + h.Geo.BodyY + m.shotPreviewImageRow()
	want := screenshotPlacementState{
		x: x, y: y, cols: cols, rows: rows, capture: m.ShotPreview.Capture,
	}
	if m.shotImagePlaced && m.shotPlacement == want {
		return
	}

	var buf []byte
	if m.shotImagePlaced {
		buf = appendKittyUnplace(buf, screenshotImageID, screenshotPlacement)
	}
	// The host holds one picture under this id. It is this capture's only if
	// the last upload was this capture's, so that is what the question is.
	if m.shotPlacement.capture != want.capture || !m.shotImageSent {
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
//
// The bookkeeping is reset whether or not anything was placed. Guarding the
// whole function on shotImagePlaced was the trap: a preview that uploaded a
// picture and then never placed it left shotImageSent standing, and the next
// capture read that as "the host already holds my picture".
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

// fitBoxToPicture shrinks a cell box until the picture drawn into it keeps its
// own proportions.
//
// a=p scales the image to whatever box it is given, so handing it the whole
// panel body stretched a wide capture tall and a tall one wide. The box is
// reduced on one axis instead, which letterboxes rather than distorts. A
// picture whose size is unknown gets the box unchanged, which is the old
// behaviour and no worse than it was.
//
// The cell size is an argument rather than something this reads for itself.
// That is the whole bug the second time round: it read the launcher's icon box,
// which is two cells wide, so every sum here was done in units twice the width
// of a cell and the answer came out half as wide as it should be. A caller now
// has to say which pixels it means.
func fitBoxToPicture(cols, rows, pixelW, pixelH, cellW, cellH int) (int, int) {
	if pixelW <= 0 || pixelH <= 0 || cellW <= 0 || cellH <= 0 || cols <= 0 || rows <= 0 {
		return cols, rows
	}
	boxW, boxH := cols*cellW, rows*cellH
	// Whichever axis runs out first sets the scale.
	if pixelW*boxH < pixelH*boxW {
		boxW = pixelW * boxH / pixelH
	} else {
		boxH = pixelH * boxW / pixelW
	}
	// Only one axis is ever reduced and never below a cell, so the result
	// cannot outgrow the box it was offered and needs no clamp against it.
	return max(1, boxW/cellW), max(1, boxH/cellH)
}
