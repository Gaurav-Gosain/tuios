package tuie2e

import (
	"encoding/base64"
	"fmt"
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
			runInShell(t, term, "echo READY", "READY", shellTimeout)

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
		})
	}
}
