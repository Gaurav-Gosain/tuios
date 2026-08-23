package app

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

// A kitty placement carries two rectangles that have to describe the same
// picture: x,y,w,h select a region of the image, and c,r give the cell box to
// draw it into. A region smaller than its box is magnified to fill it, by
// exactly the ratio between them.
//
// Working out that region needs one number the placement command does not
// carry: how many of the image's pixels one cell is worth. That comes from the
// transmission - a=p says which image and how many cells and nothing about how
// big the image is - and the placement record built for a guest's own a=p did
// not keep it. With nothing to divide, the refresh pass fell back to the host's
// cell size, which is the right answer only for a guest that draws exactly one
// cell's worth of pixels per cell.
//
// A browser does not. Chromium renders at its device pixel ratio, so a pane of
// 119x40 cells is a bitmap of twice that many pixels on each axis, and the
// region asked for was then half the width and half the height of the one that
// belonged in the box: the top-left quarter of the page, drawn across the whole
// pane at twice its size. Which is the report - the same page, legible, several
// times too big.
//
// The first placement is fine, because it is the guest's own a=p forwarded on
// with the guest's own numbers and no region at all. The wrong one is the
// refresh pass's, and the refresh pass only re-places when something changes
// while the render loop is running - which is why the pane was reported to blow
// up not when the image arrived but when a neighbouring pane started printing.

// transmitThenPlace is the efficient shape a long-lived graphics client uses:
// send the bitmap once under an id, then place that id, and keep placing it
// without ever re-sending the pixels.
func transmitThenPlace(imageID uint32, pixelW, pixelH, cols, rows int) []byte {
	raw := make([]byte, pixelW*pixelH*4)
	for i := range raw {
		raw[i] = byte(i * 7)
	}
	encoded := base64.StdEncoding.EncodeToString(raw)

	var b strings.Builder
	b.WriteString("\x1b[H")
	first := true
	for len(encoded) > 0 {
		chunk := encoded
		if len(chunk) > 4096 {
			chunk = chunk[:4096]
		}
		encoded = encoded[len(chunk):]
		more := 0
		if len(encoded) > 0 {
			more = 1
		}
		if first {
			fmt.Fprintf(&b, "\x1b_Ga=t,i=%d,f=32,s=%d,v=%d,t=d,q=2,m=%d;",
				imageID, pixelW, pixelH, more)
			first = false
		} else {
			fmt.Fprintf(&b, "\x1b_Gm=%d;", more)
		}
		b.WriteString(chunk)
		b.WriteString("\x1b\\")
	}
	fmt.Fprintf(&b, "\x1b_Ga=p,i=%d,p=1,c=%d,r=%d,C=1,q=2\x1b\\", imageID, cols, rows)
	return []byte(b.String())
}

// placementRegion is the source region and cell box of a placement command.
func placementRegion(cmd string) (srcW, srcH, cols, rows int) {
	for _, part := range strings.Split(cmd, ",") {
		var k string
		var v int
		if n, _ := fmt.Sscanf(part, "%1s=%d", &k, &v); n != 2 {
			continue
		}
		switch k {
		case "w":
			srcW = v
		case "h":
			srcH = v
		case "c":
			cols = v
		case "r":
			rows = v
		}
	}
	return
}

// TestPlacedImageIsNotMagnified pins the invariant on the placement the refresh
// pass emits: the region of the image it asks for must be the region that
// belongs in the cell box it asks for.
//
// The pane is 119x31 cells at 10x20 px. The guest occupies 119x40 cells, so the
// pane shows all of its width and 31 of its 40 rows. Whatever the guest's own
// pixel scale, that is the region: the full width, and 31/40 of the height.
func TestPlacedImageIsNotMagnified(t *testing.T) {
	const (
		screenW, screenH = 121, 33 // border 1 -> 119x31 content cells
		cellW, cellH     = 10, 20  // what the host reports, as newPaneRig sets it
		guestCols        = 119
		guestRows        = 40
		paneRows         = 31
	)

	cases := []struct {
		name           string
		pixelW, pixelH int
	}{
		// One cell's worth of pixels per cell: the case where the host's cell
		// size happens to be the right divisor, so the fallback was correct and
		// this must stay correct too.
		{"same scale as the host", guestCols * cellW, guestRows * cellH},
		// Twice that, which is what a browser at a device pixel ratio of 2
		// draws, and where the fallback was wrong by a factor of two.
		{"twice the host's scale", guestCols * cellW * 2, guestRows * cellH * 2},
		// An awkward ratio, so a test that passes cannot be passing because the
		// numbers divide neatly.
		{"three halves the host's scale", guestCols * cellW * 3 / 2, guestRows * cellH * 3 / 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newPaneRig(t, screenW, screenH, 1)
			// The normal screen: an image placed on the alternate screen is a
			// separate fault, covered below.
			_, _ = r.em.Write([]byte("\x1b[?1049l"))

			var refreshed string
			for _, cmd := range r.frame(transmitThenPlace(1, tc.pixelW, tc.pixelH, guestCols, guestRows)) {
				if strings.HasPrefix(cmd, "a=p,") && strings.Contains(cmd, ",w=") {
					refreshed = cmd
				}
			}
			if refreshed == "" {
				t.Fatalf("the refresh pass never re-placed the image, so the command " +
					"this is about was never emitted")
			}

			srcW, srcH, cols, rows := placementRegion(refreshed)
			// The image is guestCols x guestRows cells of its own pixels.
			perCol := tc.pixelW / guestCols
			perRow := tc.pixelH / guestRows
			wantW := cols * perCol
			wantH := rows * perRow
			if cols != guestCols || rows != paneRows {
				t.Fatalf("the cell box is not the pane: got c=%d,r=%d, want c=%d,r=%d in %q",
					cols, rows, guestCols, paneRows, refreshed)
			}
			if srcW != wantW || srcH != wantH {
				t.Errorf("the region asked for is not the region that belongs in the box:\n"+
					"  image     %dx%d px in %dx%d cells (%dx%d px per cell)\n"+
					"  cell box  %d x %d cells = %d x %d px\n"+
					"  asked for %d x %d px  (want %d x %d px)\n"+
					"  magnifies by %.2fx across and %.2fx down\n"+
					"  command   %q",
					tc.pixelW, tc.pixelH, guestCols, guestRows, perCol, perRow,
					cols, rows, cols*cellW, rows*cellH,
					srcW, srcH, wantW, wantH,
					float64(wantW)/float64(max(srcW, 1)), float64(wantH)/float64(max(srcH, 1)),
					refreshed)
			}
		})
	}
}

// TestPlacedImageOnAltScreenSurvivesARefresh is the other half of the same
// omission. The record built for a guest's a=p did not say which screen it was
// made on, so a placement made on the alternate screen looked to the refresh
// pass like one made on the normal screen, and the mismatch deleted it on the
// very next pass: a graphics app that transmits and then places lost its image
// immediately, leaving the pane blank.
func TestPlacedImageOnAltScreenSurvivesARefresh(t *testing.T) {
	const screenW, screenH = 121, 33
	r := newPaneRig(t, screenW, screenH, 1) // newPaneRig takes the alternate screen

	var deleted bool
	for _, cmd := range r.frame(transmitThenPlace(1, 1190, 800, 119, 40)) {
		if strings.HasPrefix(cmd, "a=d,") {
			deleted = true
		}
	}
	if deleted {
		t.Errorf("the image was deleted on the first refresh after being placed on the " +
			"alternate screen, which leaves the pane blank")
	}
}
