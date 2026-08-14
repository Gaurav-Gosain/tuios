package tuie2e

// Perf measurements: the numbers a user actually feels.
//
// These live beside the behavioural e2e tests because they need the same thing
// those tests need, a real binary in a real PTY with a real daemon behind it.
// A Go benchmark in the main module can time a render function; it cannot time
// the path from a keypress to the character on screen, which crosses a socket,
// a PTY, a shell and a compositor before it gets there.
//
// They are gated behind TUIOS_PERF on top of TUIOS_E2E because they are
// measurements rather than assertions: they take minutes, they report numbers
// instead of passing or failing, and a number that moved is a thing for a human
// to read rather than for CI to reject. Wall-clock thresholds in CI would be
// flaky in exactly the way that trains people to ignore a red build.
//
//	cd e2e/tui && TUIOS_E2E=1 TUIOS_PERF=1 go test -count=1 -v -run TestPerf ./...
//
// Every number is reported as a distribution, not a mean. Startup and latency
// both have long right tails (a scheduler hiccup, a cold page fault), and a mean
// hides which of "usually fast" and "always fast" is true. The median is what a
// user feels most of the time and p90 is what they complain about.

import (
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const (
	// perfEnv gates the whole file on top of TUIOS_E2E.
	perfEnv = "TUIOS_PERF"

	// perfCols and perfRows are the maintainer's real host size, the same one
	// the render benchmarks in internal/app use. Per-frame cost scales with
	// total cells, so measuring at the default 120x40 would flatter every
	// number here by a factor of about 2.4.
	perfCols, perfRows = 207, 55

	// perfPromptMark is the pane shell's prompt, overridden so "the pane is
	// usable" is a string on screen rather than a guess about a border being
	// drawn. It has to be a token no chrome would ever paint.
	perfPromptMark = "TUIOSRDY"

	// perfStartRuns is how many times a startup path is measured. Startup is
	// seconds-scale work with a wide spread, and each run forks a whole
	// multiplexer, so this trades resolution against a suite that finishes.
	perfStartRuns = 7

	// perfKeyRuns is how many keystrokes a latency sample is taken over. Each
	// one adds a character to the pane's prompt line, so this is also the
	// longest the typed line gets, and it must stay inside the narrowest pane
	// the multi-pane cases produce or the line wraps and the match breaks.
	perfKeyRuns = 16
)

// perfGate skips unless perf measurement was asked for explicitly.
func perfGate(t *testing.T) {
	t.Helper()
	if os.Getenv(perfEnv) == "" {
		t.Skipf("perf: skipping, set %s=1 to measure (minutes, not seconds)", perfEnv)
	}
}

// perfEnvVars is the environment every perf instance runs with: a prompt that
// announces itself, so "usable pane" is observable.
func perfEnvVars() []string {
	return []string{"PS1=" + perfPromptMark + "$ "}
}

// dist is a set of timings and the shape they came out in.
type dist []time.Duration

func (d dist) report(t *testing.T, what string) {
	t.Helper()
	if len(d) == 0 {
		t.Errorf("%s: no samples", what)
		return
	}
	s := append(dist(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	at := func(q float64) time.Duration {
		i := int(q * float64(len(s)-1))
		return s[i]
	}
	t.Logf("PERF %-34s n=%2d  min %8s  med %8s  p90 %8s  max %8s",
		what, len(s), round(s[0]), round(at(0.5)), round(at(0.9)), round(s[len(s)-1]))
}

// round trims a duration to a resolution worth printing. Sub-millisecond digits
// on a wall-clock measurement of a process starting are noise dressed as
// precision.
func round(d time.Duration) time.Duration {
	if d < time.Millisecond {
		return d.Round(10 * time.Microsecond)
	}
	return d.Round(100 * time.Microsecond)
}

// waitTextAt blocks until substr is on screen and returns how long that took,
// measured from the moment the caller says the clock started. tuitest wakes its
// waiters when the emulator consumes output rather than on a poll tick, so the
// resolution here is the read, not a polling interval.
func waitTextAt(t *testing.T, term *tuitest.Terminal, start time.Time, substr string, timeout time.Duration) time.Duration {
	t.Helper()
	if err := term.WaitForText(substr, timeout); err != nil {
		t.Fatalf("perf: never saw %q: %v\n%s", substr, err, term.Snapshot())
	}
	return time.Since(start)
}

// ---------------------------------------------------------------------------
// Startup

// TestPerfStartupCold measures `tuios new` from a machine with no daemon: fork,
// exec, daemon spawn, socket handshake, first frame. This is the slowest path a
// user ever takes and the first impression the program makes.
func TestPerfStartupCold(t *testing.T) {
	perfGate(t)
	var boot dist
	for i := range perfStartRuns {
		t.Run(fmt.Sprintf("run%d", i), func(t *testing.T) {
			t0 := time.Now()
			term, _ := start(t, startOpts{
				cols: perfCols, rows: perfRows,
				args: []string{"new", fmt.Sprintf("perf%d", i)},
				env:  perfEnvVars(),
			})
			boot = append(boot, waitTextAt(t, term, t0, welcomeText, bootTimeout))
		})
	}
	boot.report(t, "startup/cold: exec -> first frame")
}

// TestPerfStartupWarm measures the same thing against a daemon that is already
// up, which is every launch after the first. The gap between this and the cold
// number is what starting the daemon costs.
func TestPerfStartupWarm(t *testing.T) {
	perfGate(t)
	base := t.TempDir()

	// The first client is the one that pays for the daemon; it is warmup, not a
	// sample, and it stays alive so the daemon it started stays up.
	warm := startIn(t, base, startOpts{cols: perfCols, rows: perfRows, args: []string{"new", "keeper"}, env: perfEnvVars()})
	waitBoot(t, warm)

	var boot dist
	for i := range perfStartRuns {
		t0 := time.Now()
		term := startIn(t, base, startOpts{
			cols: perfCols, rows: perfRows,
			args: []string{"new", fmt.Sprintf("warm%d", i)},
			env:  perfEnvVars(),
		})
		boot = append(boot, waitTextAt(t, term, t0, welcomeText, bootTimeout))
		// Closed here rather than left to the test's cleanup so only one client
		// at a time is attached; a pile of live clients would make each later
		// run measure a daemon fanning state out to all of them.
		_ = term.Close()
	}
	boot.report(t, "startup/warm: exec -> first frame")
}

// TestPerfFirstPane measures the other half of "new to a usable pane": from the
// welcome screen, the keystroke that creates a window through to that window's
// shell having printed its prompt. Forking a PTY and a shell is in here, so it
// is a floor set partly by the OS.
func TestPerfFirstPane(t *testing.T) {
	perfGate(t)
	var pane dist
	for i := range perfStartRuns {
		t.Run(fmt.Sprintf("run%d", i), func(t *testing.T) {
			term, _ := start(t, startOpts{
				cols: perfCols, rows: perfRows,
				args: []string{"new", fmt.Sprintf("pane%d", i)},
				env:  perfEnvVars(),
			})
			waitBoot(t, term)
			t0 := time.Now()
			if err := term.SendKeys("n"); err != nil {
				t.Fatalf("send 'n': %v", err)
			}
			pane = append(pane, waitTextAt(t, term, t0, perfPromptMark, uiTimeout))
		})
	}
	pane.report(t, "startup/first pane: 'n' -> prompt")
}

// TestPerfAttach measures `tuios attach` to a session that already exists and
// already has content: the client's cost to fetch state and paint a screen,
// with no session creation in it. Measured with one pane and with eight,
// because what the daemon sends on attach scales with the session and this is
// where that shows up.
func TestPerfAttach(t *testing.T) {
	perfGate(t)
	for _, panes := range []int{1, 8} {
		t.Run(fmt.Sprintf("panes%d", panes), func(t *testing.T) {
			base := t.TempDir()
			seed := startIn(t, base, startOpts{cols: perfCols, rows: perfRows, args: []string{"new", "attachme"}, env: perfEnvVars()})
			waitBoot(t, seed)
			for range panes {
				newWindow(t, seed)
			}
			// Detach the seeding client so attach is measured against a session
			// nobody is watching, which is what reattaching after a detach is.
			_ = seed.Close()

			var att dist
			for range perfStartRuns {
				t0 := time.Now()
				term := startIn(t, base, startOpts{
					cols: perfCols, rows: perfRows,
					args: []string{"attach", "attachme"},
					env:  perfEnvVars(),
				})
				att = append(att, waitTextAt(t, term, t0, perfPromptMark, bootTimeout))
				_ = term.Close()
			}
			att.report(t, fmt.Sprintf("attach/%d panes: exec -> rendered", panes))
		})
	}
}

// ---------------------------------------------------------------------------
// Input latency

// typeLatency measures the echo round trip of single keystrokes into the
// focused pane: client to daemon to PTY, the shell's own echo back, and the
// compositor painting it.
//
// The keys are typed onto a line that starts with '#', so the shell treats the
// whole accumulated line as a comment and nothing typed here ever runs. Each
// keystroke is matched on the whole prefix typed so far, which is unique at
// every length, so a match cannot be satisfied by an earlier iteration's
// character. That is also why the run count is capped: the line must not wrap
// inside the pane, or the prefix acquires a newline and stops matching.
func typeLatency(t *testing.T, term *tuitest.Terminal) dist {
	t.Helper()
	const alphabet = "abcdefghijklmnopqrstuvwxyz"

	if err := term.SendKeys("#"); err != nil {
		t.Fatalf("send '#': %v", err)
	}
	if err := term.WaitForText("#", uiTimeout); err != nil {
		t.Fatalf("comment guard never echoed: %v\n%s", err, term.Snapshot())
	}

	var d dist
	line := "#"
	for i := range perfKeyRuns {
		ch := string(alphabet[i%len(alphabet)])
		line += ch
		t0 := time.Now()
		if err := term.SendKeys(ch); err != nil {
			t.Fatalf("send %q: %v", ch, err)
		}
		d = append(d, waitTextAt(t, term, t0, line, uiTimeout))
	}
	return d
}

// TestPerfInputLatency is the number that decides whether an editor in a pane
// feels native. It is measured with one pane and with eight, because every open
// pane is work the compositor does on the frame that carries the keystroke, and
// if that cost is on the critical path it shows up as a slower echo.
func TestPerfInputLatency(t *testing.T) {
	perfGate(t)
	for _, panes := range []int{1, 4, 8} {
		t.Run(fmt.Sprintf("panes%d", panes), func(t *testing.T) {
			term, _ := start(t, startOpts{
				cols: perfCols, rows: perfRows,
				args: []string{"new", fmt.Sprintf("lat%d", panes)},
				env:  perfEnvVars(),
			})
			waitBoot(t, term)
			for range panes {
				newWindow(t, term)
			}
			enterTerminalMode(t, term)
			typeLatency(t, term).report(t, fmt.Sprintf("input latency/%d panes", panes))
		})
	}
}

// ---------------------------------------------------------------------------
// Throughput under load

// floodCmd keeps a pane producing output as fast as the emulator will take it.
// `yes` is used rather than a loop with a sleep because the interesting
// question is what happens when a pane is saturating the pipe, not what happens
// when it is polite.
const floodCmd = "yes tuiosflood"

// TestPerfTypeWhileFlooding is the case that makes a multiplexer feel bad and
// the one nobody benchmarks: one pane is dumping output at full speed while the
// user types in another. If output handling and input handling share a critical
// section, or if a flooded pane forces frames the typed pane has to wait behind,
// it shows up here as a latency far worse than the idle number from
// TestPerfInputLatency, which is the comparison this test exists to make.
func TestPerfTypeWhileFlooding(t *testing.T) {
	perfGate(t)
	for _, floods := range []int{1, 3} {
		t.Run(fmt.Sprintf("flooding%d", floods), func(t *testing.T) {
			term, _ := start(t, startOpts{
				cols: perfCols, rows: perfRows,
				args: []string{"new", fmt.Sprintf("flood%d", floods)},
				env:  perfEnvVars(),
			})
			waitBoot(t, term)

			// One pane to type in plus the flooding ones. The typing pane is
			// created first so Tab lands back on it after the floods are armed.
			for range floods + 1 {
				newWindow(t, term)
			}

			for range floods {
				if err := term.SendKeys(tuitest.Tab); err != nil {
					t.Fatalf("tab: %v", err)
				}
				enterTerminalMode(t, term)
				if err := term.SendKeys(floodCmd, tuitest.Enter); err != nil {
					t.Fatalf("start flood: %v", err)
				}
				if err := term.WaitForText("tuiosflood", shellTimeout); err != nil {
					t.Fatalf("flood never started: %v\n%s", err, term.Snapshot())
				}
				leaveTerminalMode(t, term)
			}

			// Back to the pane that is not flooding.
			if err := term.SendKeys(tuitest.Tab); err != nil {
				t.Fatalf("tab back: %v", err)
			}
			enterTerminalMode(t, term)
			typeLatency(t, term).report(t, fmt.Sprintf("input latency/%d panes flooding", floods))
		})
	}
}
