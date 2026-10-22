package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The target can lose focus, be hidden, or inherit the wrong option. An
// explicit zero must win even for TML, and out-of-range state must stay bounded.
func TestPopupScrimTarget(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*OS, *terminal.Window)
		want   int
	}{
		{"plain", func(*OS, *terminal.Window) {}, 40},
		{"tml", func(_ *OS, w *terminal.Window) { w.Kind = session.WindowKindTML }, 30},
		{"override", func(_ *OS, w *terminal.Window) { w.PopupDim = &config.PopupDim{Percent: 60} }, 60},
		{"tml override", func(_ *OS, w *terminal.Window) {
			w.Kind = session.WindowKindTML
			w.PopupDim = &config.PopupDim{Percent: 60}
		}, 60},
		{"explicit zero", func(_ *OS, w *terminal.Window) { w.PopupDim = &config.PopupDim{} }, 0},
		{"tml zero", func(_ *OS, w *terminal.Window) { w.Kind = session.WindowKindTML; w.PopupDim = &config.PopupDim{} }, 0},
		{"option off", func(m *OS, _ *terminal.Window) { m.Settings.PopupDim = 0 }, 0},
		{"modal off", func(m *OS, _ *terminal.Window) { m.Settings.ModalDim = 0 }, 40},
		{"focus away", func(m *OS, _ *terminal.Window) { m.FocusedWindow = 0 }, 0},
		{"no focus", func(m *OS, _ *terminal.Window) { m.FocusedWindow = -1 }, 0},
		{"minimized", func(_ *OS, w *terminal.Window) { w.Minimized = true }, 0},
		{"other workspace", func(_ *OS, w *terminal.Window) { w.Workspace = 2 }, 0},
		{"tiled", func(_ *OS, w *terminal.Window) { w.Tiled = true }, 0},
		{"plain pane", func(_ *OS, w *terminal.Window) { w.IsPopup = false }, 0},
		{"negative", func(_ *OS, w *terminal.Window) { w.PopupDim = &config.PopupDim{Percent: -1} }, 0},
		{"clamped", func(_ *OS, w *terminal.Window) { w.PopupDim = &config.PopupDim{Percent: 100} }, 90},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, w := popupOS(t, "50%", "50%")
			m.Settings.PopupDim, m.Settings.ModalDim = 40, 30
			m.DaemonClient = &session.TUIClient{}
			tc.change(m, w)
			id, dim := m.popupScrimTarget()
			wantID := ""
			if tc.want > 0 {
				wantID = w.ID
			}
			if id != wantID || dim != tc.want {
				t.Fatalf("target = (%q, %d), want (%q, %d)", id, dim, wantID, tc.want)
			}
		})
	}
}

// A lone popup can fill the pane region. Its dock still needs the canvas
// scrim, while the same popup with dim off can take the fullscreen shortcut.
func TestPopupScrimNeedsTheCanvas(t *testing.T) {
	m := keystrokeOS(t, 1, 120, 40)
	m.Settings.SidebarEnabled = false
	m.Settings.DockbarPosition = "bottom"
	w := m.Windows[0]
	w.IsPopup, w.IsFloating = true, true
	w.X, w.Y, w.Width, w.Height = 0, m.GetTopMargin(), m.GetRenderWidth(), m.GetUsableHeight()
	m.Settings.PopupDim = 0
	if _, ok := m.fullscreenFastWindow(); !ok {
		t.Fatal("the undimmed fullscreen popup did not take the fast path")
	}
	m.Settings.PopupDim = 30
	if _, ok := m.fullscreenFastWindow(); ok {
		t.Fatal("the dimmed fullscreen popup skipped the dock's scrim")
	}
}
