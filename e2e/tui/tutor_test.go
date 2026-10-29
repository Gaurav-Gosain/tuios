package tuie2e

import (
	"io/fs"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// TestTutorBasics walks the basics track of tuios tutor with the keys a
// person presses, and checks the promise the tutor makes: nothing on the
// machine changes.
//
// Four runs share one isolation root:
//
//  1. 80x24 at 16 colours: the menu, steps 1 to 3, ctrl+b of step 4 lit,
//     then SIGTERM.
//  2. 120x36 in truecolor: the menu offers Continue, and the whole track
//     runs, with frames of the menu, step 4, the hint and the finish at
//     120x36, 80x24 and 60x18, and the "make the window bigger" screen at
//     50x14. F2 and Continue are clicked, and ctrl+b q opens the menu. q at
//     the finish leaves.
//  3. With TUIOS_PANE_ID set, as in a tuios pane: the first screen says how
//     to give every key to the tutor.
//  4. A plain tuios, for the positive halves below.
//
// Across all of them:
//   - A listener on the daemon's socket, also named by TUIOS_SOCKET, takes no
//     connection from the tutor. tuios ls, after, does reach it.
//   - The tutor starts no process: no child of its pid is ever seen. The
//     plain tuios's shell is seen.
//   - The tutor's temp dir is in TMPDIR while it runs, and gone after q and
//     after SIGTERM.
//   - No file or folder appears under the isolation root except
//     $XDG_STATE_HOME/tuios/tutor.json.
//   - An Omarchy theme, Catppuccin Latte, sits under HOME. The plain tuios
//     follows it to a light ground. The tutor's card stays on the dark
//     default, because the tutor moves HOME away. NEGATIVE_CONTROLS.md
//     records the run without that move.
//
// Frames go under artifactDir as PNGs in the frame font.
func TestTutorBasics(t *testing.T) {
	base := t.TempDir()
	frames := artifactDir(t)
	useShippedLooks(base)
	omarchySwitch(t, base, "catppuccin-latte")

	// Every folder the harness makes, made now, so the tree below is the
	// one before any tutor ran.
	tmp := filepath.Join(base, "tmp")
	mustMkdir(tmp)
	for _, key := range xdgKeys {
		xdgDir(base, key)
	}
	workDirIn(t, base)
	for _, key := range []string{"XDG_CACHE_HOME", "XDG_DATA_HOME"} {
		mustMkdir(filepath.Join(base, key, "fish", "generated_completions"))
	}

	// The daemon's socket, with a listener that counts who dials it.
	sock := filepath.Join(xdgDir(base, "XDG_RUNTIME_DIR"), "tuios", "tuios.sock")
	mustMkdir(filepath.Dir(sock))
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen on %s: %v", sock, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var dials atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			dials.Add(1)
			_ = c.Close()
		}
	}()

	roots := []string{base, xdgDir(base, "XDG_RUNTIME_DIR")}
	before := tree(t, roots)
	progress := filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "tutor.json")
	env := []string{"TMPDIR=" + tmp, "TUIOS_SOCKET=" + sock}

	tutor := func(cols, rows int, extra ...string) (*tuitest.Terminal, *childWatch) {
		t.Helper()
		term := startIn(t, base, startOpts{
			cols: cols, rows: rows, args: []string{"tutor"}, animations: true,
			env: append(slices.Clone(env), extra...),
		})
		return term, watchChildren(term.Pid())
	}
	// sandboxes are the tutor's temp dirs in TMPDIR.
	sandboxes := func() []string {
		m, _ := filepath.Glob(filepath.Join(tmp, "tuios-tutor-*"))
		return m
	}
	shot16 := shot.XTermPalette()
	frame := func(term *tuitest.Terminal, name string) {
		t.Helper()
		saveArtifact(t, term, frames, name)
		savePNG(t, term.Screen(), shot16, frames, name)
	}
	// atSizes saves the frame at 120x36, 80x24 and 60x18, waiting at each
	// for markers, and goes back to 120x36.
	atSizes := func(term *tuitest.Terminal, name string, lesson bool, markers ...string) {
		t.Helper()
		for _, sz := range [][2]int{{120, 36}, {80, 24}, {60, 18}} {
			if err := term.Resize(sz[0], sz[1]); err != nil {
				t.Fatalf("resize to %dx%d: %v", sz[0], sz[1], err)
			}
			// The card says where the lesson is in its own words at each
			// size: "6 of 9" on the 4-row card, "6/9" on the strip. A frame
			// still drawn for the old size has the other one.
			want := markers
			if lesson {
				if sz[0] >= 80 && sz[1] >= 24 {
					want = append(slices.Clone(markers), " of 9")
				} else {
					want = append(slices.Clone(markers), "/9 ")
				}
			} else {
				// A screen of the tutor's own is drawn whole for each
				// size; give the frame for the new one time to land.
				time.Sleep(500 * time.Millisecond)
			}
			waitFor(t, term, name+" at "+strconv.Itoa(sz[0])+"x"+strconv.Itoa(sz[1]), func(s tuitest.Screen) bool {
				c, r := s.Size()
				return c == sz[0] && r == sz[1] && screenHas(s, want...) && (!lesson || cardAtFoot(s))
			})
			frame(term, name+"-"+strconv.Itoa(sz[0])+"x"+strconv.Itoa(sz[1]))
		}
		if err := term.Resize(120, 36); err != nil {
			t.Fatal(err)
		}
		waitFor(t, term, name+" back at 120x36", func(s tuitest.Screen) bool {
			c, _ := s.Size()
			return c == 120 && screenHas(s, markers...)
		})
	}
	press := func(term *tuitest.Terminal, keys ...any) {
		t.Helper()
		if err := term.SendKeys(keys...); err != nil {
			t.Fatalf("send %q: %v", keys, err)
		}
	}
	step := func(term *tuitest.Terminal, n int, title string) {
		t.Helper()
		waitScreen(t, term, "step "+strconv.Itoa(n), " "+strconv.Itoa(n)+" of 9", title)
	}

	// Run 1: 16 colours, three steps, SIGTERM.
	term, kids := tutor(80, 24, "TERM=xterm", "COLORTERM=")
	waitScreen(t, term, "the menu", "tuios tutor", "The basics", "Nothing on your computer changes")
	// With no progress the track's row is its plain title.
	if text := term.Screen().Text(); strings.Contains(text, "Continue") || strings.Contains(text, "over") {
		t.Errorf("a first run's menu offers to continue or start over:\n%s", text)
	}
	if len(sandboxes()) != 1 {
		t.Fatalf("while the tutor runs, TMPDIR holds %v, want its one temp dir", sandboxes())
	}
	frame(term, "16-menu")
	press(term, tuitest.Enter)
	step(term, 1, "Open a window")
	press(term, "n")
	step(term, 2, "Start typing in it")
	press(term, "i")
	step(term, 3, "Say hi to the shell")
	time.Sleep(insertGuard + 100*time.Millisecond)
	press(term, "fastfetch", tuitest.Enter)
	step(term, 4, "Split with the leader key")
	press(term, tuitest.Ctrl('b'))
	waitFor(t, term, "ctrl+b lit at 16 colours", func(s tuitest.Screen) bool { return capReversed(s, "[ctrl+b]") })
	frame(term, "16-step4-leader")
	kids.stop(t, "the 16 colour run")
	if err := syscall.Kill(term.Pid(), syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM: %v", err)
	}
	if _, err := term.WaitExit(uiTimeout); err != nil {
		t.Fatalf("the tutor did not end on SIGTERM: %v", err)
	}
	if left := sandboxes(); len(left) != 0 {
		t.Errorf("after SIGTERM the temp dir is still there: %v", left)
	}

	// Run 2: truecolor, Continue, the whole track.
	term, kids = tutor(120, 36, "COLORTERM=truecolor")
	waitScreen(t, term, "Continue on the menu", "Continue the basics", "3 steps of 9 done", "Start the basics over")
	atSizes(term, "menu", false, "Continue the basics", "Start the basics over")
	press(term, tuitest.Enter)
	step(term, 1, "Open a window")
	// The card sits on the default dark surface. Followed, the Omarchy
	// theme under HOME would make it light.
	if g := cardGround(term.Screen()); luminance(g) == 0 || luminance(g) > 128*1000 {
		t.Errorf("the card's ground is %+v: the tutor took the theme from the person's HOME (Catppuccin Latte), want the dark default", g)
	}
	press(term, "n")
	step(term, 2, "Start typing in it")
	press(term, "i")
	step(term, 3, "Say hi to the shell")
	time.Sleep(insertGuard + 100*time.Millisecond)
	press(term, "fastfetch", tuitest.Enter)
	step(term, 4, "Split with the leader key")
	unlit, ok := capGround(term.Screen(), "ctrl+b")
	if !ok {
		t.Fatalf("no ctrl+b keycap on the card:\n%s", term.Screen().Text())
	}
	press(term, tuitest.Ctrl('b'))
	waitFor(t, term, "ctrl+b lit", func(s tuitest.Screen) bool {
		g, ok := capGround(s, "ctrl+b")
		return ok && g != unlit
	})
	atSizes(term, "step4-leader", true, "Split with the leader key")
	press(term, "|")
	step(term, 5, "Back to window mode")

	// The mouse: F2 on the card opens the menu, Continue goes back.
	s := term.Screen()
	row, col, ok := textAt(s, "F2 menu", 0)
	if !ok {
		t.Fatalf("no F2 button on the card:\n%s", s.Text())
	}
	mouseClick(t, term, col+1, row, tuitest.MouseLeft, 0)
	waitScreen(t, term, "the menu after a click on F2", "Back one step", "Restart this track")
	s = term.Screen()
	row, col, ok = textAt(s, "Continue", 0)
	if !ok {
		t.Fatalf("no Continue row:\n%s", s.Text())
	}
	mouseClick(t, term, col+2, row, tuitest.MouseLeft, 0)
	step(term, 5, "Back to window mode")
	// A quit key of tuios opens the menu too.
	press(term, tuitest.Ctrl('b'), "q")
	waitScreen(t, term, "the menu after ctrl+b q", "Back one step", "Leave")
	press(term, tuitest.Esc)
	step(term, 5, "Back to window mode")

	press(term, tuitest.Ctrl('b'), tuitest.Esc)
	step(term, 6, "Hop between windows")
	// Idle: the hint comes after 8 seconds.
	waitFor(t, term, "the hint", func(s tuitest.Screen) bool {
		return screenHas(s, "Hint: In window mode, tab moves")
	}, 15*time.Second)
	atSizes(term, "step6-hint", true, "Hint:")
	// Too small: the lesson waits, and comes back.
	if err := term.Resize(50, 14); err != nil {
		t.Fatal(err)
	}
	waitScreen(t, term, "the too small screen", "Make the window bigger")
	frame(term, "too-small-50x14")
	if err := term.Resize(120, 36); err != nil {
		t.Fatal(err)
	}
	step(term, 6, "Hop between windows")

	press(term, tuitest.Tab)
	step(term, 7, "Close one")
	press(term, "x")
	step(term, 8, "Every key, one page")
	press(term, "?")
	time.Sleep(400 * time.Millisecond)
	press(term, tuitest.Esc)
	step(term, 9, "Or just search for it")
	press(term, tuitest.Ctrl('p'))
	time.Sleep(400 * time.Millisecond)
	press(term, "zoom")
	time.Sleep(200 * time.Millisecond)
	press(term, tuitest.Enter)
	waitScreen(t, term, "the finish", "The basics: done", "9 steps in")
	atSizes(term, "finish", false, "The basics: done")
	if len(sandboxes()) != 1 {
		t.Fatalf("while the tutor runs, TMPDIR holds %v, want its one temp dir", sandboxes())
	}
	kids.stop(t, "the truecolor run")
	press(term, "q")
	if _, err := term.WaitExit(uiTimeout); err != nil {
		t.Fatalf("the tutor did not end on q: %v", err)
	}
	if left := sandboxes(); len(left) != 0 {
		t.Errorf("after q the temp dir is still there: %v", left)
	}

	// Run 3: inside a tuios pane.
	term, kids = tutor(80, 24, "TUIOS_PANE_ID=e2e-pane", "TUIOS_SESSION=e2e")
	waitScreen(t, term, "the nested screen", "You are inside tuios", "ctrl+b then enter", "F12 gives the keys back")
	frame(term, "nested")
	press(term, tuitest.Enter)
	waitScreen(t, term, "the menu after the nested screen", "The basics", "done")
	kids.stop(t, "the nested run")
	press(term, "q")
	if _, err := term.WaitExit(uiTimeout); err != nil {
		t.Fatalf("the tutor did not end on q: %v", err)
	}

	// The machine after three tutors.
	if n := dials.Load(); n != 0 {
		t.Errorf("the daemon socket took %d connections from the tutor, want none", n)
	}
	after := tree(t, roots)
	var added []string
	for p := range after {
		if !before[p] {
			added = append(added, p)
		}
	}
	slices.Sort(added)
	want := []string{filepath.Dir(progress), progress}
	if !slices.Equal(added, want) {
		t.Errorf("the tutor runs left %q under the isolation root, want only %q", added, want)
	}
	if left := sandboxes(); len(left) != 0 {
		t.Errorf("a temp dir is left: %v", left)
	}

	// Positive halves. tuios ls reaches the listener, so the socket is the
	// one a tuios dials.
	_, _ = tuiosCLI(t, base, "ls")
	if dials.Load() == 0 {
		t.Errorf("tuios ls did not dial %s: the socket check above watched the wrong path", sock)
	}
	// A plain tuios follows the Omarchy theme under HOME, and runs a shell.
	plain := startIn(t, base, startOpts{env: []string{"COLORTERM=truecolor"}})
	plainKids := watchChildren(plain.Pid())
	waitBoot(t, plain)
	newWindow(t, plain)
	waitFor(t, plain, "a light ground from the Omarchy theme", func(s tuitest.Screen) bool {
		cols, rows := s.Size()
		for y := range rows {
			for x := range cols {
				if luminance(s.Cell(x, y).Bg) > 200*1000 {
					return true
				}
			}
		}
		return false
	})
	if len(plainKids.seen()) == 0 {
		time.Sleep(time.Second)
	}
	if len(plainKids.seen()) == 0 {
		t.Errorf("no child of a plain tuios was seen: the process check cannot see a shell")
	}
	plainKids.halt()
}

