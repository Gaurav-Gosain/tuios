package tuie2e

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The picture-in-picture view (toggle_pip, p in window mode, and tuios pip)
// driven through a real PTY.
//
// How these could pass wrongly, written down first:
//   - The box found could be a pane's own border. It is found by its exact
//     size, 40 by 12 with the name on its top edge, and the panes here are
//     60 wide.
//   - The ticks in the box could be the pane's own output showing through.
//     The box sits over columns the pane's short lines never reach.
//   - "Live" could be one frame that happened to be current. The box has to
//     show a tick that moves on, and one within a few ticks of the pane.
//   - The cursor check could look only at the moments the test chose. A
//     sampler reads the screen every 40 ms for the whole test.
//   - A click could focus the pane through the tile under the box. The pane
//     the box shows is not under the box when it is clicked.

// pipBox is the view's box on the screen, in cells, inclusive of the border.
type pipBox struct {
	x0, y0, x1, y1 int
}

func (b pipBox) holds(col, row int) bool {
	return col >= b.x0 && col <= b.x1 && row >= b.y0 && row <= b.y1
}

// pipBoxWidth and pipBoxHeight are [pip]'s default size.
const (
	pipBoxWidth  = 40
	pipBoxHeight = 12
)

// findPiPBox finds a 40 by 12 box with name on its top edge.
func findPiPBox(s tuitest.Screen, name string) (pipBox, bool) {
	cols, rows := s.Size()
	for y := 0; y+pipBoxHeight-1 < rows; y++ {
		line := s.Line(y)
		if !strings.Contains(line, name) {
			continue
		}
		for x := 0; x+pipBoxWidth-1 < cols; x++ {
			if s.Cell(x, y).Content != "╭" || s.Cell(x+pipBoxWidth-1, y).Content != "╮" {
				continue
			}
			yb := y + pipBoxHeight - 1
			if s.Cell(x, yb).Content != "╰" || s.Cell(x+pipBoxWidth-1, yb).Content != "╯" {
				continue
			}
			var top strings.Builder
			for c := x; c < x+pipBoxWidth; c++ {
				top.WriteString(s.Cell(c, y).Content)
			}
			if strings.Contains(top.String(), " "+name+" ") {
				return pipBox{x, y, x + pipBoxWidth - 1, yb}, true
			}
		}
	}
	return pipBox{}, false
}

// pipBoxText is the text inside the box, one line per row.
func pipBoxText(s tuitest.Screen, b pipBox) string {
	var out strings.Builder
	for y := b.y0 + 1; y < b.y1; y++ {
		for x := b.x0 + 1; x < b.x1; x++ {
			c := s.Cell(x, y).Content
			if c == "" {
				c = " "
			}
			out.WriteString(c)
		}
		out.WriteByte('\n')
	}
	return out.String()
}

var pipTick = regexp.MustCompile(`PIPTICK-([0-9]+)`)

// lastTick is the highest tick in text, or -1.
func lastTick(text string) int {
	best := -1
	for _, m := range pipTick.FindAllStringSubmatch(text, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > best {
			best = n
		}
	}
	return best
}

// cornerOf names the corner of the screen the box is in.
func cornerOf(s tuitest.Screen, b pipBox) string {
	cols, rows := s.Size()
	v, h := "top", "left"
	if b.y0 > rows/2 {
		v = "bottom"
	}
	if b.x0 > cols/2 {
		h = "right"
	}
	return v + "-" + h
}

// waitPiP waits for the box to be drawn, and returns it.
func waitPiP(t *testing.T, term *tuitest.Terminal, name, what string) pipBox {
	t.Helper()
	var box pipBox
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		b, ok := findPiPBox(s, name)
		box = b
		return ok
	}, uiTimeout); err != nil {
		t.Fatalf("%s: no picture-in-picture view of %s on the screen\n%s", what, name, term.Snapshot())
	}
	return box
}

// waitNoPiP waits for the box to be gone.
func waitNoPiP(t *testing.T, term *tuitest.Terminal, name, what string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, ok := findPiPBox(s, name)
		return !ok
	}, uiTimeout); err != nil {
		t.Fatalf("%s: the picture-in-picture view is still on the screen\n%s", what, term.Snapshot())
	}
}

// cursorWatch reads the screen every 40 ms and counts the samples where the
// visible cursor is inside the view's box.
type cursorWatch struct {
	samples, covered atomic.Int64
	mu               sync.Mutex
	first            string
	stop             chan struct{}
	done             chan struct{}
}

