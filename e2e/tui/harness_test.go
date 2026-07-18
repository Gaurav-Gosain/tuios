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
// # Two harness footguns this file works around
//
//  1. WaitStable can report stability against a pre-action frame: called right
//     after sending input, its quiet window can elapse before tuios has reacted.
//     Everything here waits on expected content instead.
//
//  2. tuios boots into window-management mode, where plain characters are
//     window-manager commands rather than shell input, and for 150ms after
//     entering terminal mode it deliberately swallows unmodified single-character
//     keys. enterTerminalMode handles both.
package tuie2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const (
	welcomeText = "Terminal UI Operating System"
	welcomeHint = "Press 'n' for a new window"

	// insertGuard is tuios's post-terminal-mode suppression window for
	// unmodified single-character keys (internal/input/keyboard_terminal.go).
	insertGuard = 150 * time.Millisecond

	// bootTimeout is generous because the first run also starts the daemon.
	bootTimeout = 20 * time.Second
	// uiTimeout covers an ordinary UI reaction to a keystroke.
	uiTimeout = 10 * time.Second
)

// tuiosBin is the binary under test, resolved once by TestMain.
var tuiosBin string

func TestMain(m *testing.M) {
	if os.Getenv("TUIOS_E2E") == "" {
		fmt.Fprintln(os.Stderr, "e2e: skipping, set TUIOS_E2E=1 to run (spawns real multiplexer daemons)")
		os.Exit(0)
	}

	if bin := os.Getenv("TUIOS_E2E_BIN"); bin != "" {
		abs, err := filepath.Abs(bin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "e2e: resolve TUIOS_E2E_BIN: %v\n", err)
			os.Exit(1)
		}
		tuiosBin = abs
	} else {
		dir, err := os.MkdirTemp("", "tuios-e2e-bin")
		if err != nil {
			fmt.Fprintf(os.Stderr, "e2e: temp dir: %v\n", err)
			os.Exit(1)
		}
		defer os.RemoveAll(dir)
		tuiosBin = filepath.Join(dir, "tuios")
		build := exec.Command("go", "build", "-o", tuiosBin, "./cmd/tuios")
		build.Dir = "../.."
		build.Stderr = os.Stderr
		build.Stdout = os.Stderr
		if err := build.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "e2e: build tuios: %v\n", err)
			os.Exit(1)
		}
	}

	code := m.Run()
	os.Exit(code)
}

// startOpts configures a tuios instance for a test.
type startOpts struct {
	// cols and rows size the PTY. Zero means 120x40.
	cols, rows int
	// args are extra tuios command-line flags, e.g. "--shared-borders".
	args []string
	// env are extra KEY=VALUE entries layered over the isolated defaults.
	env []string
}

// start spawns tuios in a hermetic environment and returns the terminal plus
// the isolation directory root, which multi-client tests reuse so a second
// client reaches the same daemon.
func start(t *testing.T, o startOpts) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	term := startIn(t, base, o)
	return term, base
}

// xdgKeys is the set of directories redirected per test. XDG_RUNTIME_DIR is the
// important one: the daemon's unix socket lives there, and leaving it pointing
// at /run/user/$UID would attach every test to the developer's live session.
var xdgKeys = []string{
	"XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_STATE_HOME",
	"XDG_CACHE_HOME", "XDG_DATA_HOME",
}

// startIn spawns tuios against an explicit isolation root, so two clients can
// share one daemon by sharing the root.
func startIn(t *testing.T, base string, o startOpts) *tuitest.Terminal {
	t.Helper()

	env := make([]string, 0, len(xdgKeys)+len(o.env)+2)
	for _, key := range xdgKeys {
		dir := filepath.Join(base, key)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("start: mkdir %s: %v", key, err)
		}
		env = append(env, key+"="+dir)
	}
	// A predictable POSIX shell, and no user rc files changing the prompt.
	env = append(env, "SHELL=/bin/sh", "ENV=", "PS1=$ ")
	env = append(env, o.env...)

	cols, rows := o.cols, o.rows
	if cols == 0 {
		cols, rows = 120, 40
	}

	argv := append([]string{tuiosBin}, o.args...)
	// Animations make frames non-deterministic without testing anything these
	// assertions care about.
	argv = append(argv, "--no-animations")

	logPath := filepath.Join(t.TempDir(), "pty.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("start: create pty log: %v", err)
	}
	t.Cleanup(func() { _ = logFile.Close() })

	term := tuitest.StartT(t, argv,
		tuitest.WithSize(cols, rows),
		tuitest.WithTerm("xterm-256color"),
		tuitest.WithEnv(env...),
		tuitest.WithLog(logFile),
	)
	return term
}

