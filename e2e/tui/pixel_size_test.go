package tuie2e

import (
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

// Issue #506: a Textual app quit with ZeroDivisionError as soon as the mouse
// entered its pane. Textual turns on in-band resize reports (mode 2048) and,
// when the terminal has them, SGR-pixel mouse reports (mode 1016). It reads the
// pane's size in pixels from the 2048 report and divides each mouse report by
// pixels per cell. tuios sent 0 for both pixel sizes in every 2048 report.
//
// The guest here asks for every size a program can read: TIOCGWINSZ, the 2048
// report, XTWINOPS 14 (text area in pixels) and XTWINOPS 16 (cell in pixels),
// and then prints each SGR-pixel mouse report it gets.
//
// How these could pass wrongly, written down first:
//   - The sizes could be non-zero and still disagree, and a program that takes
//     its scale from one and its pointer from another lands on the wrong cell.
//     Every size is checked against one cell size, the host's.
//   - The cell size could be a constant that happens to be non-zero. The host
//     cell here is 8x16, which is no fallback tuios has, so the numbers must
//     come from the host.
//   - The mouse reports could be in cells and still be non-zero. Two hovers one
//     cell apart must be one cell width apart in the reports.
//   - A pane made before any client attached has no host to ask. Its sizes
//     must still be non-zero, and that case runs on a detached session.

// pixelGuest prints the sizes as SIZES ws=<rows>;<cols>;<xpix>;<ypix>
// ib=<rows>;<cols>;<ypix>;<xpix> t14=<ypix>;<xpix> t16=<h>;<w> END, then, unless
// run with "once", tracks the mouse in SGR-pixel mode and prints each motion
// report as PX<button>;<x>;<y>. and each later 2048 report as
// IB<rows>;<cols>;<ypix>;<xpix>. A q ends it.
const pixelGuest = `import fcntl, os, re, select, struct, sys, termios, tty
fd = sys.stdin.fileno()
old = termios.tcgetattr(fd)
tty.setraw(fd)
def out(s):
    os.write(1, (s + "\r\n").encode())
os.write(1, b"\x1b[?2048h\x1b[14t\x1b[16t")
r = b""
while r.count(b"t") < 3 and select.select([fd], [], [], 3)[0]:
    r += os.read(fd, 256)
rows, cols, xpix, ypix = struct.unpack("HHHH", fcntl.ioctl(fd, termios.TIOCGWINSZ, b"\0" * 8))
s = r.decode(errors="replace")
def field(p):
    m = re.search("\x1b\\[" + p + ";([0-9;]*)t", s)
    return m.group(1) if m else "none"
out("SIZES ws=%d;%d;%d;%d ib=%s t14=%s t16=%s END" % (rows, cols, xpix, ypix, field("48"), field("4"), field("6")))
if sys.argv[1:] == ["once"]:
    os.write(1, b"\x1b[?2048l")
    termios.tcsetattr(fd, termios.TCSADRAIN, old)
    sys.exit(0)
os.write(1, b"\x1b[?1003h\x1b[?1006h\x1b[?1016h")
out("PIX" + "ON")
buf = b""
while True:
    buf += os.read(fd, 256)
    if b"q" in buf:
        break
    while True:
        m = re.search(rb"\x1b\[<([0-9]+);([0-9]+);([0-9]+)[Mm]|\x1b\[48;([0-9;]+)t", buf)
        if not m:
            break
        if m.group(4):
            out("IB%s." % m.group(4).decode())
        else:
            out("PX%s;%s;%s." % (m.group(1).decode(), m.group(2).decode(), m.group(3).decode()))
        buf = buf[m.end():]
os.write(1, b"\x1b[?1016l\x1b[?1006l\x1b[?1003l\x1b[?2048l")
termios.tcsetattr(fd, termios.TCSADRAIN, old)
`

var pixelSizes = regexp.MustCompile(`SIZES ws=(\d+);(\d+);(\d+);(\d+) ib=(\S+) t14=(\S+) t16=(\S+) END`)

// paneSizes is what pixelGuest printed.
type paneSizes struct {
	rows, cols, xpix, ypix int
	ib, t14, t16           string
}

func parsePaneSizes(text string) (paneSizes, bool) {
	m := pixelSizes.FindStringSubmatch(text)
	if m == nil {
		return paneSizes{}, false
	}
	n := func(s string) int { v, _ := strconv.Atoi(s); return v }
	return paneSizes{rows: n(m[1]), cols: n(m[2]), xpix: n(m[3]), ypix: n(m[4]), ib: m[5], t14: m[6], t16: m[7]}, true
}

// check says what is wrong with the sizes for a cell of cw x ch pixels, or "".
func (s paneSizes) check(cw, ch int) string {
	var bad []string
	if s.rows <= 0 || s.cols <= 0 {
		bad = append(bad, fmt.Sprintf("the pane is %dx%d cells", s.cols, s.rows))
	}
	if want := fmt.Sprintf("%d;%d", s.cols*cw, s.rows*ch); fmt.Sprintf("%d;%d", s.xpix, s.ypix) != want {
		bad = append(bad, fmt.Sprintf("TIOCGWINSZ is %dx%d pixels, want %s", s.xpix, s.ypix, want))
	}
	if want := fmt.Sprintf("%d;%d;%d;%d", s.rows, s.cols, s.rows*ch, s.cols*cw); s.ib != want {
		bad = append(bad, fmt.Sprintf("the 2048 report is %q, want %q", s.ib, want))
	}
	if want := fmt.Sprintf("%d;%d", s.rows*ch, s.cols*cw); s.t14 != want {
		bad = append(bad, fmt.Sprintf("XTWINOPS 14 is %q, want %q", s.t14, want))
	}
	if want := fmt.Sprintf("%d;%d", ch, cw); s.t16 != want {
		bad = append(bad, fmt.Sprintf("XTWINOPS 16 is %q, want %q", s.t16, want))
	}
	return strings.Join(bad, "; ")
}

func writePixelGuest(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pixel_guest.py")
	if err := os.WriteFile(path, []byte(pixelGuest), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestPaneReportsItsPixelSize covers a pane on an attached client, in a
// standalone session and in a daemon session. The host cell is 8x16.
func TestPaneReportsItsPixelSize(t *testing.T) {
	for _, tc := range []struct {
		name   string
		daemon bool
	}{{"standalone", false}, {"daemon", true}} {
		t.Run(tc.name, func(t *testing.T) {
			guest := writePixelGuest(t)
			out := &lockedBuffer{}
			term, _ := start(t, startOpts{
				daemonDefault: tc.daemon,
				out:           out,
				env:           []string{"TUIOS_CELL_SIZE=8x16"},
			})
			waitBoot(t, term)
			newWindow(t, term)
			enterTerminalMode(t, term)
			typeLine(t, term, "python3 "+guest)

			var sizes paneSizes
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				var ok bool
				sizes, ok = parsePaneSizes(s.Text())
				return ok && strings.Contains(s.Text(), "PIXON")
			}, shellTimeout); err != nil {
				t.Fatalf("the guest never printed its sizes: %v\n%s", err, term.Snapshot())
			}
			t.Logf("sizes: %+v", sizes)
			if bad := sizes.check(8, 16); bad != "" {
				t.Fatalf("ASSERTION: %s\n%s", bad, term.Snapshot())
			}

			// Two hovers one cell apart. In pixels the reports are one cell
			// width apart, and both land inside the pane's pixel width. The
			// first hover is in cells: tuios turns 1016 on in its own
			// terminal only after it, as TestSGRPixelMouseCarriesTheHostPixel
			// covers, and the hovers after that are in host pixels.
			col, row := paneCell(t, term)
			x0 := lastPixelX(t, term, "the first hover", -1, func() { mouseHover(t, term, col, row) })
			if err := waitOutput(out, "\x1b[?1016h", uiTimeout); err != nil {
				t.Fatalf("tuios never turned on SGR-pixel reports in its terminal: %v", err)
			}
			x1 := lastPixelX(t, term, "the hover one cell right", x0, func() {
				sendMouseThenWait(t, term, "pixel hover", tuitest.MouseEvent{
					Col: (col+1)*8 + 4, Row: row*16 + 8,
					Button: tuitest.MouseNone, Action: tuitest.MouseMove, Pixel: true,
				}, mouseGap)
			})
			if d := x1 - x0; d != 8 {
				t.Fatalf("ASSERTION: two hovers one cell apart were reported %d apart (%d, %d), want 8, one cell in pixels\n%s", d, x0, x1, term.Snapshot())
			}
			if x1 < 1 || x1 > sizes.xpix {
				t.Fatalf("ASSERTION: the report x %d is outside the pane's %d pixels\n%s", x1, sizes.xpix, term.Snapshot())
			}
			if err := term.SendKeys("q"); err != nil {
				t.Fatal(err)
			}
			alive(t, term, "after the pixel guest")
		})
	}
}

// lastPixelX sends with send until the guest's last motion report has an x
// other than not, and returns it.
func lastPixelX(t *testing.T, term *tuitest.Terminal, what string, not int, send func()) int {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		send()
		if all := pixelReport.FindAllStringSubmatch(term.Screen().Text(), -1); len(all) > 0 {
			if x, _ := strconv.Atoi(all[len(all)-1][1]); x != not {
				return x
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("%s never reached the guest\n%s", what, term.Snapshot())
	return 0
}

// TestDetachedPaneReportsAPixelSize covers a pane no client has seen: a
// detached session's first pane. No host cell size is known, so tuios uses a
// fallback cell, and every size agrees with it and none is zero. A client
// with an 8x16 cell then attaches, and the guest, which still has 2048 on, is
// sent a report in that cell: it scales the mouse by the last one it got.
func TestDetachedPaneReportsAPixelSize(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	guest := writePixelGuest(t)
	if out, err := tuiosCLI(t, base, "new", "-d", "pixels"); err != nil {
		t.Fatalf("create the detached session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "pixels", "--raw", "python3 "+guest); err != nil {
		t.Fatalf("send-keys: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "pixels", "Enter"); err != nil {
		t.Fatalf("send-keys Enter: %v\n%s", err, out)
	}
	var sizes paneSizes
	var out string
	deadline := time.Now().Add(shellTimeout)
	for {
		out, _ = tuiosOut(base, "capture-pane", "-s", "pixels")
		var ok bool
		if sizes, ok = parsePaneSizes(out); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the guest never printed its sizes:\n%s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("sizes: %+v", sizes)
	cw, ch := 0, 0
	if h, w, ok := strings.Cut(sizes.t16, ";"); ok {
		ch, _ = strconv.Atoi(h)
		cw, _ = strconv.Atoi(w)
	}
	if cw <= 0 || ch <= 0 {
		t.Fatalf("ASSERTION: XTWINOPS 16 says the cell is %q\n%s", sizes.t16, out)
	}
	if bad := sizes.check(cw, ch); bad != "" {
		t.Fatalf("ASSERTION: %s\n%s", bad, out)
	}
	if cw == 8 && ch == 16 {
		t.Fatalf("the fallback cell is the client's 8x16, so the report after the attach proves nothing")
	}

	term := startIn(t, base, startOpts{
		args: []string{"attach", "pixels"},
		env:  []string{"TUIOS_CELL_SIZE=8x16"},
	})
	ib := regexp.MustCompile(`IB(\d+);(\d+);(\d+);(\d+)\.`)
	var last string
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		all := ib.FindAllStringSubmatch(s.Text(), -1)
		if len(all) == 0 {
			return false
		}
		m := all[len(all)-1]
		last = m[0]
		rows, _ := strconv.Atoi(m[1])
		cols, _ := strconv.Atoi(m[2])
		return m[3] == strconv.Itoa(rows*16) && m[4] == strconv.Itoa(cols*8)
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: after a client with an 8x16 cell attached, the guest's last 2048 report is %q, want one in 8x16 cells: %v\n%s", last, err, term.Snapshot())
	}
	if err := term.SendKeys("q"); err != nil {
		t.Fatal(err)
	}
}