func watchCursor(term *tuitest.Terminal, name string) *cursorWatch {
	w := &cursorWatch{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		tick := time.NewTicker(40 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-w.stop:
				return
			case <-tick.C:
			}
			s := term.Screen()
			box, ok := findPiPBox(s, name)
			col, row, visible := s.Cursor()
			if !ok || !visible {
				continue
			}
			w.samples.Add(1)
			if box.holds(col, row) {
				if w.covered.Add(1) == 1 {
					w.mu.Lock()
					w.first = fmt.Sprintf("cursor (%d,%d) inside box %+v", col, row, box)
					w.mu.Unlock()
				}
			}
		}
	}()
	return w
}

func (w *cursorWatch) finish(t *testing.T) {
	t.Helper()
	close(w.stop)
	<-w.done
	w.mu.Lock()
	defer w.mu.Unlock()
	t.Logf("cursor sampler: %d frames with the view and a visible cursor, %d with the cursor inside the view",
		w.samples.Load(), w.covered.Load())
	if w.covered.Load() > 0 {
		t.Errorf("the view covered the cursor in %d samples; first: %s", w.covered.Load(), w.first)
	}
	if w.samples.Load() < 20 {
		t.Errorf("the sampler saw the view with a cursor only %d times, too few to say anything", w.samples.Load())
	}
}

// windowIDNamed is the id of the window with this name in session work.
func windowIDNamed(t *testing.T, base, name string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "get-window", name, "--json", "--session", "work")
	if err != nil {
		t.Fatalf("get-window %s: %v\n%s", name, err, out)
	}
	var res struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.ID == "" {
		t.Fatalf("get-window %s: %v\n%s", name, err, out)
	}
	return res.ID
}

// startPiPPanes starts a client with two tiled panes, the right one named
// agent and printing a tick every 0.2 s, and returns with the agent pane
// focused in window mode. loop is the shell loop the agent pane runs.
func startPiPPanes(t *testing.T, base string, args []string, loop string) *tuitest.Terminal {
	t.Helper()
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: args})
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	enableTiling(t, term)
	renameWindow(t, term, "agent")
	enterTerminalMode(t, term)
	runInShell(t, term, loop, "PIPTICK-2", shellTimeout)
	leaveTerminalMode(t, term)
	return term
}

// toWindowMode leaves terminal mode when the dock says the client is in it.
// A click on a pane starts typing into it, so a click leaves terminal mode on.
func toWindowMode(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if strings.Contains(dockRows(term.Screen()), "Terminal mode") {
		leaveTerminalMode(t, term)
	}
}

// pinWithKey presses p in window mode and waits for the dock to say so.
func pinWithKey(t *testing.T, term *tuitest.Terminal, want string) {
	t.Helper()
	toWindowMode(t, term)
	if err := term.SendKeys("p"); err != nil {
		t.Fatalf("send p: %v", err)
	}
	if err := term.WaitForText(want, uiTimeout); err != nil {
		t.Fatalf("the dock never said %q after p\n%s", want, term.Snapshot())
	}
}

// assertLive waits for the box's tick to move on, and checks it against the
// pane's own screen.
func assertLive(t *testing.T, term *tuitest.Terminal, base string, daemon bool) {
	t.Helper()
	var first int
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		b, ok := findPiPBox(s, "agent")
		first = -1
		if ok {
			first = lastTick(pipBoxText(s, b))
		}
		return first > 0
	}, uiTimeout); err != nil {
		t.Fatalf("the view shows no tick from the agent pane\n%s", term.Snapshot())
	}
	var now int
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		b, ok := findPiPBox(s, "agent")
		if !ok {
			return false
		}
		now = lastTick(pipBoxText(s, b))
		return now >= first+3
	}, uiTimeout); err != nil {
		t.Fatalf("the view is stuck at tick %d\n%s", first, term.Snapshot())
	}
	if !daemon {
		return
	}
	pane, err := tuiosCLI(t, base, "capture-pane", "-s", "work", "-w", "agent")
	if err != nil {
		t.Fatalf("capture the agent pane: %v\n%s", err, pane)
	}
	if real := lastTick(pane); real < now || real-now > 5 {
		t.Fatalf("the view shows tick %d and the pane is at %d", now, real)
	}
	t.Logf("the view followed the agent pane from tick %d to %d", first, now)
}

