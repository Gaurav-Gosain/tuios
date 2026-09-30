package tuie2e

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
	"github.com/Gaurav-Gosain/tuitest"
)

// These tests put tuios in front of a stand-in sixel terminal and check what
// that terminal would show.
//
// The stand-in is tuios's own emulator, fed the bytes tuios wrote to its host
// from the first byte on. Its sixel handling is the useful part: it does what a
// sixel terminal does to the cells a picture lands on. Each cell an image covers
// is marked with the image and the cell's place in it, text written into a cell
// replaces the mark, and an erase clears it. So after the replay, the marks
// left in its grid are exactly the pixels a real sixel terminal would still be
// showing, and each can be traced to the pixels tuios sent for it.
//
// That turns "the image lands at the right place, clipped to the pane, and is
// gone when cleared" into checks on a grid:
//
//   - every cell the host shows image pixels in is one tuios drew as a blank
//     (an image cell), so no picture is left behind under text or chrome;
//   - the pixels in each such cell are the pixels of the pane's own image cell
//     at that position, so the crop and the placement are right;
//   - the host shows image pixels in every image cell of the frame except the
//     screen's last row, so nothing visible is missing.

// sixelHost records what tuios writes to its terminal and answers the startup
// probe as a terminal with the given DA1 attributes and graphics answers.
type sixelHost struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	probe chan struct{}
	reply string
}

func newSixelHost(sixel, kitty bool) *sixelHost {
	reply := fmt.Sprintf("\x1b[4;%d;%dt\x1b[6;%d;%dt", 40*cellH, 120*cellW, cellH, cellW)
	if kitty {
		reply += "\x1b_Gi=1;OK\x1b\\\x1b_Gi=2;OK\x1b\\\x1b_Gi=3;OK\x1b\\"
	}
	if sixel {
		reply += "\x1b[?62;4;22c"
	} else {
		reply += "\x1b[?62;22c"
	}
	return &sixelHost{probe: make(chan struct{}, 1), reply: reply}
}

func (h *sixelHost) Write(p []byte) (int, error) {
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

func (h *sixelHost) bytes() []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]byte(nil), h.buf.Bytes()...)
}

func (h *sixelHost) answer(term *tuitest.Terminal) {
	go func() {
		select {
		case <-h.probe:
			_ = term.Type(h.reply)
		case <-time.After(bootTimeout):
		}
	}()
}

// sixelFixture is a test picture: cols by rows cells at the host's cell size,
// each cell one flat colour of its own, so a cell of pixels names the cell it
// came from.
type sixelFixture struct {
	cols, rows int
	path       string
}

// period is how many columns go by before a colour repeats: the fixture
// needs one register per distinct cell, and there are 256.
func (f sixelFixture) period() int { return min(f.cols, vt.SixelMaxRegisters/f.rows) }

func (f sixelFixture) register(col, row int) int { return row*f.period() + col%f.period() }

func (f sixelFixture) colorAt(col, row int) color.RGBA {
	i := f.register(col, row)
	return color.RGBA{uint8(40 + (i*37)%200), uint8(30 + (i*91)%210), uint8(20 + (i*53)%220), 255}
}

