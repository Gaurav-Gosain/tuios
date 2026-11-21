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

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/vt"
)

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

type sixelFixture struct {
	cols, rows int
	path       string
}

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

func imageOrigin(t *testing.T, term *tuitest.Terminal, marker string) image.Point {
	t.Helper()
	s := term.Screen()
	_, rows := s.Size()
	for y := range rows {
		line := s.Line(y)
		if strings.Contains(line, "printf") {
			continue
		}
		if before, _, ok := strings.Cut(line, marker); ok {
			return image.Pt(len([]rune(before)), y+1)
		}
	}
	t.Fatalf("marker %q not on screen\n%s", marker, s.Text())
	return image.Point{}
}

var sixelDCS = regexp.MustCompile(`\x1bP[0-9;]*q`)

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
	runInShell(t, term, "echo RE''ADY", "READY", shellTimeout)
	return term, base
}

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

			f := writeSixelFixture(t, t.TempDir(), 140, 16)
			origin := showFixture(t, term, f, "IMGTOP")

			hv := replayHost(t, host.bytes(), 120, 40)
			if hv.sixels == 0 {
				t.Fatalf("the host was sent no sixel image")
			}
			shown := checkHostImage(t, "drawn", term, hv, f, origin)
			if want := f.rows * (119 - origin.X); shown != want {
				t.Errorf("host shows %d image cells, want %d (%d rows of the pane's %d columns)", shown, want, f.rows, 119-origin.X)
			}

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

			enterTerminalMode(t, term)
			runInShell(t, term, "clear; echo CLEA''RED", "CLEARED", shellTimeout)
			time.Sleep(time.Second)
			hv = replayHost(t, host.bytes(), 120, 40)
			if left := checkHostImage(t, "cleared", term, hv, f, origin); left != 0 {
				t.Errorf("after clear the host still shows %d image cells", left)
			}

			if bytes.Contains(host.bytes(), []byte(vt.SixelMarkerLead)) {
				t.Errorf("an image marker reached the host as text")
			}
		})
	}
}

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
		for line := range strings.SplitSeq(s.Text(), "\n") {
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
// sent the picture as a kitty image, cropped with a source rectangle. A host
// with neither is shown the picture as block glyphs by default, and a box
// with appearance.image_symbols off; it is never sent a sixel or raw marker
// text. The pane is told sixel only when the picture will be shown.
func TestSixelFallbacks(t *testing.T) {
	for _, tc := range []struct {
		name         string
		sixel, kitty bool
		env          string
		symbols      string
	}{
		{"sixel", true, false, "TUIOS_KITTY_GRAPHICS=0", ""},
		{"kitty", false, true, "TUIOS_SIXEL_GRAPHICS=0", ""},
		{"plain", false, false, "TUIOS_SIXEL_GRAPHICS=0 TUIOS_KITTY_GRAPHICS=0", ""},
		{"plain-off", false, false, "TUIOS_SIXEL_GRAPHICS=0 TUIOS_KITTY_GRAPHICS=0", "off"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := newSixelHost(tc.sixel, tc.kitty)
			env := append([]string{"TUIOS_CELL_SIZE=10x20"}, strings.Fields(tc.env)...)
			if tc.symbols != "" {
				home := t.TempDir()
				writeConfigIn(t, home, "[appearance]\nimage_symbols = \""+tc.symbols+"\"\n")
				env = append(env, "XDG_CONFIG_HOME="+home)
			}
			term, base := start(t, startOpts{cols: 120, rows: 40, out: host, env: env})
			_ = base
			host.answer(term)
			waitBoot(t, term)
			newWindow(t, term)
			enterTerminalMode(t, term)
			runInShell(t, term, "echo RE''ADY", "READY", shellTimeout)

			dir := t.TempDir()
			da1 := paneDA1(t, term, dir)
			told := strings.Contains(";"+da1+";", ";4;")
			if want := tc.name != "plain-off"; told != want {
				t.Errorf("pane DA1 = %q: sixel listed %v, want %v", da1, told, want)
			}

			f := writeSixelFixture(t, dir, 140, 4)
			before := len(host.bytes())
			origin := showFixture(t, term, f, "IMGTOP")
			out := host.bytes()[before:]
			if tc.sixel || tc.kitty {
				for r := range f.rows {
					for c := range 8 {
						if cell := term.Screen().Cell(origin.X+c, origin.Y+r); cell.Bg.Kind != tuitest.ColorDefault || strings.TrimSpace(cell.Content) != "" {
							t.Errorf("%s host: image cell %d,%d drawn as text %q", tc.name, c, r, cell.Content)
						}
					}
				}
			}
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
			case "plain", "plain-off":
				if sixelDCS.Match(out) || bytes.Contains(out, []byte("\x1b_G")) {
					t.Errorf("a host with no graphics was sent graphics")
				}
				text := term.Screen().Text()
				box := strings.Contains(text, "┌─") && strings.Contains(text, "└─")
				painted := 0
				for r := range f.rows {
					for c := range 8 {
						if term.Screen().Cell(origin.X+c, origin.Y+r).Bg.Kind != tuitest.ColorDefault {
							painted++
						}
					}
				}
				if tc.name == "plain-off" && (!box || painted > 0) {
					t.Errorf("image_symbols off: want the placeholder box and no picture, got box %v and %d painted cells\n%s", box, painted, text)
				}
				if tc.name == "plain" && (box || painted != 8*f.rows) {
					t.Errorf("default: want the picture drawn as cells, got box %v and %d of %d painted cells\n%s", box, painted, 8*f.rows, text)
				}
			}
			if bytes.Contains(host.bytes(), []byte(vt.SixelMarkerLead)) {
				t.Errorf("an image marker reached the host as text")
			}
		})
	}
}

