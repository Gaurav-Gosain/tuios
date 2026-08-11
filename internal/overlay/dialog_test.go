package overlay

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
)

func dialogTestPalette() Palette {
	return Palette{
		Canvas:       charmtone.Pepper,
		Surface:      charmtone.Char,
		Fg:           charmtone.Butter,
		FgDim:        charmtone.Smoke,
		FgMute:       charmtone.Oyster,
		Accent:       charmtone.Charple,
		AccentBright: charmtone.Hazy,
	}
}

// plainLines strips styling so the frame's own geometry can be read.
func plainLines(s string) []string {
	out := strings.Split(s, "\n")
	for i, l := range out {
		out[i] = stripSGR(l)
	}
	return out
}

func stripSGR(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			i = j + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// TestDialogSetsItsFurnitureInTheBorders: a micro-dialog spends no interior row
// on a title or a hint strip, so a one-field prompt is three rows tall.
func TestDialogSetsItsFurnitureInTheBorders(t *testing.T) {
	d := Dialog{
		Title: "rename",
		Width: 28,
		Body:  " > build-2",
		Hints: []Hint{{Key: "↵", Label: "save"}, {Key: "esc", Label: "cancel"}},
	}
	out, geo := d.Render(dialogTestPalette())
	lines := plainLines(out)

	if len(lines) != 3 {
		t.Fatalf("a one-line body should render three rows, got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if geo.Width != 30 || geo.Height != 3 || geo.InnerWidth != 28 {
		t.Errorf("geometry %+v, want 30x3 inner 28", geo)
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w != 30 {
			t.Errorf("row %d is %d cells, want 30: %q", i, w, l)
		}
	}
	if !strings.HasPrefix(lines[0], "╭─ rename ") || !strings.HasSuffix(lines[0], "╮") {
		t.Errorf("the title is not set into the top border: %q", lines[0])
	}
	if !strings.Contains(lines[2], "↵ save  esc cancel") || !strings.HasPrefix(lines[2], "╰") {
		t.Errorf("the hints are not set into the bottom border: %q", lines[2])
	}
	if !strings.HasPrefix(lines[1], "│") || !strings.HasSuffix(lines[1], "│") {
		t.Errorf("the body row is not framed: %q", lines[1])
	}
}

// A dialog too narrow for all of its hints drops them from the end rather than
// wrapping onto a row it does not have.
func TestDialogDropsHintsItCannotCarry(t *testing.T) {
	d := Dialog{
		Title: "rename",
		Width: 12,
		Body:  " > x",
		Hints: []Hint{{Key: "↵", Label: "save"}, {Key: "esc", Label: "cancel"}},
	}
	out, _ := d.Render(dialogTestPalette())
	lines := plainLines(out)
	if len(lines) != 3 {
		t.Fatalf("want three rows, got %d", len(lines))
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w != 14 {
			t.Errorf("row %d is %d cells, want 14: %q", i, w, l)
		}
	}
	if strings.Contains(lines[2], "cancel") {
		t.Errorf("a hint that does not fit is still in the border: %q", lines[2])
	}
}

// ASCII mode swaps the frame for +-| and the dashed rule for -, keeping every
// cell one wide.
func TestDialogDegradesToASCII(t *testing.T) {
	SetASCII(true)
	t.Cleanup(func() { SetASCII(false) })

	d := Dialog{Title: "accent", Width: 20, Body: " > red", Hints: []Hint{{Key: "esc", Label: "cancel"}}}
	out, _ := d.Render(dialogTestPalette())
	for i, l := range plainLines(out) {
		if strings.ContainsAny(l, "╭╮╰╯─│╌") {
			t.Errorf("row %d still draws box glyphs in ASCII mode: %q", i, l)
		}
		if w := lipgloss.Width(l); w != 22 {
			t.Errorf("row %d is %d cells, want 22: %q", i, w, l)
		}
	}
	if r := stripSGR(DashRule(6, charmtone.Pepper, dialogTestPalette())); r != "------" {
		t.Errorf("the dashed rule did not degrade: %q", r)
	}
}

// The cursor is reverse video, not a painted background: it is the one mark
// that still has to be found on a monochrome terminal.
func TestDialogCursorIsReverseVideo(t *testing.T) {
	pal := dialogTestPalette()
	c := Cursor(" ", pal.Canvas, pal.Fg)
	if !strings.Contains(c, "\x1b[7;") && !strings.Contains(c, ";7m") && !strings.Contains(c, "\x1b[7m") {
		t.Errorf("the cursor does not set reverse video: %q", c)
	}
	if lipgloss.Width(stripSGR(c)) != 1 {
		t.Errorf("the cursor is not one cell: %q", c)
	}
}
