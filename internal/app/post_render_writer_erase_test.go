//go:build unix

package app

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
)

// TestAnErasingFrameCarriesItsPictures holds the order the end-to-end suite
// cannot force. When the View for a resize's last message runs before the
// renderer writes its ED 2, that View has already seen the picture as placed
// and no later View comes. So the frame that erases has to carry the picture
// itself: its upload and its placement, behind the erase, in one write.
func TestAnErasingFrameCarriesItsPictures(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	prw := NewPostRenderWriter(w)
	key := previewKey{cols: 4, rows: 2, mode: composer.PreviewPixels}
	other := previewKey{cols: 9, rows: 9, mode: composer.PreviewPixels}
	p := &replyPreview{
		pics:      map[previewKey]*previewPicture{key: {png: []byte("picture")}, other: {png: []byte("other")}},
		hostID:    map[previewKey]uint32{key: 7, other: 8},
		hostOrder: []previewKey{key, other},
		shown:     map[previewID]previewPlacement{{img: 7, placement: 1}: {key: key, x: 3, y: 4, cols: 4, rows: 2}},
	}
	prw.AddEraseHook(p.hostErased)

	if _, err := prw.Write([]byte("no erase")); err != nil {
		t.Fatal(err)
	}
	if _, err := prw.Write([]byte("\x1b[H\x1b[2Jtext")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if strings.Count(s, "a=t,") != 1 || strings.Count(s, "a=p,i=7,p=1,") != 1 {
		t.Fatalf("the two frames carried no single upload and placement: %q", s)
	}
	erase, upload, place := strings.Index(s, "\x1b[2J"), strings.Index(s, "a=t,"), strings.Index(s, "a=p,")
	if erase >= upload || upload >= place {
		t.Errorf("want the erase, the upload, then the placement, at %d, %d, %d: %q", erase, upload, place, s)
	}
	if id, up := p.hostID[other]; up {
		t.Errorf("the picture off screen still counts as uploaded under %d: the host dropped it", id)
	}
	if id := p.hostID[key]; id != 7 {
		t.Errorf("the picture on screen is under %d, want 7", id)
	}

	// A frame rewritten for a kitty host leaves the pictures on the host: only
	// the placement goes out again, and nothing counts as lost.
	kept := string(p.hostErased(true))
	if strings.Contains(kept, "a=t,") || strings.Count(kept, "a=p,i=7,p=1,") != 1 {
		t.Errorf("a kept frame sent %q, want one placement and no upload", kept)
	}
	if _, up := p.hostID[key]; !up {
		t.Error("a kept frame forgot the picture on screen")
	}
	out, erased := keepPicturesFrame([]byte("a\x1b[H\x1b[2Jb"))
	if !erased || strings.Contains(string(out), "\x1b[2J") || !strings.Contains(string(out), "\x1b[H\x1b[Jb") {
		t.Errorf("keepPicturesFrame gave %q, erased %v", out, erased)
	}
}
