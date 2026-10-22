package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// BenchmarkPopupScrimFrame holds the popup's frame cost beside its disabled
// path. A live popup dirties its own content on each frame over nine cached
// panes at 207x55. The dim adds one canvas walk; with it off there must be no
// walk at all. This uses the whole compose and emit path
// so a benchmark of the shade function alone cannot hide extra frame work.
func BenchmarkPopupScrimFrame(b *testing.B) {
	benchmarkPopupScrim(b, true)
}

// BenchmarkPopupScrimCompose separates the canvas work from ANSI emission,
// whose allocations also depend on the length of the shaded frame's string.
func BenchmarkPopupScrimCompose(b *testing.B) {
	benchmarkPopupScrim(b, false)
}

func benchmarkPopupScrim(b *testing.B, emit bool) {
	prev := theme.CurrentThemeID()
	_ = theme.Initialize("catppuccin_mocha")
	b.Cleanup(func() { _ = theme.Initialize(prev) })
	for _, dim := range []int{0, 30} {
		b.Run(fmt.Sprintf("dim-%d", dim), func(b *testing.B) {
			m := benchOS(b, 9)
			m.UserConfig = config.DefaultConfig()
			m.Settings.PopupDim = dim
			popup := benchWindow(b, "popup-scrim", 80, 20)
			popup.X, popup.Y = 60, 16
			popup.Width, popup.Height = 80, 20
			popup.Workspace, popup.IsPopup = 1, true
			popup.IsFloating = true
			popup.Z = 100
			m.Windows = append(m.Windows, popup)
			m.FocusedWindow = len(m.Windows) - 1
			m.composeFrame()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				popup.MarkContentDirty()
				if emit {
					_ = m.composeFrame()
				} else {
					_ = m.GetCanvas(true)
				}
			}
		})
	}
}
