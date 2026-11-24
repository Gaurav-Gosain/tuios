package tuie2e

// Smooth under load: frame pacing and typing latency while other panes
// produce heavy output.
//
// Each case opens one pane to type in and N busy panes, tiled. The busy
// panes run a mix of heavy writers: yes, a fast seq, cat of a large log in a
// loop, and framepace drawing a full screen at 60 Hz (the shape of htop or
// btop). The focused pane is a plain shell. While the others run, keys are
// typed into it at a person's pace and each echo is timed to the frame that
// shows it.
//
// The numbers:
//
//	frame     the time between two frames the client writes to the host.
//	late      frames that came more than half a period after the one before.
//	missing   frames the host did not get against max_fps times the run time.
//	tui       of the 60 Hz guests' frames, the share that reached the host.
//	key       keystroke to the frame that shows its echo, p50/p99/max.
//	cpu       the client's and the daemon's CPU, in ms per second of run.
//
//	cd e2e/tui && TUIOS_E2E=1 TUIOS_PERF=1 go test -count=1 -v -run TestPerfSmooth .
//
// TUIOS_PERF_CASES narrows the cases to the names that contain one of its
// comma-separated words. TUIOS_PERF_PPROF names a directory for CPU profiles
// of the client and the daemon over the typed window, and TUIOS_PERF_TRACE
// (with TUIOS_PERF_PPROF) adds an execution trace of each, 3 seconds long.
// TUIOS_PERF_OUT keeps each case's report as JSON.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/perf"
	"github.com/Gaurav-Gosain/tuitest"
)

// smoothCase is one load measurement.
type smoothCase struct {
	name    string
	maxFPS  int
	busy    int      // busy panes beside the focused one
	writers []string // the writers, given to the busy panes in turn
}

// smoothReport is one case's result, also written as JSON.
type smoothReport struct {
	Case       string     `json:"case"`
	MaxFPS     int        `json:"max_fps"`
	Busy       int        `json:"busy"`
	Seconds    float64    `json:"seconds"`
	HostFrames int        `json:"host_frames"`
	Frame      perf.Stats `json:"frame"`
	Late       int        `json:"late"`
	Missing    int        `json:"missing"`
	TUIDrawn   int        `json:"tui_drawn"`
	TUIShown   int        `json:"tui_shown"`
	Key        perf.Stats `json:"key"`
	ClientCPU  float64    `json:"client_cpu_ms_per_s"`
	DaemonCPU  float64    `json:"daemon_cpu_ms_per_s"`
	HostMBps   float64    `json:"host_mb_per_s"`
}

func (r smoothReport) line() string {
	ms := func(d time.Duration) string { return fmt.Sprintf("%.2f", float64(d)/1e6) }
	tui := "-"
	if r.TUIDrawn > 0 {
		tui = fmt.Sprintf("%.0f%%", 100*float64(r.TUIShown)/float64(r.TUIDrawn))
	}
	return fmt.Sprintf("PERF smooth/%s: %.0f fps (missing %d, late %d)  frame p50 %s p99 %s max %s ms  tui shown %s  key p50 %s p99 %s max %s ms  cpu client %.0f daemon %.0f ms/s  host %.1f MB/s",
		r.Case, float64(r.HostFrames)/r.Seconds, r.Missing, r.Late,
		ms(r.Frame.P50), ms(r.Frame.P99), ms(r.Frame.Max), tui,
		ms(r.Key.P50), ms(r.Key.P99), ms(r.Key.Max),
		r.ClientCPU, r.DaemonCPU, r.HostMBps)
}

// smoothLog writes a log of about 64 MB, the shape of a build or server log,
// for the cat writer.
func smoothLog(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "big.log")
	var b strings.Builder
	for i := range 400_000 {
		fmt.Fprintf(&b, "2026-11-24T10:%02d:%02d.%06dZ INFO  worker[%03d] request id=%08x path=/api/v1/items/%d status=200 bytes=%d dur=%dus\n",
			(i/60000)%60, (i/1000)%60, i%1000000, i%256, i*2654435761, i%9973, 512+i%4096, 100+i%9000)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write the log: %v", err)
	}
	return path
}

// writerCmd is the shell line that starts a writer in a busy pane.
func writerCmd(kind, logPath, framepace, guestLog string, seqBase int) string {
	switch kind {
	case "yes":
		return "exec yes tuiosflood\n"
	case "seq":
		return "while :; do seq 1 100000000; done\n"
	case "cat":
		return "while :; do cat " + logPath + "; done\n"
	case "tui60":
		return fmt.Sprintf("clear; exec %s text 60 %s %d\n", framepace, guestLog, seqBase)
	}
	return ""
}

var smoothMix = []string{"tui60", "yes", "cat", "seq"}

