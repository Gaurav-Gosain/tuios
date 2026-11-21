package app

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fileFrameStream(imageID uint32, path string, w, h int) []byte {
	return fmt.Appendf(nil,
		"\x1b_Ga=T,i=%d,f=32,s=%d,v=%d,t=f,q=2,C=1;%s\x1b\\",
		imageID, w, h, base64.StdEncoding.EncodeToString([]byte(path)))
}

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

	writeFrame(t, path, imgW, imgH, 1)
	tx, pl := graphicsKinds(r.frame(fileFrameStream(1, path, imgW, imgH)))
	if tx == 0 || pl == 0 {
		t.Fatalf("the first frame was not forwarded and placed (%d transmits, %d placements)", tx, pl)
	}

	for i := range 3 {
		tx, pl := graphicsKinds(r.frame(fileFrameStream(1, path, imgW, imgH)))
		if tx != 0 || pl != 0 {
			t.Errorf("repeat %d: the host was sent a frame it is already showing "+
				"(%d transmits, %d placements); a re-transmit costs it a re-read of the "+
				"file and a redraw of the whole image", i+1, tx, pl)
		}
	}

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