// waitBoot blocks until the welcome screen is up.
func waitBoot(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.WaitForText(welcomeText, bootTimeout); err != nil {
		t.Fatalf("tuios never reached the welcome screen: %v", err)
	}
}

// newWindow presses 'n' and waits until the dock reports one more window than
// before. Waiting on the count rather than on "the frame changed" avoids
// WaitStable's documented pre-action-frame trap, and it is a real assertion
// that the window exists rather than that something was repainted.
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

// settledWindowCount reads the dock's window count only once the same value has
// been observed twice in a row.
//
// A single read can catch a half-drawn dock. That is not hypothetical: taking
// the "before" count straight off the screen immediately after a resize read 0
// while three windows existed, and newWindow then waited forever for a count of
// 1. Because the number is used as a baseline for a later equality check, a
// transient misread does not self-correct, it poisons the assertion.
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

// dockStatus matches the dock bar's leftmost status field, "<workspace>:<count>",
// e.g. "1:2" for two windows on workspace one.
//
// Renamed windows also put a "1:name" pill on the same row, so this insists on
// digits after the colon and only the first match on the row is used; the status
// field is leftmost, and a name pill can never match an all-digit second group
// unless the user names a window a bare number, which countWindows tolerates by
// preferring the leftmost match.
var dockStatus = regexp.MustCompile(`([0-9]+):([0-9]+)`)

// countWindows reads the live window count out of the dock status field. Tests
// use it to wait for a window to actually exist rather than for a frame to
// merely change, which is what makes create/close assertions trustworthy.
// It returns -1 when the dock is not on screen, so callers can distinguish
// "no windows" from "could not tell".
func countWindows(s tuitest.Screen) int {
	_, rows := s.Size()
	for r := rows - 1; r >= max(0, rows-3); r-- {
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

// waitWindowCount blocks until the dock reports exactly n windows.
func waitWindowCount(t *testing.T, term *tuitest.Terminal, n int, what string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == n
	}, uiTimeout); err != nil {
		t.Fatalf("%s: window count never reached %d (last %d): %v\n%s",
			what, n, countWindows(term.Screen()), err, term.Snapshot())
	}
}

// enterTerminalMode switches the focused window into terminal mode and waits out
// the 150ms single-character suppression guard, after which typed text reaches
// the shell instead of being eaten as a window-manager binding.
//
// The "i" is retried because a single one is not reliably delivered: tuios
// suppresses unmodified single-character keys for a window after several mode
// transitions, and a keystroke that lands inside one of those windows is
// silently dropped with no feedback. Retrying is what a user does, and it keeps
// the tests from failing for a reason that has nothing to do with what they are
// asserting. Terminal mode is idempotent, so a duplicate "i" that does arrive
// costs nothing.
func enterTerminalMode(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	const attempts = 4
	for range attempts {
		if err := term.SendKeys("i"); err != nil {
			t.Fatalf("send 'i': %v", err)
		}
		if err := term.WaitForText("Terminal Mode", 3*time.Second); err == nil {
			time.Sleep(insertGuard + 150*time.Millisecond)
			return
		}
		if _, exited := term.ExitCode(); exited {
			t.Fatalf("tuios exited while entering terminal mode\n%s", term.Snapshot())
		}
	}
	t.Fatalf("did not enter terminal mode after %d attempts\n%s", attempts, term.Snapshot())
}

// leaveTerminalMode returns to window management mode via Alt+Esc.
func leaveTerminalMode(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("send alt+esc: %v", err)
	}
	if err := term.WaitForText("Window Management Mode", uiTimeout); err != nil {
		t.Fatalf("did not return to window management mode: %v", err)
	}
	// The mode switch also re-arms input handling; give it the same beat.
	time.Sleep(insertGuard)
}

// runInShell types a command into the focused pane's shell and waits for a
// marker the shell itself must compute, so the assertion cannot pass on a mere
// echo of the keystrokes.
func runInShell(t *testing.T, term *tuitest.Terminal, cmd, want string, timeout time.Duration) {
	t.Helper()
	if err := term.SendKeys(cmd, tuitest.Enter); err != nil {
		t.Fatalf("type %q: %v", cmd, err)
	}
	if want == "" {
		return
	}
	if err := term.WaitForText(want, timeout); err != nil {
		t.Fatalf("command %q never produced %q: %v", cmd, want, err)
	}
}

