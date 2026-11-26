package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// TestQuickLookFolderFrameBudget holds a folder in quick look to its budget.
// A listing holds up to 2000 rows (fileViewMaxEntries), read from a folder of
// any size. A frame where nothing changed makes no allocation, a move of the
// selection draws only the rows on screen, so a move in the full folder
// costs about what a move in a small one does. Both are timed in the same
// run, which holds on a slow or shared runner where a fixed bound in
// milliseconds does not. The selection must reach the panel: a move that left
// the frame as it was would be a walk the person cannot see.
func TestQuickLookFolderFrameBudget(t *testing.T) {
	m := keystrokeOS(t, 1, 200, 60)
	m.KeybindRegistry = config.NewKeybindRegistry(config.DefaultConfig())
	shipped := m.Settings
	ascii, nerd, panels := overlay.UseASCII(), overlay.UseNerdFonts(), overlay.FramePanels()
	t.Cleanup(func() {
		syncOverlaySettings(&shipped)
		overlay.SetASCII(ascii)
		overlay.SetNerdFonts(nerd)
		overlay.SetFramePanels(panels)
	})
	syncOverlaySettings(&m.Settings)

	folder := func(n int) []fileEntry {
		entries := make([]fileEntry, n)
		for i := range entries {
			name := fmt.Sprintf("file%05d.go", i)
			entries[i] = fileEntry{Name: name, Icon: fileIconFor(name, false)}
		}
		target := quickLookTarget{path: "/src", name: "src", dir: true}
		res := &quickLookResult{target: target, kind: "dir", folder: fileListing{Entries: entries}}
		m.quickLook = quickLookState{open: true, target: target, res: res, inside: true}
		return entries
	}
	render := func() string {
		content, _, _ := m.renderQuickLook()
		return content
	}

	// small is a folder a few screens tall: its moves scroll as the full
	// folder's do. A key held through it, down and back up, makes as many
	// moves as a walk of the full folder.
	const small = 200
	moves := fileViewMaxEntries - 2
	folder(small)
	start := time.Now()
	for i := range moves {
		m.QuickLookLine(i/(small-1)%2 == 0)
		_ = render()
	}
	base := time.Since(start) / time.Duration(moves)

	entries := folder(fileViewMaxEntries)
	before := render()
	if n := testing.AllocsPerRun(10, func() { _ = render() }); n > 0 {
		t.Errorf("a folder frame with nothing changed made %.0f allocations, want 0", n)
	}
	m.QuickLookLine(true)
	after := render()
	if after == before {
		t.Errorf("a move of the selection left the panel as it was")
	}
	if !strings.Contains(ansi.Strip(after), "file00001.go") {
		t.Errorf("the panel does not show the selected row file00001.go")
	}

	// A key held from the first row to the last: one move and one frame
	// each, as the loop does them.
	start = time.Now()
	for range moves {
		m.QuickLookLine(true)
		_ = render()
	}
	per := time.Since(start) / time.Duration(moves)
	if m.quickLook.dirSel != len(entries)-1 {
		t.Fatalf("the selection is on row %d after the walk, want the last row %d", m.quickLook.dirSel, len(entries)-1)
	}
	if !strings.Contains(ansi.Strip(render()), entries[len(entries)-1].Name) {
		t.Errorf("the panel does not show the last row after the walk")
	}
	t.Logf("one move and one frame: %v in a folder of %d rows, %v in one of %d", per, len(entries), base, small)
	if per > 3*base {
		t.Errorf("one move and one frame took %v in a folder of %d rows and %v in one of %d, want at most 3 times as long", per, len(entries), base, small)
	}
}
