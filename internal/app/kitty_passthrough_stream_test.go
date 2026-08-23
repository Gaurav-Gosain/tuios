package app

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// streamFileFrame writes one transmit-and-place frame naming a file, which is
// what a shared-memory or file stream sends for every frame it draws: the same
// image id, new pixels behind it, a placement asked for each time.
func streamFileFrame(em *vt.Emulator, id, w, h int, path string) {
	enc := base64.StdEncoding.EncodeToString([]byte(path))
	_, _ = em.Write(fmt.Appendf(nil,
		"\x1b_Ga=T,f=32,s=%d,v=%d,t=f,i=%d,p=1,C=1,q=2;%s\x1b\\", w, h, id, enc))
}

// TestStreamedFileFrameIsPlacedEveryFrame is the regression that froze a
// compositor running in a pane on its first paint.
//
// Transmitting an image id the host already holds replaces the stored image,
// and the placement drawn from the old pixels does not follow it. A stream that
// is only placed once therefore shows its first frame forever while every frame
// after it is received, stored, and never drawn -- which is what tuios did: 210
// frames forwarded, two placements sent, one picture on screen.
func TestStreamedFileFrameIsPlacedEveryFrame(t *testing.T) {
	_, em, _, refresh := placementHarness(t, 100, 40, 1)
	refresh() // let the harness's own frame settle

	if idle := countCmd(refresh(), "a=p,"); idle != 0 {
		t.Fatalf("a refresh with nothing new sent %d placements, want none", idle)
	}

	imgW, imgH := (100-2)*10, (40-2)*20
	path := filepath.Join(t.TempDir(), "frame")

	for frame := 1; frame <= 4; frame++ {
		// New pixels behind the same id, which is what a stream sends and what
		// the placement has to follow. Re-advertising the same bytes is a
		// different thing entirely - a frame the host is already showing - and
		// is covered by TestRepeatFileFrameIsNotForwarded.
		if err := os.WriteFile(path, []byte{byte(frame)}, 0o600); err != nil {
			t.Fatal(err)
		}
		streamFileFrame(em, 2, imgW, imgH, path)
		if placed := countCmd(refresh(), "a=p,"); placed == 0 {
			t.Errorf("frame %d was transmitted and never placed; the pane still shows frame %d",
				frame, frame-1)
		}
	}
}
