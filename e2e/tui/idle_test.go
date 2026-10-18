package tuie2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// byteCounter tallies bytes written to it. tuitest mirrors PTY traffic from its
// own reader goroutine, so the tally is atomic.
type byteCounter struct{ n atomic.Int64 }

func (b *byteCounter) Write(p []byte) (int, error) {
	b.n.Add(int64(len(p)))
	return len(p), nil
}

// wireRecorder counts the bytes written to it and keeps the first
// wireRecorderKeep of them, for a test that looks for one sequence.
type wireRecorder struct {
	byteCounter
	mu  sync.Mutex
	buf []byte
}

// wireRecorderKeep is how much of the stream a wireRecorder keeps.
const wireRecorderKeep = 4 << 20

func (w *wireRecorder) Write(p []byte) (int, error) {
	w.mu.Lock()
	if room := wireRecorderKeep - len(w.buf); room > 0 {
		w.buf = append(w.buf, p[:min(room, len(p))]...)
	}
	w.mu.Unlock()
	return w.byteCounter.Write(p)
}

// contains reports whether the kept stream holds s.
func (w *wireRecorder) contains(s string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return bytes.Contains(w.buf, []byte(s))
}

// idleWireBudget bounds the bytes tuios may write during a 10s idle window. A
// held frame-skip writes nothing; the budget tolerates a stray settling frame
// or two but is far below what a leaking 10Hz render of changing content costs.
const idleWireBudget = 2048

// idleWindow is how long TestIdleCostStaysLow leaves the client alone.
const idleWindow = 10 * time.Second

// idleBudget is what one idle window of TestIdleCostStaysLow may cost. Each
// count is the difference between the client's counters at the two ends of the
// window.
type idleBudget struct {
	// wire is the most bytes written to the terminal, and minWire the
	// fewest: a clock that ticks must reach the screen.
	wire, minWire int64
	// ticks is the maintenance ticks handled, and work those that ran the
	// full scans.
	ticks, work uint64
	// tickerWakes is how often a frame woke Bubble Tea's frame ticker to the
	// frame rate.
	tickerWakes uint64
	// ctxsw is the client's voluntary context switches, all threads, on
	// Linux only.
	ctxsw int64
}

// TestIdleCostStaysLow is the idle-cost regression guard for the whole program:
// one client, three idle shells. Over a 10s idle window the client must write
// almost nothing, and it must not wake for nothing: the maintenance ticks, the
// frame ticker wakes, and on Linux the context switches each have a budget.
//
// With the clock on, the dock draws one frame a second. Those frames go out
// through kickFlush and must not wake the frame ticker: before they did, a
// clock with seconds cost about 35 frame ticks a second for one frame.
//
// Negative controls are in NEGATIVE_CONTROLS.md. In-process, tick work at idle
// is guarded by BenchmarkIdleTick and TestIdleTickSkipsScans. See docs/perf.md.
func TestIdleCostStaysLow(t *testing.T) {
	t.Run("clock off", func(t *testing.T) {
		runIdleWindow(t, "[appearance.selection]\nosc52_write = \"on\"\n", idleBudget{
			wire: idleWireBudget,
			// The tick is armed only for a job that is due, and none is.
			ticks: 2, work: 1,
			tickerWakes: 1,
			ctxsw:       400,
		}, nil, clipboardAtIdle)
	})
	t.Run("clock on", func(t *testing.T) {
		// With seconds, so the window holds ten clock frames. The default
		// layout has no seconds and draws once a minute.
		runIdleWindow(t, "[appearance]\nshow_clock = true\nclock_format = \"15:04:05\"\n", idleBudget{
			// Ten clock frames, and they must go out.
			wire: 4 * idleWireBudget, minWire: 100,
			ticks: 2, work: 1,
			// Was 10, one per clock frame.
			tickerWakes: 1,
			ctxsw:       400,
		}, nil, nil)
	})
	t.Run("one late output", func(t *testing.T) {
		// A pane writes once, inside the window, while the client is idle.
		// The frame for it wakes the frame ticker in View, after Update has
		// armed the next tick, so the tick that stops the ticker again is
		// armed for the flush that follows. Without it the ticker ran at the
		// frame rate until the next message, which here never comes.
		runIdleWindow(t, "", idleBudget{
			wire: idleWireBudget,
			// The idle switch after the output's frame.
			ticks: 2, work: 1,
			// The output's frame.
			tickerWakes: 1,
			// About 300 with the ticker stopped again after the frame, and
			// about 2700 with it left running.
			ctxsw: 800,
		}, writeLate, sawLateOutput)
	})
	t.Run("git section", func(t *testing.T) {
		// The rail's git section reads the focused pane's repository every
		// 2 s, and each reading composes a frame equal to the last one. That
		// frame must not wake the frame ticker: it has nothing to write.
		runIdleWindowOpts(t, idleGitConfig, startOpts{daemonDefault: true}, idleBudget{
			wire: idleWireBudget,
			// One tick for each reading's due time, about five.
			ticks: 8, work: 1,
			// Five, one per reading, when an equal frame woke it.
			tickerWakes: 1,
			// 260 to 300, and 1186 with an equal frame waking the ticker.
			ctxsw: 600,
		}, gitRepoPane, nil)
	})
}

