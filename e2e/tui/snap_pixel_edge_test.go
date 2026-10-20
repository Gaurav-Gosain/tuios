package tuie2e

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Snapping a floating pane to the left edge while a pane on screen tracks the
// mouse in SGR-pixel mode broke the whole screen, reported with disktree (a
// kitty graphics app) in one pane.
//
// tuios turns SGR-pixel reports (1016) on in its own terminal while such a pane
// is on screen. In 1016 ghostty does not clamp the pointer to the grid: a
// pointer left of the first column, in the window padding or past the window,
// is reported at a negative x, and one above the first row at a negative y.
// The input decoder reads a CSI parameter as digits only, so it stopped at the
// minus sign and handed the rest of the report on as typed keys. A drag to the
// left edge then typed "5;328M" into window management mode on every motion:
// digits picked windows, M restored every minimized one, and the release ended
// in "m", which minimized the pane being dragged. The drag itself never ended.
//
// The test drags the pane in host pixels, holds the pointer past the left edge
// as a real drag does, and lets go there. The pane must land on the left half,
// and once it has landed the client must stop drawing and stop sending image
// commands.

// The host cell is the one kittyHost reports in its answer to the startup
// probe, which is the cell size the client divides pixel reports by.
const (
	snapPixelCellW = cellW
	snapPixelCellH = cellH
)

// snapPixelReport is a motion report the pixel reporter printed.
var snapPixelReport = regexp.MustCompile(`PX35;(\d+);(\d+)\.`)

func TestFloatingSnapLeftWithPixelMousePastTheEdge(t *testing.T) {
	for _, tc := range []struct {
		name   string
		daemon bool
	}{{"standalone", false}, {"daemon", true}} {
		t.Run(tc.name, func(t *testing.T) {
			host := newKittyHost()
			statsPath := filepath.Join(t.TempDir(), "stats")
			term, base := start(t, startOpts{
				cols: 160, rows: 45,
				out:           host,
				animations:    true,
				daemonDefault: tc.daemon,
				env: []string{
					"TUIOS_SIXEL_GRAPHICS=0",
					"TUIOS_CELL_SIZE=8x16",
					"TUIOS_STATS_FILE=" + statsPath,
				},
			})
			if tc.daemon {
				killDaemon(t, base)
			}
			host.answerProbe(t, term)
			waitBoot(t, term)
			newWindow(t, term)
			newWindow(t, term)
			waitWindowCount(t, term, 2, "snap setup")
			// Tiling puts the two panes side by side. Turning it off leaves
			// them there as floating panes, with the second one on the right
			// half and focused.
			disableTiling(t, term)

			// The right pane shows an image and then tracks the mouse in
			// pixels, as disktree does.
			enterTerminalMode(t, term)
			_, img := writeIcatPNG(t, t.TempDir())
			enc := base64.StdEncoding.EncodeToString(img)
			runToExit(t, term, fmt.Sprintf(`printf '\033_Ga=T,q=2,f=100,s=%d,v=%d;%s\033\\'`, icatImageW, icatImageH, enc))
			runInShell(t, term, pixelReporter, "PIXON", shellTimeout)

			// tuios asks its terminal for 1016 after a mouse event. A report
			// read as cells reaches the pane at the centre of its cell, 8
			// pixels down an 8x16 pane cell and 9 on the wire. A report read
			// as pixels keeps its offset, here 3 host pixels into the cell.
			col, row := 120, 20
			mouseHover(t, term, col, row)
			if err := waitHost(host, "\x1b[?1016h", uiTimeout); err != nil {
				t.Fatalf("tuios never turned on SGR-pixel reports in its terminal: %v\n%s", err, term.Snapshot())
			}
			readsPixels := false
			for deadline := time.Now().Add(uiTimeout); !readsPixels && time.Now().Before(deadline); {
				sendPixel(t, term, 35, col*snapPixelCellW+1, row*snapPixelCellH+3+1, 'M')
				time.Sleep(150 * time.Millisecond)
				all := snapPixelReport.FindAllStringSubmatch(term.Screen().Text(), -1)
				if len(all) == 0 {
					continue
				}
				y, _ := strconv.Atoi(all[len(all)-1][2])
				readsPixels = y%16 != 9
			}
			if !readsPixels {
				t.Fatalf("the client never read a host report as pixels\n%s", term.Snapshot())
			}
			leaveTerminalMode(t, term)
			// The mode change shows a notice in the dock, and the client draws
			// while a notice counts down. It has to be gone before the drag,
			// so the frames counted after the snap are the snap's own.
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return !strings.Contains(s.Text(), "Window management mode")
			}, 20*time.Second); err != nil {
				t.Fatalf("the mode notice never cleared\n%s", term.Snapshot())
			}
			if !strings.Contains(term.Snapshot(), "PIXON") {
				t.Fatalf("the reporter pane is not on screen\n%s", term.Snapshot())
			}

			// Grab the right pane's title bar and drag it to the left edge,
			// all in host pixels.
			const grabRow, dropRow = 0, 20
			px := func(c int) int { return c*snapPixelCellW + snapPixelCellW/2 }
			py := func(r int) int { return r*snapPixelCellH + snapPixelCellH/2 }
			sendPixel(t, term, 0, px(110), py(grabRow), 'M')
			for _, c := range []int{100, 80, 60, 40, 20, 5, 0} {
				sendPixel(t, term, 32, px(c), py(dropRow), 'M')
			}

			// Hold the pointer past the left edge, trembling inside one cell as
			// a hand does. Each of these reports came in as typed keys.
			snapFrame(t, term, "snap-left-"+tc.name+"-1-held")
			for i := range 20 {
				sendPixel(t, term, 32, -6, []int{dropRow*snapPixelCellH + 1, dropRow*snapPixelCellH + 12}[i%2], 'M')
			}

			// Let go past the edge, as a drag that snaps left ends.
			sendPixel(t, term, 0, -6, py(dropRow), 'm')

			// The pane lands on the left half at full height, on top.
			var final string
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				final = s.Text()
				return snappedLeft(final)
			}, uiTimeout); err != nil {
				snapFrame(t, term, "snap-left-"+tc.name+"-2-released")
				t.Fatalf("the pane never landed on the left half at full height\n%s", term.Snapshot())
			}

			// And then everything holds still: no frames and no image
			// commands once the snap has landed.
			time.Sleep(time.Second)
			pid := term.Pid()
			settled := sampleIdle(t, pid, statsPath)
			settledFrom := len(host.bytes())
			time.Sleep(3 * time.Second)
			after := sampleIdle(t, pid, statsPath)
			renders := after.stats["render"] - settled.stats["render"]
			images := bytes.Count(host.bytes()[settledFrom:], []byte("\x1b_G"))
			t.Logf("after the snap landed: %d renders and %d image commands in 3s", renders, images)
			if renders != 0 {
				t.Errorf("the client drew %d frames in 3s after the snap landed, want 0", renders)
			}
			if images != 0 {
				t.Errorf("the client sent %d image commands in 3s after the snap landed, want 0", images)
			}
			if !snappedLeft(term.Screen().Text()) {
				t.Errorf("the pane left the left half after it landed\n%s", term.Snapshot())
			}
			snapFrame(t, term, "snap-left-"+tc.name+"-2-released")
			alive(t, term, "after a pixel drag to the left edge")
		})
	}
}

