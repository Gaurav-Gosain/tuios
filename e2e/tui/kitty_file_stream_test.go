package tuie2e

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// fileFrameKeys matches the control keys of a kitty command that names a file
// (t=f), so a test can tell a frame sent with its placement (a=T) from one sent
// to wait for a placement (a=t).
var fileFrameKeys = regexp.MustCompile("\x1b_G([^;\x1b]*t=f[^;\x1b]*);")

// TestAFileStreamIsPlacedWithEachFrame: a guest that streams frames through a
// ring of t=f files under one image id, as srterm does, has each frame sent to
// the host as one a=T that places it. The host reads each file once and draws
// it.
//
// A frame used to go out as a=t, and the refresh pass placed it with an a=p on
// the next pass. Two frames that came between passes were both read by the
// host, and only the second was drawn.
//
// Negative control: with t=f kept off the path that places a frame at once
// (kittyMediumIsOneFrame on main), the host gets a=t and a=p for every frame,
// and the test fails.
func TestAFileStreamIsPlacedWithEachFrame(t *testing.T) {
	term, host := startGraphicsPane(t, true)
	bin := buildFrameloop(t)
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	typeLine(t, term, fmt.Sprintf("%s %s 60 0 file %s", bin, filepath.Join(dir, "geom"), count))
	waitFrames := func(n int) {
		t.Helper()
		deadline := time.Now().Add(shellTimeout)
		for guestFrames(count) < n {
			if time.Now().After(deadline) {
				t.Fatalf("the guest wrote %d frames, want %d\n%s", guestFrames(count), n, term.Snapshot())
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	// The first frames are placed by the refresh pass, which is what makes
	// the placement a later frame can go out with.
	waitFrames(20)
	host.mark("steady")
	from := guestFrames(count)
	waitFrames(from + 60)
	// The client sends the last frames it has.
	time.Sleep(gestureGap)
	saveFrame(t, term, "file-stream")

	stream := host.bytes()
	if i := bytes.Index(stream, []byte(phaseMark+"steady")); i >= 0 {
		stream = stream[i:]
	}
	placedWith, sentAlone := 0, 0
	for _, m := range fileFrameKeys.FindAllSubmatch(stream, -1) {
		switch {
		case bytes.HasPrefix(m[1], []byte("a=T,")):
			placedWith++
		case bytes.HasPrefix(m[1], []byte("a=t,")):
			sentAlone++
		}
	}
	placements := bytes.Count(stream, []byte("\x1b_Ga=p,"))
	t.Logf("after the first %d frames: %d frames with their placement, %d frames alone, %d placements on their own", from, placedWith, sentAlone, placements)
	if placedWith < 30 {
		t.Fatalf("ASSERTION: %d of 60 frames reached the host with their placement, want most of them (%d sent alone, %d placements on their own)", placedWith, sentAlone, placements)
	}
	if sentAlone != 0 || placements != 0 {
		t.Fatalf("ASSERTION: %d frames went to the host to wait for a placement and %d placements came on their own; each frame of a stream in place goes as one a=T", sentAlone, placements)
	}
	alive(t, term, "after the file stream")
}
