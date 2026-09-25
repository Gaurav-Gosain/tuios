package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi/kitty"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestHostDrawsPlaceholdersReadsTheTerminalsOwnAnswer pins the detection. It is
// a heuristic, and the point of the test is that it is a heuristic over what
// the terminal said about itself rather than over an inherited TERM.
func TestHostDrawsPlaceholders(t *testing.T) {
	reply := func(s string) string { return "\x1bP>|" + s + "\x1b\\" }

	for _, tc := range []struct {
		name string
		resp string
		want bool
	}{
		// The two spellings seen in the wild.
		{"ghostty 1.3.1 draws them", reply("ghostty 1.3.1"), true},
		{"kitty 0.32.2 draws them", reply("kitty(0.32.2)"), true},
		{"wezterm draws them", reply("wezterm 20240203"), true},
		// Older than the version that added the feature.
		{"kitty 0.26 is too old", reply("kitty(0.26.5)"), false},
		{"ghostty 0.9 is too old", reply("ghostty 0.9.0"), false},
		// A terminal nobody has vouched for falls back rather than guessing.
		{"an unknown terminal is not assumed", reply("someterm 9.9.9"), false},
		{"no answer at all is not assumed", "", false},
		{"a malformed answer is not assumed", "\x1bP>|\x1b\\", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hostDrawsPlaceholders(tc.resp); got != tc.want {
				t.Errorf("hostDrawsPlaceholders(%q) = %v, want %v", tc.resp, got, tc.want)
			}
		})
	}
}

// TestChangingTheSettingReachesOpenPanes is what makes the row in the settings
// page worth having. The mode is installed when a pane is created, so without
// this a change would only apply to the next pane somebody opened.
//
// Negative control: removing the refreshKittyPlaceholderMode call from
// applyAppearanceLive left the pane on its old mode and this failed.
func TestChangingTheSettingReachesOpenPanes(t *testing.T) {
	term := vt.New(40, 10)
	m := &OS{
		Settings: config.DefaultSettings(),
		Caps:     &HostCapabilities{KittyGraphics: true, KittyPlaceholders: true},
		Windows:  []*terminal.Window{{Terminal: term}},
	}
	m.KittyPassthrough = newTestKittyPassthrough(t)

	m.Settings.KittyPlaceholders = config.KittyPlaceholdersOn
	m.refreshKittyPlaceholderMode()
	if _, err := term.Write([]byte(placeholderRowForTest(0x0a0b0c, 0, 2))); err != nil {
		t.Fatalf("write: %v", err)
	}
	if cell := term.CellAt(0, 0); cell == nil || !vt.IsKittyPlaceholder(cell.Content) {
		t.Fatal(`with the setting on, the pane dropped a placeholder cell`)
	}

	// Turn it off and a pane that redraws stops keeping them.
	m.Settings.KittyPlaceholders = config.KittyPlaceholdersOff
	m.refreshKittyPlaceholderMode()
	if _, err := term.Write([]byte("\x1b[2J\x1b[H" + placeholderRowForTest(0x0a0b0c, 0, 2))); err != nil {
		t.Fatalf("write: %v", err)
	}
	if cell := term.CellAt(0, 0); cell != nil && vt.IsKittyPlaceholder(cell.Content) {
		t.Error(`with the setting off, the pane kept a placeholder cell`)
	}
}

// placeholderRowForTest writes one row of placeholder cells the way an
// application does: the id in the foreground, the row on the first cell.
func placeholderRowForTest(id uint32, row, cols int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm", (id>>16)&0xff, (id>>8)&0xff, id&0xff)
	b.WriteRune(kitty.Placeholder)
	b.WriteRune(kitty.Diacritic(row))
	for range cols - 1 {
		b.WriteRune(kitty.Placeholder)
	}
	b.WriteString("\x1b[39m")
	return b.String()
}
