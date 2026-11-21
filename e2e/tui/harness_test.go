// Package tuie2e drives a real tuios binary inside a real pseudo-terminal and
// asserts on what a user would actually see on screen.
//
// # Why this is a separate, nested Go module
//
// The harness (github.com/Gaurav-Gosain/tuitest) is a test-only dependency that
// spawns PTYs and vendors a VT emulator. tuitest is public, so requiring it
// would work, but it would land in the dependency graph of everyone who imports
// tuios, for code that only ever runs under `go test`. A nested module keeps the
// tests versioned alongside the code they guard while leaving the main module's
// go.mod and go.sum untouched.
//
// It is nested under e2e/ rather than placed beside it because e2e/ already
// holds the build-tagged control-plane suite, which belongs to the main module.
// A nested module is invisible to the parent, so `go test -tags e2e ./e2e/...`
// at the repo root still runs exactly what it ran before and does not descend
// here.
//
// # Running
//
//	cd e2e/tui && TUIOS_E2E=1 go test -count=1 ./...
//
// Without TUIOS_E2E the whole package skips, because every test here forks a
// full multiplexer plus its shell children. TestMain builds the binary under
// test once into a temporary directory; set TUIOS_E2E_BIN to point the same
// assertions at a prebuilt binary, which is how the negative controls described
// in NEGATIVE_CONTROLS.md are run.
//
// # Always pass -count=1
//
// This is not a style preference. Go caches test results, and a cached result
// survives a change of TUIOS_E2E_BIN, so re-running the suite against a
// deliberately broken binary can replay the previous PASS and report that a
// regression test caught nothing when in fact it was never executed. That
// happened while writing this suite and briefly made a genuine negative control
// look like a false one. Every documented command here passes -count=1, and any
// new verification run must too.
//
// # -race is not useful here
//
// The race detector instruments the test process, and the code under test runs
// in a separate tuios process, so -race on this package costs time and detects
// nothing. Race coverage for tuios's own internals belongs in the main module's
// unit tests, which is where the emulator-resize race is pinned.
//
// # Isolation
//
// Every tuios instance gets a private set of XDG directories under the test's
// own TempDir, so the daemon socket, session state, and config file never touch
// the developer's real ~/.config, ~/.local/state, or /run/user/$UID. tuitest
// starts the child with setsid and tears down the whole process group, so the
// daemon and its panes are reaped even when a test fails.
//
// # Harness footguns this file works around
//
//  1. WaitStable can report stability against a pre-action frame: called right
//     after sending input, its quiet window can elapse before tuios has reacted.
//     Everything here waits on expected content instead.
//
//  2. tuios boots into window-management mode, where plain characters are
//     window-manager commands rather than shell input, and for 150ms after
//     entering terminal mode it deliberately swallows unmodified single-character
//     keys. enterTerminalMode handles both. An *attached* client is the other way
//     round and boots into terminal mode; windowManagementMode is the fix.
//
//  3. An emulator panic in tuitest takes the whole test binary with it. A
//     panic in its pump goroutine cannot be recovered by the test, so every
//     result not yet printed is lost and t.Cleanup never runs. It killed an
//     850 second fuzz campaign and every finding in it.
//
//     The panic that did it is fixed. tuitest used to panic on a scroll region
//     past the bottom of the screen after a shrink, which a client is entitled
//     to emit when a frame lands after the PTY shrank. These three lines
//     reproduced it, and they pass against the tuitest this module pins
//     (re-run on 2026-10-08):
//
//     term := tuitest.StartT(t, []string{"/bin/sh"}, tuitest.WithSize(80, 24))
//     term.Resize(80, 10)
//     term.SendKeys(`printf '\033[1;24r\033[3S'`, tuitest.Enter)
//
//     tuitest still has no recover around its emulator, so the next emulator
//     bug would do the same. Until it has one, a long campaign runs in seed
//     batches so one crash costs one batch: see TUIOS_FUZZ_FIRST on
//     TestFuzzPTY.
package tuie2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testknob"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
)

const (
	welcomeText = "Terminal UI Operating System"
	welcomeHint = "new window"

	insertGuard = 150 * time.Millisecond

	bootTimeout       = 20 * time.Second
	uiTimeout         = 10 * time.Second
	shellTimeout      = 20 * time.Second
	soakTimeout       = 30 * time.Second
	bulkTimeout       = 60 * time.Second
	terminalModeProbe = 3 * time.Second
)

var tuiosBin string

