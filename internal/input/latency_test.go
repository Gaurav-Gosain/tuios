package input

// Local input latency: the keystroke tuios answers by itself.
//
// This is the floor of the whole audit. A key that switches pane never leaves
// the process: no socket, no PTY, no guest, no coalescer. Whatever it costs is
// what tuios adds to a keystroke before any of the distributed machinery is
// involved, and the gap between this and the echo number in internal/app is
// what the daemon and the guest are worth.
//
// It lives here rather than beside the other latency measurements because the
// real key routing lives here, and internal/app cannot import it: input imports
// app, so the handler reaches app only through SetInputHandler, and a test in
// package app finds no handler registered at all.
//
// INCLUDES: HandleKeyPress routing, the action it dispatches, the per-key
// SyncStateToDaemon that internal/input does on daemon sessions, and a full
// composeFrame.
// EXCLUDES: the host terminal, bubbletea's stdin decode, and the diff written
// back to the tty.
//
//	go test ./internal/input/ -run TestLatencyLocal -v   (needs TUIOS_PERF=1)

import (
	"fmt"
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/perf"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

const (
	latencyEnv  = "TUIOS_PERF"
	latencyRuns = 500

	// latCols and latRows are the maintainer's real host size, the same one the
	// render benchmarks and the e2e perf suite use. Compose cost scales with
	// total cells, so measuring at the default would flatter this number.
	latCols, latRows = 207, 55
)

func latencyGate(t *testing.T) {
	t.Helper()
	if os.Getenv(latencyEnv) == "" {
		t.Skipf("perf: skipping, set %s=1 to measure", latencyEnv)
	}
}

// latencyOS builds an n-pane client at the real host size with every pane
// holding painted content, which is the state a frame is actually composed
// from. A pane full of blanks would compose faster than any real one.
func latencyOS(t *testing.T, n int) *app.OS {
	t.Helper()
	cfg := config.DefaultConfig()
	o := app.NewOS(app.OSOptions{
		UserConfig:      cfg,
		KeybindRegistry: config.NewKeybindRegistry(cfg),
	})
	o.Width, o.Height = latCols, latRows
	o.EffectiveWidth, o.EffectiveHeight = latCols, latRows

	// A grid, so each pane is a realistic fraction of the screen rather than n
	// copies of the whole thing.
	cols := 1
	for cols*cols < n {
		cols++
	}
	rows := (n + cols - 1) / cols
	winW, winH := latCols/cols, latRows/rows

	ptyData := make(chan struct{}, 1)
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-ptyData:
			case <-stop:
				return
			}
		}
	}()
	t.Cleanup(func() { close(stop) })

	for i := range n {
		id := fmt.Sprintf("lat-%d", i)
		win := terminal.NewDaemonWindow(id, "pane", (i%cols)*winW, (i/cols)*winH, winW, winH, i, "pty-"+id, ptyData)
		if win == nil {
			t.Fatal("NewDaemonWindow returned nil")
		}
		t.Cleanup(win.Close)
		win.Workspace = 1
		win.LockIO()
		for y := 1; y <= winH; y++ {
			line := fmt.Sprintf("line %03d ", y)
			for len(line) < winW-12 {
				line += "content "
			}
			_, _ = win.Terminal.Write(fmt.Appendf(nil, "\x1b[%d;1H\x1b[38;5;%dm%s\x1b[m", y, 16+(y%200), line))
		}
		win.UnlockIO()
		o.Windows = append(o.Windows, win)
	}
	o.CurrentWorkspace, o.FocusedWindow = 1, 0
	return o
}

// frame composes and returns the client's frame, which is what bubbletea does
// with the tea.View that View returns.
func frame(t *testing.T, o *app.OS) string {
	t.Helper()
	v := o.View()
	return fmt.Sprint(v)
}

// TestLatencyLocal measures a key that moves focus to the next pane, in to the
// frame that reflects it.
//
// The key goes through HandleKeyPress, so the real gate chain and the real
// action dispatch are in the number, and the frame is composed afterwards
// because bubbletea composes after every message it delivers.
func TestLatencyLocal(t *testing.T) {
	latencyGate(t)
	app.SetInputHandler(HandleInput)

	for _, panes := range []int{1, 4, 8} {
		t.Run(fmt.Sprintf("panes%d", panes), func(t *testing.T) {
			o := latencyOS(t, panes)
			// One composed frame up front, so the first sample is not paying
			// for caches every later sample finds warm.
			_ = frame(t, o)

			var d perf.Dist
			for range latencyRuns {
				t0 := time.Now()
				o, _ = HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyTab}, o)
				_ = frame(t, o)
				d.AddSince(t0)
			}
			t.Log(d.Line(fmt.Sprintf("local/next pane -> frame, %d panes", panes)))
		})
	}
}

// TestLatencyLocalRouting isolates the routing from the compose: the same key,
// without composing a frame afterwards. Subtracting this from TestLatencyLocal
// says whether a local keystroke is dominated by deciding what to do or by
// drawing the result, which are two different things to go and fix.
func TestLatencyLocalRouting(t *testing.T) {
	latencyGate(t)
	app.SetInputHandler(HandleInput)

	o := latencyOS(t, 4)
	_ = frame(t, o)

	var d perf.Dist
	for range latencyRuns {
		t0 := time.Now()
		o, _ = HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyTab}, o)
		d.AddSince(t0)
	}
	t.Log(d.Line("local/next pane routing only, 4 panes"))
}
