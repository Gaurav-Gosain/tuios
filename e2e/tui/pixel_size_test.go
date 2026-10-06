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
	"unicode/utf8"

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
			// The first hover is a cell report, so the guest is told the
			// centre of the pane cell under it: the pane's first column is
			// where the guest's SIZES line starts on the screen.
			left := paneLeftOf(t, term, "SIZES ws=")
			if want := (col-left)*8 + 4 + 1; x0 != want {
				t.Fatalf("ASSERTION: the first hover, at pane column %d, was reported at x %d, want %d, the centre of that cell in pixels\n%s", col-left, x0, want, term.Snapshot())
			}
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

// paneLeftOf is the screen column of marker, which the guest printed at the
// start of a line, so it is the pane's first column.
func paneLeftOf(t *testing.T, term *tuitest.Terminal, marker string) int {
	t.Helper()
	for _, line := range strings.Split(term.Screen().Text(), "\n") {
		if i := strings.Index(line, marker); i >= 0 {
			return utf8.RuneCountInString(line[:i])
		}
	}
	t.Fatalf("%q is not on the screen\n%s", marker, term.Snapshot())
	return 0
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

// ibReports is every 2048 report the guest printed, as rows, cols, pixel
// height and pixel width, read from the daemon's copy of the pane.
func ibReports(base, session string) (string, [][4]int) {
	out, _ := tuiosOut(base, "capture-pane", "-s", session)
	var got [][4]int
	for _, m := range regexp.MustCompile(`IB(\d+);(\d+);(\d+);(\d+)\.`).FindAllStringSubmatch(out, -1) {
		var r [4]int
		for i := range r {
			r[i], _ = strconv.Atoi(m[i+1])
		}
		got = append(got, r)
	}
	return out, got
}

// cellOf is the cell a 2048 report implies, as width x height.
func cellOf(r [4]int) string {
	if r[0] == 0 || r[1] == 0 {
		return "none"
	}
	return fmt.Sprintf("%dx%d", r[3]/r[1], r[2]/r[0])
}

// TestPaneCellFollowsOneClient covers two clients with different fonts on
// one session. The pane's cell used to be set by whichever client spoke
// last, on every resize and attach, so a guest's pixel size flipped between
// them. It is now the cell of one client: under window_size = smallest, the
// default, the client that attached first.
//
// The negative half: a second client with a 12x24 cell attaches, and every
// report after it is still in client a's 8x16. The positive half: client a
// leaves, and the cell moves to the client that is left.
func TestPaneCellFollowsOneClient(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	guest := writePixelGuest(t)
	if out, err := tuiosCLI(t, base, "new", "-d", "cells"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "cells", "--raw", "python3 "+guest); err != nil {
		t.Fatalf("send-keys: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "cells", "Enter"); err != nil {
		t.Fatalf("send-keys Enter: %v\n%s", err, out)
	}

	// waitCell waits until the guest's last 2048 report implies cell, and
	// returns how many reports there were then.
	waitCell := func(what, cell string) int {
		t.Helper()
		deadline := time.Now().Add(uiTimeout)
		for {
			out, got := ibReports(base, "cells")
			if len(got) > 0 && cellOf(got[len(got)-1]) == cell {
				return len(got)
			}
			if time.Now().After(deadline) {
				t.Fatalf("ASSERTION: %s: the guest's 2048 reports %v never came to a %s cell\n%s", what, got, cell, out)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	// The guest is in its report loop, with the fallback cell, before any
	// client attaches. Otherwise the first client could settle the cell
	// before the guest turns 2048 on, and no report would follow.
	deadline := time.Now().Add(shellTimeout)
	for {
		out, _ := tuiosOut(base, "capture-pane", "-s", "cells")
		if s, ok := parsePaneSizes(out); ok && strings.Contains(out, "PIXON") {
			if s.t16 != "20;10" {
				t.Fatalf("the guest started with a %q cell, want the fallback\n%s", s.t16, out)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the guest never printed its sizes:\n%s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}

	a := attachIn(t, base, "cells", startOpts{env: []string{"TUIOS_CELL_SIZE=8x16"}})
	waitCell("client a attached", "8x16")

	b := attachIn(t, base, "cells", startOpts{cols: 100, rows: 30, env: []string{"TUIOS_CELL_SIZE=12x24"}})
	// b is smaller, so the session shrinks to it and the guest gets a report
	// for the new size. That report must still be in a's cell. The wait is for
	// the shrink to reach the guest, so the check below has b's attach in it.
	deadline = time.Now().Add(uiTimeout)
	for {
		_, got := ibReports(base, "cells")
		if len(got) > 0 && got[len(got)-1][1] <= 100 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the session never shrank to client b: reports %v", got)
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(time.Second)
	out, got := ibReports(base, "cells")
	for _, r := range got {
		if cellOf(r) != "8x16" && cellOf(r) != "10x20" {
			t.Fatalf("ASSERTION: with client a attached first, the guest was told a %s cell (%v), not client a's 8x16\n%s", cellOf(r), got, out)
		}
	}
	if last := cellOf(got[len(got)-1]); last != "8x16" {
		t.Fatalf("ASSERTION: the guest's last report is in a %s cell, want client a's 8x16 (%v)\n%s", last, got, out)
	}
	t.Logf("reports with both clients attached: %v", got)

	if err := a.Close(); err != nil {
		t.Fatalf("close client a: %v", err)
	}
	waitCell("client a left", "12x24")
	if err := b.SendKeys("q"); err != nil {
		t.Fatal(err)
	}
}
