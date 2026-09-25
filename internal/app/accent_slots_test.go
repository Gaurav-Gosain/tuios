package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// withTheme pins the active theme for one test and puts the old one back. The
// theme is global, which is exactly why a slot accent is worth keeping as a
// slot: it is the thing that moves under a stored colour.
func withTheme(t *testing.T, id string) {
	t.Helper()
	prev := theme.CurrentThemeID()
	if err := theme.Initialize(id); err != nil {
		t.Fatalf("theme %q: %v", id, err)
	}
	t.Cleanup(func() { _ = theme.Initialize(prev) })
}
