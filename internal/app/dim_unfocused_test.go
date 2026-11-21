package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

func dimTestWindow(t testing.TB, w, h int) *terminal.Window {
	t.Helper()
	win := newTestWindow(t, "dim", w, h)
	for i := range h {
		_ = i
		win.WriteOutput([]byte("\x1b[38;2;200;200;200;48;2;40;40;60mhello world\x1b[0m\r\n"))
	}
	win.MarkContentDirty()
	return win
}

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
	if !strings.Contains(unfocused, "hello world") {
		t.Error("the dim ate the content")
	}
}

func TestTheDimIsPartOfTheContentCacheKey(t *testing.T) {
	withDim(t, 50)
	win := dimTestWindow(t, 40, 6)
	m := newTestOS(win)

	unfocused := m.renderTerminal(win, false, false)
	if win.ContentDirty {
		t.Fatal("the frame was not cached, so this proves nothing")
	}
	focused := m.renderTerminal(win, true, false)
	if focused == unfocused {
		t.Error("the cached dimmed frame was served to a focused pane")
	}
}
