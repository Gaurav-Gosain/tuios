package tuie2e

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/vt"
)

// The decorative motion, appearance.motion = full: an overlay fades in when it
// opens, and a working agent's row on the rail shimmers. Both are read off the
// cells the real binary sends, sampled while they move.

// fadeSample is the palette's title chip as one frame drew it.
type fadeSample struct {
	at time.Duration
	bg tuitest.Color
}

// sampleFade opens the palette and records the ground of its title chip on
// every screen that differs from the last, until the screen has held still for
// a while. The last sample is the chip as it settled.
func sampleFade(t *testing.T, term *tuitest.Terminal) []fadeSample {
	t.Helper()
	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("open the palette: %v", err)
	}
	start := time.Now()
	var out []fadeSample
	lastChange := start
	for time.Since(start) < 3*time.Second {
		s := term.Screen()
		row, col, ok := textAt(s, paletteTitle, 0)
		if ok {
			bg := s.Cell(col, row).Bg
			if len(out) == 0 || out[len(out)-1].bg != bg {
				out = append(out, fadeSample{at: time.Since(start), bg: bg})
				lastChange = time.Now()
			}
		}
		if len(out) > 0 && time.Since(lastChange) > 400*time.Millisecond {
			return out
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("the palette never settled\n%s", term.Snapshot())
	return nil
}

// TestOverlayFadesIn opens the command palette with the motion level at full
// on a truecolor terminal and watches its title chip come up: at least one
// frame has to show the chip at a colour other than the one it settles on,
// and the settled colour has to be the one the palette draws with no motion
// at all. Closing is instant: once the title is gone the screen does not
// change again.
//
// At 256 colours, and at the basic level, the chip is drawn at its final
// colour from its first frame.
//
// How this could pass wrongly, written down first:
//   - The in-between colour could be a frame of something else, such as the
//     title drawn before its chip: a sample is taken only once the title text
//     is on screen, and only its chip's ground is compared.
//   - A slow machine could coalesce the whole fade into one frame and the
//     test would then call a working fade broken. The palette is opened up to
//     three times and one fade seen is enough; a build with no fade shows
//     none in any of the three, which is the negative control.
//   - The settled colour could itself be wrong (a fade that never finishes):
//     it is compared with the chip of a client running with motion none.
func TestOverlayFadesIn(t *testing.T) {
	type run struct {
		name  string
		env   []string
		args  []string
		fades bool
	}
	runs := []run{
		{name: "truecolor-full", env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}, fades: true},
		{name: "256-full", env: []string{"TERM=xterm-256color", "COLORTERM="}},
		{name: "truecolor-basic", env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}, args: []string{"basic"}},
	}
	for _, r := range runs {
		t.Run(r.name, func(t *testing.T) {
			// The settled chip, from a client that never animates.
			still := startMotionClient(t, r.env, "none")
			settled := sampleFade(t, still)
			want := settled[len(settled)-1].bg
			if len(settled) != 1 {
				t.Fatalf("with motion none the chip changed colour while opening: %+v", settled)
			}

			level := "full"
			if len(r.args) > 0 {
				level = r.args[0]
			}
			term := startMotionClient(t, r.env, level)
			dir := artifactDir(t)
			var seen []fadeSample
			faded := false
			for attempt := range 3 {
				seen = sampleFade(t, term)
				saveArtifact(t, term, dir, fmt.Sprintf("open-%d", attempt))
				if got := seen[len(seen)-1].bg; got != want {
					t.Fatalf("the chip settled at %+v, and a client with no motion draws %+v\n%s", got, want, term.SnapshotStyled())
				}
				faded = len(seen) > 1
				closeAndHold(t, term)
				if faded || !r.fades {
					break
				}
			}
			t.Logf("chip grounds while opening: %+v", seen)
			if r.fades && !faded {
				t.Fatalf("the palette appeared at its final colour on every one of three opens; no fade was drawn")
			}
			if !r.fades && faded {
				t.Fatalf("the palette faded in where it must not: %+v", seen)
			}
		})
	}
}

