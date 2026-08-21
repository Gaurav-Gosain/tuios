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
//     keys. enterTerminalMode handles both. An *attached* client is the other way
//     round and boots into terminal mode; windowManagementMode is the fix.
//
//  3. tuitest's own emulator panics on a scroll region wider than the screen,
//     which takes the whole test binary with it. Three lines reproduce it:
//
//     term := tuitest.StartT(t, []string{"/bin/sh"}, tuitest.WithSize(80, 24))
//     term.Resize(80, 10)
//     term.SendKeys(`printf '\033[1;24r\033[3S'`, tuitest.Enter)
//
//     internal/vt/screen.go setVerticalMargins stores DECSTBM's bottom margin
//     without clamping it to the buffer, and ultraviolet's DeleteLineArea limits
//     the delete count against the region but then indexes b.Lines[src] without
//     limiting it against the buffer, so the next scroll up runs off the end.
//     A real terminal clamps DECSTBM to the screen.
//
//     This is not exotic. A client renders a frame for the size it last knew
//     about, the PTY shrinks, and the frame lands afterwards; tuios is entitled
//     to emit that and every real terminal tolerates it. It killed an 850 second
//     fuzz campaign and took every finding in it, because a panic in the pump
//     goroutine cannot be recovered by the test. Until tuitest clamps, a long
//     campaign has to be run in seed batches so one crash costs one batch: see
//     TUIOS_FUZZ_FIRST on TestFuzzPTY.
package tuie2e

import (
	"encoding/json"
	"fmt"
	"io"
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
	welcomeHint = "new window"

	// insertGuard is tuios's post-terminal-mode suppression window for
	// unmodified single-character keys (internal/input/keyboard_terminal.go).
	insertGuard = 150 * time.Millisecond

	// bootTimeout is generous because the first run also starts the daemon.
	bootTimeout = 20 * time.Second
	// uiTimeout covers an ordinary UI reaction to a keystroke.
	uiTimeout = 10 * time.Second
	// shellTimeout covers a whole shell command: typed in, forked, run, and its
	// output back on screen through the daemon. That is why it is longer than an
	// ordinary UI reaction, and it is the budget behind every `echo MARKER`
	// assertion in the suite.
	shellTimeout = 20 * time.Second
	// soakTimeout is shellTimeout for the tests that keep the pane busy while
	// the assertion waits (soak, freeze), where the output being waited for is
	// queued behind everything else those tests are generating.
	soakTimeout = 30 * time.Second
	// bulkTimeout covers a pane producing thousands of lines of scrollback,
	// which is bounded by how fast the emulator can consume them rather than by
	// any UI reaction.
	bulkTimeout = 60 * time.Second
	// terminalModeProbe is the per-attempt budget in enterTerminalMode, which
	// retries. It is deliberately short: a swallowed 'i' is worth retrying
	// rather than waiting out.
	terminalModeProbe = 3 * time.Second
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
	// out receives a copy of the PTY traffic, so a test can assert on output
	// that never reaches the grid. OSC 52 clipboard writes are the reason it
	// exists: a copy is invisible on screen but unmistakable on the wire. The
	// stream carries both directions, which is harmless for that purpose since
	// nothing the tests send contains an OSC 52.
	out io.Writer
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

	// Registered before the child is spawned so cleanup order is: tear the
	// client down first (tuitest's Close, registered inside StartT below), then
	// kill whatever daemon it left behind. Registering it here rather than
	// leaving it to each test means a test that starts creating a detached
	// daemon later cannot forget.
	killDaemon(t, base)

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
	// GORACE is forwarded so a tuios built with -race can be driven through this
	// suite and have its findings survive. tuitest replaces the child's whole
	// environment, so without this the child runs with GORACE unset and the race
	// detector writes to stderr, which is the PTY the assertions read: the report
	// is painted into the terminal, shredded across the emulator's line wrapping,
	// and lost. With log_path set, each process writes its own file instead.
	if gorace := os.Getenv("GORACE"); gorace != "" {
		env = append(env, "GORACE="+gorace)
	}
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

	var mirror io.Writer = logFile
	if o.out != nil {
		mirror = io.MultiWriter(logFile, o.out)
	}
	return tuitest.StartT(t, argv,
		tuitest.WithSize(cols, rows),
		tuitest.WithTerm("xterm-256color"),
		tuitest.WithEnv(env...),
		tuitest.WithLog(mirror),
	)
}