func writeSixelFixture(t *testing.T, dir string, cols, rows int) sixelFixture {
	t.Helper()
	f := sixelFixture{cols: cols, rows: rows}
	img := &vt.SixelImage{Width: cols * cellW, Height: rows * cellH, Palette: make([]color.RGBA, vt.SixelMaxRegisters)}
	img.Pix = make([]uint16, img.Width*img.Height)
	for r := range rows {
		for c := range cols {
			reg := f.register(c, r)
			img.Palette[reg] = f.colorAt(c, r)
			for y := r * cellH; y < (r+1)*cellH; y++ {
				for x := c * cellW; x < (c+1)*cellW; x++ {
					img.Pix[y*img.Width+x] = uint16(reg + 1)
				}
			}
		}
	}
	seq := vt.EncodeSixel(img, image.Rect(0, 0, img.Width, img.Height), img.Width, img.Height)
	f.path = filepath.Join(dir, "fixture.six")
	if err := os.WriteFile(f.path, seq, 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

// hostView is the stand-in terminal after a replay.
type hostView struct {
	emu    *vt.Emulator
	images map[uint32]*vt.SixelImage
	sixels int
}

func replayHost(t *testing.T, stream []byte, cols, rows int) *hostView {
	t.Helper()
	hv := &hostView{emu: vt.NewEmulator(cols, rows), images: map[uint32]*vt.SixelImage{}}
	hv.emu.SetCellSize(cellW, cellH)
	var next uint32
	hv.emu.SetSixelPassthroughFunc(func(cmd *vt.SixelCommand, _, _ int) uint32 {
		next++
		hv.images[next] = vt.DecodeSixel(cmd)
		hv.sixels++
		return next
	})
	// Replies the stand-in makes to tuios's queries go nowhere.
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := hv.emu.Read(buf); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { _ = hv.emu.Close() })
	if _, err := hv.emu.Write(stream); err != nil {
		t.Fatalf("replay: %v", err)
	}
	return hv
}

// shownColor is the colour of the first image pixel the host shows in cell
// (x, y), and false where it shows no image. A cell on an image's last row may
// hold only its top few pixel rows, so the whole cell is searched.
func (hv *hostView) shownColor(x, y int) (color.RGBA, bool) {
	c := hv.emu.CellAt(x, y)
	if c == nil {
		return color.RGBA{}, false
	}
	id, row, col, ok := vt.ParseSixelMarker(c.Content)
	if !ok || hv.images[id] == nil {
		return color.RGBA{}, false
	}
	img := hv.images[id]
	for py := row * cellH; py < (row+1)*cellH; py++ {
		for px := col * cellW; px < (col+1)*cellW; px++ {
			if v, ok := img.At(px, py); ok {
				return v, true
			}
		}
	}
	return color.RGBA{}, false
}

// checkHostImage compares the host with tuios's own frame. origin is the
// screen cell of the pane image's top-left cell, and fixture the image.
// It returns how many cells show the image.
func checkHostImage(t *testing.T, what string, term *tuitest.Terminal, hv *hostView, f sixelFixture, origin image.Point) int {
	t.Helper()
	s := term.Screen()
	cols, rows := s.Size()
	shown := 0
	var bad []string
	for y := range rows {
		for x := range cols {
			got, onHost := hv.shownColor(x, y)
			frame := s.Cell(x, y)
			blank := frame.Content == " " || frame.Content == ""
			if onHost {
				shown++
				if !blank {
					bad = append(bad, fmt.Sprintf("%d,%d shows image pixels under %q", x, y, frame.Content))
					continue
				}
				c, r := x-origin.X, y-origin.Y
				if c < 0 || r < 0 || c >= f.cols || r >= f.rows {
					bad = append(bad, fmt.Sprintf("%d,%d shows image pixels outside the image", x, y))
					continue
				}
				// Sixel states colours in percent, so a channel may come
				// back a step or two off.
				if want := f.colorAt(c, r); !closeColor(got, want) {
					bad = append(bad, fmt.Sprintf("%d,%d shows %v, the pane's cell %d,%d is %v", x, y, got, c, r, want))
				}
			}
		}
	}
	if len(bad) > 0 {
		t.Errorf("%s: %d host cells wrong, first: %s\n%s", what, len(bad), strings.Join(bad[:min(8, len(bad))], "; "), s.Text())
	}
	return shown
}

// imageOrigin finds the image's top-left cell: the start of the row under
// the line the fixture command printed first.
func imageOrigin(t *testing.T, term *tuitest.Terminal, marker string) image.Point {
	t.Helper()
	s := term.Screen()
	_, rows := s.Size()
	for y := range rows {
		line := s.Line(y)
		if strings.Contains(line, "printf") {
			continue
		}
		if i := strings.Index(line, marker); i >= 0 {
			return image.Pt(len([]rune(line[:i])), y+1)
		}
	}
	t.Fatalf("marker %q not on screen\n%s", marker, s.Text())
	return image.Point{}
}

var sixelDCS = regexp.MustCompile(`\x1bP[0-9;]*q`)

// startSixelPane boots tuios against the given host, opens one pane and
// leaves it in terminal mode at a shell prompt.
func startSixelPane(t *testing.T, host *sixelHost, daemon bool) (*tuitest.Terminal, string) {
	t.Helper()
	term, base := start(t, startOpts{cols: 120, rows: 40, out: host, daemonDefault: daemon, env: []string{"TUIOS_CELL_SIZE=10x20", "TUIOS_KITTY_GRAPHICS=0"}})
	if daemon {
		t.Cleanup(func() { killDaemon(t, base) })
	}
	host.answer(term)
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo READY", "READY", shellTimeout)
	return term, base
}

// showFixture prints the marker line and then the picture, and waits for
// both to reach the screen.
func showFixture(t *testing.T, term *tuitest.Terminal, f sixelFixture, marker string) image.Point {
	t.Helper()
	typeLine(t, term, "clear; printf '%s\\n' '"+marker[:2]+"''"+marker[2:]+"'; cat "+f.path+"; echo; echo SHOWN")
	if err := term.WaitForText("SHOWN", shellTimeout); err != nil {
		t.Fatalf("fixture did not print: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(time.Second)
	return imageOrigin(t, term, marker)
}

// TestSixelShowsClippedAndClears is the whole path on a sixel terminal: the
// picture lands at the pane's cursor, is cut at the pane's right edge, is cut
// around a popup and comes back when it closes, and leaves nothing when the
// pane is cleared. Standalone and daemon mode draw through the same path but
// reach the emulator differently, so both run.
func TestSixelShowsClippedAndClears(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "daemon"}[daemon], func(t *testing.T) {
			host := newSixelHost(true, false)
			term, _ := startSixelPane(t, host, daemon)

			// Wider than the pane, so the host must be sent a crop.
			// Tall enough to run under the palette, which opens mid-screen.
			f := writeSixelFixture(t, t.TempDir(), 140, 16)
			origin := showFixture(t, term, f, "IMGTOP")

			hv := replayHost(t, host.bytes(), 120, 40)
			if hv.sixels == 0 {
				t.Fatalf("the host was sent no sixel image")
			}
			shown := checkHostImage(t, "drawn", term, hv, f, origin)
			// The pane is the full width less its border: the crop is the
			// pane's content columns, every row.
			if want := f.rows * (119 - origin.X); shown != want {
				t.Errorf("host shows %d image cells, want %d (%d rows of the pane's %d columns)", shown, want, f.rows, 119-origin.X)
			}

			// A popup over the picture: the host must show nothing under it.
			leaveTerminalMode(t, term)
			if err := term.SendKeys(legacyCtrlP); err != nil {
				t.Fatal(err)
			}
			waitPaletteOpen(t, term, "over the picture")
			time.Sleep(time.Second)
			hv = replayHost(t, host.bytes(), 120, 40)
			if dump := os.Getenv("TUIOS_SIXEL_DUMP"); dump != "" {
				_ = os.WriteFile(dump, host.bytes(), 0o644)
			}
			covered := checkHostImage(t, "under the palette", term, hv, f, origin)
			t.Logf("host image cells: drawn %d, under the palette %d", shown, covered)
			if covered >= shown || covered == 0 {
				t.Errorf("with the palette open the host shows %d image cells, want fewer than %d and more than 0", covered, shown)
			}
			closePalette(t, term, "over the picture")
			time.Sleep(time.Second)
			hv = replayHost(t, host.bytes(), 120, 40)
			if back := checkHostImage(t, "palette closed", term, hv, f, origin); back != shown {
				t.Errorf("after the palette closed the host shows %d image cells, want %d", back, shown)
			}

			// Clearing the pane takes the picture off the host.
			enterTerminalMode(t, term)
			runInShell(t, term, "clear; echo CLEARED", "CLEARED", shellTimeout)
			time.Sleep(time.Second)
			hv = replayHost(t, host.bytes(), 120, 40)
			if left := checkHostImage(t, "cleared", term, hv, f, origin); left != 0 {
				t.Errorf("after clear the host still shows %d image cells", left)
			}

			// No raw marker ever reaches the host as text.
			if bytes.Contains(host.bytes(), []byte(vt.SixelMarkerLead)) {
				t.Errorf("an image marker reached the host as text")
			}
		})
	}
}

