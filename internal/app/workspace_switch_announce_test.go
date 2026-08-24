package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// A workspace switch moves no pane between screens: every pane keeps the tile it
// already had. So it must send zero SIGWINCH. A guest told its size again
// repaints its prompt, and the paint it drew before the switch is left stranded
// above the new one - the stacked prompts and the blank gap between them that a
// switch used to leave behind.

// newSwitchOS builds a client with panes spread over two workspaces, each pane
// carrying a recorder for the sizes its PTY is told.
func newSwitchOS(t *testing.T, width, height int, perWorkspace map[int]int) (*OS, map[string]*toldSize) {
	t.Helper()
	m := &OS{
		// The layout reads the model's session-settled geometry, seeded from
		// the globals the way NewOS seeds it.
		SharedBorders:        config.SharedBorders,
		PaneGap:              config.PaneGap,
		NumWorkspaces:        9,
		CurrentWorkspace:     1,
		WorkspaceFocus:       make(map[int]int),
		WorkspaceLayouts:     make(map[int][]WindowLayout),
		WorkspaceHasCustom:   map[int]bool{},
		WorkspaceMasterRatio: map[int]float64{},
		Width:                width,
		Height:               height,
		AutoTiling:           true,
		UseBSPLayout:         true,
		PendingResizes:       make(map[string][2]int),
	}
	told := make(map[string]*toldSize)
	for ws := 1; ws <= 2; ws++ {
		for i := range perWorkspace[ws] {
			id := fmt.Sprintf("ws%d-pane-%d", ws, i+1)
			win, rec := newAnnounceWindow(t, id, 60, 20)
			win.Workspace = ws
			told[id] = rec
			m.Windows = append(m.Windows, win)
		}
	}
	m.FocusedWindow = 0
	return m, told
}

// drawableSizes records the size every pane can draw in, keyed by pane ID.
func drawableSizes(m *OS) map[string][2]int {
	sizes := make(map[string][2]int, len(m.Windows))
	for _, w := range m.Windows {
		sizes[w.ID] = [2]int{w.ContentWidth(), w.ContentHeight()}
	}
	return sizes
}

// callCounts snapshots how many times each pane's PTY has been told a size.
func callCounts(told map[string]*toldSize) map[string]int {
	counts := make(map[string]int, len(told))
	for id, rec := range told {
		counts[id] = rec.calls
	}
	return counts
}

// checkNoSpuriousWinch asserts that every pane whose drawable size survived the
// transition was left alone, and that the guest's own view (the emulator grid)
// still matches what the pane can draw.
func checkNoSpuriousWinch(t *testing.T, m *OS, told map[string]*toldSize, before map[string][2]int, counts map[string]int, label string) {
	t.Helper()
	after := drawableSizes(m)
	for _, w := range m.Windows {
		fired := told[w.ID].calls - counts[w.ID]
		if before[w.ID] == after[w.ID] && fired != 0 {
			t.Errorf("%s: %s kept its drawable %dx%d yet its PTY was told a size %d time(s)",
				label, w.ID, after[w.ID][0], after[w.ID][1], fired)
		}
		ew, eh := w.Terminal.Width(), w.Terminal.Height()
		if ew != after[w.ID][0] || eh != after[w.ID][1] {
			t.Errorf("%s: %s emulator %dx%d, drawable %dx%d",
				label, w.ID, ew, eh, after[w.ID][0], after[w.ID][1])
		}
	}
}

func TestWorkspaceSwitchSendsNoSpuriousWinch(t *testing.T) {
	prevAnim := config.AnimationsEnabled
	prevShared := config.SharedBorders
	prevSidebarEnabled := config.SidebarEnabled
	prevSidebarPos := config.SidebarPosition
	config.AnimationsEnabled = false
	t.Cleanup(func() {
		config.AnimationsEnabled = prevAnim
		config.SharedBorders = prevShared
		config.SidebarEnabled = prevSidebarEnabled
		config.SidebarPosition = prevSidebarPos
	})

	for _, shared := range []bool{false, true} {
		for _, sidebar := range []string{"off", "left", "right"} {
			for _, custom := range []bool{false, true} {
				name := fmt.Sprintf("shared=%v/sidebar=%s/custom=%v", shared, sidebar, custom)
				t.Run(name, func(t *testing.T) {
					config.SharedBorders = shared
					config.SidebarEnabled = sidebar != "off"
					if sidebar != "off" {
						config.SidebarPosition = sidebar
					}

					m, told := newSwitchOS(t, 200, 50, map[int]int{1: 2, 2: 2})
					// Settle both workspaces so every pane already holds the tile
					// the switch will hand it back.
					m.TileAllWindows()
					m.SwitchToWorkspace(2)
					m.TileAllWindows()
					m.SwitchToWorkspace(1)

					if custom {
						// A user resize on each workspace: the switch keeps these
						// rectangles instead of retiling, so it must announce
						// nothing either.
						m.MarkLayoutCustom()
						m.SwitchToWorkspace(2)
						m.MarkLayoutCustom()
						m.SwitchToWorkspace(1)
					}

					for _, dir := range []int{2, 1, 2, 1} {
						before := drawableSizes(m)
						counts := callCounts(told)
						m.SwitchToWorkspace(dir)
						checkNoSpuriousWinch(t, m, told, before, counts,
							fmt.Sprintf("%s/switch-to-%d", name, dir))
					}
				})
			}
		}
	}
}

// TestWorkspaceSwitchKeepsGuestScreen drives the guest's own view: a pane that
// printed a banner and a prompt must still show exactly one of each after a
// round trip through another workspace.
func TestWorkspaceSwitchKeepsGuestScreen(t *testing.T) {
	prevAnim := config.AnimationsEnabled
	prevShared := config.SharedBorders
	config.AnimationsEnabled = false
	config.SharedBorders = true
	t.Cleanup(func() {
		config.AnimationsEnabled = prevAnim
		config.SharedBorders = prevShared
	})

	m, _ := newSwitchOS(t, 200, 50, map[int]int{1: 1, 2: 1})
	m.TileAllWindows()

	win := m.Windows[0]
	// The guest paints; a resize would reflow this and a SIGWINCH would make a
	// real shell paint it again.
	win.LockIO()
	_, _ = win.Terminal.Write([]byte("Welcome to fish, the friendly interactive shell\r\n> "))
	win.UnlockIO()
	beforeW, beforeH := win.Terminal.Width(), win.Terminal.Height()
	before := screenText(win)

	m.SwitchToWorkspace(2)
	m.SwitchToWorkspace(1)

	if w, h := win.Terminal.Width(), win.Terminal.Height(); w != beforeW || h != beforeH {
		t.Errorf("guest grid moved %dx%d → %dx%d across a workspace round trip", beforeW, beforeH, w, h)
	}
	if got := screenText(win); got != before {
		t.Errorf("guest screen changed across a workspace round trip:\nbefore:\n%s\nafter:\n%s", before, got)
	}
}

// screenText reads the guest's visible grid as text.
func screenText(w *terminal.Window) string {
	w.RLockIO()
	defer w.RUnlockIO()
	out := ""
	for y := range w.Terminal.Height() {
		for x := range w.Terminal.Width() {
			cell := w.Terminal.CellAt(x, y)
			if cell == nil || cell.String() == "" {
				out += " "
				continue
			}
			out += cell.String()
		}
		out += "\n"
	}
	return out
}