// alive fails the test if tuios has exited, attaching the last screen.
func alive(t *testing.T, term *tuitest.Terminal, when string) {
	t.Helper()
	if code, exited := term.ExitCode(); exited {
		t.Fatalf("tuios exited with code %d %s\n%s", code, when, term.Snapshot())
	}
}

// altScreenCmd builds a shell command that enters the alternate screen, paints
// marker lines with a DELIBERATELY BLANK FIRST ROW, and then sits idle emitting
// nothing at all.
//
// The blank first row is the whole point. clipWindowContent used to measure a
// window's width from lines[0] alone; an unfocused tiled pane under shared
// borders is composited from raw, right-trimmed lines, so an application whose
// top row is empty measured as zero columns wide and the leftmost tile at x=0
// tripped the "entirely offscreen" guard and was discarded. Emitting nothing
// afterwards matters too: it means only the cached layer can keep the pane on
// screen, which is exactly the path that used to serve an empty layer forever.
//
// The escape is written as the POSIX \0ddd octal form, "\033" being \0 followed
// by the two octal digits 33 (decimal 27, ESC). Writing "\0033" instead is a
// trap: printf reads three octal digits, emits ESC, and leaves a stray literal
// '3' that breaks the sequence, which is silent because the pane then shows the
// escape as text rather than acting on it.
//
// The pane execs sleep so the shell is replaced and the process emits nothing
// further, which is the idle-alt-screen state the cache bug lived in.
func altScreenCmd(markers ...string) string {
	var b strings.Builder
	// Enter alt screen, clear it, home the cursor, then a bare newline so row
	// one is blank.
	b.WriteString(`printf '\033[?1049h\033[2J\033[H\n`)
	for _, m := range markers {
		b.WriteString("  " + m + `\n`)
	}
	b.WriteString(`'; exec sleep 120`)
	return b.String()
}

// screenHas reports whether the rendered screen contains every marker.
func screenHas(s tuitest.Screen, markers ...string) bool {
	text := s.Text()
	for _, m := range markers {
		if !strings.Contains(text, m) {
			return false
		}
	}
	return true
}

// waitForAll blocks until every marker is on screen at the same time.
func waitForAll(t *testing.T, term *tuitest.Terminal, timeout time.Duration, what string, markers ...string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, markers...)
	}, timeout); err != nil {
		t.Fatalf("%s: markers %v never all on screen together: %v", what, markers, err)
	}
}

// tuiosCLI runs a tuios subcommand (ls, send-keys, ...) against the daemon
// living under an isolation root, and returns its combined output.
func tuiosCLI(t *testing.T, base string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(tuiosBin, args...)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+filepath.Join(base, key))
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// killDaemon shuts down the daemon rooted at base. Tests that create a daemon
// register this so a failure cannot leave one running; the maintainer's machine
// has been flooded by leaked test daemons before.
func killDaemon(t *testing.T, base string) {
	t.Helper()
	var once sync.Once
	t.Cleanup(func() {
		once.Do(func() {
			out, err := tuiosCLI(t, base, "kill-server")
			if err != nil {
				t.Logf("kill-server (best effort): %v: %s", err, strings.TrimSpace(out))
			}
		})
	})
}

// enableTiling toggles tiling on and waits for the layout to actually be tiled,
// which it establishes by requiring every pane's marker to be on screen at the
// same time. Floating windows overlap, so only the topmost one's content shows;
// tiled windows all show at once.
//
// This deliberately does not wait for the "Tiling Mode Enabled" toast. Toasts
// linger and stack, and with several windows open the one being waited for can
// be pushed out of the visible toast area, so waiting on it is flaky in exactly
// the situation the tiling tests care about.
func enableTiling(t *testing.T, term *tuitest.Terminal, markers ...string) {
	t.Helper()
	if err := term.SendKeys("t"); err != nil {
		t.Fatalf("toggle tiling: %v", err)
	}
	if len(markers) == 0 {
		// Nothing to key off; give the relayout a beat.
		time.Sleep(500 * time.Millisecond)
		return
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, markers...)
	}, uiTimeout); err != nil {
		t.Fatalf("layout never became tiled (markers %v not all visible together): %v\n%s",
			markers, err, term.Snapshot())
	}
}
