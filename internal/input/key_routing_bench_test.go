package input

import (
	"fmt"
	"os/exec"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"tuios.dev/vt"
)

// sinkPty is a PTY that accepts every write and keeps nothing, so a benchmark
// measures the routing and not the growth of a capture buffer.
type sinkPty struct{ n int }

func (p *sinkPty) Write(b []byte) (int, error) { p.n += len(b); return len(b), nil }
func (p *sinkPty) Read([]byte) (int, error)    { return 0, nil }
func (p *sinkPty) Close() error                { return nil }
func (p *sinkPty) Fd() uintptr                 { return 0 }
func (p *sinkPty) Resize(_, _ int) error       { return nil }
func (p *sinkPty) Size() (int, int, error)     { return 80, 24, nil }
func (p *sinkPty) Name() string                { return "sink-pty" }
func (p *sinkPty) Start(_ *exec.Cmd) error     { return nil }

// benchOS is a client with one focused local pane, the state a person typing
// into a shell is in.
func benchOS(b *testing.B, mode app.Mode) (*app.OS, *sinkPty) {
	b.Helper()
	cfg := config.DefaultConfig()
	em := vt.NewEmulator(80, 24)
	b.Cleanup(func() { _ = em.Close() })
	pty := &sinkPty{}
	win := &terminal.Window{ID: "bench-0001", Terminal: em, Pty: pty, X: 0, Y: 0, Width: 82, Height: 26}
	o := app.NewOS(app.OSOptions{UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
	o.Width, o.Height = 120, 40
	o.Windows = []*terminal.Window{win}
	o.FocusedWindow = 0
	win.Workspace = o.CurrentWorkspace
	o.Mode = mode
	return o, pty
}

// BenchmarkKeyTerminalTyped is a plain letter typed into a focused shell: the
// hottest key path there is, and the one every gate on the way to the PTY is
// paid on.
func BenchmarkKeyTerminalTyped(b *testing.B) {
	o, pty := benchOS(b, app.TerminalMode)
	msg := tea.KeyPressMsg{Code: 'a', Text: "a"}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		HandleInput(msg, o)
	}
	b.StopTimer()
	if pty.n != b.N {
		b.Fatalf("pty got %d bytes for %d keys", pty.n, b.N)
	}
}

// BenchmarkKeyTerminalCtrl is a control chord typed into a shell, which also
// goes through the reserved-chord lookups in the main section.
func BenchmarkKeyTerminalCtrl(b *testing.B) {
	o, pty := benchOS(b, app.TerminalMode)
	msg := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		HandleInput(msg, o)
	}
	b.StopTimer()
	if pty.n != b.N {
		b.Fatalf("pty got %d bytes for %d keys", pty.n, b.N)
	}
}

// BenchmarkKeyWindowModeTab is the key the earlier latency pass measured: a
// window-mode action resolved through the flattened main map.
func BenchmarkKeyWindowModeTab(b *testing.B) {
	o, _ := benchOS(b, app.WindowManagementMode)
	msg := tea.KeyPressMsg{Code: tea.KeyTab}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		HandleInput(msg, o)
	}
}

// BenchmarkKeyPrefixChord is leader then a prefix key, the two-key path.
func BenchmarkKeyPrefixChord(b *testing.B) {
	o, _ := benchOS(b, app.TerminalMode)
	leader := tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}
	// prefix_next_window is bound in the prefix section by default.
	next := tea.KeyPressMsg{Code: 'n', Text: "n"}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		HandleInput(leader, o)
		HandleInput(next, o)
	}
}

// BenchmarkMouseMotionFiltered is a pointer moving over an idle desktop with
// nothing that reacts to it: the filter must drop it before Update sees it.
func BenchmarkMouseMotionFiltered(b *testing.B) {
	o, _ := benchOS(b, app.TerminalMode)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		msg := tea.MouseMotionMsg{X: 100 + i%10, Y: 35}
		if app.FilterMouseMotion(o, msg) != nil {
			b.Fatal("motion over nothing reached Update")
		}
	}
}

// BenchmarkMouseMotionHover is a pointer moving over pane content, which the
// link hover clause lets through, then the full handler.
func BenchmarkMouseMotionHover(b *testing.B) {
	o, _ := benchOS(b, app.TerminalMode)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		msg := tea.MouseMotionMsg{X: 10 + i%20, Y: 10}
		if m := app.FilterMouseMotion(o, msg); m != nil {
			o.Update(m)
		}
	}
}

// BenchmarkMouseSweepContent is a pointer sweeping across a pane full of plain
// text at the maintainer's host size, through the filter and, when it passes,
// through Update and a composed frame. This is what moving the mouse over a
// shell costs.
func BenchmarkMouseSweepContent(b *testing.B) {
	cfg := config.DefaultConfig()
	o := app.NewOS(app.OSOptions{UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
	o.Width, o.Height = 207, 55
	o.EffectiveWidth, o.EffectiveHeight = 207, 55
	em := vt.NewEmulator(205, 53)
	b.Cleanup(func() { _ = em.Close() })
	win := &terminal.Window{ID: "sweep-0001", Terminal: em, Pty: &sinkPty{}, X: 0, Y: 0, Width: 207, Height: 55}
	for y := 1; y <= 53; y++ {
		_, _ = em.Write(fmt.Appendf(nil, "\x1b[%d;1Hline %03d of plain shell output with words and numbers 12345 and no address at all", y, y))
	}
	o.Windows = []*terminal.Window{win}
	o.FocusedWindow = 0
	win.Workspace = o.CurrentWorkspace
	o.Mode = app.WindowManagementMode
	_ = fmt.Sprint(o.View())

	frames := 0
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		var msg tea.Msg = tea.MouseMotionMsg{X: 2 + i%200, Y: 2 + (i/200)%50}
		if msg = app.FilterMouseMotion(o, msg); msg == nil {
			continue
		}
		o.Update(msg)
		_ = fmt.Sprint(o.View())
		frames++
	}
	b.ReportMetric(float64(frames)/float64(b.N), "frames/event")
}
