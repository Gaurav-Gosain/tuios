package tuie2e

// Frame pacing: how steadily frames reach the host terminal, and how long a
// frame the guest drew takes to get there.
//
// A guest (./framepace) draws at a fixed rate and logs when each frame was
// ready. The host side of the client's PTY is read here with no rendering at
// all, and each frame the client finishes (the end of its synchronized update)
// is timestamped as it arrives. Every guest frame carries its sequence number
// where the byte stream shows it, so a host frame is matched to the guest
// frame it shows.
//
// The numbers:
//
//	interval  the time between two host frames that carry new content. At a
//	          steady 120 fps every one is 8.3 ms; the tail is the stutter.
//	latency   from the guest having a frame ready to the host receiving it.
//	shown     guest frames that reached the host, of those the guest drew.
//	cpu       the client's and the daemon's CPU, in ms per second of run.
//
// Gated like the other perf tests:
//
//	cd e2e/tui && TUIOS_E2E=1 TUIOS_PERF=1 go test -count=1 -v -run TestPerfFrames .
//
// TUIOS_PERF_OUT, when set, names a directory that gets one JSON file per case
// with every sample, so two builds can be compared offline.

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/perf"
	"github.com/Gaurav-Gosain/tuitest"
)

// frameClock reads the client's host stream and timestamps what arrives.
type frameClock struct {
	mu   sync.Mutex
	tail []byte
	// ends are the arrival times of the client's frame ends.
	ends []int64
	// seen maps a guest frame to when the host first received it.
	seen map[int]int64
	// images counts the kitty image transmissions the host received.
	images int
	// hostBytes is everything the host received.
	hostBytes int64
	probe     chan struct{}
	// screen returns the host's screen text once the client is running. The
	// tags are read off the screen rather than the byte stream: the client
	// writes only the cells that changed, so a tag is rarely whole in the
	// stream.
	screen func() string
	// shm reads each t=s frame's object the way the host terminal does,
	// and removes it when the client asked for that (the client's own
	// objects; the guest's object is left alone).
	shmPrefix string
	// dump, when set, receives every chunk the host reads with its arrival
	// time, up to dumpLimit bytes. TUIOS_PERF_DUMP turns it on.
	dump      *os.File
	dumpBytes int
}

const dumpLimit = 4 << 20

func newFrameClock() *frameClock {
	return &frameClock{seen: map[int]int64{}, probe: make(chan struct{}, 1)}
}

var (
	frameSyncEndSeq = []byte("\x1b[?2026l")
	textTagRE       = regexp.MustCompile(`[<>][0-9a-j]{8}`)
	apcStart        = []byte("\x1b_G")
	apcEnd          = []byte("\x1b\\")
	kittyKeyRE      = regexp.MustCompile(`(?:^|,)([a-zA-Z])=([^,;]*)`)
)

// decodeTag reads a sequence number back from a tag (see framepace's tag).
func decodeTag(b []byte) (int, bool) {
	n := 0
	for _, c := range b[1:9] {
		switch {
		case c >= '0' && c <= '9':
			n = n*10 + int(c-'0')
		case c >= 'a' && c <= 'j':
			n = n*10 + int(c-'a')
		default:
			return 0, false
		}
	}
	return n, true
}

