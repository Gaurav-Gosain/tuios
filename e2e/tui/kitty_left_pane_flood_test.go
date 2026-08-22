package tuie2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The reported scenario, as reported: two tiled panes with shared borders, a
// graphics app filling the LEFT one, an ordinary shell in the RIGHT one, and
// nothing happening but that shell printing.
//
// Three things here are not interchangeable with the earlier neighbour test,
// and each of them is a different code path in the passthrough:
//
//   - The image is in the LEFT pane. A right-hand pane's image ends at the
//     screen's right edge, where the refresh has a clamp of its own; a
//     left-hand one does not, so a difference seen here is not that clamp.
//   - The frames arrive over shared memory (t=s), which is what
//     terminal-browser uses. A host that can read files gets the path
//     forwarded verbatim from forwardFileTransmit; only a host that cannot
//     takes the direct-transmission path the earlier test exercised.
//   - The host answers the capability probe, so KittyFileTransfer is true.
//     Without an answer tuios assumes a browser-shaped host and re-encodes
//     every file transmission inline, which is a different writer entirely.
//
// kittyHost plays a real kitty terminal: it records every byte tuios writes and
// answers the capability probe with the replies kitty gives, so tuios takes the
// native file-transmission path rather than the browser fallback.
type kittyHost struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	probe chan struct{}
}

func newKittyHost() *kittyHost {
	return &kittyHost{probe: make(chan struct{}, 1)}
}

func (h *kittyHost) Write(p []byte) (int, error) {
	h.mu.Lock()
	h.buf.Write(p)
	h.mu.Unlock()
	if bytes.Contains(p, []byte(da1Query)) {
		select {
		case h.probe <- struct{}{}:
		default:
		}
	}
	return len(p), nil
}

func (h *kittyHost) mark(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.buf.WriteString(phaseMark + name + "\x00\x00")
}

func (h *kittyHost) bytes() []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]byte(nil), h.buf.Bytes()...)
}

// cellW and cellH are the cell size this host reports, so a frame sized to the
// pane can be generated in pixels.
const (
	cellW = 10
	cellH = 20
)

// answerProbe replies the way kitty does: pixel geometry, an OK for each of the
// three graphics probes, and DA1 last because DA1 is what closes the read.
func (h *kittyHost) answerProbe(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	go func() {
		for {
			select {
			case <-h.probe:
				reply := fmt.Sprintf("\x1b[4;%d;%dt\x1b[6;%d;%dt", 40*cellH, 120*cellW, cellH, cellW) +
					"\x1b_Gi=1;OK\x1b\\" +
					"\x1b_Gi=2;OK\x1b\\" +
					"\x1b_Gi=3;OK\x1b\\" +
					da1Reply
				_ = term.Type(reply)
			case <-time.After(bootTimeout):
				return
			}
		}
	}()
}

// buildFrameloop compiles the graphics-app stand-in once per test binary and
// returns its path.
func buildFrameloop(t *testing.T) string {
	t.Helper()
	frameloopOnce.Do(func() {
		dir, err := os.MkdirTemp("", "frameloop")
		if err != nil {
			frameloopErr = err
			return
		}
		bin := filepath.Join(dir, "frameloop")
		build := exec.Command("go", "build", "-o", bin, "./frameloop")
		if out, err := build.CombinedOutput(); err != nil {
			frameloopErr = fmt.Errorf("build frameloop: %v\n%s", err, out)
			return
		}
		frameloopBin = bin
	})
	if frameloopErr != nil {
		t.Fatalf("%v", frameloopErr)
	}
	return frameloopBin
}

var (
	frameloopOnce sync.Once
	frameloopBin  string
	frameloopErr  error
)