// TestMain exists only to turn runE2E's return into an exit status. The build
// directory is removed by a defer inside runE2E, and os.Exit runs no defers, so
// every statement that needs cleaning up lives in the function below rather
// than here. A build directory holds a linked tuios binary, and on a machine
// where /tmp is a tmpfs each leaked one is memory that never comes back.
func TestMain(m *testing.M) {
	runRelayClientIfAsked()
	runThrottleIfAsked()
	runStandInIfAsked()
	runAsTuiosIfAsked()
	os.Exit(runE2E(m))
}

func runE2E(m *testing.M) (code int) {
	if os.Getenv("TUIOS_E2E") == "" {
		fmt.Fprintln(os.Stderr, "e2e: skipping, set TUIOS_E2E=1 to run (spawns real multiplexer daemons)")
		return 0
	}
	if err := setupBackend(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: %v\n", err)
		return 1
	}
	if err := startRunTemp(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: temp dir: %v\n", err)
		return 1
	}
	defer func() { code = finishRunTemp(code) }()
	sweepShortRuntimeRoots()
	testutil.ClearPaneEnv()
	_ = os.Unsetenv("OMARCHY_PATH")
	_ = os.Unsetenv("SWATCH_OMARCHY_DIR")
	for _, key := range harnessDirKeys {
		_ = os.Unsetenv(key)
	}
	_ = os.Setenv(testknob.Env, testknob.Merge(os.Getenv(testknob.Env), "card_check=1"))
	unstub, err := installAgentStubs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: %v\n", err)
		return 1
	}
	defer unstub()
	defer func() { _ = os.RemoveAll(shortRuntimeRoot) }()
	defer func() {
		if otherProtocolDir != "" {
			_ = os.RemoveAll(otherProtocolDir)
		}
		if newerDir != "" {
			_ = os.RemoveAll(newerDir)
		}
	}()

	if bin := os.Getenv("TUIOS_E2E_BIN"); bin != "" {
		abs, err := filepath.Abs(bin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "e2e: resolve TUIOS_E2E_BIN: %v\n", err)
			return 1
		}
		tuiosBin = abs
	} else {
		dir, err := os.MkdirTemp("", "tuios-e2e-bin")
		if err != nil {
			fmt.Fprintf(os.Stderr, "e2e: temp dir: %v\n", err)
			return 1
		}
		defer os.RemoveAll(dir)
		tuiosBin = filepath.Join(dir, "tuios")
		build := goBuild("-o", tuiosBin, "./cmd/tuios")
		build.Dir = "../.."
		build.Stderr = os.Stderr
		build.Stdout = os.Stderr
		if err := build.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "e2e: build tuios: %v\n", err)
			return 1
		}
	}

	if simOn {
		_ = os.Setenv(simBinEnv, tuiosBin)
	}

	unown, err := ownTuiosOnPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: %v\n", err)
		return 1
	}
	defer unown()

	before := snapshotUserServices()
	code = m.Run()
	if diff := before.changes(); diff != "" {
		fmt.Fprintf(os.Stderr, "e2e: the run changed the user services of the person who ran it. Restore them by hand:\n%s", diff)
		code = 1
	}
	return code
}

type startOpts struct {
	cols, rows    int
	dir           string
	args          []string
	env           []string
	out           io.Writer
	animations    bool
	daemonDefault bool
	shippedLooks  bool
	logPath       *string
	logLimit      int64
	wrap          []string
}

func start(t *testing.T, o startOpts) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	term := startIn(t, base, o)
	return term, base
}

var xdgKeys = []string{
	"XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_STATE_HOME",
	"XDG_CACHE_HOME", "XDG_DATA_HOME",
	"XDG_CONFIG_DIRS", "XDG_DATA_DIRS",
	"HOME",
}

var harnessDirKeys = []string{
	"CLAUDE_CONFIG_DIR", "CODEX_HOME", "COPILOT_HOME", "CURSOR_CONFIG_DIR",
	"GROK_HOME", "HERMES_HOME", "KIMI_CODE_HOME", "PI_CODING_AGENT_DIR",
	"QODER_CONFIG_DIR", "QWEN_HOME", "ANTIGRAVITY_CLI_CONFIG_DIR", "VIBE_HOME",
	"APPDATA", "LOCALAPPDATA",
}