// TestPiPWatchesAPaneFromTheCorner is the view end to end in a daemon session:
// pin with the key, watch it follow the pane from another pane, type until the
// cursor reaches the view's corner, click the view, unpin with the key, pin
// with tuios pip, and close the pane.
func TestPiPWatchesAPaneFromTheCorner(t *testing.T) {
	base := t.TempDir()
	term := startPiPPanes(t, base, []string{"new", "work"},
		`i=0; while :; do i=$((i+1)); echo PIPTICK-$i; sleep 0.2; done`)
	agentID := windowIDNamed(t, base, "agent")

	// Pin the focused pane. It has the focus, so no view is drawn yet.
	pinWithKey(t, term, "Pinned agent")
	if _, ok := findPiPBox(term.Screen(), "agent"); ok {
		t.Fatalf("the view is drawn while its own pane has the focus\n%s", term.Snapshot())
	}

	// Focus the other pane with a click on it. The view comes up in the
	// bottom-right corner and follows the agent pane.
	mouseClick(t, term, 20, 10, tuitest.MouseLeft, 0)
	box := waitPiP(t, term, "agent", "after focusing the other pane")
	if c := cornerOf(term.Screen(), box); c != "bottom-right" {
		t.Fatalf("the view is in the %s corner, want bottom-right\n%s", c, term.Snapshot())
	}
	watch := watchCursor(term, "agent")
	assertLive(t, term, base, true)
	t.Logf("the view over two panes:\n%s", term.Snapshot())

	// Zoom the focused pane over the whole region. The view stays on top of
	// it, and is now the only place the agent pane shows.
	toWindowMode(t, term)
	if err := term.SendKeys("z"); err != nil {
		t.Fatalf("send z: %v", err)
	}
	waitPiP(t, term, "agent", "over the zoomed pane")
	assertLive(t, term, base, true)

	// Type in the zoomed pane until the line runs into the view's corner. The
	// view moves off the bottom edge, since the line crosses both bottom
	// corners, and the sampler checks that it never covers the cursor.
	enterTerminalMode(t, term)
	runInShell(t, term, "seq 1 60; echo SEQ$((6*10))DONE", "SEQ60DONE", shellTimeout)
	for range 10 {
		if err := term.SendKeys(strings.Repeat("x", 10)); err != nil {
			t.Fatalf("type: %v", err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	box = waitPiP(t, term, "agent", "after typing into its corner")
	col, row, _ := term.Screen().Cursor()
	if c := cornerOf(term.Screen(), box); c != "top-right" {
		t.Fatalf("with the cursor at (%d,%d) the view is in the %s corner, want top-right\n%s",
			col, row, c, term.Snapshot())
	}
	if line := term.Screen().Line(row); !strings.Contains(line, "$ "+strings.Repeat("x", 100)) {
		t.Fatalf("the line being typed is not all on the screen: %q\n%s", line, term.Snapshot())
	}
	t.Logf("the view moved away from the cursor at (%d,%d):\n%s", col, row, term.Snapshot())
	assertLive(t, term, base, true)
	if err := term.SendKeys(tuitest.Ctrl('u')); err != nil {
		t.Fatalf("clear the line: %v", err)
	}
	leaveTerminalMode(t, term)
	if err := term.SendKeys("z"); err != nil {
		t.Fatalf("send z: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Count(s.Line(0), "╭") == 2 }, uiTimeout); err != nil {
		t.Fatalf("the zoom never ended\n%s", term.Snapshot())
	}

	// Move the agent pane to workspace 2. The view keeps following it.
	if out, err := tuiosCLI(t, base, "move-window", "2", "-w", "agent", "-s", "work"); err != nil {
		t.Fatalf("move-window: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Count(s.Line(0), "╭") == 1 }, uiTimeout); err != nil {
		t.Fatalf("the agent pane never left the workspace\n%s", term.Snapshot())
	}
	box = waitPiP(t, term, "agent", "with the agent pane on workspace 2")
	assertLive(t, term, base, true)

	// A click on the view goes to the agent pane on its workspace, and the
	// view goes away while that pane has the focus. The agent pane is not on
	// this workspace, so only the view can have taken the click there.
	mouseClick(t, term, (box.x0+box.x1)/2, (box.y0+box.y1)/2, tuitest.MouseLeft, 0)
	waitNoPiP(t, term, "agent", "after the click")
	deadline := time.Now().Add(uiTimeout)
	for focusedWindowID(t, base, "work") != agentID {
		if time.Now().After(deadline) {
			t.Fatalf("the click on the view did not focus the agent pane\n%s", term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(term.Screen().Text(), "PIPTICK-") {
		t.Fatalf("the jump did not show the agent pane\n%s", term.Snapshot())
	}

	// Back on workspace 1 the view comes back, and it still follows the
	// pane: leaving workspace 2 kept the pinned pane's stream.
	if err := term.SendKeys(tuitest.Alt('1')); err != nil {
		t.Fatalf("send alt+1: %v", err)
	}
	waitPiP(t, term, "agent", "back on workspace 1")
	assertLive(t, term, base, true)
	watch.finish(t)

	// p unpins from the other pane.
	pinWithKey(t, term, "Unpinned agent")
	waitNoPiP(t, term, "agent", "after the unpin")

	// tuios pip pins by name.
	out, err := tuiosCLI(t, base, "pip", "agent", "-s", "work")
	if err != nil || !strings.Contains(out, "Pinned") {
		t.Fatalf("tuios pip agent: %v\n%s", err, out)
	}
	waitPiP(t, term, "agent", "after tuios pip")
	// The unpin closed the stream of a pane on another workspace, and the pin
	// opened it again.
	assertLive(t, term, base, true)

	// The agent pane's shell exits. The view goes, and the dock says why.
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "work", "-w", "agent", "ctrl+c"); err != nil {
		t.Fatalf("stop the loop: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-text", "-s", "work", "-w", "agent", "exit\n"); err != nil {
		t.Fatalf("exit the shell: %v\n%s", err, out)
	}
	if err := term.WaitForText("agent closed", uiTimeout); err != nil {
		t.Fatalf("no dock note when the pinned pane closed\n%s", term.Snapshot())
	}
	waitNoPiP(t, term, "agent", "after the pane closed")
	t.Logf("after the pinned pane closed:\n%s", term.Snapshot())
}

// TestPiPInALocalSession is the view without a daemon: pin, watch from the
// other pane, and the pinned pane's shell exits while the view is up.
func TestPiPInALocalSession(t *testing.T) {
	base := t.TempDir()
	term := startPiPPanes(t, base, nil,
		`i=0; while [ $i -lt 45 ]; do i=$((i+1)); echo PIPTICK-$i; sleep 0.2; done; exit`)

	pinWithKey(t, term, "Pinned agent")
	mouseClick(t, term, 20, 10, tuitest.MouseLeft, 0)
	waitPiP(t, term, "agent", "after focusing the other pane")
	assertLive(t, term, base, false)
	t.Logf("the view in a local session:\n%s", term.Snapshot())

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "agent closed")
	}, 20*time.Second); err != nil {
		t.Fatalf("no dock note when the pinned pane's shell exited\n%s", term.Snapshot())
	}
	waitNoPiP(t, term, "agent", "after the pane closed")
}

// TestPiPDoesNotFreezeAcrossASessionSwitch pins a pane that sits on another
// workspace, where only the pin keeps its stream open, then switches to
// another session and back. The switch drops every stream. A pin that
// survived it came back naming the same pane with nothing streaming it, and
// the view showed a frozen screen. The switch now ends the pin: the view is
// either gone, or it is live.
func TestPiPDoesNotFreezeAcrossASessionSwitch(t *testing.T) {
	base := t.TempDir()
	// The other session exists before the client starts, so the client's
	// session list has it.
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "other", "--detach"); err != nil {
		t.Fatalf("create other: %v\n%s", err, out)
	}
	term := startPiPPanes(t, base, []string{"new", "work"},
		`i=0; while :; do i=$((i+1)); echo PIPTICK-$i; sleep 0.2; done`)
	pinWithKey(t, term, "Pinned agent")
	mouseClick(t, term, 20, 10, tuitest.MouseLeft, 0)
	if out, err := tuiosCLI(t, base, "move-window", "2", "-w", "agent", "-s", "work"); err != nil {
		t.Fatalf("move-window: %v\n%s", err, out)
	}
	waitPiP(t, term, "agent", "with the agent pane on workspace 2")
	assertLive(t, term, base, true)

	for _, want := range []string{"other", "work"} {
		if err := term.SendKeys(tuitest.Alt("N")); err != nil {
			t.Fatalf("next session: %v", err)
		}
		if err := term.WaitForText("Session: "+want, uiTimeout); err != nil {
			t.Fatalf("never landed on %s\n%s", want, term.Snapshot())
		}
	}
	// Long enough for a live view to move on by several ticks.
	time.Sleep(2 * time.Second)
	first, ok := findPiPBox(term.Screen(), "agent")
	if !ok {
		t.Logf("back on work, the pin is gone:\n%s", term.Snapshot())
		return
	}
	before := lastTick(pipBoxText(term.Screen(), first))
	time.Sleep(time.Second)
	b, _ := findPiPBox(term.Screen(), "agent")
	if after := lastTick(pipBoxText(term.Screen(), b)); after <= before {
		t.Fatalf("the view is frozen at tick %d after the session round trip\n%s", before, term.Snapshot())
	}
}
