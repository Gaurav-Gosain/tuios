package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

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