// startMotionClient starts a client at the given motion level with the
// shipped looks and one pane, in window management mode.
func startMotionClient(t *testing.T, env []string, level string) *tuitest.Terminal {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, "[startup]\ntiled = true\n[appearance]\nmotion = \""+level+"\"\n")
	if out, err := tuiosCLI(t, base, "new", "e2e-motion", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-motion"}, shippedLooks: true, env: env, animations: true})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled: %v", err)
	}
	return term
}

// closeAndHold closes the palette and requires the screen to stay as the
// first closed frame drew it: a closing overlay is not animated.
func closeAndHold(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	closePalette(t, term, "after sampling the fade")
	first := term.Snapshot()
	styled := term.SnapshotStyled()
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if term.SnapshotStyled() != styled {
			t.Fatalf("the screen kept changing after the palette closed\nfirst:\n%s\nnow:\n%s", first, term.Snapshot())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// agentNameCells is where the working agent's name sits in the rail's agents
// section: the row under the section's header that carries the name.
func agentNameCells(s tuitest.Screen, name string) (row, col int, ok bool) {
	railX := railHeaderColumn(s)
	if railX < 0 {
		return 0, 0, false
	}
	_, rows := s.Size()
	for y := range rows {
		line := []rune(s.Line(y))
		if railX >= len(line) || !strings.HasPrefix(strings.TrimSpace(string(line[railX:])), "agents") {
			continue
		}
		for r := y + 1; r < min(y+4, rows); r++ {
			runes := []rune(s.Line(r))
			if railX >= len(runes) {
				continue
			}
			tail := string(runes[railX:])
			if before, _, ok := strings.Cut(tail, name); ok {
				return r, railX + len([]rune(before)), true
			}
		}
	}
	return 0, 0, false
}

// nameLook is how one frame drew the agent's name: every cell's ink and
// weight, as one comparable string.
func nameLook(s tuitest.Screen, row, col, n int) string {
	var b strings.Builder
	for x := col; x < col+n; x++ {
		c := s.Cell(x, row)
		fmt.Fprintf(&b, "%v/%v/%v;", c.Fg, c.Bold, c.Faint)
	}
	return b.String()
}

// sampleName records every distinct look of the name over d, and the screens
// of the first few, which are the frames worth looking at.
func sampleName(term *tuitest.Terminal, row, col, n int, d time.Duration) (map[string]bool, []tuitest.Screen) {
	looks := map[string]bool{}
	var frames []tuitest.Screen
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		s := term.Screen()
		l := nameLook(s, row, col, n)
		if !looks[l] && len(frames) < 6 {
			frames = append(frames, s)
		}
		looks[l] = true
		time.Sleep(3 * time.Millisecond)
	}
	return looks, frames
}

// TestWorkingAgentRowShimmers holds the working shimmer to its rules.
//
//   - A working agent's name on the rail changes look over time: its ink in
//     truecolor, faint and bold at 16 colours.
//   - When the agent stops working the name holds still, and the client stops
//     writing to the terminal: the motion clock has no reason to run.
//   - With motion none the working name never moves.
//
// How this could pass wrongly, written down first:
//   - The name could change for another reason, such as the elapsed figure
//     beside it ticking over. Only the name's own cells are compared, and the
//     elapsed figure is on the far side of the row.
//   - The name found could be the pane's row in the terminals section, which
//     never shimmers. It is looked for under the agents header only.
//   - The stop could be judged too early, while the last shimmer frame is
//     still in flight: the check waits for the idle glyph and then a settle
//     before it starts counting bytes.
//
// Frames are saved under artifactDir, including PNGs from tuios.dev/shot.
func TestWorkingAgentRowShimmers(t *testing.T) {
	for _, d := range []chromeDepth{chromeDepths[0], chromeDepths[2]} {
		for _, level := range []string{"full", "none"} {
			t.Run(d.name+"-"+level, func(t *testing.T) {
				shimmerRun(t, d, level)
			})
		}
	}
}

func shimmerRun(t *testing.T, d chromeDepth, level string) {
	const name = "shimworker"
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, "[startup]\ntiled = true\n[appearance]\nmotion = \""+level+"\"\n")
	if out, err := tuiosCLI(t, base, "new", "shim", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "shim", "--name", name); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "shim", "working", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	var wire byteCounter
	term := startIn(t, base, startOpts{args: []string{"attach", "shim"}, shippedLooks: true, env: d.env, animations: true, out: &wire})
	var row, col int
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		var ok bool
		row, col, ok = agentNameCells(s, name)
		return ok
	}, bootTimeout); err != nil {
		t.Fatalf("the working agent never reached the rail: %v\n%s", err, term.Snapshot())
	}
	dir := artifactDir(t)
	host := hostPalette(t, "")
	n := len(name)

	looks, frames := sampleName(term, row, col, n, 2500*time.Millisecond)
	saveArtifact(t, term, dir, "working")
	for i, f := range frames {
		savePNG(t, f, host, dir, fmt.Sprintf("working-%d", i))
	}
	t.Logf("distinct looks of the working name over 2.5s: %d", len(looks))
	switch level {
	case "full":
		if len(looks) < 3 {
			t.Fatalf("the working name took %d looks over 2.5s; the shimmer is not moving\n%s", len(looks), term.SnapshotStyled())
		}
		if d.name == "16" {
			faint := false
			for l := range looks {
				faint = faint || strings.Contains(l, "/true;")
			}
			if !faint {
				t.Fatalf("no 16-colour frame of the working name used faint or bold\n%s", term.SnapshotStyled())
			}
		}
	default:
		if len(looks) != 1 {
			t.Fatalf("with motion none the working name took %d looks\n%s", len(looks), term.SnapshotStyled())
		}
	}

	// The agent stops: the name settles and the client goes quiet.
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "shim", "idle"); err != nil {
		t.Fatalf("set-agent-state idle: %v\n%s", err, out)
	}
	time.Sleep(time.Second)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled after the agent stopped: %v", err)
	}
	r2, c2, ok := agentNameCells(term.Screen(), name)
	if !ok {
		t.Fatalf("the agent left the rail\n%s", term.Snapshot())
	}
	if still, _ := sampleName(term, r2, c2, n, time.Second); len(still) != 1 {
		t.Fatalf("an idle agent's name took %d looks in a second\n%s", len(still), term.SnapshotStyled())
	}
	before := wire.n.Load()
	time.Sleep(2 * time.Second)
	if wrote := wire.n.Load() - before; wrote > idleWireBudget/5 {
		t.Fatalf("with no working agent the client wrote %d bytes in 2s; the shimmer clock kept running", wrote)
	}
	saveArtifact(t, term, dir, "idle")
}