// sixelRowOf returns the screen row a line containing want is on, or -1.
func sixelRowOf(s tuitest.Screen, want string) int {
	_, rows := s.Size()
	for y := range rows {
		if line := s.Line(y); strings.Contains(line, want) && !strings.Contains(line, "echo") {
			return y
		}
	}
	return -1
}

// TestSixelScrollsWithThePane checks a picture that scrolls partly off the
// top of the pane is cropped to its remaining rows, and that the rows that
// left are not left on the host.
func TestSixelScrollsWithThePane(t *testing.T) {
	host := newSixelHost(true, false)
	term, _ := startSixelPane(t, host, false)
	f := writeSixelFixture(t, t.TempDir(), 20, 6)
	origin := showFixture(t, term, f, "IMGTOP")
	hv := replayHost(t, host.bytes(), 120, 40)
	if n := checkHostImage(t, "drawn", term, hv, f, origin); n != 6*20 {
		t.Fatalf("host shows %d image cells, want %d", n, 6*20)
	}

	// The pane's last content row is the one above its bottom border.
	s := term.Screen()
	_, rows := s.Size()
	last := -1
	for y := origin.Y; y < rows; y++ {
		if c := s.Cell(origin.X-1, y).Content; c == "└" || c == "╰" || c == "┗" {
			last = y - 1
			break
		}
	}
	shown0 := sixelRowOf(s, "SHOWN")
	if last < 0 || shown0 < 0 {
		t.Fatalf("cannot find the pane's bottom (%d) or SHOWN (%d)\n%s", last, shown0, s.Text())
	}
	// Enough blank lines that the screen scrolls by four: the marker line
	// and the picture's top three rows leave the pane.
	n := last - shown0 + 1
	runInShell(t, term, fmt.Sprintf("for i in $(seq %d); do echo; done; echo SCROLL''ED", n), "SCROLLED", shellTimeout)
	time.Sleep(time.Second)
	shift := shown0 - sixelRowOf(term.Screen(), "SHOWN")
	if shift < 2 || shift > 6 {
		t.Fatalf("the pane scrolled by %d rows, want the picture partly off the top\n%s", shift, term.Snapshot())
	}
	moved := image.Pt(origin.X, origin.Y-shift)
	hv = replayHost(t, host.bytes(), 120, 40)
	got := checkHostImage(t, "scrolled", term, hv, f, moved)
	if want := (7 - shift) * 20; got != want {
		t.Errorf("after scrolling by %d the host shows %d image cells, want %d", shift, got, want)
	}
}