func startIn(t *testing.T, base string, o startOpts) *tuitest.Terminal {
	t.Helper()

	killDaemon(t, base)

	env := make([]string, 0, len(xdgKeys)+len(o.env)+2)
	for _, key := range xdgKeys {
		env = append(env, key+"="+xdgDir(base, key))
	}
	for _, key := range []string{"XDG_CACHE_HOME", "XDG_DATA_HOME"} {
		dir := filepath.Join(base, key, "fish", "generated_completions")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("start: mkdir %s: %v", dir, err)
		}
	}
	env = append(env, "SHELL=/bin/sh", "ENV=", "PS1=$ ")
	if !o.daemonDefault {
		env = append(env, "TUIOS_NO_DAEMON=1")
	}
	if o.shippedLooks {
		useShippedLooks(base)
	}
	configHome := xdgDir(base, "XDG_CONFIG_HOME")
	ownTuiosGuard(t, os.Getenv("PATH"))
	ownTuiosEnvGuard(t, o.env)
	for _, kv := range o.env {
		agentPathGuard(t, kv)
		if v, ok := strings.CutPrefix(kv, "XDG_CONFIG_HOME="); ok {
			configHome = v
		}
	}
	pinPreV080LooksIn(t, base, configHome)
	if gorace := os.Getenv("GORACE"); gorace != "" {
		env = append(env, "GORACE="+gorace)
	}
	if v := os.Getenv("TUIOS_CELL_FRAME"); v != "" {
		env = append(env, "TUIOS_CELL_FRAME="+v)
	}
	if dir := os.Getenv("GOCOVERDIR"); dir != "" {
		env = append(env, "GOCOVERDIR="+dir)
	}
	eventsLog := filepath.Join(t.TempDir(), "events.log")
	env = append(env, "TUIOS_EVENTS_LOG="+eventsLog)
	railCheck := filepath.Join(t.TempDir(), "rail-check.log")
	env = append(env, "TUIOS_RAIL_CHECK="+railCheck)
	t.Cleanup(func() {
		if b, err := os.ReadFile(railCheck); err == nil && len(b) > 0 {
			t.Errorf("the rail cache served a stale rail:\n%s", b)
		}
	})
	frameCheck := filepath.Join(t.TempDir(), "frame-check.log")
	env = append(env, "TUIOS_FRAME_CHECK="+frameCheck)
	t.Cleanup(func() {
		if b, err := os.ReadFile(frameCheck); err == nil && len(b) > 0 {
			t.Errorf("a frame differs from a fresh compose:\n%s", b)
		}
	})
	env = append(env, o.env...)

	cols, rows := o.cols, o.rows
	if cols == 0 {
		cols, rows = 120, 40
	}

	argv := append(append(append([]string{}, o.wrap...), tuiosBin), o.args...)
	if !o.animations {
		argv = append(argv, "--no-animations")
	}

	logPath := filepath.Join(t.TempDir(), "pty.log")
	limit := o.logLimit
	if limit == 0 {
		limit = ptyLogLimit
	}
	logFile, err := newBoundedLog(logPath, limit)
	if err != nil {
		t.Fatalf("start: create pty log: %v", err)
	}
	t.Cleanup(func() { _ = logFile.Close() })
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("last bytes of the pty log:\n%q", logFile.Tail(ptyLogTailOnFailure))
		}
	})
	if o.logPath != nil {
		*o.logPath = logPath
	}

	t.Cleanup(func() {
		if t.Failed() {
			logSurvivors(t, base)
		}
	})

	clientDir := o.dir
	if clientDir == "" {
		clientDir = workDirIn(t, base)
	}
	opts := []tuitest.Option{
		tuitest.WithSize(cols, rows),
		tuitest.WithTerm("xterm-256color"),
		tuitest.WithEnv(env...),
		tuitest.WithLog(logFile),
		tuitest.WithDir(clientDir),
		tuitest.WithSixel(),
		tuitest.WithCellSize(hostCellSize(o.env)),
	}
	if o.out != nil {
		opts = append(opts, tuitest.WithOutputMirror(o.out))
	}
	opts = append(opts, traceOptions(t)...)
	var term *tuitest.Terminal
	if simOn {
		term = simStart(t, base, o, argv[len(o.wrap)+1:], env, clientDir, opts)
	}
	if term == nil {
		term = tuitest.StartT(t, argv, opts...)
	}
	traceClient(t, term)
	clientStarts.Store(term, clientStart{at: time.Now(), events: eventsLog})
	return term
}

func hostCellSize(env []string) (int, int) {
	w, h := 8, 16
	for _, kv := range env {
		v, ok := strings.CutPrefix(kv, "TUIOS_CELL_SIZE=")
		if !ok {
			continue
		}
		var cw, ch int
		if n, _ := fmt.Sscanf(v, "%dx%d", &cw, &ch); n == 2 && cw > 0 && ch > 0 {
			w, h = cw, ch
		}
	}
	return w, h
}