// TestFullMotionWithoutAgentsStaysIdle is TestIdleCostStaysLow at the motion
// level people get by default: three idle shells, no agent anywhere, motion
// full. Nothing moves, so nothing may be written.
//
// Bytes alone cannot see a timer that redraws the same frame, since nothing
// changed reaches the wire, so the client's own count of motion clock frames
// is read too, and it has to be zero.
func TestFullMotionWithoutAgentsStaysIdle(t *testing.T) {
	var wire byteCounter
	statsPath := filepath.Join(t.TempDir(), "tickstats")
	term, _ := start(t, startOpts{out: &wire, animations: true, env: []string{"TUIOS_STATS_FILE=" + statsPath}})
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 3, "opening three idle shells")
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled before idle: %v\n%s", err, term.Snapshot())
	}
	before := wire.n.Load()
	time.Sleep(5 * time.Second)
	idle := wire.n.Load() - before
	t.Logf("idle wire bytes over 5s at motion full: %d (budget %d)", idle, idleWireBudget)
	if idle > idleWireBudget {
		t.Fatalf("idle at motion full wrote %d bytes (budget %d)\n%s", idle, idleWireBudget, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "q"); err != nil {
		t.Fatalf("send leader q: %v", err)
	}
	waitExit(t, term, "idle motion test quit")
	data, err := os.ReadFile(statsPath)
	if err != nil {
		t.Fatalf("read tick stats: %v", err)
	}
	stats := strings.TrimSpace(string(data))
	t.Logf("tick stats: %s", stats)
	if !strings.Contains(stats, " motion=") {
		t.Fatalf("the stats file has no motion count: %q", stats)
	}
	if parseTickStats(stats)["motion"] != 0 {
		t.Fatalf("the motion clock ran with nothing moving: %q", stats)
	}
}