func closeColor(a, b color.RGBA) bool {
	d := func(x, y uint8) bool { return x-y <= 3 || y-x <= 3 }
	return d(a.R, b.R) && d(a.G, b.G) && d(a.B, b.B)
}

// checkHostMatchesFrame is the check for a picture whose pixels the test does
// not know, one a real program drew: the host shows image pixels in exactly
// the cells tuios drew as image cells (concealed blanks), except the screen's
// last row, which a sixel is never sent to. It returns the number of cells.
func checkHostMatchesFrame(t *testing.T, what string, term *tuitest.Terminal, hv *hostView) int {
	t.Helper()
	s := term.Screen()
	cols, rows := s.Size()
	shown, missing, extra := 0, 0, 0
	var first []string
	for y := range rows {
		for x := range cols {
			_, onHost := hv.shownColor(x, y)
			frame := s.Cell(x, y)
			image := frame.Conceal && (frame.Content == " " || frame.Content == "")
			switch {
			case onHost && image:
				shown++
			case onHost:
				extra++
				if len(first) < 4 {
					first = append(first, fmt.Sprintf("pixels under %q at %d,%d", frame.Content, x, y))
				}
			case image && y < rows-1:
				missing++
				if len(first) < 4 {
					first = append(first, fmt.Sprintf("no pixels in image cell %d,%d", x, y))
				}
			}
		}
	}
	if extra > 0 || missing > 0 {
		t.Errorf("%s: %d cells show pixels they should not, %d image cells show none: %s\n%s",
			what, extra, missing, strings.Join(first, "; "), s.Text())
	}
	return shown
}

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 160, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestSixelRealPrograms runs the sixel programs this machine has in a pane on
// a sixel host and checks what the host is left showing matches the frame.
// A program that is not installed is skipped.
func TestSixelRealPrograms(t *testing.T) {
	dir := t.TempDir()
	pic := filepath.Join(dir, "pic.png")
	writePNG(t, pic, 320, 200)
	writePNG(t, filepath.Join(dir, "pic2.png"), 200, 320)

	cases := []struct {
		name, bin, cmd string
	}{
		{"img2sixel", "img2sixel", "img2sixel " + pic},
		{"chafa", "chafa", "chafa -f sixel -s 40x10 " + pic},
		{"chafa-probe", "chafa", "chafa -s 40x10 " + pic},
		{"timg", "timg", "timg -ps -g 40x10 " + pic},
		{"lsix", "lsix", "cd " + dir + " && lsix pic.png pic2.png; cd - >/dev/null"},
	}
	host := newSixelHost(true, false)
	term, _ := startSixelPane(t, host, false)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.bin); err != nil {
				t.Skipf("%s is not installed", tc.bin)
			}
			if tc.bin == "img2sixel" {
				// The libsixel build on some machines writes nothing at all
				// for any input; that is not tuios's to test.
				if out, _ := exec.Command("img2sixel", pic).Output(); len(out) == 0 {
					t.Skipf("img2sixel writes no output on this machine")
				}
			}
			before := len(host.bytes())
			typeLine(t, term, "clear; "+tc.cmd+`; echo "DONE""-`+tc.name+`"`)
			if err := term.WaitForText("DONE-"+tc.name, shellTimeout); err != nil {
				t.Fatalf("%s did not finish: %v\n%s", tc.name, err, term.Snapshot())
			}
			time.Sleep(time.Second)
			stream := host.bytes()
			if !sixelDCS.Match(stream[before:]) {
				t.Fatalf("%s: the host was sent no sixel\n%s", tc.name, term.Snapshot())
			}
			hv := replayHost(t, stream, 120, 40)
			if n := checkHostMatchesFrame(t, tc.name, term, hv); n == 0 {
				t.Errorf("%s: the host shows no image cells\n%s", tc.name, term.Snapshot())
			} else {
				t.Logf("%s: %d image cells on the host", tc.name, n)
			}
		})
	}

	// yazi previews the picture it is opened on, in its right-hand column,
	// and picks its image protocol from TERM_PROGRAM and the DA1 answer.
	t.Run("yazi", func(t *testing.T) {
		if _, err := exec.LookPath("yazi"); err != nil {
			t.Skip("yazi is not installed")
		}
		before := len(host.bytes())
		typeLine(t, term, "clear; cd "+dir+" && yazi pic.png; cd - >/dev/null; echo \"DONE\"\"-yazi\"")
		deadline := time.Now().Add(15 * time.Second)
		for !sixelDCS.Match(host.bytes()[before:]) {
			if time.Now().After(deadline) {
				t.Fatalf("yazi drew no sixel\n%s", term.Snapshot())
			}
			time.Sleep(200 * time.Millisecond)
		}
		time.Sleep(time.Second)
		hv := replayHost(t, host.bytes(), 120, 40)
		if n := checkHostMatchesFrame(t, "yazi", term, hv); n == 0 {
			t.Errorf("yazi: the host shows no image cells\n%s", term.Snapshot())
		} else {
			t.Logf("yazi: %d image cells on the host", n)
		}
		if err := term.SendKeys("q"); err != nil {
			t.Fatal(err)
		}
		if err := term.WaitForText("DONE-yazi", shellTimeout); err != nil {
			t.Fatalf("yazi did not quit: %v\n%s", err, term.Snapshot())
		}
		time.Sleep(time.Second)
		hv = replayHost(t, host.bytes(), 120, 40)
		if n := checkHostMatchesFrame(t, "yazi closed", term, hv); n != 0 {
			t.Errorf("after yazi quit the host still shows %d image cells", n)
		}
	})
}