// idleGitConfig is the rail with its git section, in a daemon session: a
// standalone client draws no git section.
const idleGitConfig = `[startup]
daemon = true

[appearance.sidebar]
enabled = true
position = "left"
width = 28
sections = "sessions,git"
`

// gitRepoPane puts the focused pane in a new repository and waits for the
// rail's git section to show it.
func gitRepoPane(t *testing.T, term *tuitest.Terminal, base string) {
	t.Helper()
	dir := filepath.Join(base, "repo")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	enterTerminalMode(t, term)
	runInShell(t, term, "cd "+dir+` && printf '\033]7;file://%s\033\\%s\n' "$PWD" mar""ked`, "marked", uiTimeout)
	leaveTerminalMode(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return railHeaderRow(s, "git") >= 0 && !strings.Contains(s.Text(), "Window management mode")
	}, uiTimeout); err != nil {
		t.Fatalf("the git section never showed the repository: %v\n%s", err, term.Snapshot())
	}
}

// lateOutputAfter is how long after writeLate returns its pane writes

// lateOutputAfter is how long after writeLate returns its pane writes: past
// the settle and the second runIdleWindow waits, so inside the idle window.
const lateOutputAfter = 4 * time.Second

// writeLate has the focused pane write one word lateOutputAfter from now, and
// nothing after it: the shell keeps sleeping, so no prompt follows. The pane
// waits for a file rather than for a time, so the mode switches and the
// message they show have run out before the window starts.
func writeLate(t *testing.T, term *tuitest.Terminal, base string) {
	t.Helper()
	gate := filepath.Join(base, "late-output")
	enterTerminalMode(t, term)
	typeLine(t, term, fmt.Sprintf(`while [ ! -e %s ]; do sleep 0.1; done; printf LATE""OUT; sleep 600`, gate))
	leaveTerminalMode(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Window management mode")
	}, uiTimeout); err != nil {
		t.Fatalf("the mode message never went: %v\n%s", err, term.Snapshot())
	}
	timer := time.AfterFunc(lateOutputAfter, func() { _ = os.WriteFile(gate, nil, 0o600) })
	t.Cleanup(func() { timer.Stop() })
}

// sawLateOutput checks that writeLate's word reached the screen, then ends
// the pane's sleep so the client quits without asking.
func sawLateOutput(t *testing.T, term *tuitest.Terminal, _ *wireRecorder) {
	t.Helper()
	if err := term.WaitForText("LATEOUT", uiTimeout); err != nil {
		t.Fatalf("the pane's late output never reached the screen: %v\n%s", err, term.Snapshot())
	}
	enterTerminalMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('c')); err != nil {
		t.Fatalf("send ctrl+c: %v", err)
	}
	runInShell(t, term, `echo back""again`, "backagain", uiTimeout)
	leaveTerminalMode(t, term)
}

// runIdleWindow starts a client with config (none when empty), opens three
// idle shells, and holds one idle window to budget. setup, when set, runs
// once the shells are open, with the client's base directory. after, when set,
// runs on the idle client once the window is over.
func runIdleWindow(t *testing.T, config string, budget idleBudget, setup func(*testing.T, *tuitest.Terminal, string), after func(*testing.T, *tuitest.Terminal, *wireRecorder)) {
	runIdleWindowOpts(t, config, startOpts{}, budget, setup, after)
}

