package app

// Input latency, decomposed.
//
// "Input latency" is not one number. The path a keystroke takes crosses a
// socket, a PTY, a guest, a ring buffer, a socket again, an emulator, a
// coalescer and a compositor, and the hop that dominates is not the one people
// assume. e2e/tui/perf_test.go measures the whole loop honestly, against the
// real binary in a real PTY, but a single end-to-end number cannot say which
// hop to go and fix. These measurements cut the same path into pieces.
//
// The rig is the rehydration rig: a real Daemon in this process, a real
// TUIClient over its socket, and a real OS restored through the attach path.
// Nothing is stubbed.
//
// What every number here INCLUDES: the client's write to the daemon socket, the
// daemon's read and PTY write, the guest, the daemon's ring append and
// broadcast, the client's read loop, the outputWriter batch into the client
// emulator, the render coalescer, and composeFrame.
//
// What every number here EXCLUDES: the host terminal's own input handling,
// bubbletea's stdin decode into a KeyPressMsg, and the final write of the diff
// to the host tty. Those three are what the e2e number adds on top, and the gap
// between the two harnesses is how much they cost.
//
//	go test ./internal/app/ -run TestLatency -v   (needs TUIOS_PERF=1)

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/perf"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

const (
	// latencyEnv gates these on top of the normal test run. They are
	// measurements, not assertions: they take tens of seconds and they report
	// numbers rather than passing or failing. A wall-clock threshold in CI
	// would be flaky in exactly the way that teaches people to ignore a red
	// build.
	latencyEnv = "TUIOS_PERF"

	// latencyRuns is the sample count per measurement. A p99 needs enough
	// samples that the 99th percentile is an observation rather than an
	// extrapolation: at n=200 it is the second-slowest sample, which is a real
	// keystroke. The e2e suite runs at n=16, where a "p99" would be the max
	// wearing a different label.
	latencyRuns = 200

	// latencyWait bounds a single sample. Anything this slow is a hang, not a
	// tail.
	latencyWait = 5 * time.Second

	// latencySettle is how long a pane is left quiet before a sample is taken.
	// It matters: the render coalescer's behaviour depends on whether the pane
	// has produced output recently, and a keystroke is by definition something
	// a user does to a pane they were looking at rather than one mid-flood.
	latencySettle = 30 * time.Millisecond

	// latCols and latRows are the maintainer's real host size, the same one the
	// render benchmarks, the e2e perf suite and the local-latency measurement
	// in internal/input use. Per-frame cost scales with total cells, so the
	// rig's default 80x24 would flatter every number here.
	latCols, latRows = 207, 55
)

func latencyGate(t *testing.T) {
	t.Helper()
	if os.Getenv(latencyEnv) == "" {
		t.Skipf("perf: skipping, set %s=1 to measure", latencyEnv)
	}
}

// latencyRig builds the rig with one pane and puts its shell in a state where
// typing is echoed and nothing typed ever runs: the line opens with '#', so the
// shell treats the whole accumulated line as a comment.
func latencyRig(t *testing.T) *rig {
	t.Helper()
	r := newRigSized(t, 1, latCols, latRows)
	w := r.m.GetFocusedWindow()
	if w == nil {
		t.Fatal("no focused window")
	}
	r.waitDaemonShows(w.PTYID, "$")
	if err := r.m.SendInputToDaemon(w, []byte("#")); err != nil {
		t.Fatalf("comment guard: %v", err)
	}
	r.waitDaemonShows(w.PTYID, "#")
	r.settle()
	return r
}

// frame composes the client's frame and returns its text with the styling
// removed, which is exactly what bubbletea composes and what a reader sees.
//
// The strip is not cosmetic. The compositor emits a style run per colour
// change, so with more than one pane the escape sequences land between the
// characters of the typed line and a substring match on the raw frame fails
// even though the frame draws the line correctly.
func frame(m *OS) string {
	m.View()
	return ansi.Strip(m.cachedViewContent)
}