// paneDA1 asks the pane's terminal for DA1 from inside the pane, the way an
// image tool does, and returns the attribute list it got.
func paneDA1(t *testing.T, term *tuitest.Terminal, dir string) string {
	t.Helper()
	script := filepath.Join(dir, "da1.py")
	const src = `import os, select, sys, termios, tty
fd = sys.stdin.fileno()
old = termios.tcgetattr(fd)
tty.setraw(fd)
os.write(1, b"\x1b[c")
r = b""
while select.select([fd], [], [], 2)[0]:
    r += os.read(fd, 100)
    if r.endswith(b"c"):
        break
termios.tcsetattr(fd, termios.TCSADRAIN, old)
print("DA1=" + r.decode(errors="replace").replace("\x1b[?", "").rstrip("c") + "=END")
`
	if err := os.WriteFile(script, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	typeLine(t, term, "python3 "+script)
	var got string
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		for _, line := range strings.Split(s.Text(), "\n") {
			if i := strings.Index(line, "DA1="); i >= 0 && strings.Contains(line, "=END") && !strings.Contains(line, "python3") {
				got = line[i+4 : strings.Index(line, "=END")]
				return true
			}
		}
		return false
	}, shellTimeout); err != nil {
		t.Fatalf("no DA1 answer in the pane: %v\n%s", err, term.Snapshot())
	}
	return got
}

