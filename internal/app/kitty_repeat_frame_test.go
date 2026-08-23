package app

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A guest that streams through a file re-sends whether or not anything moved.
// A browser showing a page that has finished loading hands over a whole new
// bitmap several times a second, and half of those bitmaps are the picture
// already on the screen.
//
// Forwarding one of those costs the host a re-read of the file and a redraw of
// the whole image, because a re-transmitted image does not repaint the
// placement drawn from the old one and so has to be followed by a fresh a=p
// (see "place every frame of a stream, not just the first"). A still page was
// therefore redrawn several times a second for as long as it was open.
//
// The other two transmission paths have always dropped a repeat - the direct
// one by diffing the bitmap, the inline one by hashing it. This pins the same
// behaviour on the file path, and pins the other half of it too: a frame that
// does differ must still get through, or the pane freezes on the frame it
// started with, which is the fault that put the a=p there in the first place.

// fileFrameStream is a guest transmitting a frame by handing over a path, the
// way a browser or a video player does.
func fileFrameStream(imageID uint32, path string, w, h int) []byte {
	return fmt.Appendf(nil,
		"\x1b_Ga=T,i=%d,f=32,s=%d,v=%d,t=f,q=2,C=1;%s\x1b\\",
		imageID, w, h, base64.StdEncoding.EncodeToString([]byte(path)))
}

// writeFrame fills a file with a bitmap whose every byte derives from tint, so
// two frames with different tints differ everywhere and two with the same tint
// are byte for byte identical.
func writeFrame(t *testing.T, path string, w, h int, tint byte) {
	t.Helper()
	raw := make([]byte, w*h*4)
	for i := range raw {
		raw[i] = byte(i*7) ^ tint
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

// graphicsKinds says what a batch of forwarded commands contains.
func graphicsKinds(cmds []string) (transmits, placements int) {
	for _, c := range cmds {
		switch {
		case strings.HasPrefix(c, "a=t,"), strings.HasPrefix(c, "a=T,"):
			transmits++
		case strings.HasPrefix(c, "a=p,"):
			placements++
		}
	}
	return
}

func TestRepeatFileFrameIsNotForwarded(t *testing.T) {
	const (
		screenW, screenH = 121, 33
		imgW, imgH       = 400, 300
	)
	r := newPaneRig(t, screenW, screenH, 1)
	path := filepath.Join(t.TempDir(), "frame.rgba")

	// First frame: transmitted and placed. Nothing to compare it with, so it
	// has to go through.
	writeFrame(t, path, imgW, imgH, 1)
	tx, pl := graphicsKinds(r.frame(fileFrameStream(1, path, imgW, imgH)))
	if tx == 0 || pl == 0 {
		t.Fatalf("the first frame was not forwarded and placed (%d transmits, %d placements)", tx, pl)
	}

	// The guest hands over the same pixels again, three times over. The host is
	// already showing them.
	for i := range 3 {
		tx, pl := graphicsKinds(r.frame(fileFrameStream(1, path, imgW, imgH)))
		if tx != 0 || pl != 0 {
			t.Errorf("repeat %d: the host was sent a frame it is already showing "+
				"(%d transmits, %d placements); a re-transmit costs it a re-read of the "+
				"file and a redraw of the whole image", i+1, tx, pl)
		}
	}

	// The page changes. This one must get through, or the pane freezes.
	writeFrame(t, path, imgW, imgH, 2)
	tx, pl = graphicsKinds(r.frame(fileFrameStream(1, path, imgW, imgH)))
	if tx == 0 {
		t.Errorf("a frame with different pixels was dropped, which leaves the pane " +
			"showing the frame before it for ever")
	}
	if pl == 0 {
		t.Errorf("a frame with different pixels was transmitted without a placement; " +
			"the host does not repaint a placement whose image was replaced underneath it, " +
			"so the pane would keep showing the old one")
	}
}

// TestChangingFileStreamKeepsFlowing is the guard's own negative: a stream in
// which every frame differs must lose none of them. It is what stops the check
// from being written as "drop anything that looks similar", and it is the
// behaviour that would break first if the comparison were ever made cheaper by
// sampling part of a frame instead of all of it.
func TestChangingFileStreamKeepsFlowing(t *testing.T) {
	const (
		screenW, screenH = 121, 33
		imgW, imgH       = 200, 160
		frames           = 40
	)
	r := newPaneRig(t, screenW, screenH, 1)
	path := filepath.Join(t.TempDir(), "video.rgba")

	forwarded := 0
	for i := range frames {
		writeFrame(t, path, imgW, imgH, byte(i+1))
		tx, _ := graphicsKinds(r.frame(fileFrameStream(1, path, imgW, imgH)))
		forwarded += tx
	}
	if forwarded != frames {
		t.Errorf("%d of %d frames reached the host; a stream whose every frame differs "+
			"must lose none of them", forwarded, frames)
	}
}
