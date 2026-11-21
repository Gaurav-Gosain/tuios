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

type byteCounter struct{ n atomic.Int64 }

func (b *byteCounter) Write(p []byte) (int, error) {
	b.n.Add(int64(len(p)))
	return len(p), nil
}

type wireRecorder struct {
	byteCounter
	mu  sync.Mutex
	buf []byte
}

const wireRecorderKeep = 4 << 20

func (w *wireRecorder) Write(p []byte) (int, error) {
	w.mu.Lock()
	if room := wireRecorderKeep - len(w.buf); room > 0 {
		w.buf = append(w.buf, p[:min(room, len(p))]...)
	}
	w.mu.Unlock()
	return w.byteCounter.Write(p)
}

func (w *wireRecorder) contains(s string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return bytes.Contains(w.buf, []byte(s))
}

const idleWireBudget = 2048

const idleWindow = 10 * time.Second

type idleBudget struct {
	wire, minWire int64
	ticks, work   uint64
	tickerWakes   uint64
	ctxsw         int64
	noSettle      bool
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
	requireReal(t, "asserts on OS processes (pids, signals, /proc)")
	t.Run("clock off", func(t *testing.T) {
		runIdleWindow(t, "[appearance.selection]\nosc52_write = \"on\"\n", idleBudget{
			wire:  idleWireBudget,
			ticks: 2, work: 1,
			tickerWakes: 1,
			ctxsw:       400,
		}, nil, clipboardAtIdle)
	})
	t.Run("clock on", func(t *testing.T) {
		runIdleWindow(t, "[appearance]\nshow_clock = true\nclock_format = \"15:04:05\"\n", idleBudget{
			wire: 4 * idleWireBudget, minWire: 100,
			ticks: 2, work: 1,
			tickerWakes: 1,
			ctxsw:       400,
		}, nil, nil)
	})
	t.Run("one late output", func(t *testing.T) {
		runIdleWindow(t, "", idleBudget{
			noSettle: true,
			wire:     idleWireBudget,
			ticks:    2, work: 1,
			tickerWakes: 1,
			ctxsw:       800,
		}, writeLate, sawLateOutput)
	})
	t.Run("tml surface", func(t *testing.T) {
		runIdleWindowOpts(t, "", startOpts{daemonDefault: true}, idleBudget{
			wire:        idleWireBudget,
			ticks:       2,
			work:        1,
			tickerWakes: 1,
			ctxsw:       400,
		}, tmlPaneAtIdle, nil)
	})
	t.Run("tml surface after motion", func(t *testing.T) {
		runIdleWindowOpts(t, "[startup]\ndaemon = true\n\n[appearance]\nmotion = \"full\"\n", startOpts{daemonDefault: true}, idleBudget{
			wire:        idleWireBudget,
			ticks:       2,
			work:        1,
			tickerWakes: 1,
			ctxsw:       400,
		}, tmlMotionSettled, nil)
	})
	t.Run("minimized tml surface", func(t *testing.T) {
		runIdleWindowOpts(t, "", startOpts{daemonDefault: true}, idleBudget{
			wire:  idleWireBudget,
			ticks: 2, work: 1,
			tickerWakes: 1,
			ctxsw:       1200,
		}, minimizedTMLPatched, nil)
	})
	t.Run("agent title on a hidden workspace", func(t *testing.T) {
		runIdleWindowOpts(t, idleAgentTitleConfig, startOpts{daemonDefault: true}, idleBudget{
			wire:  idleWireBudget,
			ticks: 5, work: 1,
			tickerWakes: 1,
			ctxsw:       600,
		}, hiddenAgentTitled, nil)
	})
	t.Run("agent spinner in a watched pane", func(t *testing.T) {
		runIdleWindowOpts(t, idleAgentTitleConfig, startOpts{daemonDefault: true}, idleBudget{
			wire:  idleWireBudget,
			ticks: 2, work: 1,
			tickerWakes: 1,
			ctxsw:       2000,
		}, watchedAgentSpinning, nil)
	})
	t.Run("git section", func(t *testing.T) {
		runIdleWindowOpts(t, idleGitConfig, startOpts{daemonDefault: true}, idleBudget{
			wire:  idleWireBudget,
			ticks: 8, work: 1,
			tickerWakes: 1,
			ctxsw:       600,
		}, gitRepoPane, nil)
	})
}