// waitFor waits for cond, uiTimeout unless a timeout is given.
func waitFor(t *testing.T, term *tuitest.Terminal, what string, cond func(tuitest.Screen) bool, timeout ...time.Duration) {
	t.Helper()
	d := uiTimeout
	if len(timeout) > 0 {
		d = timeout[0]
	}
	if err := term.WaitFor(cond, d); err != nil {
		t.Fatalf("waiting for %s: %v\n%s", what, err, term.Screen().Text())
	}
}

// cardAtFoot reports whether the card ends on the screen's last row: the
// full card's last row holds the tutor's keys, and the strip's first row,
// one up from the foot, holds F2.
func cardAtFoot(s tuitest.Screen) bool {
	_, rows := s.Size()
	return strings.Contains(s.Line(rows-1), "F2 menu") || strings.Contains(s.Line(rows-2), "F2 menu")
}

// cardRow is the screen row of the card's first line, the one with "of 9",
// or -1.
func cardRow(s tuitest.Screen) int {
	_, rows := s.Size()
	for y := rows - 1; y >= 0; y-- {
		if strings.Contains(s.Line(y), " of 9") {
			return y
		}
	}
	return -1
}

// cardGround is the background of the card's first cell.
func cardGround(s tuitest.Screen) tuitest.Color {
	y := cardRow(s)
	if y < 0 {
		return tuitest.Color{}
	}
	return s.Cell(0, y).Bg
}