// startFrameloop runs the graphics-app stand-in in the focused pane and returns
// the geometry it was actually given. The geometry comes back through a file
// because the app takes the alt screen the moment it starts, so a marker
// printed to the grid is gone before a poll can read it.
func startFrameloop(t *testing.T, term *tuitest.Terminal, repaintMS int) (geom string, cols, rows, xpx, ypx int) {
	t.Helper()
	bin := buildFrameloop(t)
	geom = filepath.Join(t.TempDir(), "geom")
	typeLine(t, term, fmt.Sprintf("%s %s 20 %d", bin, geom, repaintMS))
	deadline := time.Now().Add(shellTimeout)
	for time.Now().Before(deadline) {
		if sizes := announcedSizes(geom); len(sizes) > 0 {
			s := sizes[0]
			return geom, s[0], s[1], s[2], s[3]
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the graphics app never reported its geometry\n%s", term.Snapshot())
	return "", 0, 0, 0, 0
}

// announcedSizes is every size the guest has been told it has, in order, as
// cols/rows/xpixels/ypixels.
func announcedSizes(geom string) [][4]int {
	b, err := os.ReadFile(geom)
	if err != nil {
		return nil
	}
	var out [][4]int
	for _, line := range strings.Split(string(b), "\n") {
		var s [4]int
		if n, _ := fmt.Sscan(line, &s[0], &s[1], &s[2], &s[3]); n == 4 && s[2] > 0 {
			out = append(out, s)
		}
	}
	return out
}

// graphicsRE matches one kitty APC command on the wire, params and payload
// apart. Params never contain an ESC, which is what bounds the match.
var graphicsRE = regexp.MustCompile(`\x1b_G([^;\x1b]*)(?:;([^\x1b]*))?\x1b\\`)

// wireCmd is one graphics command as the host received it.
type wireCmd struct {
	phase  string
	action string
	image  int
	cols   int
	rows   int
	srcW   int
	srcH   int
	srcX   int
	srcY   int
	pixW   int
	pixH   int
	params string
}

// wireCmds parses every graphics command out of a capture, tagged with the
// phase it landed in.
func wireCmds(stream []byte) []wireCmd {
	var out []wireCmd
	prevEnd, prevName := 0, "boot"
	scan := func(phase string, chunk []byte) {
		for _, m := range graphicsRE.FindAllSubmatch(chunk, -1) {
			params := string(m[1])
			c := wireCmd{phase: phase, params: params}
			for _, kv := range strings.Split(params, ",") {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					continue
				}
				n, _ := strconv.Atoi(v)
				switch k {
				case "a":
					c.action = v
				case "i":
					c.image = n
				case "c":
					c.cols = n
				case "r":
					c.rows = n
				case "w":
					c.srcW = n
				case "h":
					c.srcH = n
				case "x":
					c.srcX = n
				case "y":
					c.srcY = n
				case "s":
					c.pixW = n
				case "v":
					c.pixH = n
				}
			}
			out = append(out, c)
		}
	}
	for _, m := range markRE.FindAllSubmatchIndex(stream, -1) {
		scan(prevName, stream[prevEnd:m[0]])
		prevEnd, prevName = m[1], string(stream[m[2]:m[3]])
	}
	scan(prevName, stream[prevEnd:])
	return out
}

// TestKittyLeftPaneImageSurvivesRightPaneFlood is the report: image left, shell
// right, shared borders, and `ls` on a loop in the right pane. The left pane is
// not resized, nothing is dragged, and its own guest keeps drawing the same
// frame at the same size throughout. Every placement the host is given for that
// image must therefore describe the same rectangle, in every phase.
func TestKittyLeftPaneImageSurvivesRightPaneFlood(t *testing.T) {
	host := newKittyHost()
	term, _ := start(t, startOpts{
		cols: 120, rows: 40,
		args: []string{"--shared-borders"},
		env:  []string{"TUIOS_SIXEL_GRAPHICS=0"},
		out:  host,
	})
	host.answerProbe(t, term)
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	enableTiling(t, term)
	waitWindowCount(t, term, 2, "two tiled panes")

	// Left pane: the graphics app, rendering at the size it was given.
	mouseClick(t, term, 20, 12, tuitest.MouseLeft, 0)
	time.Sleep(400 * time.Millisecond)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo IMAGEPANE", "IMAGEPANE", shellTimeout)
	_, cols, rows, xpx, ypx := startFrameloop(t, term, 0)
	t.Logf("left pane: %dx%d cells, %dx%d px (%d x %d px per cell)",
		cols, rows, xpx, ypx, xpx/cols, ypx/rows)
	leaveTerminalMode(t, term)

	host.mark("image-only")
	time.Sleep(3 * time.Second)

	// Right pane: the flood, exactly as reported.
	mouseClick(t, term, 95, 12, tuitest.MouseLeft, 0)
	time.Sleep(400 * time.Millisecond)
	enterTerminalMode(t, term)
	typeLine(t, term, "while :; do ls; done")
	leaveTerminalMode(t, term)

	host.mark("neighbour-flood")
	time.Sleep(6 * time.Second)

	if dump := os.Getenv("TUIOS_KITTY_CAPTURE"); dump != "" {
		if err := os.WriteFile(dump, host.bytes(), 0o644); err != nil {
			t.Fatalf("write capture: %v", err)
		}
	}

	assertWholeImage(t, term, host.bytes(), cols, rows, xpx, ypx)
}

// assertWholeImage is the invariant the report is about. The guest renders a
// bitmap of exactly its pane, so every command that tells the host how to draw
// that image must say: this many cells, which is the pane, and this much of the
// image, which is all of it. A cell count that is not the pane's scales the
// bitmap into the wrong box; a source rectangle smaller than the bitmap throws
// the rest of it away and scales what is left over the same box. Either one is
// the image the report calls stretched.
func assertWholeImage(t *testing.T, term *tuitest.Terminal, stream []byte, cols, rows, xpx, ypx int) {
	t.Helper()
	cmds := wireCmds(stream)
	var drawing []wireCmd
	for _, c := range cmds {
		// a=p places; a=T transmits and places; a=t carrying a cell count is
		// also telling the host how big to draw the image it is replacing.
		if c.action == "p" || c.action == "T" || (c.action == "t" && c.cols > 0) {
			drawing = append(drawing, c)
		}
	}
	if len(drawing) == 0 {
		t.Fatalf("no placement ever reached the host; commands seen:\n%s\n%s",
			summarise(cmds), term.Snapshot())
	}

	shapes := map[string]int{}
	for _, c := range drawing {
		shapes[fmt.Sprintf("%s a=%s c=%d r=%d src=%d,%d %dx%d",
			c.phase, c.action, c.cols, c.rows, c.srcX, c.srcY, c.srcW, c.srcH)]++
	}
	t.Logf("what the host was told to draw, by phase:\n%s", tally(shapes))

	var bad []string
	for _, c := range drawing {
		switch {
		case c.cols != cols || c.rows != rows:
			bad = append(bad, fmt.Sprintf("phase=%s cells=%dx%d want %dx%d: %q",
				c.phase, c.cols, c.rows, cols, rows, c.params))
		case c.srcX != 0 || c.srcY != 0 ||
			(c.srcW != 0 && c.srcW != xpx) || (c.srcH != 0 && c.srcH != ypx):
			bad = append(bad, fmt.Sprintf("phase=%s source=%d,%d %dx%d of a %dx%d image: %q",
				c.phase, c.srcX, c.srcY, c.srcW, c.srcH, xpx, ypx, c.params))
		}
	}
	if len(bad) > 0 {
		t.Fatalf("%d of %d draw commands did not describe the whole %dx%d image in the "+
			"pane's whole %dx%d cells:\n%s",
			len(bad), len(drawing), xpx, ypx, cols, rows, strings.Join(uniq(bad), "\n"))
	}
}

func summarise(cmds []wireCmd) string {
	seen := map[string]int{}
	for _, c := range cmds {
		seen["a="+c.action]++
	}
	return tally(seen)
}

func tally(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "  %6d  %s\n", m[k], k)
	}
	return b.String()
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if len(out) > 20 {
		return append(out[:20], fmt.Sprintf("... and %d more distinct", len(out)-20))
	}
	return out
}