func runSmoothCase(t *testing.T, sc smoothCase) smoothReport {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, fmt.Sprintf("[appearance]\nmax_fps = %d\n[startup]\ntiled = true\n", sc.maxFPS))
	profDir := os.Getenv("TUIOS_PERF_PPROF")
	var daemonAddr, clientAddr string
	if profDir != "" {
		daemonAddr = startDaemonWithPprof(t, base)
		clientAddr = fmt.Sprintf("127.0.0.1:%d", freePort(t))
	}
	if out, err := tuiosCLI(t, base, "new", "fp", "--detach"); err != nil {
		t.Fatalf("create the session: %v: %s", err, out)
	}
	clock := newFrameClock()
	term := startIn(t, base, startOpts{
		cols: perfCols, rows: perfRows,
		args: attachArgs(clientAddr),
		env:  append(perfEnvVars(), "TUIOS_SIXEL_GRAPHICS=0", "COLORTERM=truecolor"),
		out:  clock,
	})
	clock.mu.Lock()
	clock.screen = func() string { return term.Screen().Text() }
	clock.mu.Unlock()
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached: %v\n%s", err, term.Snapshot())
	}
	ids := windowIDs(t, base, "fp")
	for len(ids) < sc.busy+1 {
		newWindowIn(t, base, "fp")
		ids = windowIDs(t, base, "fp")
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= sc.busy+1 }, uiTimeout); err != nil {
		t.Fatalf("the panes never showed: %v\n%s", err, term.Snapshot())
	}

	dir := t.TempDir()
	logPath := ""
	var guestLogs []string
	fpBin := ""
	for i := range sc.busy {
		kind := sc.writers[i%len(sc.writers)]
		if kind == "cat" && logPath == "" {
			logPath = smoothLog(t, dir)
		}
		gl := ""
		if kind == "tui60" {
			if fpBin == "" {
				fpBin = buildFramepace(t)
			}
			gl = filepath.Join(dir, fmt.Sprintf("guest%d.log", i))
			guestLogs = append(guestLogs, gl)
		}
		if err := paneSend(base, "fp", ids[i+1], writerCmd(kind, logPath, fpBin, gl, (i+1)*guestSeqBase)); err != nil {
			t.Fatalf("start writer %s: %v", kind, err)
		}
	}
	if out, err := tuiosCLI(t, base, "focus-window", ids[0], "-s", "fp"); err != nil {
		t.Fatalf("focus the typing pane: %v: %s", err, out)
	}
	// An attached client starts in terminal mode, so keys reach the shell.
	// Let the writers reach their steady rate.
	pause(t, "the writers reach their steady rate, which nothing on screen shows", time.Second)

	daemonPid := 0
	if raw, err := os.ReadFile(filepath.Join(base, "XDG_RUNTIME_DIR", "tuios", "tuios.sock.pid")); err == nil {
		daemonPid, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
	}
	clock.mu.Lock()
	ends0, bytes0 := len(clock.ends), clock.hostBytes
	clock.mu.Unlock()
	c0, d0 := cpuTicks(term.Pid()), cpuTicks(daemonPid)
	t0 := time.Now()
	var profWG sync.WaitGroup
	if profDir != "" {
		_ = os.MkdirAll(profDir, 0o755)
		name := strings.ReplaceAll(sc.name, "/", "_")
		for who, addr := range map[string]string{"client": clientAddr, "daemon": daemonAddr} {
			profWG.Go(func() {
				body, err := httpGet(fmt.Sprintf("http://%s/debug/pprof/profile?seconds=8", addr))
				if err == nil {
					_ = os.WriteFile(filepath.Join(profDir, name+"."+who+".pprof"), []byte(body), 0o644)
				}
				if os.Getenv("TUIOS_PERF_TRACE") == "" {
					return
				}
				body, err = httpGet(fmt.Sprintf("http://%s/debug/pprof/trace?seconds=3", addr))
				if err == nil {
					_ = os.WriteFile(filepath.Join(profDir, name+"."+who+".trace"), []byte(body), 0o644)
				}
			})
		}
	}
	keys := typeLatencyEvery(t, term, typingGap)
	t1 := time.Now()
	c1, d1 := cpuTicks(term.Pid()), cpuTicks(daemonPid)
	clock.mu.Lock()
	ends := append([]int64(nil), clock.ends[ends0:]...)
	hostBytes := clock.hostBytes - bytes0
	clock.mu.Unlock()
	profWG.Wait()

	secs := t1.Sub(t0).Seconds()
	period := time.Second / time.Duration(sc.maxFPS)
	var frames perf.Dist
	late := 0
	for i := 1; i < len(ends); i++ {
		iv := time.Duration(ends[i] - ends[i-1])
		frames = append(frames, iv)
		if iv > period*3/2 {
			late++
		}
	}
	r := smoothReport{
		Case: sc.name, MaxFPS: sc.maxFPS, Busy: sc.busy, Seconds: secs,
		HostFrames: len(ends),
		Frame:      frames.Stats(),
		Late:       late,
		Missing:    max(0, int(secs*float64(sc.maxFPS))-len(ends)),
		Key:        keys.Stats(),
		ClientCPU:  float64(c1-c0) * 10 / secs,
		DaemonCPU:  float64(d1-d0) * 10 / secs,
		HostMBps:   float64(hostBytes) / secs / 1e6,
	}
	// The 60 Hz guests: which of their frames in the window reached the host.
	clock.mu.Lock()
	lo, hi := t0.UnixNano(), t1.UnixNano()-int64(100*time.Millisecond)
	for _, gl := range guestLogs {
		for seq, ready := range readGuestLog(gl) {
			if ready < lo || ready > hi {
				continue
			}
			r.TUIDrawn++
			if _, ok := clock.seen[seq]; ok {
				r.TUIShown++
			}
		}
	}
	clock.mu.Unlock()
	t.Log(r.line())
	if dir := os.Getenv("TUIOS_PERF_OUT"); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		b, _ := json.Marshal(r)
		_ = os.WriteFile(filepath.Join(dir, "smooth_"+strings.ReplaceAll(sc.name, "/", "_")+".json"), b, 0o644)
	}
	return r
}

