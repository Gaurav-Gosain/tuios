package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// The bridge between tuios and a terminal that speaks the drag and drop
// protocol itself (internal/app/dnd_host.go), against a real kitty under
// Xvfb. kitty and the programs in its windows speak XDND to each other as
// they do on a desktop; the drags are made by xdotool through XTEST, so the X
// server sees a real pointer. The drags tuios carries between its own panes,
// with no such terminal, are in dnd_osc72_test.go.
//
// TUIOS_E2E_KITTY names the kitty binary and turns the test on; the kitten is
// taken from beside it. Xvfb, xdotool and xclip have to be in PATH.
//
// The debug log tuios writes with TUIOS_DEBUG_INTERNAL=1 holds the exchange
// with kitty, which is how a test tells a drag kitty carried from one tuios
// carried, and that a gesture went unanswered.

// deskConfig is dndConfig with the bridge on.
const deskConfig = dndConfig + "drag_and_drop_desktop = true\n"

// x11Desk is an Xvfb display and the programs on it. t is the test that owns
// the display; a kitty window belongs to the subtest that starts it.
type x11Desk struct {
	t       *testing.T
	display string
	env     []string
	kitty   string
	kitten  string
}

// startDesk starts an Xvfb of its own, or skips when the pieces are missing.
func startDesk(t *testing.T) *x11Desk {
	t.Helper()
	kitty := os.Getenv("TUIOS_E2E_KITTY")
	if kitty == "" {
		t.Skip("TUIOS_E2E_KITTY is not set")
	}
	kitten := filepath.Join(filepath.Dir(kitty), "kitten")
	if _, err := os.Stat(kitten); err != nil {
		if kitten, err = exec.LookPath("kitten"); err != nil {
			t.Skipf("no kitten beside %s or in PATH", kitty)
		}
	}
	for _, tool := range []string{"Xvfb", "xdotool", "xclip"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not in PATH", tool)
		}
	}
	d := &x11Desk{t: t, kitty: kitty, kitten: kitten}
	// A display of its own: the suite may run beside a desktop, and a
	// second test must not land on the first one's server.
	for n := 90; n < 110; n++ {
		display := ":" + strconv.Itoa(n)
		cmd := exec.Command("Xvfb", display, "-screen", "0", "1280x800x24")
		if err := cmd.Start(); err != nil {
			t.Fatalf("start Xvfb: %v", err)
		}
		d.display = display
		d.env = []string{"DISPLAY=" + display, "LIBGL_ALWAYS_SOFTWARE=1", "GDK_BACKEND=x11"}
		if d.waitDisplay(cmd) {
			t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
			return d
		}
		_, _ = cmd.Process.Wait()
	}
	t.Fatal("no free X display between :90 and :109")
	return nil
}

// waitDisplay reports whether the X server answers before xvfb exits, which
// it does at once when the display is taken.
func (d *x11Desk) waitDisplay(xvfb *exec.Cmd) bool {
	done := make(chan struct{})
	go func() { _, _ = xvfb.Process.Wait(); close(done) }()
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			return false
		default:
		}
		if _, err := d.xdotool("getdisplaygeometry"); err == nil {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// environ is the test process's environment under this display, with the
// Wayland display taken out: kitty would take it over the X display.
func (d *x11Desk) environ(extra ...string) []string {
	env := make([]string, 0, len(os.Environ())+len(d.env)+len(extra))
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "WAYLAND_DISPLAY=") && !strings.HasPrefix(kv, "DISPLAY=") {
			env = append(env, kv)
		}
	}
	return append(append(env, d.env...), extra...)
}