// runIdleWindowOpts is runIdleWindow with start options: daemonDefault for a
// client attached to a daemon, which the rail's git section needs.
func runIdleWindowOpts(t *testing.T, config string, o startOpts, budget idleBudget, setup func(*testing.T, *tuitest.Terminal, string), after func(*testing.T, *tuitest.Terminal, *wireRecorder)) {
	base := t.TempDir()
	if config != "" {
		writeConfig(t, base, config)
	}
	wire := &wireRecorder{}
	statsPath := filepath.Join(base, "tickstats")
	o.out = wire
	o.env = append(o.env, "TUIOS_STATS_FILE="+statsPath)
	term := startIn(t, base, o)
	waitBoot(t, term)

	newWindow(t, term)
	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 3, "opening three idle shells")
	if setup != nil {
		setup(t, term, base)
	}
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled before idle: %v\n%s", err, term.Snapshot())
	}
	// Past idleTickerAfter, so the frame ticker has gone idle.
	time.Sleep(time.Second)

	pid := term.Pid()
	start := sampleIdle(t, pid, statsPath)
	before := wire.n.Load()
	time.Sleep(idleWindow)
	idleBytes := wire.n.Load() - before
	end := sampleIdle(t, pid, statsPath)

	got := idleBudget{
		wire:        idleBytes,
		ticks:       end.stats["ticks"] - start.stats["ticks"],
		work:        end.stats["work"] - start.stats["work"],
		tickerWakes: end.stats["tickerWakes"] - start.stats["tickerWakes"],
		ctxsw:       -1,
	}
	if start.ctxsw >= 0 && end.ctxsw >= 0 {
		got.ctxsw = end.ctxsw - start.ctxsw
	}
	t.Logf("idle window %v: wire=%dB ticks=%d work=%d tickerWakes=%d ctxsw=%d",
		idleWindow, got.wire, got.ticks, got.work, got.tickerWakes, got.ctxsw)

	var over []string
	if got.wire > budget.wire {
		over = append(over, fmt.Sprintf("wrote %d bytes (budget %d): a timer-driven render is leaking", got.wire, budget.wire))
	}
	if got.wire < budget.minWire {
		over = append(over, fmt.Sprintf("wrote %d bytes, under the %d the clock's frames take: they did not go out", got.wire, budget.minWire))
	}
	if got.ticks > budget.ticks {
		over = append(over, fmt.Sprintf("handled %d maintenance ticks (budget %d)", got.ticks, budget.ticks))
	}
	if got.work > budget.work {
		over = append(over, fmt.Sprintf("%d ticks ran the full scans (budget %d): the idle gate is not holding", got.work, budget.work))
	}
	if got.tickerWakes > budget.tickerWakes {
		over = append(over, fmt.Sprintf("woke the frame ticker %d times (budget %d)", got.tickerWakes, budget.tickerWakes))
	}
	if got.ctxsw > budget.ctxsw {
		over = append(over, fmt.Sprintf("made %d context switches (budget %d)", got.ctxsw, budget.ctxsw))
	}
	if start.stats["ticks"] == 0 && end.stats["ticks"] == 0 && start.stats["render"] == 0 {
		over = append(over, "the stats file holds only zeros; the counter pipeline is broken")
	}
	if len(over) > 0 {
		t.Fatalf("idle window over budget:\n  %s\n%s", strings.Join(over, "\n  "), term.Snapshot())
	}
	if after != nil {
		after(t, term, wire)
	}

	// Standalone with only idle shells quits at once on leader-q. A daemon
	// client detaches on leader-d.
	quit := "q"
	if o.daemonDefault {
		quit = "d"
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), quit); err != nil {
		t.Fatalf("send leader %s: %v", quit, err)
	}
	waitExit(t, term, "idle test quit")
}

