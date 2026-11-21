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

const (
	snapPixelCellW = cellW
	snapPixelCellH = cellH
)

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
			disableTiling(t, term)

			enterTerminalMode(t, term)
			_, img := writeIcatPNG(t, t.TempDir())
			enc := base64.StdEncoding.EncodeToString(img)
			runToExit(t, term, fmt.Sprintf(`printf '\033_Ga=T,q=2,f=100,s=%d,v=%d;%s\033\\'`, icatImageW, icatImageH, enc))
			runInShell(t, term, pixelReporter, "PIXON", shellTimeout)

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
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return !strings.Contains(s.Text(), "Window management mode")
			}, 20*time.Second); err != nil {
				t.Fatalf("the mode notice never cleared\n%s", term.Snapshot())
			}
			if !strings.Contains(term.Snapshot(), "PIXON") {
				t.Fatalf("the reporter pane is not on screen\n%s", term.Snapshot())
			}

			const grabRow, dropRow = 0, 20
			px := func(c int) int { return c*snapPixelCellW + snapPixelCellW/2 }
			py := func(r int) int { return r*snapPixelCellH + snapPixelCellH/2 }
			sendPixel(t, term, 0, px(110), py(grabRow), 'M')
			for _, c := range []int{100, 80, 60, 40, 20, 5, 0} {
				sendPixel(t, term, 32, px(c), py(dropRow), 'M')
			}

			snapFrame(t, term, "snap-left-"+tc.name+"-1-held")
			for i := range 20 {
				sendPixel(t, term, 32, -6, []int{dropRow*snapPixelCellH + 1, dropRow*snapPixelCellH + 12}[i%2], 'M')
			}

			sendPixel(t, term, 0, -6, py(dropRow), 'm')

			var final string
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				final = s.Text()
				return snappedLeft(final)
			}, uiTimeout); err != nil {
				snapFrame(t, term, "snap-left-"+tc.name+"-2-released")
				t.Fatalf("the pane never landed on the left half at full height\n%s", term.Snapshot())
			}

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

func snapFrame(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	dir := os.Getenv("TUIOS_E2E_ARTIFACTS")
	if dir == "" {
		return
	}
	saveArtifact(t, term, dir, name)
	savePNG(t, term.Screen(), hostPalette(t, ""), dir, name)
}

func sendPixel(t *testing.T, term *tuitest.Terminal, button, x, y int, final byte) {
	t.Helper()
	if err := term.Type(fmt.Sprintf("\x1b[<%d;%d;%d%c", button, x, y, final)); err != nil {
		t.Fatalf("send pixel report: %v", err)
	}
	time.Sleep(mouseGap)
}

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
