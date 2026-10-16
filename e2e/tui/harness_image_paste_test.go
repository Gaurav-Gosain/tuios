package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHarnessTakesAPastedImagePath measures what a real agent harness does
// with the path of an image pasted on its own, which is how a queued reply
// hands over an image for a harness whose [input] images is path_paste. It
// runs the harness in a pane, waits for its input box, pastes the path with
// paste-buffer (bracketed, as the queue types it), and reads the screen.
// Nothing is submitted, so no model is asked anything.
//
// It is opt-in, because it runs the harness installed on this machine with
// the user's own settings: TUIOS_E2E_HARNESSES lists the ones to run, such as
// "opencode crush pi". A harness not installed is skipped. CI runs none.
//
// What each one showed is in its manifest's images_source:
//
//   - opencode 1.19.3: the path became [Image 1] in the input box.
//   - crush v0.98.1: the path became an attachment, shot.png, over the input.
//   - pi 0.73.1: the path stayed text in the input box, which is path_text.
func TestHarnessTakesAPastedImagePath(t *testing.T) {
	list := strings.Fields(os.Getenv("TUIOS_E2E_HARNESSES"))
	if len(list) == 0 {
		t.Skip("TUIOS_E2E_HARNESSES is not set: this runs real agent harnesses")
	}
	// The mark each harness draws for an attached image, and none for a
	// harness that keeps the path as text.
	attached := map[string]string{"opencode": "[Image 1]", "crush": "shot.png  "}
	for _, h := range list {
		t.Run(h, func(t *testing.T) {
			bin, err := exec.LookPath(h)
			if err != nil {
				t.Skip(h + " is not installed")
			}
			base := t.TempDir()
			killDaemon(t, base)
			if out, err := tuiosCLI(t, base, "new", "h", "--detach"); err != nil {
				t.Fatalf("create the session: %v\n%s", err, out)
			}
			img := filepath.Join(workDirIn(t, base), "shot.png")
			if err := os.WriteFile(img, tinyPNG(t, 320, 200), 0o644); err != nil {
				t.Fatal(err)
			}
			tuiosCLI(t, base, "send-keys", "-s", "h", "--raw", bin)
			tuiosCLI(t, base, "send-keys", "-s", "h", "Enter")
			time.Sleep(12 * time.Second)
			if h == "crush" {
				// A first run asks to initialize the project: Nope.
				tuiosCLI(t, base, "send-keys", "-s", "h", "Right")
				time.Sleep(500 * time.Millisecond)
				tuiosCLI(t, base, "send-keys", "-s", "h", "Enter")
				time.Sleep(3 * time.Second)
			}
			set, _ := json.Marshal(map[string]any{"name": "img", "data": img})
			if out, err := tuiosCLI(t, base, "call", "set-buffer", string(set)); err != nil {
				t.Fatalf("set-buffer: %v\n%s", err, out)
			}
			paste, _ := json.Marshal(map[string]any{"session": "h", "name": "img"})
			out, err := tuiosCLI(t, base, "call", "paste-buffer", string(paste))
			if err != nil || !strings.Contains(out, `"bracketed":true`) {
				t.Fatalf("paste-buffer: %v\n%s", err, out)
			}
			time.Sleep(3 * time.Second)
			screen, _ := tuiosCLI(t, base, "capture-pane", "-s", "h")
			t.Logf("%s after the paste:\n%s", h, screen)
			tuiosCLI(t, base, "kill-session", "h")
			if mark, ok := attached[h]; ok && !strings.Contains(screen, mark) {
				t.Fatalf("%s did not attach the pasted image path: no %q on the screen", h, mark)
			}
		})
	}
}
