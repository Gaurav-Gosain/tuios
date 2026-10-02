package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
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
		if railX >= len(line) || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(string(line[railX:]))), "agents") {
			continue
		}
		for r := y + 1; r < min(y+4, rows); r++ {
			runes := []rune(s.Line(r))
			if railX >= len(runes) {
				continue
			}
			tail := string(runes[railX:])
			if i := strings.Index(tail, name); i >= 0 {
				return r, railX + len([]rune(tail[:i])), true
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
// Frames are saved under artifactDir, including PNGs from internal/shot.
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
	if !strings.HasSuffix(stats, " motion=0") {
		t.Fatalf("the motion clock ran with nothing moving: %q", stats)
	}
}