type clientStart struct {
	at     time.Time
	events string
}

var clientStarts sync.Map

func keyEvidence(term *tuitest.Terminal, sent string, sentAt time.Time) string {
	var b strings.Builder
	v, _ := clientStarts.Load(term)
	start, _ := v.(clientStart)
	if !start.at.IsZero() {
		fmt.Fprintf(&b, "sent %s %v after the client started, %v ago\n", sent, sentAt.Sub(start.at).Round(time.Millisecond), time.Since(sentAt).Round(time.Millisecond))
	}
	if st, exited := term.ExitStatus(); exited {
		fmt.Fprintf(&b, "the client has exited: %v\n", st)
	} else {
		fmt.Fprintf(&b, "the client still runs (pid %d)\n", term.Pid())
	}
	fmt.Fprintf(&b, "terminal modes the client set: %+v\n", term.TermState())
	if start.events != "" {
		data, err := os.ReadFile(start.events)
		switch {
		case err != nil || len(data) == 0:
			b.WriteString("the client logged no key read at all\n")
		default:
			lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
			fmt.Fprintf(&b, "the last keys the client read (%d logged):\n", len(lines))
			for _, l := range lines[max(0, len(lines)-20):] {
				fmt.Fprintf(&b, "  %s\n", l)
			}
		}
	}
	return b.String()
}

func describeProc(pid int) string {
	dir := filepath.Join("/proc", strconv.Itoa(pid))
	var fields []string
	if status, err := os.ReadFile(filepath.Join(dir, "status")); err == nil {
		for _, line := range strings.Split(string(status), "\n") {
			for _, key := range []string{"Name:", "State:", "PPid:", "Threads:"} {
				if strings.HasPrefix(line, key) {
					fields = append(fields, strings.Join(strings.Fields(line), " "))
				}
			}
		}
	}
	wchan, _ := os.ReadFile(filepath.Join(dir, "wchan"))
	cmdline, _ := os.ReadFile(filepath.Join(dir, "cmdline"))
	return fmt.Sprintf("pid %d, %s, wchan %q, argv %q", pid,
		strings.Join(fields, ", "), wchan, strings.ReplaceAll(string(cmdline), "\x00", " "))
}

func logSurvivors(t *testing.T, base string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		return
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	parent := map[int]int{}
	var inBase []int
	daemons := map[int]bool{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		parent[pid] = procPPid(pid)
		cwd, err := os.Readlink(filepath.Join("/proc", e.Name(), "cwd"))
		if err != nil || !strings.HasPrefix(cwd, base) {
			continue
		}
		inBase = append(inBase, pid)
		if argv := procArgv(pid); len(argv) > 1 && filepath.Base(argv[0]) == filepath.Base(tuiosBin) && argv[1] == "daemon" {
			daemons[pid] = true
		}
	}
	if simOn {
		daemons[os.Getpid()] = true
	}
	underDaemon := func(pid int) bool {
		for seen := 0; pid > 1 && seen < 64; seen++ {
			if daemons[pid] {
				return true
			}
			pid = parent[pid]
		}
		return false
	}
	left := 0
	for _, pid := range inBase {
		if underDaemon(pid) {
			left++
			continue
		}
		t.Logf("still running in %s after the client's teardown: %s", base, describeProc(pid))
	}
	if left > 0 {
		t.Logf("%d processes in %s are the daemon and its panes, which killDaemon stops after this; not listed", left, base)
	}
}

func procPPid(pid int) int {
	status, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(status), "\n") {
		if v, ok := strings.CutPrefix(line, "PPid:"); ok {
			n, _ := strconv.Atoi(strings.TrimSpace(v))
			return n
		}
	}
	return 0
}

func procArgv(pid int) []string {
	cmdline, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")
}

func attachIn(t *testing.T, base, session string, o startOpts) *tuitest.Terminal {
	t.Helper()
	o.args = append(append([]string{}, o.args...), "attach", session)
	term := startIn(t, base, o)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) >= 0
	}, bootTimeout); err != nil {
		t.Fatalf("client never rehydrated session %q: %v\n%s", session, err, term.Snapshot())
	}
	windowManagementMode(t, term)
	return term
}

func windowManagementMode(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	sentAt := time.Now()
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("normalise to window management mode: %v", err)
	}
	if err := term.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("never settled in window management mode: %v\n%s\n%s", err,
			keyEvidence(term, "Alt+Esc (bytes \\x1b\\x1b)", sentAt), term.Snapshot())
	}
	pause(t, "no sign on screen says a client that just booted is ready for keys", insertGuard)
}