const idleGitConfig = `[startup]
daemon = true

[appearance.sidebar]
enabled = true
position = "left"
width = 28
sections = "sessions,git"
`

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

const idleAgentTitleConfig = `[startup]
daemon = true

[daemon]
agent_detect_seconds = 1

[appearance.sidebar]
enabled = true
position = "left"
width = 34
`

func hiddenAgentTitled(t *testing.T, term *tuitest.Terminal, base string) {
	t.Helper()
	rig, session, window := agentPane(t, base, "", "--workspace", "2", "--no-focus")
	rig.title(t, session, window, "✳ hidden-agent-name")
	waitAgentRow(t, term, "hidden-agent-name")
}

func watchedAgentSpinning(t *testing.T, term *tuitest.Terminal, base string) {
	t.Helper()
	agentPane(t, base, "spin")
	waitAgentRow(t, term, "spinning-agent")
}

func agentPane(t *testing.T, base, script string, args ...string) (titleAgentRig, string, string) {
	t.Helper()
	rig := newTitleAgentRig(t, base, "claude")
	out, err := tuiosCLI(t, base, append([]string{"new-window", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("open a pane: %v\n%s", err, out)
	}
	var made struct {
		Session string `json:"session"`
		ID      string `json:"id"`
		Window  string `json:"window_id"`
	}
	if err := json.Unmarshal([]byte(out), &made); err != nil {
		t.Fatalf("read new-window: %v\n%s", err, out)
	}
	window := made.Window
	if window == "" {
		window = made.ID
	}
	path := rig.script
	if script == "spin" {
		path = rig.spinner
	}
	rig.run(t, made.Session, window, "claude", path, "claude-code")
	return rig, made.Session, window
}

func waitAgentRow(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), name) &&
			!strings.Contains(s.Text(), "Window created") && !strings.Contains(s.Text(), "Session size")
	}, 3*uiTimeout); err != nil {
		t.Fatalf("the rail never showed %s: %v\n%s", name, err, term.Snapshot())
	}
}

func tmlPaneAtIdle(t *testing.T, term *tuitest.Terminal, base string) {
	t.Helper()
	doc := writeTMLDoc(t, base, "run.tml", tmlRunDoc)
	if out, err := tuiosCLI(t, base, "tml", "show", doc, "--pane"); err != nil {
		t.Fatalf("tuios tml show --pane: %v\n%s", err, out)
	}
	if err := term.WaitForText("Failures", uiTimeout); err != nil {
		t.Fatalf("the surface never drew: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Window created") && !strings.Contains(s.Text(), "Session size")
	}, 3*uiTimeout); err != nil {
		t.Fatalf("the window messages never went: %v\n%s", err, term.Snapshot())
	}
}

const tmlMotionDoc = `<tml version="1" title="Motion">
  <col pad="1" gap="1" stagger="80ms">
    <card title="Motion ready" enter="fade"><text>first</text></card>
    <card title="Second" enter="slide-up"><text>second</text></card>
    <toast role="success" ttl="1s">Saved for a second</toast>
  </col>
</tml>`

func tmlMotionSettled(t *testing.T, term *tuitest.Terminal, base string) {
	t.Helper()
	doc := writeTMLDoc(t, base, "motion.tml", tmlMotionDoc)
	if out, err := tuiosCLI(t, base, "tml", "show", doc, "--pane"); err != nil {
		t.Fatalf("tuios tml show --pane: %v\n%s", err, out)
	}
	if err := term.WaitForText("Saved for a second", uiTimeout); err != nil {
		t.Fatalf("the toast never showed: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Saved for a second") && strings.Contains(s.Text(), "Motion ready")
	}, uiTimeout); err != nil {
		t.Fatalf("the toast never went at the end of its ttl: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Window created") && !strings.Contains(s.Text(), "Session size")
	}, 3*uiTimeout); err != nil {
		t.Fatalf("the window messages never went: %v\n%s", err, term.Snapshot())
	}
}