// capGround is the ground under the first letter of the keycap reading
// label, on the card's second row.
func capGround(s tuitest.Screen, label string) (tuitest.Color, bool) {
	y := cardRow(s)
	if y < 0 {
		return tuitest.Color{}, false
	}
	r, c, ok := textAt(s, label, 0)
	if !ok || r != y+1 {
		return tuitest.Color{}, false
	}
	return s.Cell(c, r).Bg, true
}

// capReversed reports whether the bracketed keycap is in reverse video, as a
// lit key is at 16 colours.
func capReversed(s tuitest.Screen, label string) bool {
	r, c, ok := textAt(s, label, 0)
	return ok && s.Cell(c+1, r).Reverse
}

// tree lists every file and folder under roots, sockets aside.
func tree(t *testing.T, roots []string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&fs.ModeSocket != 0 {
				return nil
			}
			out[p] = true
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	return out
}

// childWatch records every child process of one pid, read from /proc every
// 50 ms, until it is stopped.
type childWatch struct {
	pid  int
	mu   sync.Mutex
	kids map[int]string
	quit chan struct{}
	once sync.Once
}

func watchChildren(pid int) *childWatch {
	w := &childWatch{pid: pid, kids: map[int]string{}, quit: make(chan struct{})}
	go func() {
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for {
			w.scan()
			select {
			case <-w.quit:
				return
			case <-tick.C:
			}
		}
	}()
	return w
}

func (w *childWatch) scan() {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	for _, e := range entries {
		n, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue
		}
		// pid (comm) state ppid ...: comm may hold spaces, so read after
		// its closing paren.
		s := string(stat)
		end := strings.LastIndexByte(s, ')')
		if end < 0 {
			continue
		}
		fields := strings.Fields(s[end+1:])
		if len(fields) < 2 {
			continue
		}
		if ppid, _ := strconv.Atoi(fields[1]); ppid == w.pid {
			w.mu.Lock()
			w.kids[n] = s[strings.IndexByte(s, '(')+1 : end]
			w.mu.Unlock()
		}
	}
}

func (w *childWatch) seen() map[int]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return maps.Clone(w.kids)
}

func (w *childWatch) halt() { w.once.Do(func() { close(w.quit) }) }

// stop ends the watch and fails the test if the tutor had a child.
func (w *childWatch) stop(t *testing.T, run string) {
	t.Helper()
	w.scan()
	w.halt()
	if kids := w.seen(); len(kids) > 0 {
		t.Errorf("in %s the tutor started processes: %v", run, kids)
	}
}
