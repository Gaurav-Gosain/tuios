package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
	"github.com/charmbracelet/x/ansi"
)

// TestKeyboardEnhancementsFollowThePane checks the half of release forwarding
// that happens before any key is pressed: tuios can only pass on releases the
// host sends it, so a pane asking for them has to change what tuios asks for.
func TestKeyboardEnhancementsFollowThePane(t *testing.T) {
	em := vt.NewEmulator(80, 24)
	t.Cleanup(func() { _ = em.Close() })
	win := &terminal.Window{ID: "enhance-0001", Terminal: em, Width: 82, Height: 26}
	m := &OS{Mode: TerminalMode, FocusedWindow: 0, Windows: []*terminal.Window{win}}

	if got := m.keyboardEnhancements(); got.ReportEventTypes {
		t.Error("a session with no pane asking for releases should not ask the host for them")
	}

	// CSI >11u: what a compositor running in a pane pushes.
	_, _ = em.Write([]byte("\x1b[>11u"))
	if flags := m.PaneKeyboardFlags(); flags&ansi.KittyReportEventTypes == 0 {
		t.Fatalf("pane flags = %d, want the event-type bit set", flags)
	}
	got := m.keyboardEnhancements()
	if !got.ReportEventTypes {
		t.Error("a pane asking for releases must make tuios ask the host for them")
	}
	if !got.ReportAllKeysAsEscapeCodes {
		t.Error("a host only reports the release of a key it sends as an escape code, so all keys must be asked for")
	}
	if !got.ReportAssociatedText {
		t.Error("all-keys-as-escape-codes stops text being sent as text; associated text has to come with it")
	}
}
