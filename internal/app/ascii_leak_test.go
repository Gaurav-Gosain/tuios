package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// withASCII puts the session in ASCII-only mode for the test.
func withASCII(t *testing.T) {
	t.Helper()
	prev := config.UseASCIIOnly
	config.UseASCIIOnly = true
	t.Cleanup(func() {
		config.UseASCIIOnly = prev
		overlay.SetASCII(prev)
	})
}

// TestASCIIModeIsRightOnTheFirstFrame is the leak the audit needed a warm-up
// render to hide: the overlay package's glyph set was synced inside
// renderOverlays, which runs after the sidebar layer is already built, so a
// client launched with ascii_only = true painted one frame of unicode glyphs in
// the rail before correcting itself.
//
// No warm-up here on purpose: this is the first frame the process draws.
func TestASCIIModeIsRightOnTheFirstFrame(t *testing.T) {
	withASCII(t)
	overlay.SetASCII(false) // as a fresh process starts, before any frame

	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	m := newNarrowOS(t, 120, 40)
	m.CurrentWorkspace = 1
	m.SessionName = "main"
	m.Windows = []*terminal.Window{
		{ID: "aaaaaaaa1111", CustomName: "claude", Width: 40, Height: 20, Workspace: 1, AgentState: "needs_input"},
	}
	m.FocusedWindow = 0

	frame := ansi.Strip(lipgloss.Sprint(m.GetCanvas(true).Render()))
	for i, r := range frame {
		if r > 127 {
			t.Fatalf("the first ASCII-mode frame carries %q at offset %d:\n%s", r, i, frame)
		}
	}
}

// TestWindowControlsPillIsASCIISafe covers the one unguarded glyph left on the
// pane frame: the maximize button drew a literal U+25A1 while the close button
// beside it had a fallback. Every control style is checked, because each brings
// glyphs of its own and the dots style's disc is the newest of them.
func TestWindowControlsPillIsASCIISafe(t *testing.T) {
	withASCII(t)
	for _, style := range config.WindowButtonStyles {
		t.Run(style, func(t *testing.T) {
			prev := config.WindowButtonStyle
			config.WindowButtonStyle = style
			t.Cleanup(func() { config.WindowButtonStyle = prev })

			win := &terminal.Window{ID: "w", CustomName: "pane", X: 0, Y: 0, Width: 40, Height: 12, Workspace: 1}
			m := &OS{}
			border := m.addToBorder(strings.Repeat(" ", 40), 38, lipgloss.Color("#ffffff"), win, 1, false)
			for _, r := range ansi.Strip(border) {
				if r > 127 {
					t.Fatalf("the window controls pill drew %q in ASCII mode: %q", r, ansi.Strip(border))
				}
			}
		})
	}
}

// TestASCIIEnterHintUsesTheKeyboardsName pins the key's spelling: the app names
// this key "enter" everywhere it is written out, so the ASCII form of the glyph
// is that word and not an abbreviation only this dialog used.
func TestASCIIEnterHintUsesTheKeyboardsName(t *testing.T) {
	withASCII(t)
	overlay.SetASCII(true)
	if got := overlay.EnterKey(); got != "enter" {
		t.Errorf("the ASCII enter hint reads %q, want %q", got, "enter")
	}
}