func killDaemonNow(t *testing.T, base string) {
	t.Helper()
	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Logf("kill-server under %s (best effort): %v: %s", base, err, strings.TrimSpace(out))
	}
}

func waitBoot(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.WaitForText(welcomeText, bootTimeout); err != nil {
		t.Fatalf("tuios never reached the welcome screen: %v", err)
	}
}

func newWindow(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	before := settledWindowCount(t, term)
	if err := term.SendKeys("n"); err != nil {
		t.Fatalf("send 'n': %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == before+1 && !strings.Contains(s.Text(), welcomeHint)
	}, uiTimeout); err != nil {
		t.Fatalf("no new window after 'n' (count %d -> %d): %v\n%s",
			before, countWindows(term.Screen()), err, term.Snapshot())
	}
}

func waitDockWorkspace(t *testing.T, term *tuitest.Terminal, ws int) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return dockWorkspace(s) == ws
	}, uiTimeout); err != nil {
		t.Fatalf("the dock never showed workspace %d: %v\n%s", ws, err, term.Snapshot())
	}
}

func settledWindowCount(t *testing.T, term *tuitest.Terminal) int {
	t.Helper()
	prev := -2
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		n := countWindows(term.Screen())
		if n >= 0 && n == prev {
			return n
		}
		prev = n
		time.Sleep(60 * time.Millisecond)
	}
	t.Fatalf("dock window count never settled\n%s", term.Snapshot())
	return -1
}

var dockStatus = regexp.MustCompile(`([0-9]+):([0-9]+)`)

func countWindows(s tuitest.Screen) int {
	_, rows := s.Size()
	var order []int
	for r := rows - 1; r >= max(0, rows-3); r-- {
		order = append(order, r)
	}
	if rows > 1 && strings.HasPrefix(strings.TrimSpace(s.Line(1)), "────") {
		order = append(order, 0)
	}
	for _, r := range order {
		if m := dockStatus.FindStringSubmatch(s.Line(r)); m != nil {
			n, err := strconv.Atoi(m[2])
			if err != nil {
				continue
			}
			return n
		}
	}
	return -1
}

func waitWindowCount(t *testing.T, term *tuitest.Terminal, n int, what string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == n
	}, uiTimeout); err != nil {
		t.Fatalf("%s: window count never reached %d (last %d): %v\n%s",
			what, n, countWindows(term.Screen()), err, term.Snapshot())
	}
}

func enterTerminalMode(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	const attempts = 4
	for range attempts {
		if err := term.SendKeys("i"); err != nil {
			t.Fatalf("send 'i': %v", err)
		}
		if err := term.WaitForText("Terminal mode", terminalModeProbe); err == nil {
			pause(t, "no sign on screen says the tiling animation is done", insertGuard+150*time.Millisecond)
			return
		}
		if _, exited := term.ExitCode(); exited {
			t.Fatalf("tuios exited while entering terminal mode\n%s", term.Snapshot())
		}
	}
	t.Fatalf("did not enter terminal mode after %d attempts\n%s", attempts, term.Snapshot())
}

func leaveTerminalMode(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("send alt+esc: %v", err)
	}
	if err := term.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("did not return to window management mode: %v", err)
	}
}

func runInShell(t *testing.T, term *tuitest.Terminal, cmd, want string, timeout time.Duration) {
	t.Helper()
	if want == "" {
		t.Fatalf("runInShell(%q) was given no marker to wait for; "+
			"a command with nothing to assert on cannot fail", cmd)
	}
	if strings.Contains(cmd, want) {
		t.Fatalf("runInShell(%q): the marker %q is in the command, so the "+
			"echo of the keystrokes satisfies the wait", cmd, want)
	}
	if err := term.SendKeys(cmd, tuitest.Enter); err != nil {
		t.Fatalf("type %q: %v", cmd, err)
	}
	if err := term.WaitForText(want, timeout); err != nil {
		t.Fatalf("command %q never produced %q: %v", cmd, want, err)
	}
}

func splitMarker(m string) string {
	return m[:len(m)/2] + `""` + m[len(m)/2:]
}

func renameDialogUp(s tuitest.Screen) bool {
	text := s.Text()
	return strings.Contains(text, "rename") && strings.Contains(text, "esc cancel")
}

func renameWindow(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()

	if err := term.SendKeys("r"); err != nil {
		t.Fatalf("open rename editor: %v", err)
	}
	if err := term.WaitFor(renameDialogUp, uiTimeout); err != nil {
		t.Fatalf("the rename dialog never opened: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(name, tuitest.Enter); err != nil {
		t.Fatalf("type the new name %q: %v", name, err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), name) && !renameDialogUp(s)
	}, uiTimeout); err != nil {
		t.Fatalf("the window never committed the name %q: %v\n%s", name, err, term.Snapshot())
	}
}

