package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// dimTestWindow is a pane with coloured guest output in it, which is what the
// dim has anything to do to.
func dimTestWindow(t testing.TB, w, h int) *terminal.Window {
	t.Helper()
	win := newTestWindow(t, "dim", w, h)
	// Explicit SGR, so the cells carry colours of their own rather than the
	// terminal default the dim deliberately leaves alone.
	for i := range h {
		_ = i
		win.WriteOutput([]byte("\x1b[38;2;200;200;200;48;2;40;40;60mhello world\x1b[0m\r\n"))
	}
	win.MarkContentDirty()
	return win
}

// withDim turns the dim on for one test, with a theme so there is a ground to
// carry toward.
func withDim(t *testing.T, percent int) {
	t.Helper()
	prevDim := config.Global.DimUnfocused
	prevTheme := theme.CurrentThemeID()
	config.Global.DimUnfocused = percent
	_ = theme.Initialize("catppuccin_mocha")
	t.Cleanup(func() {
		config.Global.DimUnfocused = prevDim
		_ = theme.Initialize(prevTheme)
	})
}

func TestDimQuietsAnUnfocusedPaneAndLeavesTheFocusedOneAlone(t *testing.T) {
	withDim(t, 50)
	win := dimTestWindow(t, 40, 6)
	m := newTestOS(win)

	focused := m.renderTerminal(win, true, false)
	win.MarkContentDirty()
	unfocused := m.renderTerminal(win, false, false)

	if focused == unfocused {
		t.Fatal("the unfocused frame is byte-identical to the focused one; nothing was dimmed")
	}
	if !strings.Contains(focused, "200;200;200") {
		t.Errorf("the focused frame lost the guest's own colour:\n%q", focused)
	}
	if strings.Contains(unfocused, "200;200;200") {
		t.Errorf("the unfocused frame kept the guest's undimmed colour:\n%q", unfocused)
	}
	// The text is the guest's and must survive being quieted.
	if !strings.Contains(unfocused, "hello world") {
		t.Error("the dim ate the content")
	}
}

func TestTheDimIsPartOfTheContentCacheKey(t *testing.T) {
	// FocusWindow gives the pane being left the lighter invalidation that keeps
	// its cached string on purpose. Without the dim in the key, the pane you
	// just stepped away from would serve the undimmed frame until its guest
	// next wrote something, which is the whole of what a user would report as
	// "it only dims sometimes".
	withDim(t, 50)
	win := dimTestWindow(t, 40, 6)
	m := newTestOS(win)

	unfocused := m.renderTerminal(win, false, false)
	if win.ContentDirty {
		t.Fatal("the frame was not cached, so this proves nothing")
	}
	// No dirty marking at all: only the focus changes, exactly as it does when
	// FocusWindow moves on.
	focused := m.renderTerminal(win, true, false)
	if focused == unfocused {
		t.Error("the cached dimmed frame was served to a focused pane")
	}
}

func TestDimOffLeavesTheRenderPathExactlyAsItWas(t *testing.T) {
	prev := config.Global.DimUnfocused
	config.Global.DimUnfocused = 0
	t.Cleanup(func() { config.Global.DimUnfocused = prev })

	win := dimTestWindow(t, 40, 6)
	m := newTestOS(win)
	unfocused := m.renderTerminal(win, false, false)
	if !strings.Contains(unfocused, "200;200;200") {
		t.Errorf("dim off changed an unfocused frame:\n%q", unfocused)
	}
}

func TestWithNoThemeTheDimLeavesDefaultColouredCellsAlone(t *testing.T) {
	// Untheme, tuios emits colour indices and the host terminal decides what
	// they look like, so there is no RGB here to carry anywhere. Guessing one
	// would repaint the user's own palette on the panes they are not looking
	// at. Validation says this out loud rather than letting it look broken.
	prevDim, prevTheme := config.Global.DimUnfocused, theme.CurrentThemeID()
	config.Global.DimUnfocused = 50
	_ = theme.Initialize("")
	t.Cleanup(func() {
		config.Global.DimUnfocused = prevDim
		_ = theme.Initialize(prevTheme)
	})

	win := newTestWindow(t, "plain", 40, 6)
	win.WriteOutput([]byte("plain text\r\n"))
	win.MarkContentDirty()
	m := newTestOS(win)

	if got := m.renderTerminal(win, false, false); !strings.Contains(got, "plain text") {
		t.Errorf("untheme render lost its content:\n%q", got)
	}

	cfg := config.DefaultConfig()
	cfg.Appearance.DimUnfocused = 50
	var warned bool
	for _, w := range config.ValidateConfig(cfg).Warnings {
		if w.Key == "dim_unfocused" {
			warned = true
		}
	}
	if !warned {
		t.Error("no warning that an unthemed dim only reaches cells a program coloured itself")
	}
}

// BenchmarkDimUnfocusedPane is the cost the feature actually charges: an
// unfocused pane with the dim on comes off the emulator's own fast renderer and
// goes cell by cell, so this measures the whole difference rather than the
// blend alone. Run it against BenchmarkUndimmedUnfocusedPane below.
func BenchmarkDimUnfocusedPane(b *testing.B) {
	benchDimPane(b, 50)
}

// BenchmarkUndimmedUnfocusedPane is the same pane with the dim off, which is
// the path every existing install stays on.
func BenchmarkUndimmedUnfocusedPane(b *testing.B) {
	benchDimPane(b, 0)
}

// BenchmarkCellLoopPaneNoDim is the same cell-by-cell path with no blend on it,
// which is what isolates the blend's own cost: the two benchmarks above differ
// by the render path as well, because the dim is what takes a pane off the
// emulator's own renderer.
func BenchmarkCellLoopPaneNoDim(b *testing.B) {
	benchCellLoopPane(b, 0)
}

// BenchmarkCellLoopPaneDimmed is that path with the blend on.
func BenchmarkCellLoopPaneDimmed(b *testing.B) {
	benchCellLoopPane(b, 50)
}

// benchCellLoopPane renders focused, which always takes the cell loop, so the
// only difference between the two callers is the blend.
func benchCellLoopPane(b *testing.B, percent int) {
	prevDim, prevTheme := config.Global.DimUnfocused, theme.CurrentThemeID()
	// Focused panes are never dimmed, so the blend is forced on by rendering
	// unfocused and taking the fast path away with a scrollback offset, which
	// is what a scrolled-back pane does anyway.
	config.Global.DimUnfocused = percent
	_ = theme.Initialize("catppuccin_mocha")
	b.Cleanup(func() {
		config.Global.DimUnfocused = prevDim
		_ = theme.Initialize(prevTheme)
	})

	win := dimTestWindow(b, 200, 50)
	win.ScrollbackOffset = 1
	m := newTestOS(win)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		win.MarkContentDirty()
		_ = m.renderTerminal(win, false, false)
	}
}

func benchDimPane(b *testing.B, percent int) {
	prevDim, prevTheme := config.Global.DimUnfocused, theme.CurrentThemeID()
	config.Global.DimUnfocused = percent
	_ = theme.Initialize("catppuccin_mocha")
	b.Cleanup(func() {
		config.Global.DimUnfocused = prevDim
		_ = theme.Initialize(prevTheme)
	})

	win := dimTestWindow(b, 200, 50)
	m := newTestOS(win)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		win.MarkContentDirty()
		_ = m.renderTerminal(win, false, false)
	}
}