// snapFrame saves the screen as text, styled text and a PNG under
// TUIOS_E2E_ARTIFACTS, when that is set.
func snapFrame(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	dir := os.Getenv("TUIOS_E2E_ARTIFACTS")
	if dir == "" {
		return
	}
	saveArtifact(t, term, dir, name)
	savePNG(t, term.Screen(), hostPalette(t, ""), dir, name)
}

// sendPixel writes one SGR mouse report with the coordinates as given, which
// may be negative: a terminal in 1016 reports the pointer outside the grid
// that way, and tuitest only encodes positions on the grid.
func sendPixel(t *testing.T, term *tuitest.Terminal, button, x, y int, final byte) {
	t.Helper()
	if err := term.Type(fmt.Sprintf("\x1b[<%d;%d;%d%c", button, x, y, final)); err != nil {
		t.Fatalf("send pixel report: %v", err)
	}
	time.Sleep(mouseGap)
}

// snappedLeft reports whether the reporter pane fills the left half: a pane
// border opens the top row at the first column and closes at the middle, the
// pane shows the reporter's marker, and the pane runs down to the dock.
func snappedLeft(screen string) bool {
	lines := strings.Split(screen, "\n")
	if len(lines) < 44 {
		return false
	}
	top := []rune(lines[0])
	bottom := []rune(lines[42])
	if len(top) < 80 || len(bottom) < 80 {
		return false
	}
	if top[0] != '╭' || top[79] != '╮' || bottom[0] != '╰' || bottom[79] != '╯' {
		return false
	}
	for _, l := range lines[1:42] {
		if strings.Contains(string([]rune(l)[:min(80, len([]rune(l)))]), "PIXON") {
			return true
		}
	}
	return false
}

// waitHost waits until tuios has written s to the kitty host.
func waitHost(host *kittyHost, s string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if bytes.Contains(host.bytes(), []byte(s)) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("%q not written within %v", s, timeout)
}
