package tuie2e

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// textSizingProbeCmd prints the text sizing probe opencode sends on start-up:
// it homes the cursor and writes OSC 66 with one space. A marker on a later
// row says the shell got that far.
const textSizingProbeCmd = `printf '\033[H\033]66;w=1; \007\033[5;3HPROBEDONE'; exec sleep 120`

// textSizingBlock is one OSC 66 passthrough write as tuios sends it to the
// host: save the cursor, place the sequence, blank around it, restore.
var textSizingBlock = regexp.MustCompile(`\x1b7((?:[^\x1b]|\x1b[^8])*?\x1b\]66;(?:[^\x1b]|\x1b[^8])*)\x1b8`)

// hostCUP matches a cursor position and captures its 1-based row.
var hostCUP = regexp.MustCompile(`\x1b\[(\d+);\d+H`)

// TestTextSizingProbeKeepsTitleBar is the regression test for opencode
// breaking its pane's top border.
//
// opencode's probe puts an OSC 66 at the pane's first content row. The
// passthrough replayed it to the host and blanked the row above as well. That
// row is the pane's title bar (or the shared border), and the blank went to
// the host behind the renderer's back, so it was never repainted: the frame
// showed the left corner, a long blank run and the tail of the title badge.
//
// The assertion is on the bytes tuios sends to the host. The screen of the
// test terminal cannot carry it: tuitest's emulator handles OSC 66 the way
// tuios's own emulator does, and blanks the row above by itself.
//
// Negative control: fails against a binary built without the contentTopY
// bound in emitOSC66 (internal/app/text_sizing_passthrough.go).
func TestTextSizingProbeKeepsTitleBar(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		tiled bool
	}{
		{"floating", []string{"new", "e2e"}, false},
		{"tiled", []string{"new", "e2e"}, true},
		{"shared borders", []string{"--shared-borders", "new", "e2e"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := &lockedBuffer{}
			term, base := start(t, startOpts{shippedLooks: true, args: tc.args, out: stream})
			killDaemon(t, base)
			waitBoot(t, term)
			newWindow(t, term)
			if tc.tiled {
				newWindow(t, term)
				enableTiling(t, term)
			}
			renameWindow(t, term, "PROBEPANE")
			enterTerminalMode(t, term)
			if err := term.SendKeys(textSizingProbeCmd, tuitest.Enter); err != nil {
				t.Fatalf("type the probe: %v", err)
			}
			waitForAll(t, term, shellTimeout, "the probe never ran", "PROBEDONE")

			// The passthrough is flushed after the frame that follows the
			// probe, so wait for it rather than for the marker alone.
			var blocks [][]string
			deadline := time.Now().Add(uiTimeout)
			for time.Now().Before(deadline) {
				if blocks = textSizingBlock.FindAllStringSubmatch(stream.String(), -1); len(blocks) > 0 {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if len(blocks) == 0 {
				t.Fatalf("tuios never passed the probe to the host\n%s", term.Snapshot())
			}

			for _, b := range blocks {
				cups := hostCUP.FindAllStringSubmatch(b[1], -1)
				// The first position is where the probe lands: the pane's
				// first content row, because the probe homes the cursor.
				top, _ := strconv.Atoi(cups[0][1])
				for _, c := range cups[1:] {
					if row, _ := strconv.Atoi(c[1]); row < top {
						t.Fatalf("the passthrough wrote host row %d, above the pane's first content row %d. "+
							"That row is the title bar.\nwrite: %q\n%s",
							row, top, strings.TrimSpace(b[1]), term.Snapshot())
					}
				}
			}
		})
	}
}
