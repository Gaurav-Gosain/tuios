package tuie2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// An application that shows an image through kitty Unicode placeholders
// (ntcharts/picture, kitten icat --unicode-placeholder) sends a=T with U=1
// and prints the cells the image occupies itself. tuios honoured U=1 only on
// a bare a=p; on a=T it reserved rows, which scrolled the guest's screen every
// frame, and placed the image at the cursor. These tests run the stand-in in
// ./placeholders through each transport and read what the host was told.

// placeholderGuestID is the image id the stand-in transmits under. The host
// knows the image by an id tuios allocates.
const placeholderGuestID = 7

// buildPlaceholders compiles the placeholder stand-in once per test binary.
func buildPlaceholders(t *testing.T) string {
	t.Helper()
	placeholdersOnce.Do(func() {
		dir, err := os.MkdirTemp("", "placeholders")
		if err != nil {
			placeholdersErr = err
			return
		}
		bin := filepath.Join(dir, "placeholders")
		build := exec.Command("go", "build", "-o", bin, "./placeholders")
		if out, err := build.CombinedOutput(); err != nil {
			placeholdersErr = fmt.Errorf("build placeholders: %v\n%s", err, out)
			return
		}
		placeholdersBin = bin
	})
	if placeholdersErr != nil {
		t.Fatalf("%v", placeholdersErr)
	}
	return placeholdersBin
}

var (
	placeholdersOnce sync.Once
	placeholdersBin  string
	placeholdersErr  error
)

// runPlaceholders boots tuios against a host that draws placeholders, runs the
// stand-in in one tiled pane for a few seconds, and returns the host stream
// from the launch on, with the screen as it ended.
func runPlaceholders(t *testing.T, daemon bool, transport string, extra ...string) ([]byte, tuitest.Screen) {
	t.Helper()
	if transport == "shm" {
		requireDevShm(t)
	}
	bin := buildPlaceholders(t)
	host := newKittyHost()
	term, base := start(t, startOpts{
		cols: 120, rows: 40,
		// The host draws placeholders and takes the 24-bit foreground that
		// carries the image id; a real ghostty or kitty says so itself.
		env:           []string{"TUIOS_SIXEL_GRAPHICS=0", "TUIOS_KITTY_PLACEHOLDERS=1", "COLORTERM=truecolor"},
		out:           host,
		daemonDefault: daemon,
	})
	if daemon {
		killDaemon(t, base)
	}
	host.answerProbe(t, term)
	waitBoot(t, term)
	newWindow(t, term)
	enableTiling(t, term)
	waitWindowCount(t, term, 1, "one pane")
	enterTerminalMode(t, term)
	runInShell(t, term, "echo IMAG\"\"EPANE", "IMAGEPANE", shellTimeout)
	// The grid is printed once, with the first frame, so read from the launch.
	host.mark("launch")
	typeLine(t, term, strings.Join(append([]string{bin, transport, "10"}, extra...), " "))
	if err := term.WaitForText("PLACEHOLDER-FOOTER", shellTimeout); err != nil {
		t.Fatalf("the placeholder app never took the pane: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(4 * time.Second)
	stream := host.bytes()
	if i := bytes.Index(stream, []byte(phaseMark+"launch")); i >= 0 {
		stream = stream[i:]
	}
	return stream, term.Screen()
}

// hostImageID is the one id the host was told to hold the guest's image under.
func hostImageID(t *testing.T, stream []byte) int {
	t.Helper()
	ids := map[int]int{}
	for _, c := range wireCmds(stream) {
		if c.action == "t" || c.action == "T" {
			ids[c.image]++
		}
	}
	if len(ids) != 1 {
		t.Fatalf("the host was sent the image under %d ids, want one: %v\n%s", len(ids), ids, summarise(wireCmds(stream)))
	}
	for id := range ids {
		return id
	}
	return 0
}

// placeholderCellsByID counts the placeholder cells in the stream by the image
// id their foreground names. A cell in any other colour counts under -1.
func placeholderCellsByID(stream []byte) map[int]int {
	out := map[int]int{}
	fg := -1
	for _, m := range placeholderTokens.FindAllSubmatch(stream, -1) {
		if len(m[1]) > 0 {
			fg = foregroundID(string(m[2]), fg)
			continue
		}
		out[fg]++
	}
	return out
}

// placeholderTokens matches an SGR sequence or a placeholder cell, in order.
var placeholderTokens = regexp.MustCompile(`(\x1b\[([0-9;:]*)m)|\x{10EEEE}`)

// foregroundID applies one SGR to the foreground id in effect: a 24-bit or
// 256-colour foreground names an id, a reset names none, and anything else
// leaves it.
func foregroundID(params string, current int) int {
	if params == "" || params == "0" {
		return -1
	}
	fields := strings.FieldsFunc(params, func(r rune) bool { return r == ';' || r == ':' })
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "0", "39":
			current = -1
		case "38":
			if i+4 < len(fields) && fields[i+1] == "2" {
				r, _ := strconv.Atoi(fields[i+2])
				g, _ := strconv.Atoi(fields[i+3])
				b, _ := strconv.Atoi(fields[i+4])
				current = r<<16 | g<<8 | b
				i += 4
			} else if i+2 < len(fields) && fields[i+1] == "5" {
				// kitty reads a 256-colour foreground's index as the
				// id, which is how tuios writes ids below 256.
				current, _ = strconv.Atoi(fields[i+2])
				i += 2
			}
		}
	}
	return current
}

