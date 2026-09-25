package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/charmbracelet/x/ansi"
)

func withButtonStyle(t *testing.T, style string, fn func()) {
	t.Helper()
	prev := config.Global.WindowButtonStyle
	config.Global.WindowButtonStyle = style
	defer func() { config.Global.WindowButtonStyle = prev }()
	fn()
}

// drawTopBorder renders a window's frame and returns the visible cells of its
// top row, with the recorded controls.
func drawTopBorder(t *testing.T, m *OS, win *terminal.Window, tiling bool) ([]rune, []WindowButtonRect) {
	t.Helper()
	content := strings.Repeat(" ", win.Width)
	out := m.addToBorder(content, lipgloss.Width(content)-2, lipgloss.Color("#7dd3fc"), win, 1, tiling)
	top, _, _ := strings.Cut(out, "\n")
	return []rune(ansi.Strip(top)), m.windowButtonRects[win.ID]
}

// Floating panes overlap, so one pane's title bar can land on another pane's
// controls. The press belongs to the pane the click resolved to, and asking by
// window is what keeps that from depending on which entry a map handed back
// first.
func TestOverlappingControlsResolveToTheirOwnWindow(t *testing.T) {
	withButtonStyle(t, config.WindowButtonStyleDots, func() {
		under := &terminal.Window{ID: "under", X: 0, Y: 5, Width: 40, Height: 8, Workspace: 1}
		over := &terminal.Window{ID: "over", X: 0, Y: 5, Width: 40, Height: 8, Workspace: 1}
		m := &OS{Settings: config.Global, Windows: []*terminal.Window{under, over}}
		_, underRects := drawTopBorder(t, m, under, false)
		drawTopBorder(t, m, over, false)

		hit := underRects[0]
		if _, ok := m.WindowButtonIn("over", hit.X, hit.Y); !ok {
			t.Fatal("the pane on top does not own the cell its own control was drawn on")
		}
		if _, ok := m.WindowButtonIn("under", hit.X, hit.Y); !ok {
			t.Fatal("the pane underneath lost the control it drew")
		}
		if _, ok := m.WindowButtonIn("nosuchwindow", hit.X, hit.Y); ok {
			t.Error("a window that drew nothing was handed another window's control")
		}
	})
}