func (c *frameClock) Write(p []byte) (int, error) {
	now := time.Now().UnixNano()
	if bytes.Contains(p, []byte(da1Query)) {
		select {
		case c.probe <- struct{}{}:
		default:
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hostBytes += int64(len(p))
	if c.dump != nil && c.dumpBytes < dumpLimit {
		c.dumpBytes += len(p)
		_, _ = fmt.Fprintf(c.dump, "=== %d %d\n", now, len(p))
		_, _ = c.dump.Write(p)
	}
	data := append(c.tail, p...)
	c.tail = nil
	// Frame ends.
	counted := 0
	for {
		k := bytes.Index(data[counted:], frameSyncEndSeq)
		if k < 0 {
			break
		}
		c.ends = append(c.ends, now)
		counted += k + len(frameSyncEndSeq)
	}
	// Kitty images.
	pos := 0
	for {
		k := bytes.Index(data[pos:], apcStart)
		if k < 0 {
			break
		}
		start := pos + k
		e := bytes.Index(data[start:], apcEnd)
		if e < 0 {
			if len(data)-start < 64<<20 {
				c.tail = append(c.tail, data[start:]...)
			}
			data = data[:start]
			break
		}
		c.kittyCommand(data[start+3:start+e], now)
		pos = start + e + 2
	}
	// Keep a little so a sequence split across two reads is still found.
	if counted > 0 && c.screen != nil {
		for _, m := range textTagRE.FindAllString(c.screen(), -1) {
			if seq, ok := decodeTag([]byte(m)); ok {
				if _, dup := c.seen[seq]; !dup {
					c.seen[seq] = now
				}
			}
		}
	}
	// A frame end already counted is not kept.
	if len(c.tail) == 0 && len(data) > 0 {
		c.tail = append(c.tail, data[max(counted, len(data)-len(frameSyncEndSeq)):]...)
	}
	return len(p), nil
}

// kittyCommand looks at one graphics command: its control keys, and the frame
// number in the first eight bytes of its pixels.
func (c *frameClock) kittyCommand(cmd []byte, now int64) {
	head, payload, _ := bytes.Cut(cmd, []byte(";"))
	keys := map[string]string{}
	for _, m := range kittyKeyRE.FindAllSubmatch(head, -1) {
		keys[string(m[1])] = string(m[2])
	}
	a := keys["a"]
	if a == "" {
		a = "t"
	}
	if a != "t" && a != "T" && a != "f" {
		return
	}
	if keys["s"] == "" && keys["m"] != "" && a != "f" {
		return // a continuation chunk
	}
	c.images++
	var first []byte
	switch keys["t"] {
	case "s", "f", "t":
		name, err := base64.StdEncoding.DecodeString(string(payload))
		if err != nil {
			return
		}
		path := string(name)
		if keys["t"] == "s" {
			path = "/dev/shm/" + path
		}
		f, err := os.Open(path)
		if err != nil {
			return
		}
		first = make([]byte, 8)
		_, err = f.ReadAt(first, 0)
		_ = f.Close()
		if err != nil {
			return
		}
		// A real kitty removes a t=s object once it has read it, and a t=t
		// file. The guest's own object is the guest's to remove.
		if keys["t"] != "f" && (c.shmPrefix == "" || !strings.HasPrefix(string(name), c.shmPrefix)) {
			_ = os.Remove(path)
		}
	default:
		if len(payload) < 12 {
			return
		}
		b, err := base64.StdEncoding.DecodeString(string(payload[:12]))
		if err != nil {
			return
		}
		first = b
	}
	if keys["o"] == "z" {
		return // compressed; the number is not readable without inflating
	}
	seq := int(binary.LittleEndian.Uint64(first))
	if seq > 0 && seq < 1<<30 {
		if _, dup := c.seen[seq]; !dup {
			c.seen[seq] = now
		}
	}
}

// frameReport is one case's result, also written as JSON.
type frameReport struct {
	Case        string          `json:"case"`
	Seconds     float64         `json:"seconds"`
	GuestFrames int             `json:"guest_frames"`
	HostFrames  int             `json:"host_frames"`
	Shown       int             `json:"shown"`
	Interval    perf.Stats      `json:"interval"`
	Latency     perf.Stats      `json:"latency"`
	ClientCPU   float64         `json:"client_cpu_ms_per_s"`
	DaemonCPU   float64         `json:"daemon_cpu_ms_per_s"`
	HostMBps    float64         `json:"host_mb_per_s"`
	Images      int             `json:"images"`
	Intervals   []time.Duration `json:"intervals_ns"`
	Latencies   []time.Duration `json:"latencies_ns"`
	Extra       map[string]any  `json:"extra,omitempty"`
}

func (r frameReport) line() string {
	ms := func(d time.Duration) string { return fmt.Sprintf("%.2f", float64(d)/1e6) }
	return fmt.Sprintf("PERF frames/%s: %.0f fps shown (%d of %d guest frames, %d host frames)  interval p50 %s p95 %s p99 %s max %s ms  latency p50 %s p95 %s p99 %s ms  cpu client %.0f daemon %.0f ms/s  host %.1f MB/s",
		r.Case, float64(r.Shown)/r.Seconds, r.Shown, r.GuestFrames, r.HostFrames,
		ms(r.Interval.P50), ms(r.Interval.P95), ms(r.Interval.P99), ms(r.Interval.Max),
		ms(r.Latency.P50), ms(r.Latency.P95), ms(r.Latency.P99),
		r.ClientCPU, r.DaemonCPU, r.HostMBps)
}

// cpuTicks is a process's user plus system time in clock ticks.
func cpuTicks(pid int) int64 {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	s := string(b)
	s = s[strings.LastIndexByte(s, ')')+2:]
	f := strings.Fields(s)
	if len(f) < 13 {
		return 0
	}
	u, _ := strconv.ParseInt(f[11], 10, 64)
	k, _ := strconv.ParseInt(f[12], 10, 64)
	return u + k
}

// readGuestLog reads framepace's log: sequence number to ready time.
func readGuestLog(path string) map[int]int64 {
	out := map[int]int64{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		a, b, ok := strings.Cut(sc.Text(), " ")
		if !ok {
			continue
		}
		seq, err1 := strconv.Atoi(a)
		ns, err2 := strconv.ParseInt(b, 10, 64)
		if err1 == nil && err2 == nil {
			out[seq] = ns
		}
	}
	return out
}

func buildFramepace(t *testing.T) string {
	t.Helper()
	framepaceOnce.Do(func() {
		dir, err := os.MkdirTemp("", "framepace")
		if err != nil {
			framepaceErr = err
			return
		}
		bin := filepath.Join(dir, "framepace")
		build := exec.Command("go", "build", "-o", bin, "./framepace")
		if out, err := build.CombinedOutput(); err != nil {
			framepaceErr = fmt.Errorf("build framepace: %v\n%s", err, out)
			return
		}
		framepaceBin = bin
	})
	if framepaceErr != nil {
		t.Fatalf("%v", framepaceErr)
	}
	return framepaceBin
}

var (
	framepaceOnce sync.Once
	framepaceBin  string
	framepaceErr  error
)

// attachArgs is the client's command line, with a profile server when addr
// is set.
func attachArgs(addr string) []string {
	if addr == "" {
		return []string{"attach", "fp"}
	}
	return []string{"attach", "fp", "--pprof", addr}
}

// guestSeqBase separates the sequence numbers of guests in different panes.
const guestSeqBase = 10_000_000

// frameCase is one measurement.
type frameCase struct {
	name   string
	maxFPS string // appearance.max_fps
	mode   string // framepace mode
	fps    int    // guest rate, 0 for as fast as it can
	panes  int    // panes running the guest, tiled
	idle   int    // extra idle panes
	kitty  bool   // answer the kitty graphics probe
}

func perfFrameSecs() time.Duration {
	if s, err := strconv.Atoi(os.Getenv("TUIOS_PERF_SECS")); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	return 5 * time.Second
}

func runFrameCase(t *testing.T, fc frameCase) frameReport {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	cfg := fmt.Sprintf("[appearance]\nmax_fps = %s\n[startup]\ntiled = true\n", fc.maxFPS)
	writeConfig(t, base, cfg)
	// TUIOS_PERF_PPROF names a directory for CPU profiles of the client and
	// the daemon, taken over the measured window.
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
	// A truecolor host, as kitty and ghostty are: without it the renderer
	// converts every colour to the 256-colour palette, which the hosts this
	// measures never ask for.
	env := append(perfEnvVars(), "TUIOS_SIXEL_GRAPHICS=0", "COLORTERM=truecolor", "TUIOS_KITTY_ANIMATION=1")
	if v := os.Getenv("TUIOS_DBG_COMPOSE"); v != "" {
		env = append(env, "TUIOS_DBG_COMPOSE="+v)
	}
	prefix := standInShmPrefix(t)
	clock.shmPrefix = prefix
	term := startIn(t, base, startOpts{
		cols: perfCols, rows: perfRows,
		args: attachArgs(clientAddr),
		env:  env,
		out:  clock,
	})
	clock.mu.Lock()
	clock.screen = func() string { return term.Screen().Text() }
	clock.mu.Unlock()
	if fc.kitty {
		answerKittyProbe(t, term, clock.probe, perfCols, perfRows)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached: %v\n%s", err, term.Snapshot())
	}
	ids := windowIDs(t, base, "fp")
	for len(ids) < fc.panes+fc.idle {
		newWindowIn(t, base, "fp")
		ids = windowIDs(t, base, "fp")
	}
	time.Sleep(500 * time.Millisecond)

	bin := buildFramepace(t)
	dir := t.TempDir()
	var logs []string
	for i := range fc.panes {
		lg := filepath.Join(dir, fmt.Sprintf("guest%d.log", i))
		logs = append(logs, lg)
		cmd := fmt.Sprintf("clear; TUIOS_E2E_SHM_PREFIX=%s exec %s %s %d %s %d\n", prefix, bin, fc.mode, fc.fps, lg, i*guestSeqBase)
		if err := paneSend(base, "fp", ids[i], cmd); err != nil {
			t.Fatalf("start the guest: %v", err)
		}
	}
	// Warm up: the guest takes the screen and the first frames settle.
	time.Sleep(1500 * time.Millisecond)

	daemonPid := 0
	if raw, err := os.ReadFile(filepath.Join(base, "XDG_RUNTIME_DIR", "tuios", "tuios.sock.pid")); err == nil {
		daemonPid, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
	}
	clock.mu.Lock()
	if dir := os.Getenv("TUIOS_PERF_DUMP"); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		clock.dump, _ = os.Create(filepath.Join(dir, strings.ReplaceAll(fc.name, "/", "_")+".dump"))
	}
	ends0 := len(clock.ends)
	bytes0 := clock.hostBytes
	images0 := clock.images
	clock.mu.Unlock()
	c0, d0 := cpuTicks(term.Pid()), cpuTicks(daemonPid)
	t0 := time.Now()
	var profWG sync.WaitGroup
	if profDir != "" {
		_ = os.MkdirAll(profDir, 0o755)
		secs := int(perfFrameSecs() / time.Second)
		for who, addr := range map[string]string{"client": clientAddr, "daemon": daemonAddr} {
			profWG.Go(func() {
				body, err := httpGet(fmt.Sprintf("http://%s/debug/pprof/profile?seconds=%d", addr, secs))
				if err != nil {
					t.Logf("profile %s: %v", who, err)
					return
				}
				name := strings.ReplaceAll(fc.name, "/", "_") + "." + who + ".pprof"
				_ = os.WriteFile(filepath.Join(profDir, name), []byte(body), 0o644)
			})
		}
	}
	time.Sleep(perfFrameSecs())
	t1 := time.Now()
	c1, d1 := cpuTicks(term.Pid()), cpuTicks(daemonPid)
	clock.mu.Lock()
	ends := append([]int64(nil), clock.ends[ends0:]...)
	hostBytes := clock.hostBytes - bytes0
	images := clock.images - images0
	seen := make(map[int]int64, len(clock.seen))
	for k, v := range clock.seen {
		seen[k] = v
	}
	clock.mu.Unlock()
	profWG.Wait()
	// Let the guests flush their logs.
	time.Sleep(200 * time.Millisecond)

	secs := t1.Sub(t0).Seconds()
	tick := float64(1000) / 100 // ms per clock tick at USER_HZ 100
	r := frameReport{
		Case:       fc.name,
		Seconds:    secs,
		HostFrames: len(ends),
		ClientCPU:  float64(c1-c0) * tick / secs,
		DaemonCPU:  float64(d1-d0) * tick / secs,
		HostMBps:   float64(hostBytes) / secs / 1e6,
		Images:     images,
	}
	// For each guest: which of its frames reached the host, how long each
	// took, and the intervals between the host frames that showed one.
	// Several guest frames shown by one host frame arrive together, so the
	// interval is between distinct arrivals. The report's own numbers are
	// the first guest's; the worst guest's shown rate and p99 interval go in
	// Extra.
	lo, hi := t0.UnixNano(), t1.UnixNano()
	worstShown, worstP99 := -1.0, time.Duration(0)
	var guestFPS []float64
	for gi, lg := range logs {
		guest := readGuestLog(lg)
		var arrivals []int64
		var lat []time.Duration
		drawn, shown := 0, 0
		for seq, ready := range guest {
			if ready < lo || ready > hi {
				continue
			}
			drawn++
			if at, ok := seen[seq]; ok {
				shown++
				lat = append(lat, time.Duration(at-ready))
				arrivals = append(arrivals, at)
			}
		}
		slices.Sort(arrivals)
		var iv []time.Duration
		var last int64
		for _, at := range arrivals {
			if last != 0 && at-last > int64(200*time.Microsecond) {
				iv = append(iv, time.Duration(at-last))
			}
			if last == 0 || at-last > int64(200*time.Microsecond) {
				last = at
			}
		}
		if gi == 0 {
			r.GuestFrames, r.Shown, r.Latencies, r.Intervals = drawn, shown, lat, iv
		}
		fps := float64(shown) / secs
		guestFPS = append(guestFPS, fps)
		if worstShown < 0 || fps < worstShown {
			worstShown = fps
		}
		if p := perf.Dist(iv).Stats().P99; p > worstP99 {
			worstP99 = p
		}
	}
	var hostIv perf.Dist
	for i := 1; i < len(ends); i++ {
		hostIv = append(hostIv, time.Duration(ends[i]-ends[i-1]))
	}
	hs := hostIv.Stats()
	r.Extra = map[string]any{
		"host_interval_min_ms": float64(hs.Min) / 1e6,
		"host_interval_p50_ms": float64(hs.P50) / 1e6,
		"host_interval_p95_ms": float64(hs.P95) / 1e6,
	}
	if len(logs) > 1 {
		r.Extra["worst_guest_fps"] = worstShown
		r.Extra["worst_guest_p99_ms"] = float64(worstP99) / 1e6
		r.Extra["guest_fps"] = guestFPS
	}
	if len(r.Intervals) == 0 {
		for i := 1; i < len(ends); i++ {
			r.Intervals = append(r.Intervals, time.Duration(ends[i]-ends[i-1]))
		}
	}
	r.Interval = perf.Dist(r.Intervals).Stats()
	r.Latency = perf.Dist(r.Latencies).Stats()
	t.Log(r.line())
	t.Logf("PERF frames/%s: host frame interval min %.2f p50 %.2f p95 %.2f ms", fc.name,
		r.Extra["host_interval_min_ms"], r.Extra["host_interval_p50_ms"], r.Extra["host_interval_p95_ms"])
	if w, ok := r.Extra["worst_guest_fps"]; ok {
		t.Logf("PERF frames/%s: worst guest %.0f fps shown, interval p99 %.2f ms; every guest %.0f", fc.name, w, r.Extra["worst_guest_p99_ms"], r.Extra["guest_fps"])
	}
	if dir := os.Getenv("TUIOS_PERF_OUT"); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		for i, lg := range logs {
			if b, err := os.ReadFile(lg); err == nil {
				_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s.guest%d.log", strings.ReplaceAll(fc.name, "/", "_"), i)), b, 0o644)
			}
		}
		b, _ := json.Marshal(r)
		_ = os.WriteFile(filepath.Join(dir, strings.ReplaceAll(fc.name, "/", "_")+".json"), b, 0o644)
	}
	return r
}