// Layout motion: a tiled pane slides from where it is drawn to where the
// layout puts it, and goes one way only. The client's render trace records
// every pane's rectangle in every frame it composes, and the host stream is
// replayed one presented frame at a time, so a pane that jumps ahead for a
// frame and then comes back is seen in both.

// hostTape records everything the client sends the host, from the start, so
// the stream can be replayed into an emulator one presented frame at a time.
type hostTape struct {
	mu   sync.Mutex
	b    bytes.Buffer
	full bool
}

const hostTapeLimit = 64 << 20

func (h *hostTape) Write(p []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.b.Len()+len(p) > hostTapeLimit {
		h.full = true
		return len(p), nil
	}
	h.b.Write(p)
	return len(p), nil
}

func (h *hostTape) len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.b.Len()
}

func (h *hostTape) bytes() ([]byte, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]byte(nil), h.b.Bytes()...), h.full
}

// presentedRows replays the tape and returns one row of the screen at the
// close of every synchronized update that ends after from: the row as each
// frame a conforming host presents shows it, and as no other.
func presentedRows(tape []byte, cols, rows, row, from int) []string {
	emu := vt.NewEmulator(cols, rows)
	var out []string
	pos := 0
	for {
		i := bytes.Index(tape[pos:], syncEnd)
		if i < 0 {
			return out
		}
		end := pos + i + len(syncEnd)
		_, _ = emu.Write(tape[pos:end])
		pos = end
		if end <= from {
			continue
		}
		var sb strings.Builder
		for x := range cols {
			if c := emu.CellAt(x, row); c != nil && c.Content != "" {
				sb.WriteString(c.Content)
			} else {
				sb.WriteString(" ")
			}
		}
		out = append(out, sb.String())
	}
}

// drawnRect is a pane's rectangle as one frame drew it.
type drawnRect struct{ X, Y, W, H int }

// traceFrames reads the frame lines the render trace wrote after offset:
// each frame's pane rectangles by short window id. syncs counts, for each
// frame, the state syncs that touched the given pane before it.
type traceFrame struct {
	anims int
	panes map[string]drawnRect
	// syncs is how many state syncs for watched were applied since the
	// previous frame.
	syncs int
}

func readTraceFrames(t *testing.T, path string, offset int64, watched string) []traceFrame {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the render trace: %v", err)
	}
	if int64(len(body)) < offset {
		t.Fatalf("the render trace shrank from %d to %d bytes", offset, len(body))
	}
	var frames []traceFrame
	pendingSyncs := 0
	for line := range strings.SplitSeq(string(body[offset:]), "\n") {
		if watched != "" && strings.Contains(line, " sync ") && strings.Contains(line, "id="+watched+" ") {
			pendingSyncs++
			continue
		}
		_, rest, ok := strings.Cut(line, " frame anims=")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		f := traceFrame{panes: map[string]drawnRect{}, syncs: pendingSyncs}
		pendingSyncs = 0
		f.anims, _ = strconv.Atoi(fields[0])
		for _, p := range fields[1:] {
			id, nums, ok := strings.Cut(p, "=")
			if !ok {
				continue
			}
			var r drawnRect
			if _, err := fmt.Sscanf(nums, "%d,%d,%d,%d", &r.X, &r.Y, &r.W, &r.H); err == nil {
				f.panes[id] = r
			}
		}
		frames = append(frames, f)
	}
	return frames
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}

// oneWay checks a sequence of values a transition drew: it starts at from,
// ends at to, never moves back toward from, and passes through at least
// three values in between, so the change was a slide and not a cut. It
// returns what is wrong, or "".
func oneWay(seq []int, from, to int) string {
	if len(seq) == 0 {
		return "no frames"
	}
	if seq[0] != from {
		return fmt.Sprintf("the first frame drew %d, want the starting %d", seq[0], from)
	}
	if seq[len(seq)-1] != to {
		return fmt.Sprintf("the last frame drew %d, want the target %d", seq[len(seq)-1], to)
	}
	dir := 1
	if to < from {
		dir = -1
	}
	for i := 1; i < len(seq); i++ {
		if (seq[i]-seq[i-1])*dir < 0 {
			return fmt.Sprintf("frame %d went back from %d to %d", i, seq[i-1], seq[i])
		}
		// Reaching the target and then leaving it is the same fault seen
		// from the other end: an early jump to the final size.
		if seq[i-1] == to && seq[i] != to {
			return fmt.Sprintf("frame %d reached the target %d early and left it again for %d", i-1, to, seq[i])
		}
	}
	between := map[int]bool{}
	for _, v := range seq {
		if v != from && v != to {
			between[v] = true
		}
	}
	if len(between) < 3 {
		return fmt.Sprintf("only %d values between %d and %d: the pane was cut, not slid", len(between), from, to)
	}
	return ""
}