// TestSixelAtTheBottomKeepsItsLastRow: an image drawn at the bottom of a pane
// scrolls it, and the text printed next goes on the row under the image, as
// in xterm. It used to land on the image's last row and erase it.
func TestSixelAtTheBottomKeepsItsLastRow(t *testing.T) {
	host := newSixelHost(true, false)
	term, _ := startSixelPane(t, host, false)
	f := writeSixelFixture(t, t.TempDir(), 20, 6)
	typeLine(t, term, "clear; seq 80; cat "+f.path+"; printf AFTER''BOT")
	if err := term.WaitForText("AFTERBOT", shellTimeout); err != nil {
		t.Fatalf("no AFTERBOT: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(time.Second)
	after := sixelRowOf(term.Screen(), "AFTERBOT")
	origin := image.Pt(1, after-6)
	hv := replayHost(t, host.bytes(), 120, 40)
	if n := checkHostImage(t, "at the bottom", term, hv, f, origin); n != 6*20 {
		t.Errorf("host shows %d of the image's %d cells", n, 6*20)
	}
}

func sixelPaintedExtent(body []byte) (w, h int) {
	cmd := vt.ParseSixelCommand(body)
	if cmd == nil {
		return 0, 0
	}
	cmd.Width, cmd.Height = 2048, 2048
	cmd.BackgroundMode = 1
	img := vt.DecodeSixel(cmd)
	for y := range img.Height {
		for x := range img.Width {
			if _, ok := img.At(x, y); ok {
				w, h = max(w, x+1), max(h, y+1)
			}
		}
	}
	return w, h
}

// TestSixelOverpaintIsCut: a guest image that declares 10x20 pixels and paints
// 400x30 covers one cell. The host must never be sent pixels past that cell;
// the guest's own bytes would paint over the panes beside it.
func TestSixelOverpaintIsCut(t *testing.T) {
	host := newSixelHost(true, false)
	term, _ := startSixelPane(t, host, false)
	path := filepath.Join(t.TempDir(), "over.six")
	body := "\x1bPq\"1;1;10;20#1;2;100;0;0!400~-!400~-!400~-!400~-!400~\x1b\\"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	before := len(host.bytes())
	runInShell(t, term, "clear; cat "+path+"; echo; echo OVER''DONE", "OVERDONE", shellTimeout)
	time.Sleep(time.Second)
	out := host.bytes()[before:]
	dcs := regexp.MustCompile(`(?s)\x1bP([0-9;]*q.*?)\x1b\\`).FindAllSubmatch(out, -1)
	if len(dcs) == 0 {
		t.Fatalf("the host was sent no sixel")
	}
	for _, m := range dcs {
		if w, h := sixelPaintedExtent(m[1]); w > cellW || h > cellH {
			t.Errorf("the host was sent a sixel painting %dx%d pixels for a one-cell image", w, h)
		}
	}
}
