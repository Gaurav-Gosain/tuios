package terminal

// The render coalescer, measured on its own.
//
// A daemon pane's output does not reach the client's event loop directly. It is
// written to the emulator by outputWriter, which raises a flag, and a separate
// goroutine turns that flag into the render signal. Everything above this in
// the stack (the socket, the PTY, the guest, the compositor) is excluded here
// on purpose: this is the one hop with a fixed interval in it, and the only way
// to know what that interval really costs is to measure it with nothing else in
// the number.
//
// The pane is left quiet before each sample. That is not a convenience: a
// keystroke is by definition something a user does to a pane they were looking
// at, so the quiet case is the one that decides how typing feels, and a
// coalescer's cost to a quiet pane is a different question from its cost to a
// pane already saturating the pipe.
//
//	go test ./internal/terminal/ -run TestLatencyCoalescer -v   (needs TUIOS_PERF=1)

import (
	"fmt"
	"math/rand/v2"
	"os"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/perf"
)

const (
	coalEnv  = "TUIOS_PERF"
	coalRuns = 300

	// coalQuiet is the floor of the silence before a sample, well past any
	// interval the coalescer could be running at, so each sample starts from a
	// genuinely idle pane rather than the tail of the one before it.
	//
	// A jitter is added on top, and it is load-bearing rather than tidy. The
	// coalescer is a free-running ticker, so what a sample costs depends
	// entirely on where in the tick period the output lands. Sleeping a fixed
	// amount makes every sample land at the same phase and reports one point of
	// the distribution as though it were all of it: a fixed 25 ms wait produced
	// min 6.01 ms and max 7.09 ms, which reads like a tight, well-behaved hop
	// and is really the same phase measured 300 times.
	coalQuiet  = 20 * time.Millisecond
	coalJitter = 16 * time.Millisecond

	coalWait = 2 * time.Second
)

// TestLatencyCoalescer measures from a pane producing output to the render
// signal carrying it, with no daemon, no guest and no compositor involved.
func TestLatencyCoalescer(t *testing.T) {
	if os.Getenv(coalEnv) == "" {
		t.Skipf("perf: skipping, set %s=1 to measure", coalEnv)
	}

	ptyData := make(chan struct{}, 1)
	w := NewDaemonWindow("coal", "pane", 0, 0, 80, 24, 0, "pty-coal", ptyData, config.DefaultScrollbackLines)
	if w == nil {
		t.Fatal("NewDaemonWindow returned nil")
	}
	t.Cleanup(w.Close)

	var d perf.Dist
	for i := range coalRuns {
		time.Sleep(coalQuiet + time.Duration(rand.Int64N(int64(coalJitter))))
		// Drain, so a sample times its own output rather than something the
		// previous one left in flight.
		select {
		case <-ptyData:
		default:
		}

		t0 := time.Now()
		w.WriteOutputAsync(fmt.Appendf(nil, "\x1b[1;1Hx%03d", i))
		select {
		case <-ptyData:
			d.AddSince(t0)
		case <-time.After(coalWait):
			t.Fatalf("run %d: pane output never raised a render signal", i)
		}
	}
	t.Log(d.Line("coalescer/quiet pane output -> signal"))
}
