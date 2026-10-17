package tuie2e

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestKittyImageSurvivesCaptureMode is the launch clip's bug: a pane showing an
// image (the clip ran chafa -f kitty), then the leader chord into screenshot
// capture mode, and the image vanished. Capture mode was on the list of
// overlays that take every image off screen, but it covers nothing: it draws a
// hint strip and a marquee over the panes, and the panes are what it captures.
//
// On the wire the bug is a delete. So the host is watched from the moment the
// image is up: opening capture mode, and closing it again, must not delete the
// image, and the host must still be holding a placement for it at the end.
//
// It then takes the window from capture mode and reads the PNG: the image has
// to be in the picture, at its cells. A kitty image is drawn by the host, so
// it is in no cell the capture reads, and a screenshot without it shows the
// blank cells under it (issue 678).
//
// Negative control: making decodeShotPieces in internal/app return nil left
// the window's cells without the image in the PNG, and the check on its
// pixels failed. See NEGATIVE_CONTROLS.md.
func TestKittyImageSurvivesCaptureMode(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "daemon"}[daemon], func(t *testing.T) {
			host := newKittyHost()
			term, base := start(t, startOpts{
				cols: 120, rows: 40,
				env:           []string{"TUIOS_SIXEL_GRAPHICS=0", "TMPDIR=" + t.TempDir()},
				out:           host,
				daemonDefault: daemon,
			})
			if daemon {
				killDaemon(t, base)
			}
			host.answerProbe(t, term)
			waitBoot(t, term)
			newWindow(t, term)
			enterTerminalMode(t, term)
			runInShell(t, term, "echo RE\"\"ADY", "READY", shellTimeout)

			// Transmit and display in one command, the way chafa does.
			_, img := writeIcatPNG(t, t.TempDir())
			enc := base64.StdEncoding.EncodeToString(img)
			host.mark("image")
			runToExit(t, term, fmt.Sprintf(`printf '\033_Ga=T,q=2,f=100,s=%d,v=%d,c=10,r=5;%s\033\\'`,
				icatImageW, icatImageH, enc))

			hostID := 0
			deadline := time.Now().Add(uiTimeout)
			for hostID == 0 && time.Now().Before(deadline) {
				for _, c := range wireCmds(host.bytes()) {
					if c.phase == "image" && c.action == "p" && c.image != 0 {
						hostID = c.image
					}
				}
				time.Sleep(100 * time.Millisecond)
			}
			if hostID == 0 {
				t.Fatalf("the image was never placed on the host\n%s", term.Snapshot())
			}

			host.mark("capture")
			if err := term.SendKeys(tuitest.Ctrl('b'), "C"); err != nil {
				t.Fatalf("send leader+C: %v", err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return screenHas(s, "capture window", "cancel")
			}, uiTimeout); err != nil {
				t.Fatalf("capture mode never opened: %v\n%s", err, term.Snapshot())
			}
			// Frames keep coming while the mode is open; give a hide the
			// chance to happen.
			time.Sleep(time.Second)

			host.mark("closed")
			if err := term.SendKeys(tuitest.Esc); err != nil {
				t.Fatalf("send esc: %v", err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return !screenHas(s, "capture window")
			}, uiTimeout); err != nil {
				t.Fatalf("capture mode never closed: %v\n%s", err, term.Snapshot())
			}
			time.Sleep(time.Second)

			for _, c := range wireCmds(host.bytes()) {
				if (c.phase == "capture" || c.phase == "closed") && c.action == "d" && c.image == hostID {
					t.Fatalf("the image was deleted in phase %q: %s", c.phase, c.params)
				}
			}

			// Take the window and look for the image in the picture.
			host.mark("shot")
			if err := term.SendKeys(tuitest.Ctrl('b'), "C"); err != nil {
				t.Fatalf("send leader+C: %v", err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return screenHas(s, "capture window", "cancel")
			}, uiTimeout); err != nil {
				t.Fatalf("capture mode never opened again: %v\n%s", err, term.Snapshot())
			}
			if err := term.SendKeys(tuitest.Enter); err != nil {
				t.Fatalf("send enter: %v", err)
			}
			var shotPath string
			deadline = time.Now().Add(uiTimeout)
			for shotPath == "" && time.Now().Before(deadline) {
				shotPath = findCapturePNG(base)
				if shotPath == "" {
					time.Sleep(100 * time.Millisecond)
				}
			}
			if shotPath == "" {
				t.Fatalf("no capture was written under %s\n%s", base, term.Snapshot())
			}
			checkIcatInPicture(t, shotPath)
		})
	}
}

// findCapturePNG returns the first finished PNG capture under base, or "".
func findCapturePNG(base string) string {
	var found string
	_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found != "" {
			return nil
		}
		name := d.Name()
		if !d.IsDir() && strings.HasPrefix(name, "tuios-") && strings.HasSuffix(name, ".png") {
			found = path
		}
		return nil
	})
	return found
}

// checkIcatInPicture checks that the test image is drawn in a capture. Its
// pixels are (7x, 11y, 99): the blue channel is 99 everywhere and the red one
// rises from left to right, which neither the theme nor any glyph draws. The
// green rises from top to bottom, and the rows with some green are counted.
// The
// image fills 10 by 5 cells of a window about 118 cells wide, so it is a few
// percent of the picture, and the cells under it are blank.
func checkIcatInPicture(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	b := img.Bounds()
	var n int
	var leftR, rightR, leftN, rightN float64
	type px struct{ x, r int }
	var hits []px
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			r8, g8, b8 := int(r>>8), int(g>>8), int(bl>>8)
			// The frame's wash crosses blue 99 too, but it has almost no
			// green. The image's green is 11y, so most of its rows pass.
			if b8 >= 96 && b8 <= 102 && g8 >= 40 {
				n++
				hits = append(hits, px{x, r8})
			}
		}
	}
	// A cell is at least 10 by 20 pixels at the default scale, so the image
	// is at least 100 by 100. Half of that is far more than stray pixels of
	// antialiased text could make.
	if n < 5000 {
		t.Fatalf("the capture %s has %d pixels of the image, want at least 5000: the image is not in the picture", path, n)
	}
	// Split at the mean column of the matches: the image holds nearly all of
	// them, and the few stray pixels of antialiased text barely move it.
	var sumX int
	for _, h := range hits {
		sumX += h.x
	}
	mid := sumX / len(hits)
	for _, h := range hits {
		if h.x < mid {
			leftR += float64(h.r)
			leftN++
		} else {
			rightR += float64(h.r)
			rightN++
		}
	}
	if leftN == 0 || rightN == 0 || leftR/leftN >= rightR/rightN {
		t.Fatalf("the image in %s does not rise in red from left to right (left %.0f, right %.0f)",
			path, leftR/max(leftN, 1), rightR/max(rightN, 1))
	}
}