func minimizedTMLPatched(t *testing.T, term *tuitest.Terminal, base string) {
	t.Helper()
	tmlPaneAtIdle(t, term, base)
	surface, _ := tmlSurfaceOf(t, base, "")
	toWindowMode(t, term, base)
	if err := term.SendKeys("m"); err != nil {
		t.Fatalf("send m: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Failures") }, uiTimeout); err != nil {
		t.Fatalf("m never minimized the TML pane: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Window management mode")
	}, 3*uiTimeout); err != nil {
		t.Fatalf("the mode message never went: %v\n%s", err, term.Snapshot())
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for n := 0; ; n++ {
			select {
			case <-done:
				return
			case <-time.After(500 * time.Millisecond):
			}
			_, _ = tuiosCLI(t, base, "tml", "patch", surface, fmt.Sprintf(`[{"op":"replace","path":"/passed","value":%d}]`, 500+n))
		}
	}()
	t.Cleanup(func() { close(done); <-stopped })
}

const lateOutputAfter = 4 * time.Second

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

func runIdleWindow(t *testing.T, config string, budget idleBudget, setup func(*testing.T, *tuitest.Terminal, string), after func(*testing.T, *tuitest.Terminal, *wireRecorder)) {
	runIdleWindowOpts(t, config, startOpts{}, budget, setup, after)
}

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
	time.Sleep(time.Second)

	pid := term.Pid()
	if !budget.noSettle {
		waitIdleSettled(t, pid, statsPath)
	}
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
	t.Logf("idle window %v: wire=%dB ticks=%d work=%d tickerWakes=%d ctxsw=%d renders=%d",
		idleWindow, got.wire, got.ticks, got.work, got.tickerWakes, got.ctxsw, end.stats["render"]-start.stats["render"])

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

	quit := "q"
	if o.daemonDefault {
		quit = "d"
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), quit); err != nil {
		t.Fatalf("send leader %s: %v", quit, err)
	}
	waitExit(t, term, "idle test quit")
}

func clipboardAtIdle(t *testing.T, term *tuitest.Terminal, wire *wireRecorder) {
	t.Helper()
	const (
		delay   = 3 * time.Second
		payload = "aWRsZS1jbGlw"
	)
	enterTerminalMode(t, term)
	typeLine(t, term, fmt.Sprintf(`sleep %d; printf '\033]52;c;%s\007'`, int(delay.Seconds()), payload))
	typed := time.Now()
	if err := term.SendKeys(tuitest.Ctrl('b'), "w", "2"); err != nil {
		t.Fatalf("switch to workspace 2: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 0 }, uiTimeout); err != nil {
		t.Fatalf("workspace 2 never showed: %v\n%s", err, term.Snapshot())
	}
	if wire.contains(payload) {
		t.Fatal("the clipboard write went out before the pane made it")
	}
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

const (
	idleSettleQuiet = 4 * time.Second
	idleSettleMax   = 15 * time.Second
)

func waitIdleSettled(t *testing.T, pid int, statsPath string) {
	t.Helper()
	began := time.Now()
	last := sampleIdle(t, pid, statsPath)
	quietSince := began
	for time.Since(quietSince) < idleSettleQuiet {
		if time.Since(began) > idleSettleMax {
			t.Logf("the client never went %v without a full scan in %v; measuring anyway", idleSettleQuiet, idleSettleMax)
			return
		}
		time.Sleep(250 * time.Millisecond)
		now := sampleIdle(t, pid, statsPath)
		if now.stats["work"] != last.stats["work"] {
			quietSince = time.Now()
		}
		last = now
	}
	t.Logf("the client went quiet %v after setup", time.Since(began).Round(100*time.Millisecond)-idleSettleQuiet)
}

type idleSample struct {
	stats map[string]uint64
	ctxsw int64
}

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
			continue
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
	requireReal(t, "asserts on OS processes (pids, signals, /proc)")
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
	if open.ticks > 2 || open.tickerWakes > 1 || open.wire > idleWireBudget {
		over = append(over, fmt.Sprintf("open: %d ticks, %d ticker wakes, %d bytes", open.ticks, open.tickerWakes, open.wire))
	}
	if openAgents > 0 || openFrames > 0 {
		over = append(over, fmt.Sprintf("open: %d ticks of the view and %d frames of it drawn afresh", openAgents, openFrames))
	}
	if len(over) > 0 {
		t.Fatalf("the open agents view costs more at idle than the closed one:\n  %s\n%s", strings.Join(over, "\n  "), term.Snapshot())
	}

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