// attachIn starts a client attached to an existing daemon session and returns
// once it has rehydrated and settled in window-management mode.
//
// It exists because every daemon-backed test in this package had to know two
// things nothing here wrote down. The first is the argv: the session name is a
// positional argument to `attach`, not a `-s` flag, and plain `tuios` with no
// arguments is not a client at all but the standalone TUI, so a test that used
// it was testing a tuios with no daemon behind it. The second is the mode, which
// windowManagementMode explains.
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

// windowManagementMode settles a client in window-management mode.
//
// An attached client boots into terminal mode, where a plain character is input
// to the shell. Every bare-key helper in this file is a window-manager command:
// newWindow presses 'n', enableTiling presses 't', renameWindow presses 'r'. A
// test that attaches and then calls one of them is reading its own keystroke
// back out of a shell, and the failure looks like the binding not working.
//
// Nothing on screen announces which mode a fresh client is in, so this is not
// something a reader can be expected to notice.
func windowManagementMode(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("normalise to window management mode: %v", err)
	}
	if err := term.WaitForText("Window Management Mode", uiTimeout); err != nil {
		t.Fatalf("never settled in window management mode: %v\n%s", err, term.Snapshot())
	}
	// The mode switch re-arms input handling; give it the same beat a mode
	// change gets everywhere else.
	time.Sleep(insertGuard)
}

// killDaemonNow shuts the daemon under base down immediately rather than at the
// end of the test.
//
// killDaemon registers a cleanup, which is right for a test that starts one
// daemon and wrong for anything that starts hundreds: a fuzz campaign builds a
// fresh isolation root per replay, and leaving every one of them to t.Cleanup
// means every daemon and every shell it forked is alive at once until the test
// ends. The registered cleanup still runs afterwards and finds nothing to do.
func killDaemonNow(t *testing.T, base string) {
	t.Helper()
	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Logf("kill-server under %s (best effort): %v: %s", base, err, strings.TrimSpace(out))
	}
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
		if err := term.WaitForText("Terminal Mode", terminalModeProbe); err == nil {
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
	// An empty marker would make this a fire-and-forget helper that passes
	// whatever the shell did, including nothing. It is a programming error
	// rather than a test outcome, so it fails loudly instead of returning.
	if want == "" {
		t.Fatalf("runInShell(%q) was given no marker to wait for; "+
			"a command with nothing to assert on cannot fail", cmd)
	}
	if err := term.SendKeys(cmd, tuitest.Enter); err != nil {
		t.Fatalf("type %q: %v", cmd, err)
	}
	if err := term.WaitForText(want, timeout); err != nil {
		t.Fatalf("command %q never produced %q: %v", cmd, want, err)
	}
}

// renameWindow renames the focused window through the rename keybinding and
// only returns once the name is committed.
//
// Two things here were previously guessed at.
//
// The editor is the rename micro-dialog, which names itself in its own top
// border and carries its keys in its bottom one. Waiting for that frame is a
// wait on the dialog and nothing else; the previous wait counted underscores,
// and an underscore is an ordinary character any shell output can put on
// screen.
//
// The commit still requires the dialog gone as well as the name present: the
// dialog renders the buffer as you type, so the name alone was satisfied by the
// harness's own keystrokes and said nothing about enter.
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

// daemonKey identifies one (test, isolation root) pair so killDaemon registers
// at most one cleanup per root even when a test and startIn both ask for it.
type daemonKey struct {
	t    *testing.T
	base string
}

var registeredKills sync.Map