// pumpUntil drives the client the way the bubbletea event loop does: block on
// the render signal, turn it into the message Update expects, compose, and ask
// whether the frame carries what the caller is waiting for.
//
// This is the honest client loop. ListenForPTYData is the Cmd bubbletea runs to
// turn PTYDataChan into a PTYDataMsg, and PTYDataMsg is what makes Update mark
// terminals dirty; doing both here reproduces the real sequence without needing
// a tea.Program attached to a tty.
func pumpUntil(t *testing.T, m *OS, want func(string) bool) bool {
	t.Helper()
	deadline := time.After(latencyWait)
	for {
		select {
		case <-m.PTYDataChan:
			m.Update(PTYDataMsg{})
			if want(frame(m)) {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// ---------------------------------------------------------------------------
// Echo latency: the full loop, and the one a user feels when typing.

// TestLatencyEcho measures keystroke in to the guest's echo of it appearing in
// a composed frame. Every hop except the host terminal is in this number.
//
// Each keystroke extends a comment line, so the string waited on is a prefix
// unique at every length and an earlier iteration's character cannot satisfy a
// later match.
func TestLatencyEcho(t *testing.T) {
	latencyGate(t)
	for _, panes := range []int{1, 4} {
		t.Run(fmt.Sprintf("panes%d", panes), func(t *testing.T) {
			r := newRigSized(t, panes, latCols, latRows)
			// The focused pane, because that is the one a user types into and
			// because an unfocused pane is deliberately repainted only every
			// third cycle (see MarkTerminalsWithNewContent), which is a
			// different measurement.
			w := r.m.GetFocusedWindow()
			if w == nil {
				t.Fatal("no focused window")
			}
			r.waitDaemonShows(w.PTYID, "$")
			if err := r.m.SendInputToDaemon(w, []byte("#")); err != nil {
				t.Fatalf("comment guard: %v", err)
			}
			r.waitDaemonShows(w.PTYID, "#")
			r.settle()

			var d perf.Dist
			line := "#"
			for i := range latencyRuns {
				// The line is reset well before it could reach the pane's width
				// and wrap, which would put a newline inside the prefix being
				// matched and break every later sample.
				if len(line) > 24 {
					if err := r.m.SendInputToDaemon(w, []byte("\x15#")); err != nil {
						t.Fatalf("reset line: %v", err)
					}
					line = "#"
					time.Sleep(latencySettle)
					drain(r.m.PTYDataChan)
				}
				ch := string(rune('a' + i%26))
				line += ch
				want := line

				time.Sleep(latencySettle)
				drain(r.m.PTYDataChan)

				t0 := time.Now()
				if err := r.m.SendInputToDaemon(w, []byte(ch)); err != nil {
					t.Fatalf("send %q: %v", ch, err)
				}
				if !pumpUntil(t, r.m, func(f string) bool { return strings.Contains(f, want) }) {
					t.Fatalf("echo of %q never reached a frame; client emulator holds:\n%s", want, clientText(w))
				}
				d.AddSince(t0)
			}
			t.Log(d.Line(fmt.Sprintf("echo/%d panes", panes)))
		})
	}
}

// ---------------------------------------------------------------------------
// The daemon round trip on its own: what a multiplexer adds over a bare
// terminal, and therefore the part worth defending.

// TestLatencyDaemonRoundTrip measures a byte written towards the guest through
// to the client's own emulator holding it, with no frame composed. It is the
// echo number minus the compositor, so the difference between the two says
// whether rendering or the wire is the thing to go and fix.
func TestLatencyDaemonRoundTrip(t *testing.T) {
	latencyGate(t)
	r := latencyRig(t)
	w := r.win(0)

	var d perf.Dist
	line := "#"
	for i := range latencyRuns {
		if len(line) > 24 {
			if err := r.m.SendInputToDaemon(w, []byte("\x15#")); err != nil {
				t.Fatalf("reset line: %v", err)
			}
			line = "#"
			time.Sleep(latencySettle)
		}
		ch := string(rune('a' + i%26))
		line += ch
		want := line

		time.Sleep(latencySettle)
		t0 := time.Now()
		if err := r.m.SendInputToDaemon(w, []byte(ch)); err != nil {
			t.Fatalf("send %q: %v", ch, err)
		}
		if !waitClient(w, want) {
			t.Fatalf("client emulator never saw %q", want)
		}
		d.AddSince(t0)
	}
	t.Log(d.Line("daemon rtt/key -> client emulator"))
}

// waitClient polls the client's emulator directly, which is the hop before the
// coalescer. Polling is the only option here: the emulator is written by a
// background goroutine and offers no signal of its own, and that is precisely
// why the coalescer exists. The poll interval bounds the resolution of this one
// number, so it is far below the quantity being measured.
func waitClient(w *terminal.Window, want string) bool {
	deadline := time.Now().Add(latencyWait)
	for time.Now().Before(deadline) {
		if strings.Contains(clientText(w), want) {
			return true
		}
		time.Sleep(50 * time.Microsecond)
	}
	return false
}

// ---------------------------------------------------------------------------
// How many frames one keystroke is worth.

// TestKeystrokeToPaneComposesAnIdenticalFrame is a count rather than a timing,
// so it is not gated and does not care what else the machine is doing.
//
// bubbletea composes after every message it delivers, and Update forces
// renderSkipped to false for every key ("Any user input must produce a fresh
// frame", update.go). For a key forwarded to a daemon pane that is a frame
// composed before anything has changed: the key went out on the socket, the
// guest has not answered yet, and the pane still holds exactly what it held.
// The echo arrives later as PTY output and composes a second frame, which is
// the one that carries it.
//
// This pins that the first of those two frames is byte-identical to the frame
// before the keystroke, which is what makes it waste rather than latency. If a
// future change makes a keystroke visibly alter the frame by itself, this test
// fails and the reasoning above needs revisiting rather than the number.
func TestKeystrokeToPaneComposesAnIdenticalFrame(t *testing.T) {
	r := newRigSized(t, 1, latCols, latRows)
	w := r.m.GetFocusedWindow()
	if w == nil {
		t.Fatal("no focused window")
	}
	r.waitDaemonShows(w.PTYID, "$")
	r.settle()

	before := frame(r.m)
	if before == "" {
		t.Fatal("composed an empty frame")
	}

	// The write, and then the frame bubbletea would compose for the key
	// message, taken before any echo could have made it back.
	if err := r.m.SendInputToDaemon(w, []byte("#")); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := frame(r.m); got != before {
		t.Log("a keystroke now changes the frame by itself; the wasted-compose " +
			"finding in docs/perf.md no longer holds as written")
		t.Fail()
	}
}

// ---------------------------------------------------------------------------
// The state push every keystroke pays for.

// TestLatencyStateSync measures SyncStateToDaemon, which internal/input calls
// after every key and mouse event on a daemon session, synchronously on the
// bubbletea Update goroutine.
//
// It is not a round trip, so it is not what roundTripMu serialises, but it is a
// full session-state build plus a gob encode plus a blocking socket write held
// under the client mutex, and it happens on the frame that carries the
// keystroke. It is measured by pane count because the state it builds is a
// per-window slice, so whatever it costs is paid again for every pane open.
func TestLatencyStateSync(t *testing.T) {
	latencyGate(t)
	for _, panes := range []int{1, 4, 8} {
		t.Run(fmt.Sprintf("panes%d", panes), func(t *testing.T) {
			r := newRigSized(t, panes, latCols, latRows)
			r.settle()

			var d perf.Dist
			for range latencyRuns {
				t0 := time.Now()
				r.m.SyncStateToDaemon()
				d.AddSince(t0)
			}
			t.Log(d.Line(fmt.Sprintf("state sync/per keystroke, %d panes", panes)))
		})
	}
}

// ---------------------------------------------------------------------------
// Frame-emit latency: what coalescing costs a quiet pane.

// TestLatencyFrameEmit measures from a pane producing output to the render
// signal that carries it reaching the client's event loop. It is the coalescer
// and nothing else.
//
// The pane is left quiet before each sample, because that is the state a pane
// is in when a user types at it, and a coalescer's cost to a quiet pane is a
// different question from its cost to a flooding one.
func TestLatencyFrameEmit(t *testing.T) {
	latencyGate(t)
	r := latencyRig(t)
	w := r.win(0)
	m := r.m

	var d perf.Dist
	for range latencyRuns {
		time.Sleep(latencySettle)
		drain(m.PTYDataChan)

		t0 := time.Now()
		if err := r.ctl.WritePTY(w.PTYID, []byte("\x1b[s")); err != nil {
			t.Fatalf("write pty: %v", err)
		}
		select {
		case <-m.PTYDataChan:
			d.AddSince(t0)
		case <-time.After(latencyWait):
			t.Fatal("no render signal for pane output")
		}
	}
	t.Log(d.Line("frame emit/quiet pane output -> signal"))
}

// drain empties the render signal so a sample times its own output rather than
// something already in flight.
func drain(ch chan struct{}) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}