// TestPerfFrames measures the frame pacing cases. TUIOS_PERF_CASES narrows
// them to the names that contain one of its comma-separated words.
func TestPerfFrames(t *testing.T) {
	perfGate(t)
	cases := []frameCase{
		{name: "text-240guest/max120", maxFPS: "120", mode: "text", fps: 240, panes: 1},
		{name: "text-240guest/max240", maxFPS: "240", mode: "text", fps: 240, panes: 1},
		{name: "text-120guest/max120", maxFPS: "120", mode: "text", fps: 120, panes: 1},
		{name: "scroll-flood/max120", maxFPS: "120", mode: "scroll", fps: 0, panes: 1},
		{name: "text-9panes/max120", maxFPS: "120", mode: "text", fps: 120, panes: 9},
		{name: "shm-120guest/max120", maxFPS: "120", mode: "shm", fps: 120, panes: 1, kitty: true},
		{name: "shm-240guest/max240", maxFPS: "240", mode: "shm", fps: 240, panes: 1, kitty: true},
		{name: "shm-60guest/max120", maxFPS: "120", mode: "shm", fps: 60, panes: 1, kitty: true},
		{name: "patch-120guest/max120", maxFPS: "120", mode: "patch", fps: 120, panes: 1, kitty: true},
		{name: "b64-60guest/max120", maxFPS: "120", mode: "b64", fps: 60, panes: 1, kitty: true},
	}
	want := os.Getenv("TUIOS_PERF_CASES")
	for _, fc := range cases {
		if want != "" {
			match := false
			for _, w := range strings.Split(want, ",") {
				if w != "" && strings.Contains(fc.name, w) {
					match = true
				}
			}
			if !match {
				continue
			}
		}
		t.Run(fc.name, func(t *testing.T) { runFrameCase(t, fc) })
	}
}