// compactSeq drops repeats, so a sequence reads as the moves it made.
func compactSeq(seq []int) []int {
	var out []int
	for _, v := range seq {
		if len(out) == 0 || out[len(out)-1] != v {
			out = append(out, v)
		}
	}
	return out
}

// TestTiledPanesMoveOneWay opens a second pane next to a first in a BSP
// daemon session, swaps the two, and closes the second, with animations on.
// In each transition the first pane's rectangle, read from every frame the
// client composed, has to move from where it was to where the layout puts it
// in one direction, through in-between frames, and never jump ahead and back.
//
// The bug it pins: while the new pane opened, the first pane stayed full
// width, flashed to half width for a frame, went back to full width and then
// slid to half. The new pane's shell reported its directory (OSC 7, as fish
// and most prompts do) and the daemon broadcast the session. The broadcast
// carried this client's own layout, with each pane at its target, and the
// client wrote those rectangles over the panes that were still sliding and
// resized their guests. The next animation step drew them mid-slide again.
//
// How this could pass wrongly, written down first:
//   - No broadcast could land during the slide, and the client would never be
//     asked to adopt a rectangle. The new pane's shell reports a directory
//     every 40ms for the length of the slide, and the trace has to show a
//     state sync for the first pane between two frames of its slide.
//   - The trace could record rectangles the screen never shows. The open is
//     also read off the host stream at the close of every synchronized
//     update: the first pane's right edge on its top row has to move one way.
//   - A transition that is a cut has no reversal to find. Each one has to
//     pass through at least three values in between.
func TestTiledPanesMoveOneWay(t *testing.T) {
	cols, rows := 120, 40
	tape := &hostTape{}
	trace := filepath.Join(t.TempDir(), "render-trace.log")
	initFile := filepath.Join(t.TempDir(), "shrc")
	// Six directory reports over about a quarter of a second, each to a new
	// directory, so the daemon has a change to broadcast throughout a slide.
	shrc := `i=0; while [ $i -lt 6 ]; do printf '\033]7;file://localhost/tmp/%s\033\\' $i; i=$((i+1)); sleep 0.04; done` + "\n"
	if err := os.WriteFile(initFile, []byte(shrc), 0o644); err != nil {
		t.Fatalf("write the shell rc: %v", err)
	}
	term, base := start(t, startOpts{cols: cols, rows: rows, args: []string{"new", "e2e-motion"}, animations: true, out: tape,
		env: []string{"ENV=" + initFile, "TUIOS_RENDER_TRACE=" + trace}})
	killDaemon(t, base)
	waitBoot(t, term)
	newWindow(t, term)
	enableTiling(t, term)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled: %v", err)
	}
	dir := artifactDir(t)

	// The first pane, alone on the screen.
	settled := readTraceFrames(t, trace, 0, "")
	if len(settled) == 0 {
		t.Fatalf("the render trace holds no frames")
	}
	last := settled[len(settled)-1]
	if len(last.panes) != 1 {
		t.Fatalf("want one pane before the open, the last frame drew %v", last.panes)
	}
	var first string
	var full drawnRect
	for id, r := range last.panes {
		first, full = id, r
	}

	// step runs one transition and returns the frames it composed.
	step := func(name string, act func()) []traceFrame {
		t.Helper()
		mark := fileSize(t, trace)
		act()
		if err := term.WaitStable(uiTimeout); err != nil {
			t.Fatalf("%s: screen never settled: %v", name, err)
		}
		frames := readTraceFrames(t, trace, mark, first)
		var b strings.Builder
		for _, f := range frames {
			fmt.Fprintf(&b, "anims=%d syncs=%d %v\n", f.anims, f.syncs, f.panes)
		}
		_ = os.WriteFile(filepath.Join(dir, name+"-frames.txt"), []byte(b.String()), 0o644)
		saveArtifact(t, term, dir, name)
		return frames
	}
	firstPane := func(frames []traceFrame, pick func(drawnRect) int) []int {
		var seq []int
		for _, f := range frames {
			if r, ok := f.panes[first]; ok {
				seq = append(seq, pick(r))
			}
		}
		return seq
	}
	width := func(r drawnRect) int { return r.W }
	left := func(r drawnRect) int { return r.X }

	// Open: the first pane narrows from the full box to its half.
	tapeMark := tape.len()
	opened := step("open", func() { newWindow(t, term) })
	half := opened[len(opened)-1].panes[first]
	widths := firstPane(opened, width)
	t.Logf("open: first pane widths %v", compactSeq(widths))
	if msg := oneWay(widths, full.W, half.W); msg != "" {
		t.Errorf("open: the first pane's width %s: %v", msg, compactSeq(widths))
	}
	// The broadcast has to have landed while the pane was sliding, or the
	// open never asked the client to adopt anything.
	slideSyncs := 0
	for i, f := range opened {
		if i > 0 && opened[i-1].anims > 0 && f.anims > 0 {
			slideSyncs += f.syncs
		}
	}
	if slideSyncs == 0 {
		t.Errorf("open: no state sync for the first pane landed during its slide, so this run proves nothing")
	}

	// The same open as the host presented it: the first pane's right edge is
	// the first border cell right of its left border on its top row. Where
	// the new pane is drawn over it, that is the new pane's left edge, which
	// moves the same way.
	raw, full2 := tape.bytes()
	if full2 {
		t.Fatalf("the host tape filled up")
	}
	// The stream and the offset the open starts at, so the frames can be
	// replayed and drawn again outside the test.
	_ = os.WriteFile(filepath.Join(dir, "host-stream.bin"), raw, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "open-offset.txt"), []byte(strconv.Itoa(tapeMark)+"\n"), 0o644)
	var edges []int
	for _, row := range presentedRows(raw, cols, rows, full.Y, tapeMark) {
		for x, r := range []rune(row) {
			if x > full.X && strings.ContainsRune("│╮╭┐┌", r) {
				edges = append(edges, x)
				break
			}
		}
	}
	t.Logf("open: presented right edges %v", compactSeq(edges))
	if msg := oneWay(edges, full.X+full.W-1, half.X+half.W-1); msg != "" {
		t.Errorf("open: on the host, the first pane's right edge %s: %v", msg, compactSeq(edges))
	}

	// Swap: the focus is on the new pane, on the right. H trades it with the
	// first pane, which slides right into its slot.
	swapped := step("swap", func() {
		if err := term.SendKeys("H"); err != nil {
			t.Fatalf("send H: %v", err)
		}
	})
	lefts := firstPane(swapped, left)
	t.Logf("swap: first pane left edges %v", compactSeq(lefts))
	slot := swapped[len(swapped)-1].panes[first]
	if slot.X <= half.X {
		t.Errorf("swap: the first pane ended at x=%d, want it right of x=%d", slot.X, half.X)
	}
	if msg := oneWay(lefts, half.X, slot.X); msg != "" {
		t.Errorf("swap: the first pane's left edge %s: %v", msg, compactSeq(lefts))
	}

	// Close: the focused new pane closes and the first pane takes the box.
	closed := step("close", func() { closeFocusedWindow(t, term, 1) })
	widths = firstPane(closed, width)
	lefts = firstPane(closed, left)
	t.Logf("close: first pane widths %v, left edges %v", compactSeq(widths), compactSeq(lefts))
	if msg := oneWay(widths, slot.W, full.W); msg != "" {
		t.Errorf("close: the first pane's width %s: %v", msg, compactSeq(widths))
	}
	if msg := oneWay(lefts, slot.X, full.X); msg != "" {
		t.Errorf("close: the first pane's left edge %s: %v", msg, compactSeq(lefts))
	}
}