// clipboardAtIdle has a pane on a hidden workspace set the clipboard while
// the client is idle, and the OSC 52 write must reach the terminal at once.
//
// Bubble Tea writes a clipboard set from the frame ticker, and an idle client
// stops that ticker. A hidden pane's output composes no frame and so wakes
// nothing, which leaves the write to the flush Update asks for after every
// message while the ticker is stopped (flushWhileIdle). Without it the write
// waits for the next key.
func clipboardAtIdle(t *testing.T, term *tuitest.Terminal, wire *wireRecorder) {
	t.Helper()
	const (
		delay = 3 * time.Second
		// "idle-clip" in base64.
		payload = "aWRsZS1jbGlw"
	)
	enterTerminalMode(t, term)
	typeLine(t, term, fmt.Sprintf(`sleep %d; printf '\033]52;c;%s\007'`, int(delay.Seconds()), payload))
	typed := time.Now()
	// Workspace 2 has no panes: the one that writes is hidden, and its
	// output draws nothing.
	if err := term.SendKeys(tuitest.Ctrl('b'), "w", "2"); err != nil {
		t.Fatalf("switch to workspace 2: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 0 }, uiTimeout); err != nil {
		t.Fatalf("workspace 2 never showed: %v\n%s", err, term.Snapshot())
	}
	if wire.contains(payload) {
		t.Fatal("the clipboard write went out before the pane made it")
	}
	// The write is due delay after the command was typed. A ticker at
	// IdleFPS would carry it within 100 ms. Nothing else happens on screen,
	// so a stopped ticker with no flush carries it never.
	deadline := typed.Add(delay + time.Second)
	for !wire.contains(payload) {
		if time.Now().After(deadline) {
			t.Fatalf("a hidden pane's clipboard write did not reach the terminal within 1s of the pane making it, "+
				"with the client idle\n%s", term.Snapshot())
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Logf("the clipboard write reached the terminal %v after the command was typed", time.Since(typed).Round(time.Millisecond))
}

// idleSample is a client's counters at one moment.
type idleSample struct {
	// stats is the line DumpTickStats writes, by name.
	stats map[string]uint64
	// ctxsw is the context switches of all its threads, -1 without /proc.
	ctxsw int64
}

// sampleIdle asks the client for its tick counters with SIGUSR1, waits for
// the file, and reads the client's context switches next to it.
func sampleIdle(t *testing.T, pid int, statsPath string) idleSample {
	t.Helper()
	_ = os.Remove(statsPath)
	if err := syscall.Kill(pid, syscall.SIGUSR1); err != nil {
		t.Fatalf("signal the client for its counters: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var data []byte
	for {
		var err error
		data, err = os.ReadFile(statsPath)
		if err == nil && strings.HasSuffix(string(data), "\n") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the client wrote no counters within 5s of SIGUSR1 (%v)", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	s := idleSample{stats: parseTickStats(string(data)), ctxsw: procCtxSwitches(pid)}
	return s
}

// parseTickStats reads a "name=N name=N" line.
func parseTickStats(line string) map[string]uint64 {
	out := map[string]uint64{}
	for field := range strings.FieldsSeq(line) {
		k, v, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		n, _ := strconv.ParseUint(v, 10, 64)
		out[k] = n
	}
	return out
}

// procCtxSwitches sums the voluntary context switches of every thread of pid,
// -1 where there is no /proc. A voluntary switch is a thread that went to
// sleep, which at idle is a wakeup that came before it. The involuntary ones
// count how busy the machine is, not the client.
func procCtxSwitches(pid int) int64 {
	dir := filepath.Join("/proc", strconv.Itoa(pid), "task")
	tasks, err := os.ReadDir(dir)
	if err != nil {
		return -1
	}
	var n int64
	for _, task := range tasks {
		data, err := os.ReadFile(filepath.Join(dir, task.Name(), "status"))
		if err != nil {
			continue // the thread ended between the two reads
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			if rest, ok := strings.CutPrefix(line, "voluntary_ctxt_switches:"); ok {
				v, _ := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
				n += v
			}
		}
	}
	return n
}

// readTickStats parses the "ticks=N work=N render=N" line DumpTickStats writes.
func readTickStats(t *testing.T, path string) (ticks, work, render uint64) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read tick stats %s: %v", path, err)
	}
	st := parseTickStats(string(data))
	return st["ticks"], st["work"], st["render"]
}

// TestAgentsViewOpenAddsNoIdleCost holds the agents view to its idle budget:
// open over agents at rest, it costs what the same client costs with it
// closed. One client, three shells marked as agents, and two idle windows
// of 10s: closed, then open. The open view arms the minute tick that moves
// the ages on, which does not fire inside a window, and draws nothing while
// nothing changes.
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: with the minute tick
// at one second the open window counts ten ticks of the view, and the check
// fails.
func TestAgentsViewOpenAddsNoIdleCost(t *testing.T) {
	base := t.TempDir()
	wire := &wireRecorder{}
	statsPath := filepath.Join(base, "tickstats")
	term := startIn(t, base, startOpts{daemonDefault: true, out: wire, env: []string{"TUIOS_STATS_FILE=" + statsPath}})
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 3, "opening three idle shells")
	out, err := tuiosCLI(t, base, "list-windows", "--json")
	if err != nil {
		t.Fatalf("list-windows: %v: %s", err, out)
	}
	var reply struct {
		Windows []struct {
			ID string `json:"window_id"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &reply); err != nil || len(reply.Windows) < 3 {
		t.Fatalf("list-windows json: %v: %s", err, out)
	}
	for i, state := range []string{"idle", "done", "idle"} {
		if out, err := tuiosCLI(t, base, "set-agent-state", state, "-w", reply.Windows[i].ID, "--harness", "claude-code"); err != nil {
			t.Fatalf("report agent %d: %v\n%s", i, err, out)
		}
	}
	var openAgents, openFrames uint64
	window := func(name string) idleBudget {
		if err := term.WaitStable(uiTimeout); err != nil {
			t.Fatalf("%s: screen never settled: %v", name, err)
		}
		time.Sleep(time.Second)
		pid := term.Pid()
		start := sampleIdle(t, pid, statsPath)
		before := wire.n.Load()
		time.Sleep(idleWindow)
		end := sampleIdle(t, pid, statsPath)
		got := idleBudget{
			wire:        wire.n.Load() - before,
			ticks:       end.stats["ticks"] - start.stats["ticks"],
			work:        end.stats["work"] - start.stats["work"],
			tickerWakes: end.stats["tickerWakes"] - start.stats["tickerWakes"],
			ctxsw:       end.ctxsw - start.ctxsw,
		}
		openAgents = end.stats["agentsTicks"] - start.stats["agentsTicks"]
		openFrames = end.stats["cockpit"] - start.stats["cockpit"]
		t.Logf("%s, idle window %v: wire=%dB ticks=%d work=%d tickerWakes=%d ctxsw=%d view ticks=%d view frames=%d",
			name, idleWindow, got.wire, got.ticks, got.work, got.tickerWakes, got.ctxsw, openAgents, openFrames)
		return got
	}
	closed := window("agents view closed")
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "Ready for review", "Idle")
	}, uiTimeout); err != nil {
		t.Fatalf("the agents view never showed the agents: %v\n%s", err, term.Snapshot())
	}
	open := window("agents view open")
	var over []string
	if open.wire > closed.wire+idleWireBudget/4 {
		over = append(over, fmt.Sprintf("wrote %d bytes, %d with it closed", open.wire, closed.wire))
	}
	if open.ticks > closed.ticks+2 || open.work > closed.work+2 {
		over = append(over, fmt.Sprintf("ran %d ticks (%d full), %d (%d) with it closed", open.ticks, open.work, closed.ticks, closed.work))
	}
	if open.tickerWakes > closed.tickerWakes+1 {
		over = append(over, fmt.Sprintf("woke the frame ticker %d times, %d with it closed", open.tickerWakes, closed.tickerWakes))
	}
	if closed.ctxsw >= 0 && open.ctxsw > closed.ctxsw*3/2+200 {
		over = append(over, fmt.Sprintf("made %d context switches, %d with it closed", open.ctxsw, closed.ctxsw))
	}
	// Open, it holds the idle budget on its own: no maintenance tick, no
	// frame, no tick of its own inside the window, and no frame of it drawn
	// afresh.
	if open.ticks > 2 || open.tickerWakes > 1 || open.wire > idleWireBudget {
		over = append(over, fmt.Sprintf("open: %d ticks, %d ticker wakes, %d bytes", open.ticks, open.tickerWakes, open.wire))
	}
	if openAgents > 0 || openFrames > 0 {
		over = append(over, fmt.Sprintf("open: %d ticks of the view and %d frames of it drawn afresh", openAgents, openFrames))
	}
	if len(over) > 0 {
		t.Fatalf("the open agents view costs more at idle than the closed one:\n  %s\n%s", strings.Join(over, "\n  "), term.Snapshot())
	}

	// An agent that changes group is held in its old one a moment. Once the
	// hold settles, the open view is back to no wakeups at all.
	if out, err := tuiosCLI(t, base, "set-agent-state", "working", "-w", reply.Windows[0].ID, "--harness", "claude-code"); err != nil {
		t.Fatalf("report agent 0 working: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "Working") }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the held row never moved to Working: %v\n%s", err, term.Snapshot())
	}
	held := window("agents view open, after a held move")
	if held.ticks > 2 || held.tickerWakes > 1 || openAgents > 0 || openFrames > 0 {
		t.Fatalf("ASSERTION: after a held move the open view still wakes the client: %d ticks, %d ticker wakes, %d view ticks, %d frames drawn afresh\n%s",
			held.ticks, held.tickerWakes, openAgents, openFrames, term.Snapshot())
	}
}