func alive(t *testing.T, term *tuitest.Terminal, when string) {
	t.Helper()
	if code, exited := term.ExitCode(); exited {
		t.Fatalf("tuios exited with code %d %s\n%s", code, when, term.Snapshot())
	}
}

func altScreenCmd(markers ...string) string {
	var b strings.Builder
	b.WriteString(`printf '\033[?1049h\033[2J\033[H\n`)
	for _, m := range markers {
		b.WriteString("  " + m + `\n`)
	}
	b.WriteString(`'; exec sleep 120`)
	return b.String()
}

func screenHas(s tuitest.Screen, markers ...string) bool {
	text := s.Text()
	for _, m := range markers {
		if !strings.Contains(text, m) {
			return false
		}
	}
	return true
}

func waitForAll(t *testing.T, term *tuitest.Terminal, timeout time.Duration, what string, markers ...string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, markers...)
	}, timeout); err != nil {
		t.Fatalf("%s: markers %v never all on screen together: %v", what, markers, err)
	}
}

func resolvedPath(t *testing.T, p string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatalf("resolve %s: %v", p, err)
	}
	return real
}

func tuiosCLI(t *testing.T, base string, args ...string) (string, error) {
	t.Helper()
	traceCLI(t, args)
	if out, done := simBeforeCLI(t, base, args); done {
		return out, nil
	}
	return tuiosCLIRun(t, base, args...)
}

func tuiosCLIRun(t *testing.T, base string, args ...string) (string, error) {
	t.Helper()
	pinPreV080Looks(t, base)
	cmd := exec.Command(tuiosBin, args...)
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	ownTuiosEnvGuard(t, cmd.Env)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func workDirIn(t *testing.T, base string) string {
	t.Helper()
	dir := filepath.Join(base, "cwd")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("workDirIn: mkdir %s: %v", dir, err)
	}
	return dir
}

type daemonKey struct {
	t    *testing.T
	base string
}

var registeredKills sync.Map

const cardInvariantLine = "Task invariant broken"

