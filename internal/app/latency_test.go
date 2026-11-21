package app

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
	latencyEnv = "TUIOS_PERF"

	latencyRuns = 200

	latencyWait = 5 * time.Second

	latencySettle = 30 * time.Millisecond

	latCols, latRows = 207, 55
)

func latencyGate(t *testing.T) {
	t.Helper()
	if os.Getenv(latencyEnv) == "" {
		t.Skipf("perf: skipping, set %s=1 to measure", latencyEnv)
	}
}

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

func frame(m *OS) string {
	m.View()
	return ansi.Strip(string(m.cachedViewContent))
}

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

	if err := r.m.SendInputToDaemon(w, []byte("#")); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := frame(r.m); got != before {
		t.Log("a keystroke now changes the frame by itself; the wasted-compose " +
			"finding in docs/perf.md no longer holds as written")
		t.Fail()
	}
}

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

func drain(ch chan struct{}) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}