func (d *x11Desk) xdotool(args ...string) (string, error) {
	cmd := exec.Command("xdotool", args...)
	cmd.Env = d.environ()
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (d *x11Desk) mustXdotool(args ...string) string {
	d.t.Helper()
	out, err := d.xdotool(args...)
	if err != nil {
		d.t.Fatalf("xdotool %v: %v: %s", args, err, out)
	}
	return out
}

// kittyWindow is a kitty window on the desk and where it is.
type kittyWindow struct {
	id   string
	x, y int
}

// startKitty starts kitty running argv in dir, with class as its X class,
// moves the window to (x, y) and returns it once it is mapped. env is added
// to the environment.
func (d *x11Desk) startKitty(t *testing.T, class, dir string, width, height, x, y int, env []string, argv ...string) kittyWindow {
	t.Helper()
	args := []string{"--config", "NONE", "--class", class, "--directory", dir,
		"-o", "enable_audio_bell=no", "-o", "remember_window_size=no", "-o", "linux_display_server=x11",
		"-o", "font_size=10", "-o", "initial_window_width=" + strconv.Itoa(width), "-o", "initial_window_height=" + strconv.Itoa(height),
		"-o", "allow_remote_control=no", "-o", "clipboard_control=write-clipboard write-primary read-clipboard read-primary"}
	args = append(args, argv...)
	cmd := exec.Command(d.kitty, args...)
	cmd.Env = d.environ(env...)
	logf, err := os.Create(filepath.Join(artifactDir(t), class+".log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = logf, logf
	// The windows of an earlier subtest are gone by now; one of this
	// subtest's own class would be a second window of the same program.
	if out, _ := d.xdotool("search", "--class", class); out != "" {
		t.Fatalf("a %s window is already on the display: %s", class, out)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start kitty: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait(); _ = logf.Close() })
	deadline := time.Now().Add(bootTimeout)
	for {
		if out, err := d.xdotool("search", "--class", class); err == nil && out != "" {
			id := strings.Fields(out)[0]
			d.mustXdotool("windowmove", id, strconv.Itoa(x), strconv.Itoa(y))
			time.Sleep(300 * time.Millisecond)
			return kittyWindow{id: id, x: x, y: y}
		}
		if time.Now().After(deadline) {
			t.Fatalf("kitty window %s never appeared", class)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// drag presses at (x1, y1), carries the pointer to (x2, y2) in steps and
// releases there, in screen pixels.
func (d *x11Desk) drag(x1, y1, x2, y2 int) {
	d.t.Helper()
	d.mustXdotool("mousemove", strconv.Itoa(x1), strconv.Itoa(y1))
	time.Sleep(300 * time.Millisecond)
	d.mustXdotool("mousedown", "1")
	time.Sleep(300 * time.Millisecond)
	const steps = 40
	for i := 1; i <= steps; i++ {
		d.mustXdotool("mousemove", strconv.Itoa(x1+(x2-x1)*i/steps), strconv.Itoa(y1+(y2-y1)*i/steps))
		time.Sleep(40 * time.Millisecond)
	}
	time.Sleep(800 * time.Millisecond)
	d.mustXdotool("mouseup", "1")
}

// screenshot keeps a picture of the whole display as an artifact, when
// ImageMagick's import is installed: what the person would have seen.
func (d *x11Desk) screenshot(t *testing.T, name string) {
	t.Helper()
	bin, err := exec.LookPath("import")
	if err != nil {
		return
	}
	cmd := exec.Command(bin, "-window", "root", filepath.Join(artifactDir(t), name+".png"))
	cmd.Env = d.environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("screenshot %s: %v: %s", name, err, out)
	}
}

// clipboard is the X clipboard's text.
func (d *x11Desk) clipboard() string {
	cmd := exec.Command("xclip", "-o", "-selection", "clipboard")
	cmd.Env = d.environ()
	out, _ := cmd.Output()
	return string(out)
}

// deskTuios is a tuios session whose client runs in a kitty window on the
// desk: the isolation root, the window, and kitty's cell size, which turns a
// pane's cell into a screen pixel.
type deskTuios struct {
	t             *testing.T
	desk          *x11Desk
	base, session string
	win           kittyWindow
	cw, ch        int
	logFrom       int64
}

// startDeskTuios makes a session with n plain panes, detached, and attaches a
// client to it in a kitty window in terminal mode, where a press reaches the
// program in a pane. The panes are plain shells; the test types into them.
func startDeskTuios(t *testing.T, desk *x11Desk, config string, n int) (*deskTuios, []winRect) {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, config)
	killDaemon(t, base)
	session := "desk"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("new session: %v\n%s", err, out)
	}
	for i := 1; i < n; i++ {
		if out, err := tuiosCLI(t, base, "new-window", "-s", session); err != nil {
			t.Fatalf("new-window: %v\n%s", err, out)
		}
	}
	env := []string{"SHELL=/bin/sh", "ENV=", "PS1=$ ", "TUIOS_DEBUG_INTERNAL=1", "TUIOS_NO_DAEMON=1"}
	for _, key := range xdgKeys {
		env = append(env, key+"="+xdgDir(base, key))
	}
	pinPreV080Looks(t, base)
	size := filepath.Join(base, "winsize")
	d := &deskTuios{t: t, desk: desk, base: base, session: session}
	if st, err := os.Stat(dndDebugLog); err == nil {
		d.logFrom = st.Size()
	}
	// The size is read from kitty's own tty before tuios takes it: kitty
	// fills in the pixel fields, which is where the cell size comes from.
	d.win = desk.startKitty(t, "tuiosB", base, 630, 700, 640, 0, env,
		"sh", "-c", buildDnDPeer(t)+" winsize "+size+" && exec "+tuiosBin+" attach --terminal-mode "+session+" 2>"+filepath.Join(base, "client.err"))
	ws := waitFile(t, size, func(s string) bool { return len(strings.Fields(s)) == 4 }, "kitty never reported its size")
	f := strings.Fields(ws)
	cols, _ := strconv.Atoi(f[0])
	rows, _ := strconv.Atoi(f[1])
	xpx, _ := strconv.Atoi(f[2])
	ypx, _ := strconv.Atoi(f[3])
	if cols == 0 || rows == 0 || xpx == 0 || ypx == 0 {
		t.Fatalf("kitty reported a size with a zero in it: %q", ws)
	}
	d.cw, d.ch = xpx/cols, ypx/rows
	// The client is up once the panes span kitty's columns rather than the
	// daemon's default.
	var rects []winRect
	deadline := time.Now().Add(bootTimeout)
	for {
		rects = waitForSettledGeometryIn(t, base, session, n)
		right := 0
		for _, r := range rects {
			right = max(right, r.X+r.Width)
		}
		if right >= cols-2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the panes never took kitty's %d columns: %+v", cols, rects)
		}
		time.Sleep(200 * time.Millisecond)
	}
	for i := range rects {
		for j := i + 1; j < len(rects); j++ {
			if rects[j].X < rects[i].X {
				rects[i], rects[j] = rects[j], rects[i]
			}
		}
	}
	if strings.Contains(config, "drag_and_drop_desktop = true") {
		waitFile(t, dndDebugLog, func(string) bool { return strings.Contains(d.log(), "host send t=o o=0 x=1 ") }, "tuios never registered with kitty")
	} else {
		time.Sleep(time.Second)
	}
	return d, rects
}

// dndDebugLog is where TUIOS_DEBUG_INTERNAL=1 writes (internal/debuglog).
const dndDebugLog = "/tmp/tuios-debug.log"

// log is what tuios logged about drag and drop since this client started.
func (d *deskTuios) log() string {
	data, _ := os.ReadFile(dndDebugLog)
	if int64(len(data)) < d.logFrom {
		return ""
	}
	var lines []string
	for line := range strings.SplitSeq(string(data[d.logFrom:]), "\n") {
		if strings.Contains(line, " DND: ") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// hostOffers counts the drags tuios offered kitty: a t=o with operation
// flags, as against the registration, which is t=o with x=1 and no flags.
func hostOffers(log string) int {
	n := 0
	for line := range strings.SplitSeq(log, "\n") {
		if strings.Contains(line, "host send t=o o=") && !strings.Contains(line, "host send t=o o=0 ") {
			n++
		}
	}
	return n
}

// hostDataRequests matches tuios asking kitty for the data of a MIME type
// (t=r with a 1-based index), as against ending a drop (t=r with x=0).
var hostDataRequests = regexp.MustCompile(`host send t=r o=\d+ x=[1-9]`)

// saveLog keeps the exchange as an artifact.
func (d *deskTuios) saveLog(name string) {
	_ = os.WriteFile(filepath.Join(artifactDir(d.t), name+".dnd.log"), []byte(d.log()+"\n"), 0o644)
}

// cell is the screen pixel at the centre of cell (cx, cy) of the program in
// pane. The rectangle the daemon reports includes the border, which the
// program's first cell sits inside of.
func (d *deskTuios) cell(pane winRect, cx, cy int) (int, int) {
	return d.win.x + (pane.X+1+cx)*d.cw + d.cw/2, d.win.y + (pane.Y+1+cy)*d.ch + d.ch/2
}

// find is the row and column of text in a pane, as the daemon captures it.
func (d *deskTuios) find(pane winRect, text string) (row, col int) {
	d.t.Helper()
	capture, _ := tuiosCLI(d.t, d.base, "capture-pane", "-s", d.session, "-w", pane.ID)
	for i, line := range strings.Split(capture, "\n") {
		if before, _, ok := strings.Cut(line, text); ok {
			return i, utf8.RuneCountInString(before)
		}
	}
	d.t.Fatalf("%q is not in the pane:\n%s", text, capture)
	return 0, 0
}

// run types a command into a pane and waits for marker.
func (d *deskTuios) run(pane winRect, command, marker string) {
	d.t.Helper()
	runInPane(d.t, d.base, d.session, pane, command, marker)
}

// kittenTarget is the dnd kitten taking drops into dir, as a pane command.
func kittenTarget(kitten, dir string) string {
	return "cd " + dir + " && exec " + kitten + " dnd --drop-anywhere=copy"
}

// readerPane is a plain reader with bracketed paste on, writing to out.
func readerPane(out string) string {
	return "stty raw -echo; printf '\\033[?2004h'; printf 'SINK%sREADY\\n' ''; cat > " + out
}

// TestDnDDesktopBridge: with [panes] drag_and_drop_desktop on, in a kitty
// that speaks the protocol, a file dragged from another window lands on the
// pane under the pointer, a file a pane drags can be dropped on another
// window, kitty carries a drag between two panes of the same window, tuios's
// own mouse gestures stay its own, and with the switch off nothing is
// bridged.
//
// How this could pass wrongly, written down first:
//   - the kitten in the pane could get the drop from kitty directly, with
//     tuios out of the loop. kitty has one program per window, tuios, and
//     without a registration pastes a dropped file's path as text into it.
//     The copied file is the kitten's work, and the switch-off subtest shows
//     what kitty does on its own.
//   - the reader could get the path from kitty's own paste rather than from
//     the bridge. kitty pastes into the focused pane; the reader is not the
//     focused one, and the bridge routes by the pointer.
//   - a drag between two panes could be tuios's own, started by the gesture
//     it recognises itself. With the bridge up it recognises none; the log
//     shows kitty's gesture and kitty's drop.
//
// Negative controls: with handleHostDnD never called, every drag subtest
// fails: nothing registers. With the dndHostOn check in DnDMotion cut, the
// same-window subtest fails: two drags. With dndHostWanted ignoring the
// switch, the switch-off subtest fails: tuios registers. See
// NEGATIVE_CONTROLS.md.
func TestDnDDesktopBridge(t *testing.T) {
	desk := startDesk(t)
	kitten := desk.kitten

	t.Run("a file from another window drops on a pane with the protocol", func(t *testing.T) {
		src, dst := t.TempDir(), t.TempDir()
		if err := os.WriteFile(filepath.Join(src, "dragged.txt"), []byte("payload from the outside\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		other := desk.startKitty(t, "dndA", src, 600, 500, 0, 0, nil, kitten, "dnd", "dragged.txt")
		d, panes := startDeskTuios(t, desk, deskConfig, 2)
		defer d.saveLog("drop-on-protocol-pane")
		d.run(panes[0], kittenTarget(kitten, dst), "transfer the files here")
		d.run(panes[1], readerPane(filepath.Join(d.base, "pasted")), "SINKREADY")
		if !strings.Contains(d.log(), "host send t=a") {
			t.Fatalf("tuios did not register with kitty:\n%s", d.log())
		}
		// The kitten draws Copy and Move zones in its middle; the drop goes
		// to the pane's upper left, where --drop-anywhere=copy holds.
		x, y := d.cell(panes[0], 4, 3)
		desk.drag(other.x+300, other.y+250, x, y)
		desk.screenshot(t, "after-drop")
		got := waitFile(t, filepath.Join(dst, "dragged.txt"), func(s string) bool { return s == "payload from the outside\n" }, "the kitten in the pane never copied the file")
		_ = os.WriteFile(filepath.Join(artifactDir(t), "copied.txt"), []byte(got), 0o644)
		for _, want := range []string{"host recv t=M", "host send t=r o=0 x=1", "host recv t=r o=0 x=1"} {
			if !strings.Contains(d.log(), want) {
				t.Errorf("the exchange with kitty lacks %q:\n%s", want, d.log())
			}
		}
	})

	t.Run("a file from another window drops on a plain pane as a paste", func(t *testing.T) {
		src := t.TempDir()
		file := filepath.Join(src, "dragged.txt")
		if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		other := desk.startKitty(t, "dndA", src, 600, 500, 0, 0, nil, kitten, "dnd", "dragged.txt")
		d, panes := startDeskTuios(t, desk, deskConfig, 2)
		defer d.saveLog("drop-on-plain-pane")
		d.run(panes[0], kittenTarget(kitten, t.TempDir()), "transfer the files here")
		out := filepath.Join(d.base, "pasted")
		d.run(panes[1], readerPane(out), "SINKREADY")
		x, y := d.cell(panes[1], 10, 5)
		desk.drag(other.x+300, other.y+250, x, y)
		want := "\x1b[200~" + file + " \x1b[201~"
		pasted := waitFile(t, out, func(s string) bool { return strings.Contains(s, "\x1b[201~") }, "the reader never got the paste")
		if pasted != want {
			t.Errorf("the reader got %q, want %q", pasted, want)
		}
	})

	t.Run("a file a pane drags drops on another window", func(t *testing.T) {
		src, dst := t.TempDir(), t.TempDir()
		if err := os.WriteFile(filepath.Join(src, "from-pane.txt"), []byte("payload from a pane\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		other := desk.startKitty(t, "dndA", dst, 600, 500, 0, 0, nil, kitten, "dnd", "--drop-anywhere=copy")
		d, panes := startDeskTuios(t, desk, deskConfig, 2)
		defer d.saveLog("drag-out")
		d.run(panes[0], "cd "+src+" && exec "+kitten+" dnd from-pane.txt", "from-pane.txt")
		d.run(panes[1], readerPane(filepath.Join(d.base, "pasted")), "SINKREADY")
		x, y := d.cell(panes[0], panes[0].Width/2, panes[0].Height/2)
		desk.screenshot(t, "before-drag")
		desk.drag(x, y, other.x+300, other.y+250)
		desk.screenshot(t, "after-drop")
		got := waitFile(t, filepath.Join(dst, "from-pane.txt"), func(s string) bool { return s == "payload from a pane\n" }, "the kitten in the other window never copied the file")
		_ = os.WriteFile(filepath.Join(artifactDir(t), "copied.txt"), []byte(got), 0o644)
		for _, want := range []string{"host recv t=o", "host send t=P o=0 x=-1", "host recv t=e o=0 x=4"} {
			if !strings.Contains(d.log(), want) {
				t.Errorf("the exchange with kitty lacks %q:\n%s", want, d.log())
			}
		}
		if hostOffers(d.log()) != 1 {
			t.Errorf("tuios offered kitty %d drags, want 1:\n%s", hostOffers(d.log()), d.log())
		}
	})

	t.Run("yazi in a pane drags a file to another window", func(t *testing.T) {
		yazi := os.Getenv("TUIOS_E2E_YAZI")
		if yazi == "" {
			t.Skip("TUIOS_E2E_YAZI is not set")
		}
		home, src, dst := t.TempDir(), t.TempDir(), t.TempDir()
		if err := os.WriteFile(filepath.Join(src, "drag-me.txt"), []byte("hello from yazi\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		other := desk.startKitty(t, "dndA", dst, 600, 500, 0, 0, nil, kitten, "dnd", "--drop-anywhere=copy")
		d, panes := startDeskTuios(t, desk, deskConfig, 2)
		defer d.saveLog("yazi-out")
		d.run(panes[0], "cd "+src+" && HOME="+home+" exec "+yazi, "drag-me.txt")
		d.run(panes[1], readerPane(filepath.Join(d.base, "pasted")), "SINKREADY")
		time.Sleep(time.Second)
		row, col := d.find(panes[0], "drag-me.txt")
		x, y := d.cell(panes[0], col+2, row)
		desk.drag(x, y, other.x+300, other.y+250)
		desk.screenshot(t, "after-drop")
		got := waitFile(t, filepath.Join(dst, "drag-me.txt"), func(s string) bool { return s == "hello from yazi\n" }, "the kitten in the other window never copied the file yazi dragged")
		_ = os.WriteFile(filepath.Join(artifactDir(t), "copied.txt"), []byte(got), 0o644)
		if !strings.Contains(d.log(), "host recv t=e o=0 x=4") {
			t.Errorf("kitty never told yazi's pane the drag finished:\n%s", d.log())
		}
	})

	t.Run("kitty carries a drag between two panes", func(t *testing.T) {
		src, dst := t.TempDir(), t.TempDir()
		if err := os.WriteFile(filepath.Join(src, "across.txt"), []byte("payload across\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		d, panes := startDeskTuios(t, desk, deskConfig, 2)
		defer d.saveLog("same-window")
		d.run(panes[0], "cd "+src+" && exec "+kitten+" dnd across.txt", "across.txt")
		d.run(panes[1], kittenTarget(kitten, dst), "transfer the files here")
		x1, y1 := d.cell(panes[0], panes[0].Width/2, panes[0].Height/2)
		x2, y2 := d.cell(panes[1], 4, 3)
		desk.drag(x1, y1, x2, y2)
		desk.screenshot(t, "after-drop")
		waitFile(t, filepath.Join(dst, "across.txt"), func(s string) bool { return s == "payload across\n" }, "the kitten in the other pane never copied the file")
		log := d.log()
		if !strings.Contains(log, "host recv t=M") {
			t.Errorf("kitty did not report the drop; tuios carried the drag itself:\n%s", log)
		}
		if hostDataRequests.MatchString(log) {
			t.Errorf("tuios asked kitty for the data of its own drag, which kitty refuses:\n%s", log)
		}
		if n := strings.Count(log, "host recv t=o"); n != 1 {
			t.Errorf("kitty reported %d gestures, want 1:\n%s", n, log)
		}
	})

	t.Run("a drag across text selects it, as without the bridge", func(t *testing.T) {
		d, panes := startDeskTuios(t, desk, deskConfig, 2)
		defer d.saveLog("own-gesture")
		d.run(panes[0], kittenTarget(kitten, t.TempDir()), "transfer the files here")
		d.run(panes[1], "printf '\\n  SELECTED%sWORDS  \\n' ''; "+readerPane(filepath.Join(d.base, "pasted")), "SINKREADY")
		row, col := d.find(panes[1], "SELECTEDWORDS")
		x1, y := d.cell(panes[1], col, row)
		x2, _ := d.cell(panes[1], col+len("SELECTEDWORDS")-1, row)
		desk.screenshot(t, "before-selection")
		desk.drag(x1, y, x2, y)
		time.Sleep(time.Second)
		desk.screenshot(t, "after-selection")
		deadline := time.Now().Add(uiTimeout)
		for !strings.Contains(desk.clipboard(), "SELECTEDWORDS") {
			if time.Now().After(deadline) {
				t.Fatalf("the selection never reached the clipboard; it holds %q\n%s", desk.clipboard(), d.log())
			}
			time.Sleep(100 * time.Millisecond)
		}
		log := d.log()
		if !strings.Contains(log, "host recv t=o") {
			t.Errorf("kitty did not report the gesture:\n%s", log)
		}
		if hostOffers(log) != 0 {
			t.Errorf("tuios offered a drag for a selection:\n%s", log)
		}
	})

	t.Run("with the switch off nothing is bridged", func(t *testing.T) {
		src, dst := t.TempDir(), t.TempDir()
		if err := os.WriteFile(filepath.Join(src, "dragged.txt"), []byte("payload from the outside\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		other := desk.startKitty(t, "dndA", src, 600, 500, 0, 0, nil, kitten, "dnd", "dragged.txt")
		d, panes := startDeskTuios(t, desk, dndConfig, 2)
		defer d.saveLog("switch-off")
		d.run(panes[0], kittenTarget(kitten, dst), "transfer the files here")
		d.run(panes[1], readerPane(filepath.Join(d.base, "pasted")), "SINKREADY")
		if strings.Contains(d.log(), "host probe sent") || strings.Contains(d.log(), "host send t=a") {
			t.Fatalf("tuios asked kitty about the protocol with the switch off:\n%s", d.log())
		}
		x, y := d.cell(panes[0], 4, 3)
		desk.drag(other.x+300, other.y+250, x, y)
		time.Sleep(3 * time.Second)
		if _, err := os.Stat(filepath.Join(dst, "dragged.txt")); err == nil {
			t.Errorf("the kitten in the pane got the file with the switch off")
		}
	})
}