func killDaemon(t *testing.T, base string) {
	t.Helper()
	if _, already := registeredKills.LoadOrStore(daemonKey{t, base}, true); already {
		return
	}
	if simOn {
		t.Cleanup(func() {
			simStopDaemon(base)
			simRealDaemonCheck(t, base)
		})
		return
	}
	t.Cleanup(func() {
		if log, err := os.ReadFile(filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "daemon.log")); err == nil {
			for line := range strings.SplitSeq(string(log), "\n") {
				if strings.Contains(line, cardInvariantLine) {
					t.Errorf("ASSERTION: the daemon broke a task card rule: %s", line)
				}
			}
		}
		if out, err := tuiosCLIRun(t, base, "kill-server"); err != nil {
			t.Logf("kill-server (best effort): %v: %s", err, strings.TrimSpace(out))
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			out, err := tuiosCLIRun(t, base, "ls", "--json")
			var sessions []struct {
				Name string `json:"name"`
			}
			if err != nil || json.Unmarshal([]byte(out), &sessions) != nil || len(sessions) == 0 {
				return
			}
			if !time.Now().Before(deadline) {
				names := make([]string, 0, len(sessions))
				for _, s := range sessions {
					names = append(names, s.Name)
				}
				t.Errorf("daemon under %s survived kill-server with sessions %v still listed; "+
					"a leaked daemon can serve the next test", base, names)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
}

const (
	mouseGap       = 30 * time.Millisecond
	multiClickHold = 15 * time.Millisecond
	multiClickGap  = 25 * time.Millisecond
	gestureGap     = 800 * time.Millisecond
)

func sendMouse(t *testing.T, term *tuitest.Terminal, what string, ev tuitest.MouseEvent) {
	t.Helper()
	sendMouseThenWait(t, term, what, ev, mouseGap)
}

func sendMouseThenWait(t *testing.T, term *tuitest.Terminal, what string, ev tuitest.MouseEvent, pause time.Duration) {
	t.Helper()
	if err := term.SendMouse(ev); err != nil {
		t.Fatalf("%s at (%d,%d): %v", what, ev.Col, ev.Row, err)
	}
	time.Sleep(pause)
}

func mousePress(t *testing.T, term *tuitest.Terminal, col, row int, button tuitest.MouseButton, mods tuitest.KeyMods) {
	t.Helper()
	sendMouse(t, term, "press", tuitest.MouseEvent{
		Col: col, Row: row, Button: button, Action: tuitest.MousePress, Mods: mods,
	})
}

func mouseRelease(t *testing.T, term *tuitest.Terminal, col, row int, button tuitest.MouseButton, mods tuitest.KeyMods) {
	t.Helper()
	sendMouse(t, term, "release", tuitest.MouseEvent{
		Col: col, Row: row, Button: button, Action: tuitest.MouseRelease, Mods: mods,
	})
}

func mouseMotion(t *testing.T, term *tuitest.Terminal, col, row int, button tuitest.MouseButton, mods tuitest.KeyMods) {
	t.Helper()
	sendMouse(t, term, "motion", tuitest.MouseEvent{
		Col: col, Row: row, Button: button, Action: tuitest.MouseDrag, Mods: mods,
	})
}

func clickToType(t *testing.T, term *tuitest.Terminal, col, row int) {
	t.Helper()
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText("Terminal mode", uiTimeout); err != nil {
		t.Fatalf("a click at %d,%d did not start typing in the pane: %v\n%s", col, row, err, term.Snapshot())
	}
}

func mouseClick(t *testing.T, term *tuitest.Terminal, col, row int, button tuitest.MouseButton, mods tuitest.KeyMods) {
	t.Helper()
	mousePress(t, term, col, row, button, mods)
	mouseRelease(t, term, col, row, button, mods)
}

func mouseDrag(t *testing.T, term *tuitest.Terminal, fromCol, fromRow, toCol, toRow int, button tuitest.MouseButton, mods tuitest.KeyMods) {
	t.Helper()
	mousePress(t, term, fromCol, fromRow, button, mods)
	steps := max(abs(toCol-fromCol), abs(toRow-fromRow))
	for i := 1; i <= steps; i++ {
		col := fromCol + (toCol-fromCol)*i/steps
		row := fromRow + (toRow-fromRow)*i/steps
		mouseMotion(t, term, col, row, button, mods)
	}
	mouseRelease(t, term, toCol, toRow, button, mods)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func mouseHover(t *testing.T, term *tuitest.Terminal, col, row int) {
	t.Helper()
	sendMouse(t, term, "hover", tuitest.MouseEvent{
		Col: col, Row: row, Button: tuitest.MouseNone, Action: tuitest.MouseMove,
	})
}

const tilingModeIcon = "\uf00a"

func tilingIsOn(s tuitest.Screen) bool {
	return strings.Contains(s.Text(), tilingModeIcon)
}

func settledTiling(t *testing.T, term *tuitest.Terminal) bool {
	t.Helper()
	prev, seen := false, false
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		now := tilingIsOn(term.Screen())
		if seen && now == prev {
			return now
		}
		prev, seen = now, true
		time.Sleep(60 * time.Millisecond)
	}
	t.Fatalf("the dock never settled on a tiling state\n%s", term.Snapshot())
	return false
}

func enableTiling(t *testing.T, term *tuitest.Terminal, markers ...string) {
	t.Helper()
	if !settledTiling(t, term) {
		if len(markers) == 0 {
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return !strings.Contains(s.Text(), "Tiling o")
			}, 10*time.Second); err != nil {
				t.Fatalf("a tiling message from an earlier step never cleared, so waiting "+
					"for this one would prove nothing: %v\n%s", err, term.Snapshot())
			}
		}
		if err := term.SendKeys("t"); err != nil {
			t.Fatalf("toggle tiling: %v", err)
		}
		if len(markers) == 0 {
			if err := term.WaitForText("Tiling on", uiTimeout); err != nil {
				t.Fatalf("tiling was never enabled: %v\n%s", err, term.Snapshot())
			}
		}
	}
	if len(markers) > 0 {
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return screenHas(s, markers...)
		}, uiTimeout); err != nil {
			t.Fatalf("layout never became tiled (markers %v not all visible together): %v\n%s",
				markers, err, term.Snapshot())
		}
	}
	if !settledTiling(t, term) {
		t.Fatalf("the dock still says the session is not tiled\n%s", term.Snapshot())
	}
}

func disableTiling(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if !settledTiling(t, term) {
		return
	}
	if err := term.SendKeys("t"); err != nil {
		t.Fatalf("toggle tiling: %v", err)
	}
	if err := term.WaitForText("Tiling off", uiTimeout); err != nil {
		t.Fatalf("tiling was never turned off: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Tiling o")
	}, 10*time.Second); err != nil {
		t.Fatalf("the tiling-off message never cleared: %v\n%s", err, term.Snapshot())
	}
}

