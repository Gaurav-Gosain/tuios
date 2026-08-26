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

// screenshotPreviewPixelBudget is the largest the preview picture ever needs to
// be, in pixels: the whole panel body, doubled.
//
// The doubling is hidpi headroom, not vanity. kitty scales a placement into the
// cell box it is given, and a picture at exactly the box's pixel size has
// nothing left to give when the box turns out a cell or two larger than this
// guess. Two times the body is a few hundred kilobytes at most, against the two
// to three megabytes the file itself is.
func (m *OS) screenshotPreviewPixelBudget() (maxW, maxH int) {
	cellW, cellH := hostCellSize()
	if cellW <= 0 || cellH <= 0 {
		return 0, 0
	}
	bodyCols, bodyRows := m.screenshotPreviewBody()
	return max(1, bodyCols*cellW*2), max(1, bodyRows*cellH*2)
}

// buildScreenshotTransmit wraps a preview picture in the chunked upload
// escapes. It runs in the render command, so the flush that has to happen
// between two frames only copies bytes.
func buildScreenshotTransmit(png []byte) []byte {
	if len(png) == 0 {
		return nil
	}
	return appendKittyTransmitPNG(nil, screenshotImageID, png)
}

// screenshotPreviewPictureBox is where the preview's picture goes inside the
// panel body: how many columns to leave to the left of it, the cell box it is
// drawn into, and whether there is a picture to draw at all.
//
// The renderer and the graphics flush both ask this. The renderer needs it to
// know which body rows the picture will cover, and the flush needs it to place
// the picture; if the two worked it out separately they could disagree and the
// panel would show cells and pixels of the same capture side by side.
//
// The picture keeps its own proportions, so it does not fill the body, and the
// slack has to go somewhere. It goes on both sides of the picture rather than
// all of it on the right. The panel cannot narrow to the picture, because the
// header, the file path and the footer all need the panel's width and a
// thirteen-column panel would cut every one of them; so the margin exists
// whatever is done with it, and a margin split in two reads as a frame while a
// margin all on one side reads as a picture that missed. The rows are the other
// way round: nothing else needs them, so a picture shorter than the body ends
// the body (see the renderer), and there is no vertical slack to place.
func (m *OS) screenshotPreviewPictureBox() (inset, cols, rows int, ok bool) {
	if !m.ShotPreview.Open || len(m.ShotPreview.PNG) == 0 || !m.screenshotGraphicsReady() {
		return 0, 0, 0, false
	}
	cellW, cellH := hostCellSize()
	bodyCols, bodyRows := m.screenshotPreviewBody()
	cols, rows = fitBoxToPicture(bodyCols, bodyRows, m.ShotPreview.PixelW, m.ShotPreview.PixelH, cellW, cellH)
	// The margin is measured across the panel, not across the body. The body
	// starts shotPreviewImageInset cells in from the panel and runs to the
	// panel's edge, so splitting the body's spare columns in two leaves the
	// gutter on one side only and the picture sits two cells right of centre.
	return max(0, (bodyCols-cols-shotPreviewImageInset)/2), cols, rows, true
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
	// A resize does not leave the picture alone. The host repaints its whole
	// screen at the new size, and what it holds under this id afterwards is not
	// what was put there: driven for real through a kitty that was resized
	// under an open panel, the picture came back as a two-column black strip
	// down the side of the body and stayed that way through every later resize.
	// So a resize is treated as the host no longer having the picture, and the
	// next flush uploads it again. It costs one upload of a preview-sized PNG
	// per resize, which is a hundred kilobytes at a human's pace, against a
	// panel that is ruined by touching the window.
	if m.shotPlacement.hostW != want.hostW || m.shotPlacement.hostH != want.hostH {
		m.shotImageSent = false
	}

	var buf []byte
	if m.shotImagePlaced {
		buf = appendKittyUnplace(buf, screenshotImageID, screenshotPlacement)
	}
	// The host holds one picture under this id. It is this capture's only if
	// the last upload was this capture's, so that is what the question is.
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
	// Whichever axis runs out first sets the scale. The reduced axis is rounded
	// to the nearest cell rather than floored, which was the last of the aspect
	// faults: that axis is the small one by definition, so half a cell of it is
	// a large fraction. A wide, short region letterboxed to 2.775 rows floored
	// to 2, drawing the picture twenty-eight percent squatter than it is;
	// rounding gives 3, which is eight percent the other way. The axis that is
	// not reduced loses nothing, and the axis that is cannot do better than half
	// a cell, because a cell is the smallest thing a placement is measured in.
	if pixelW*boxH < pixelH*boxW {
		return clampInt(roundDiv(pixelW*boxH/pixelH, cellW), 1, cols), rows
	}
	return cols, clampInt(roundDiv(pixelH*boxW/pixelW, cellH), 1, rows)
}