// TestFramePacingKeepsTheGuestsRate asserts what the frame clock is for: a
// guest that animates at the frame rate has every frame shown, at an even
// pace, and max_fps 240 draws 240 frames a second of a guest that draws 240.
// Wall-clock thresholds need a machine that is not busy with something else,
// so it runs only with TUIOS_E2E_PERF set, like TestMaxFPS240DrawsPastTheOldClamp.
//
// Before the frame clock a 120 Hz guest showed 95 to 105 of its frames at
// max_fps 120 with a p95 interval of 17 ms and a p50 latency of 11 to 15 ms,
// and max_fps 240 drew 124.
func TestFramePacingKeepsTheGuestsRate(t *testing.T) {
	if os.Getenv("TUIOS_E2E_PERF") == "" {
		t.Skip("set TUIOS_E2E_PERF=1 to measure frame pacing")
	}
	for _, c := range []struct {
		fc         frameCase
		minFPS     float64
		maxP95     time.Duration
		maxLatency time.Duration // p50, guest to host
	}{
		{frameCase{name: "pace-120guest/max120", maxFPS: "120", mode: "text", fps: 120, panes: 1}, 111, 11 * time.Millisecond, 12 * time.Millisecond},
		{frameCase{name: "pace-240guest/max240", maxFPS: "240", mode: "text", fps: 240, panes: 1}, 200, 7500 * time.Microsecond, 10 * time.Millisecond},
	} {
		t.Run(c.fc.name, func(t *testing.T) {
			r := runFrameCase(t, c.fc)
			if fps := float64(r.Shown) / r.Seconds; fps < c.minFPS {
				t.Errorf("the guest drew %d frames and %d reached the host: %.0f a second, want at least %.0f",
					r.GuestFrames, r.Shown, fps, c.minFPS)
			}
			if r.Interval.P95 > c.maxP95 {
				t.Errorf("the p95 interval between frames was %v, want at most %v", r.Interval.P95, c.maxP95)
			}
			if r.Latency.P50 > c.maxLatency {
				t.Errorf("a frame took %v (p50) from the guest to the host, want at most %v", r.Latency.P50, c.maxLatency)
			}
		})
	}
}