// TestSixelFallbacks covers the hosts that do not draw sixel. A kitty host is
// sent the picture as a kitty image, cropped with a source rectangle; a host
// with neither is shown a box, and never a sixel or raw marker text. In both
// the pane is told sixel only when the picture will be shown.
func TestSixelFallbacks(t *testing.T) {
	for _, tc := range []struct {
		name         string
		sixel, kitty bool
		env          string
	}{
		// The harness's own terminal answers DA1 and the kitty query before
		// the stand-in does, so the hosts without sixel are pinned by the
		// override the probe itself honours.
		{"sixel", true, false, "TUIOS_KITTY_GRAPHICS=0"},
		{"kitty", false, true, "TUIOS_SIXEL_GRAPHICS=0"},
		{"plain", false, false, "TUIOS_SIXEL_GRAPHICS=0 TUIOS_KITTY_GRAPHICS=0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := newSixelHost(tc.sixel, tc.kitty)
			term, base := start(t, startOpts{cols: 120, rows: 40, out: host,
				env: append([]string{"TUIOS_CELL_SIZE=10x20"}, strings.Fields(tc.env)...)})
			_ = base
			host.answer(term)
			waitBoot(t, term)
			newWindow(t, term)
			enterTerminalMode(t, term)
			runInShell(t, term, "echo READY", "READY", shellTimeout)

			dir := t.TempDir()
			da1 := paneDA1(t, term, dir)
			told := strings.Contains(";"+da1+";", ";4;")
			if want := tc.sixel || tc.kitty; told != want {
				t.Errorf("pane DA1 = %q: sixel listed %v, want %v", da1, told, want)
			}

			f := writeSixelFixture(t, dir, 140, 4)
			before := len(host.bytes())
			showFixture(t, term, f, "IMGTOP")
			out := host.bytes()[before:]
			switch tc.name {
			case "sixel":
				if !sixelDCS.Match(out) {
					t.Errorf("sixel host was sent no sixel")
				}
			case "kitty":
				if sixelDCS.Match(out) {
					t.Errorf("kitty host was sent a sixel")
				}
				if !bytes.Contains(out, []byte("a=t,f=32,o=z,s=1400,v=80")) {
					t.Errorf("kitty host was not sent the image as RGBA")
				}
				place := regexp.MustCompile(`a=p,i=\d+,p=\d+,x=0,y=0,w=(\d+),h=80,c=(\d+),r=4`).FindSubmatch(out)
				if place == nil || string(place[2]) != "118" || string(place[1]) != "1180" {
					t.Errorf("kitty placement %q, want the pane's 118 columns (1180 px) of the image", place)
				}
			case "plain":
				if sixelDCS.Match(out) || bytes.Contains(out, []byte("\x1b_G")) {
					t.Errorf("a host with no graphics was sent graphics")
				}
				text := term.Screen().Text()
				if !strings.Contains(text, "┌") || !strings.Contains(text, "image") {
					t.Errorf("no placeholder box where the image is\n%s", text)
				}
			}
			if bytes.Contains(host.bytes(), []byte(vt.SixelMarkerLead)) {
				t.Errorf("an image marker reached the host as text")
			}
		})
	}
}
