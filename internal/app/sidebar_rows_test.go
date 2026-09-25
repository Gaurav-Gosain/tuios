package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// TestSidebarSanitizesTitles checks a title carrying nerd-font private-use
// icons and control characters reaches the rail laundered.
func TestSidebarSanitizesTitles(t *testing.T) {
	if got := printableTitle(" nvim \x1b]0;x\x07"); got != "nvim ]0;x" {
		// The escape byte and the bell go; printable remnants of a title
		// sequence stay (they are the shell's bug to fix, not tofu).
		t.Errorf("printableTitle = %q", got)
	}
	overlay.SetASCII(true)
	t.Cleanup(func() { overlay.SetASCII(false) })
	if got := printableTitle("café ▲"); got != "caf" {
		t.Errorf("ASCII printableTitle = %q, want %q", got, "caf")
	}
}