// TestPerfFloodDrain times a finite flood from the command to its last line on
// the host, and counts the frames the host got while it ran. A client that
// falls behind its pane paints the flood after the program is gone, so the
// time is the drain as much as the flood.
func TestPerfFloodDrain(t *testing.T) {
	perfGate(t)
	took, frames, st := floodDrain(t)
	t.Logf("PERF flood-drain: 1500000 lines of 190 columns in %v, %d host frames (%.0f a second), interval p50 %v p95 %v max %v",
		took.Round(time.Millisecond), frames, float64(frames)/took.Seconds(), st.P50.Round(10*time.Microsecond), st.P95.Round(10*time.Microsecond), st.Max.Round(time.Millisecond))
}

// floodDrain runs 285 MB of 190-column lines through one pane at max_fps 120
// and returns how long it took to reach the host, how many frames the host got
// meanwhile, and the intervals between them.
func floodDrain(t *testing.T) (time.Duration, int, perf.Stats) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[appearance]\nmax_fps = 120\n")
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
	time.Sleep(500 * time.Millisecond)
	clock.mu.Lock()
	ends0 := len(clock.ends)
	clock.mu.Unlock()
	start := time.Now()
	if err := paneSend(base, "fp", ids[0], "yes \"$(printf %0190d 0)\" | head -n 1500000; echo DRAIN$((1+1))DONE\n"); err != nil {
		t.Fatalf("start the flood: %v", err)
	}
	if err := term.WaitForText("DRAIN2DONE", 2*time.Minute); err != nil {
		t.Fatalf("the flood never finished: %v", err)
	}
	took := time.Since(start)
	clock.mu.Lock()
	ends := append([]int64(nil), clock.ends[ends0:]...)
	clock.mu.Unlock()
	var iv perf.Dist
	for i := 1; i < len(ends); i++ {
		iv = append(iv, time.Duration(ends[i]-ends[i-1]))
	}
	return took, len(ends), iv.Stats()
}

// TestFloodStaysSmooth asserts that a pane printing as fast as the client can
// parse, which leaves the client behind it, is still drawn at an even rate.
// Under TUIOS_E2E_PERF like TestFramePacingKeepsTheGuestsRate. Before, a pane
// that fell behind was drawn every 250 ms or at the 96 ms cost ceiling: 13 to
// 37 frames a second, with a p95 gap of 100 ms.
func TestFloodStaysSmooth(t *testing.T) {
	if os.Getenv("TUIOS_E2E_PERF") == "" {
		t.Skip("set TUIOS_E2E_PERF=1 to measure frame pacing")
	}
	r := runFrameCase(t, frameCase{name: "flood-smooth/max120", maxFPS: "120", mode: "scroll", fps: 0, panes: 1})
	fps := float64(r.HostFrames) / r.Seconds
	p95 := r.Extra["host_interval_p95_ms"].(float64)
	if fps < 45 {
		t.Errorf("the flood was drawn at %.0f frames a second, want at least 45", fps)
	}
	if p95 > 30 {
		t.Errorf("the p95 gap between two frames of the flood was %.1f ms, want at most 30", p95)
	}
}