// TestPerfSmooth measures frame pacing and typing latency with 1, 4 and 9
// busy panes at max_fps 60, 120 and 240, and each writer alone in one pane.
func TestPerfSmooth(t *testing.T) {
	requireReal(t, "perf or external program, env-gated")
	perfGate(t)
	var cases []smoothCase
	for _, w := range smoothMix {
		cases = append(cases, smoothCase{name: fmt.Sprintf("1-%s/max120", w), maxFPS: 120, busy: 1, writers: []string{w}})
	}
	for _, busy := range []int{4, 9} {
		for _, fps := range []int{60, 120, 240} {
			cases = append(cases, smoothCase{name: fmt.Sprintf("%d-mix/max%d", busy, fps), maxFPS: fps, busy: busy, writers: smoothMix})
		}
	}
	want := os.Getenv("TUIOS_PERF_CASES")
	for _, sc := range cases {
		if want != "" {
			match := false
			for w := range strings.SplitSeq(want, ",") {
				if w != "" && strings.Contains(sc.name, w) {
					match = true
				}
			}
			if !match {
				continue
			}
		}
		t.Run(sc.name, func(t *testing.T) { runSmoothCase(t, sc) })
	}
}

// drainCmd is the shell line of a finite flood for a busy pane. It creates
// doneFile when the program has written the last byte.
func drainCmd(kind, logPath, doneFile string) string {
	done := "; touch " + doneFile + "\n"
	switch kind {
	case "yes":
		return fmt.Sprintf("yes tuiosflood | head -n %d", drainYesLines) + done
	case "seq":
		return fmt.Sprintf("seq 1 %d", drainSeqTo) + done
	case "cat":
		return "cat" + strings.Repeat(" "+logPath, drainCatTimes) + done
	}
	return ""
}

// The size of each finite flood: a few seconds of output for one pane.
const (
	drainYesLines = 15_000_000
	drainSeqTo    = 10_000_000
	drainCatTimes = 3
)

// drainBytes is how many bytes drainCmd prints, for the CPU per MB.
func drainBytes(kind, logPath string) int64 {
	switch kind {
	case "yes":
		return drainYesLines * int64(len("tuiosflood\n"))
	case "seq":
		var n int64
		for d, lo := int64(1), int64(1); lo <= drainSeqTo; d, lo = d+1, lo*10 {
			hi := min(lo*10-1, drainSeqTo)
			n += (hi - lo + 1) * (d + 1)
		}
		return n
	case "cat":
		if st, err := os.Stat(logPath); err == nil {
			return drainCatTimes * st.Size()
		}
	}
	return 0
}