// killDaemon shuts down the daemon rooted at base and then checks it is really
// gone. Every start registers this, so a test that grows a detached daemon
// later cannot silently leak one; the maintainer's machine has been flooded by
// leaked test daemons before.
//
// kill-server's own error is still best effort, because it legitimately fails
// when no daemon was ever started. What is not best effort is the state
// afterwards: if `ls` still answers with sessions, the daemon outlived the test
// and that is reported rather than swallowed. A swallowed teardown error is how
// one test ends up running against another's daemon, and a suite where that can
// happen cannot claim its tests are isolated.
func killDaemon(t *testing.T, base string) {
	t.Helper()
	if _, already := registeredKills.LoadOrStore(daemonKey{t, base}, true); already {
		return
	}
	t.Cleanup(func() {
		if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
			t.Logf("kill-server (best effort): %v: %s", err, strings.TrimSpace(out))
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			out, err := tuiosCLI(t, base, "ls", "--json")
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

// ---------------------------------------------------------------------------
// Mouse input
//
// A real terminal reports a pointer gesture as a strictly paired stream: one
// press report per button-down, one motion report for each cell the pointer
// crosses while a button is held, and one release report per button-up. A
// helper that takes a shortcut here does not merely test less than it claims.
// It puts tuios into a state no user can reach, and every assertion made after
// that runs against that state.
//
// Two shortcuts this suite used to take, both fixed here:
//
//  1. clickAt sent n presses and one trailing release. tuios copies a selection
//     on release, so a triple click produced a single clipboard write where a
//     real mouse produces three releases and two writes. Anything that happens
//     on an intermediate release was structurally unobservable, which is how a
//     clipboard bug survived review of the assertions: no assertion could have
//     caught it, because the harness could not generate the input.
//
//  2. leftClick and shiftRightClick sent a press and no release at all. A left
//     press inside a pane sets OS.InteractionMode, and while that flag is set
//     tuios deliberately stops polling pane content (internal/app/os_render.go
//     returns early on it). A press with no release therefore freezes every
//     pane for the remainder of the test, so any later wait for shell output is
//     waiting on a program that has stopped reading its panes.
//     TestClickInPaneDoesNotFreezeOutput is the standing guard on that.
//
// What this cannot simulate is documented in NEGATIVE_CONTROLS.md under
// "What this harness structurally cannot observe"; the short version is that
// cmd/tuios/run.go installs a whitelist filter that drops motion events unless
// a drag, a resize, an overlay drag, the scrollback browser or a mouse-tracking
// application is active, so motion sent outside those states is dropped before
// the model sees it and asserting on its effect would assert on nothing.

const (
	// mouseGap is the pause after each mouse report. Real reports arrive one at
	// a time with human-scale gaps between them, and tuios coalesces motion to
	// a frame budget, so back-to-back writes are not the input a user produces.
	mouseGap = 30 * time.Millisecond
	// multiClickHold is how long a button of a multi-click gesture stays down,
	// and multiClickGap is the pause before the next one goes down. Together
	// they set the press-to-press interval, which is the figure that has to stay
	// inside internal/input.multiClickInterval for tuios to read the clicks as
	// one gesture: 40ms against a 300ms window.
	//
	// They are shorter than mouseGap, and deliberately so. mouseGap is spaced
	// for motion, which tuios coalesces to a frame budget; presses and releases
	// pass through untouched (cmd/tuios/run.go filters motion and nothing else),
	// and the harness was measured sending all six reports of a triple click
	// with no pause at all and having every one of them counted, 10 times out of
	// 10. So the pause between the clicks of one gesture buys no fidelity, and
	// what it costs is margin: tuios measures the interval when it processes the
	// press, not when the byte arrives, so every millisecond of nominal spacing
	// is a millisecond less stall it takes to push the third click outside the
	// window and turn the gesture into a double click plus a single one.
	//
	// Measured on this machine: with the interval widened deliberately, the
	// gesture was still read as three clicks at 295ms and never at 300ms, so
	// there is no fixed overhead to leave room for, only the stall. What is left
	// is a hold short enough to be cheap and long enough to be a real button
	// press.
	multiClickHold = 15 * time.Millisecond
	multiClickGap  = 25 * time.Millisecond
	// gestureGap is long enough to guarantee the next press starts a fresh
	// gesture rather than continuing the previous one.
	gestureGap = 800 * time.Millisecond
)

// sendMouse writes one SGR mouse report and then pauses, so tuios sees a
// sequence of separate events rather than one burst.
func sendMouse(t *testing.T, term *tuitest.Terminal, what string, ev tuitest.MouseEvent) {
	t.Helper()
	sendMouseThenWait(t, term, what, ev, mouseGap)
}

// sendMouseThenWait is sendMouse with the pause named by the caller, for the
// one gesture whose whole meaning is how fast its reports arrive.
func sendMouseThenWait(t *testing.T, term *tuitest.Terminal, what string, ev tuitest.MouseEvent, pause time.Duration) {
	t.Helper()
	if err := term.SendMouse(ev); err != nil {
		t.Fatalf("%s at (%d,%d): %v", what, ev.Col, ev.Row, err)
	}
	time.Sleep(pause)
}

// mousePress sends a button-down. Every mousePress in a test must be matched by
// a mouseRelease, exactly as a physical button is.
func mousePress(t *testing.T, term *tuitest.Terminal, col, row int, button tuitest.MouseButton, mods tuitest.KeyMods) {
	t.Helper()
	sendMouse(t, term, "press", tuitest.MouseEvent{
		Col: col, Row: row, Button: button, Action: tuitest.MousePress, Mods: mods,
	})
}

// mouseRelease sends a button-up. SGR reports a release with the same button
// and modifier bits as the press that opened the gesture and a lowercase final
// byte, which is what tuitest encodes.
func mouseRelease(t *testing.T, term *tuitest.Terminal, col, row int, button tuitest.MouseButton, mods tuitest.KeyMods) {
	t.Helper()
	sendMouse(t, term, "release", tuitest.MouseEvent{
		Col: col, Row: row, Button: button, Action: tuitest.MouseRelease, Mods: mods,
	})
}

// mouseMotion sends a drag report: the motion bit plus the held button's code,
// which is what mode 1002 emits for each cell the pointer crosses with a button
// down.
//
// The action is MouseDrag rather than MouseMove. tuitest used to encode a
// MouseMove carrying a button as a drag, so the two spelled the same wire
// report; it now takes MouseMove at its word and drops the button, which turns
// every report here into a bare hover. tuios reads a press, a run of hovers and
// a release as a click, so a drag stopped moving anything at all.
func mouseMotion(t *testing.T, term *tuitest.Terminal, col, row int, button tuitest.MouseButton, mods tuitest.KeyMods) {
	t.Helper()
	sendMouse(t, term, "motion", tuitest.MouseEvent{
		Col: col, Row: row, Button: button, Action: tuitest.MouseDrag, Mods: mods,
	})
}

// mouseClick is one complete click: press then release at the same cell.
func mouseClick(t *testing.T, term *tuitest.Terminal, col, row int, button tuitest.MouseButton, mods tuitest.KeyMods) {
	t.Helper()
	mousePress(t, term, col, row, button, mods)
	mouseRelease(t, term, col, row, button, mods)
}

// mouseDrag is one complete drag: press, a motion report for every cell between
// the two points, then release at the far end.
//
// The intermediate reports are not decoration. tuios tracks a selection, a
// window move and a resize from motion, and a press-then-release with nothing
// in between is a click, not a drag: the drag-distance threshold in
// handleMouseRelease treats it as one and snaps the window back.
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

// mouseHover sends a bare pointer motion with no button held: SGR button code
// 35, which is the no-button value 3 plus the motion bit 32. MouseNone is what
// spells that now; it used to be written out by hand, because the MouseButton
// zero value is the left button and there was no way to say "no button".
//
// Read the doc comment on the mouse section before using this. tuios's motion
// filter drops bare motion in every state except an active drag, resize,
// overlay drag, scrollback browser, or a pane running a mouse-tracking
// application, so in every other state this is delivered to the program and
// dropped before Update sees it. Hover-driven behaviour, including the context
// menu's own hover highlight, cannot be observed from here at all.
func mouseHover(t *testing.T, term *tuitest.Terminal, col, row int) {
	t.Helper()
	sendMouse(t, term, "hover", tuitest.MouseEvent{
		Col: col, Row: row, Button: tuitest.MouseNone, Action: tuitest.MouseMove,
	})
}

// enableTiling toggles tiling on and waits for the layout to actually be tiled,
// which it establishes by requiring every pane's marker to be on screen at the
// same time. Floating windows overlap, so only the topmost one's content shows;
// tiled windows all show at once.
//
// When markers are given this deliberately does not wait for the "Tiling Mode
// Enabled" message: with several windows open the relayout is the thing under
// test, and the markers say the relayout happened.
//
// With no markers it waits for the message instead of sleeping, but only after
// first waiting for any earlier tiling message to leave the dock. Without that
// pre-wait the condition could be satisfied by a message an earlier step
// produced, which is the stale-message trap that already cost this suite a
// silently vacuous assertion in TestFocusCycleWithRapidKeyRepeat.
func enableTiling(t *testing.T, term *tuitest.Terminal, markers ...string) {
	t.Helper()
	if len(markers) == 0 {
		// notifications linger for config.NotificationDuration (6s), so this is
		// the budget for an earlier one to expire. In practice it is already
		// gone and this returns immediately.
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return !strings.Contains(s.Text(), "Tiling Mode")
		}, 10*time.Second); err != nil {
			t.Fatalf("a tiling message from an earlier step never cleared, so waiting "+
				"for this one would prove nothing: %v\n%s", err, term.Snapshot())
		}
	}
	if err := term.SendKeys("t"); err != nil {
		t.Fatalf("toggle tiling: %v", err)
	}
	if len(markers) == 0 {
		if err := term.WaitForText("Tiling Mode Enabled", uiTimeout); err != nil {
			t.Fatalf("tiling was never enabled: %v\n%s", err, term.Snapshot())
		}
		return
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, markers...)
	}, uiTimeout); err != nil {
		t.Fatalf("layout never became tiled (markers %v not all visible together): %v\n%s",
			markers, err, term.Snapshot())
	}
}