// assertPlaceholdersNameTheHostImage is the positive half of every test here:
// every cell reaching the host names the host's id, not the guest's.
func assertPlaceholdersNameTheHostImage(t *testing.T, stream []byte, hostID int) {
	t.Helper()
	if hostID == placeholderGuestID {
		t.Fatalf("the host holds the image under the guest's own id %d, so nothing here tests the translation", hostID)
	}
	byID := placeholderCellsByID(stream)
	if byID[hostID] == 0 {
		t.Fatalf("no placeholder cell naming the host's image %d reached the host; cells by id: %v", hostID, byID)
	}
	for id, n := range byID {
		if id != hostID {
			t.Errorf("%d placeholder cells reached the host naming image %d, which it does not hold; the image is %d", n, id, hostID)
		}
	}
}

// cursorPlacementRE matches a placement tuios positioned itself.
var cursorPlacementRE = regexp.MustCompile(`\x1b\[\d+;\d+H\x1b_Ga=p`)

// assertVirtualOnly checks that the host was told the image is virtual and
// never told to draw it somewhere.
func assertVirtualOnly(t *testing.T, stream []byte, hostID int) {
	t.Helper()
	cmds := wireCmds(stream)
	virtual, real := 0, 0
	for _, c := range cmds {
		if c.image != hostID || (c.action != "p" && c.action != "T") {
			continue
		}
		if strings.Contains(","+c.params+",", ",U=1,") {
			virtual++
			if c.cols == 0 || c.rows == 0 {
				t.Errorf("a virtual placement without its cell box: %q", c.params)
			}
		} else {
			real++
		}
	}
	if virtual == 0 {
		t.Errorf("no virtual placement reached the host for image %d:\n%s", hostID, summarise(cmds))
	}
	if real > 0 {
		t.Errorf("%d placements of image %d were positioned by tuios, want none:\n%s", real, hostID, summarise(cmds))
	}
	if n := len(cursorPlacementRE.FindAll(stream, -1)); n > 0 {
		t.Errorf("%d placements were drawn at a cursor position; a placeholder image has no position of its own", n)
	}
}

// assertPaneTextIntact checks the lines the guest printed above and below the
// picture are still there. Reserving rows for the image scrolls them off.
func assertPaneTextIntact(t *testing.T, s tuitest.Screen) {
	t.Helper()
	text := s.Text()
	for _, want := range []string{"PLACEHOLDER-HEADER", "PLACEHOLDER-FOOTER"} {
		if !strings.Contains(text, want) {
			t.Errorf("the guest's %s is gone from the pane\n%s", want, s)
		}
	}
}

// An a=T,U=1 stream leaves the pane's text alone, declares the image virtual,
// and never draws it at the cursor, on every transport and against a daemon.
func TestKittyVirtualPlacementLeavesThePaneToTheGuest(t *testing.T) {
	for _, tc := range []struct {
		name      string
		daemon    bool
		transport string
	}{
		{"b64", false, "b64"},
		{"file", false, "file"},
		{"shm", false, "shm"},
		{"daemon-b64", true, "b64"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream, screen := runPlaceholders(t, tc.daemon, tc.transport)
			hostID := hostImageID(t, stream)
			assertVirtualOnly(t, stream, hostID)
			assertPaneTextIntact(t, screen)
			assertPlaceholdersNameTheHostImage(t, stream, hostID)
		})
	}
}

// A placeholder cell split across two PTY reads is redrawn when its marks
// arrive. That redraw used the guest's foreground, so the cell named an image
// the host never had: a one-cell hole wherever a read boundary fell.
func TestKittyPlaceholderSplitAcrossWritesNamesTheHostImage(t *testing.T) {
	stream, screen := runPlaceholders(t, false, "b64", "split")
	hostID := hostImageID(t, stream)
	assertPlaceholdersNameTheHostImage(t, stream, hostID)
	assertPaneTextIntact(t, screen)
}

// The protocol lets the placeholder cells be printed before the image. A cell
// is rewritten as it is stored, so one stored before the image had a host id
// kept naming the guest's, and the picture never appeared.
func TestKittyPlaceholdersPrintedBeforeTheImageNameIt(t *testing.T) {
	stream, screen := runPlaceholders(t, false, "b64", "early")
	hostID := hostImageID(t, stream)
	assertPlaceholdersNameTheHostImage(t, stream, hostID)
	assertPaneTextIntact(t, screen)
}