// TestPerfSmoothDrain times finite floods in 1, 4 and 9 panes at max_fps 120:
// from the start to the last pane's marker on the host, and the CPU the
// client and the daemon took for each MB. The timed cases in TestPerfSmooth
// keep both processes busy whatever the cost per byte, so a cheaper parse
// shows there as more bytes taken in, and here as less CPU per MB.
func TestPerfSmoothDrain(t *testing.T) {
	requireReal(t, "perf or external program, env-gated")
	perfGate(t)
	mix := []string{"yes", "cat", "seq"}
	type drainCase struct {
		name    string
		writers []string
	}
	cases := []drainCase{
		{"1-yes", []string{"yes"}}, {"1-seq", []string{"seq"}}, {"1-cat", []string{"cat"}},
		{"4-mix", []string{"yes", "cat", "seq", "yes"}},
		{"9-mix", append(append(append([]string{}, mix...), mix...), mix...)},
	}
	want := os.Getenv("TUIOS_PERF_CASES")
	for _, dc := range cases {
		if want != "" && !strings.Contains(want, dc.name) {
			continue
		}
		t.Run(dc.name, func(t *testing.T) {
			base := t.TempDir()
			killDaemon(t, base)
			writeConfig(t, base, "[appearance]\nmax_fps = 120\n[startup]\ntiled = true\n")
			if out, err := tuiosCLI(t, base, "new", "fp", "--detach"); err != nil {
				t.Fatalf("create the session: %v: %s", err, out)
			}
			clock := newFrameClock()
			term := startIn(t, base, startOpts{
				cols: perfCols, rows: perfRows,
				args: []string{"attach", "fp"},
				env:  append(perfEnvVars(), "COLORTERM=truecolor"),
				out:  clock,
			})
			if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
				t.Fatalf("the client never attached: %v\n%s", err, term.Snapshot())
			}
			ids := windowIDs(t, base, "fp")
			for len(ids) < len(dc.writers) {
				newWindowIn(t, base, "fp")
				ids = windowIDs(t, base, "fp")
			}
			// Every pane's shell has printed its prompt, so no typed command
			// reaches a shell that is not reading yet.
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return countWindows(s) >= len(dc.writers) && strings.Count(s.Text(), "$") >= len(dc.writers)
			}, shellTimeout); err != nil {
				t.Fatalf("the panes never showed a prompt: %v\n%s", err, term.Snapshot())
			}
			logPath := smoothLog(t, t.TempDir())
			daemonPid := 0
			if raw, err := os.ReadFile(filepath.Join(base, "XDG_RUNTIME_DIR", "tuios", "tuios.sock.pid")); err == nil {
				daemonPid, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
			}
			var total int64
			for _, w := range dc.writers {
				total += drainBytes(w, logPath)
			}
			c0, d0 := cpuTicks(term.Pid()), cpuTicks(daemonPid)
			clock.mu.Lock()
			ends0 := len(clock.ends)
			clock.mu.Unlock()
			t0 := time.Now()
			doneDir := t.TempDir()
			for i, w := range dc.writers {
				if err := paneSend(base, "fp", ids[i], drainCmd(w, logPath, filepath.Join(doneDir, strconv.Itoa(i)))); err != nil {
					t.Fatalf("start the flood: %v", err)
				}
			}
			// Done when every program has finished and the client has taken
			// in what it was sent: its CPU time stays the same over three
			// samples in a row. A tiled pane can be one row tall, so a marker
			// on screen would scroll out of sight.
			waitUntil(t, "every flood finishes", 5*time.Minute, func() (bool, string) {
				left := 0
				for i := range dc.writers {
					if _, err := os.Stat(filepath.Join(doneDir, strconv.Itoa(i))); err != nil {
						left++
					}
				}
				return left == 0, fmt.Sprintf("%d floods still running", left)
			})
			// The drain ends when the client last took CPU time, once it has
			// stayed the same for quietFor.
			const quietFor = 300 * time.Millisecond
			last, changed := cpuTicks(term.Pid()), time.Now()
			waitUntil(t, "the client goes quiet", time.Minute, func() (bool, string) {
				if now := cpuTicks(term.Pid()); now != last {
					last, changed = now, time.Now()
				}
				return time.Since(changed) >= quietFor, fmt.Sprintf("client CPU still moving: %d ticks", last)
			})
			took := changed.Sub(t0)
			c1, d1 := cpuTicks(term.Pid()), cpuTicks(daemonPid)
			clock.mu.Lock()
			ends := append([]int64(nil), clock.ends[ends0:]...)
			clock.mu.Unlock()
			var frames perf.Dist
			for i := 1; i < len(ends); i++ {
				frames = append(frames, time.Duration(ends[i]-ends[i-1]))
			}
			st := frames.Stats()
			mb := float64(total) / 1e6
			cms, dms := float64(c1-c0)*10, float64(d1-d0)*10
			t.Logf("PERF smooth-drain/%s: %.1f MB in %v (%.1f MB/s)  cpu client %.1f daemon %.1f ms/MB  frames %d, p50 %.2f p99 %.2f max %.2f ms",
				dc.name, mb, took.Round(time.Millisecond), mb/took.Seconds(), cms/mb, dms/mb,
				len(ends), float64(st.P50)/1e6, float64(st.P99)/1e6, float64(st.Max)/1e6)
		})
	}
}
