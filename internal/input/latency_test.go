package input

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

	latCols, latRows = 207, 55
)

func latencyGate(t *testing.T) {
	t.Helper()
	if os.Getenv(latencyEnv) == "" {
		t.Skipf("perf: skipping, set %s=1 to measure", latencyEnv)
	}
}

func latencyOS(t *testing.T, n int) *app.OS {
	t.Helper()
	cfg := config.DefaultConfig()
	o := app.NewOS(app.OSOptions{
		UserConfig:      cfg,
		KeybindRegistry: config.NewKeybindRegistry(cfg),
	})
	o.Width, o.Height = latCols, latRows
	o.EffectiveWidth, o.EffectiveHeight = latCols, latRows

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
		win := terminal.NewDaemonWindow(id, "pane", (i%cols)*winW, (i/cols)*winH, winW, winH, i, "pty-"+id, ptyData, config.DefaultScrollbackLines)
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

// TestLatencyTypedKey measures the key a person types most: a plain letter into
// a focused shell, from HandleKeyPress to the byte reaching the pane's writer.
// No frame is composed, because the frame after a forwarded key is the same
// frame as before it (see docs/perf.md); this is the routing alone.
//
// TestLatencyLocalRouting measures a window-mode key, which reads the flattened
// keymap. A typed key reads the terminal-mode, global and main sections in
// turn, and those used to be rebuilt from the config on every lookup.
func TestLatencyTypedKey(t *testing.T) {
	latencyGate(t)
	app.SetInputHandler(HandleInput)

	o := latencyOS(t, 4)
	o.Mode = app.TerminalMode
	o.Windows[0].DaemonWriteFunc = func([]byte) error { return nil }
	_ = frame(t, o)

	msg := tea.KeyPressMsg{Code: 'a', Text: "a"}
	var d perf.Dist
	for range latencyRuns {
		t0 := time.Now()
		o, _ = HandleKeyPress(msg, o)
		d.AddSince(t0)
	}
	t.Log(d.Line("typed letter -> pane writer, 4 panes"))
}
