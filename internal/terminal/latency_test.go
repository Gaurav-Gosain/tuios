package terminal

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