const unixSocketPathMax = 103

var longestRuntimeSocket = filepath.Join("tuios", "servers", strings.Repeat("n", 14),
	"agent-link-"+strings.Repeat("h", 32)+"-"+strings.Repeat("0", 8)+".sock")

var shortPathSocket = filepath.Join("tuios", "servers", strings.Repeat("n", 14),
	"agent-"+strings.Repeat("0", 36)+".sock")

var shortRuntimeRoot = filepath.Join("/tmp", fmt.Sprintf("tue-%d", os.Getpid()))

func sweepShortRuntimeRoots() {
	const prefix = "tue-"
	entries, err := os.ReadDir("/tmp")
	if err != nil {
		return
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(strings.TrimPrefix(e.Name(), prefix))
		if !e.IsDir() || !strings.HasPrefix(e.Name(), prefix) || err != nil || pid == os.Getpid() {
			continue
		}
		if processAlive(pid) || !ownedByMe(filepath.Join("/tmp", e.Name())) {
			continue
		}
		_ = os.RemoveAll(filepath.Join("/tmp", e.Name()))
	}
}

func ownedByMe(path string) bool {
	st, err := os.Lstat(path)
	if err != nil || !st.IsDir() {
		return false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	return ok && int(sys.Uid) == os.Getuid()
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func commandLinesContaining(t *testing.T, needle string) []string {
	t.Helper()
	out, err := exec.Command("ps", "-A", "-ww", "-o", "args=").Output()
	if err != nil {
		t.Fatalf("list processes: %v", err)
	}
	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, needle) {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	return lines
}

var redirected sync.Map

func xdgDir(base, key string) string {
	dir := filepath.Join(base, key)
	if key != "XDG_RUNTIME_DIR" || len(filepath.Join(dir, longestRuntimeSocket)) <= unixSocketPathMax {
		mustMkdir(dir)
		return dir
	}

	var short string
	var already bool
	for i := 0; ; i++ {
		seed := base
		if i > 0 {
			seed = fmt.Sprintf("%s#%d", base, i)
		}
		sum := sha256.Sum256([]byte(seed))
		short = filepath.Join(shortRuntimeRoot, hex.EncodeToString(sum[:4])[:7])
		if n := len(filepath.Join(short, shortPathSocket)); n > unixSocketPathMax {
			panic(fmt.Sprintf("e2e: the redirected runtime dir %s leaves a %d byte socket path, over the %d byte limit -- pid %d has an unusually long decimal form, or this machine's /tmp is not where it is expected to be",
				short, n, unixSocketPathMax, os.Getpid()))
		}
		var prior any
		prior, already = redirected.LoadOrStore(short, base)
		if !already || prior.(string) == base {
			break
		}
	}
	mustMkdir(short)
	if !already {
		_ = os.Symlink(short, dir)
	}
	return short
}

func mustMkdir(dir string) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		panic("e2e: mkdir " + dir + ": " + err.Error())
	}
}

// TestTheSuiteReadsNoConfigButItsOwn.
//
// The isolation is what makes every other test in this package mean anything,
// and on macOS it was not isolating. A config file is looked up with
// xdg.SearchConfigFile, which walks the config home and then the config dirs.
// Redirecting the home is the whole story on Linux, where the dirs are
// /etc/xdg. On macOS the dirs include ~/.config whatever the home is set to,
// so every test read the developer's own config and their theme, rail and
// hosts came with it.
//
// That is the worst shape a test-harness fault can take: it passed in CI,
// failed locally for reasons unrelated to the change under test, and taught
// whoever was running it to stop believing local runs.
//
// Negative control: dropping XDG_CONFIG_DIRS from xdgKeys fails this on macOS
// and passes on Linux, which is exactly how it went unnoticed.
func TestTheSuiteReadsNoConfigButItsOwn(t *testing.T) {
	requireReal(t, "asserts on OS processes (pids, signals, /proc)")
	base := t.TempDir()

	for _, key := range xdgKeys {
		dir := xdgDir(base, key)
		if dir == "" {
			t.Errorf("%s is redirected to nothing", key)
			continue
		}
		if strings.HasPrefix(dir, base) || strings.HasPrefix(dir, shortRuntimeRoot) {
			continue
		}
		t.Errorf("%s points at %s, which is outside the test's own root", key, dir)
	}

	for _, key := range []string{"XDG_CONFIG_DIRS", "XDG_DATA_DIRS"} {
		if !slices.Contains(xdgKeys, key) {
			t.Errorf("%s is not redirected, so a config outside the test can be read", key)
		}
	}
}
