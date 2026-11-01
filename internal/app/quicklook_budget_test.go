package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/diffview"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// TestQuickLookFrameAllocationBudget holds a quick look frame to its budget.
// The panel is composed on every frame while it is open, frames that pane
// output causes too, and drawing it again from nothing made about 3,800
// allocations a frame on a large Go file. A frame where nothing it shows
// changed makes none. A scroll, a border style and a frame colour, each
// changed alone, must still reach the panel.
func TestQuickLookFrameAllocationBudget(t *testing.T) {
	m := keystrokeOS(t, 1, 200, 60)
	m.KeybindRegistry = config.NewKeybindRegistry(config.DefaultConfig())

	// The panel reads overlay globals a frame sets from the settings. Set
	// them as a frame does, and put them back after. The frame glyphs have
	// no getter: the shipped settings' frame is the rounded one.
	shipped := m.Settings
	ascii, nerd, panels := overlay.UseASCII(), overlay.UseNerdFonts(), overlay.FramePanels()
	t.Cleanup(func() {
		syncOverlaySettings(&shipped)
		overlay.SetASCII(ascii)
		overlay.SetNerdFonts(nerd)
		overlay.SetFramePanels(panels)
	})
	m.Settings.BorderStyle = "rounded"
	syncOverlaySettings(&m.Settings)

	lines := make([]string, 2000)
	size := 0
	for i := range lines {
		lines[i] = fmt.Sprintf("var v%04d = %q // line %d", i+1, "row", i+1)
		size += len(lines[i]) + 1
	}
	target := quickLookTarget{path: "/src/main.go", name: "main.go"}
	m.quickLook = quickLookState{open: true, target: target, res: &quickLookResult{
		target: target, kind: "text", size: int64(size), lines: lines, spans: diffview.Highlight("main.go", lines),
	}}
	render := func() string {
		content, _, _ := m.renderQuickLook()
		return content
	}
	// firstCodeRow is the body row under the line that says what the file
	// is: the dialog's top frame, that line, then the code.
	firstCodeRow := func(content string) string {
		rows := strings.Split(ansi.Strip(content), "\n")
		if len(rows) < 3 {
			t.Fatalf("the panel has %d rows:\n%s", len(rows), ansi.Strip(content))
		}
		return rows[2]
	}

	before := render()
	if !strings.Contains(before, "╭") {
		t.Fatalf("the panel is not framed in the rounded border:\n%s", ansi.Strip(before))
	}
	if n := testing.AllocsPerRun(10, func() { _ = render() }); n > 0 {
		t.Errorf("a quick look frame with nothing changed made %.0f allocations, want 0", n)
	}

	m.quickLook.scroll = 1
	after := render()
	if after == before {
		t.Errorf("a scroll of one line left the panel as it was")
	}
	if row := firstCodeRow(after); !strings.Contains(row, "v0002") {
		t.Errorf("after a scroll of one line the first code row is %q, want line 2", row)
	}

	m.Settings.BorderStyle = "double"
	// A frame sets the frame glyphs from the border style before it
	// composes the overlays.
	syncOverlaySettings(&m.Settings)
	after = render()
	if !strings.Contains(after, "╔") || strings.Contains(after, "╭") {
		t.Errorf("after the border style changed to double the panel is not framed in it:\n%s", ansi.Strip(after))
	}

	before = after
	m.Settings.OverlayBorderColor = "#d75f00"
	syncOverlaySettings(&m.Settings)
	after = render()
	if after == before {
		t.Errorf("a new frame colour left the panel as it was")
	}
	if top, _, _ := strings.Cut(after, "\n"); !strings.Contains(top, "38;2;215;95;0") {
		t.Errorf("the panel's frame is not drawn in the frame colour #d75f00: %q", top)
	}
}
