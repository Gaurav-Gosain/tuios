package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// chipOS builds a client with one bordered pane at a known place, so the chip
// row and its hit rect can be computed by hand.
func chipPaneOS(t *testing.T) (*OS, *terminal.Window) {
	t.Helper()
	m := newNarrowOS(t, 120, 40)
	win := newTestWindow(t, "chip-pane-1", 40, 12)
	win.X, win.Y = 10, 5
	win.Workspace = 1
	m.Windows = []*terminal.Window{win}
	m.FocusedWindow = 0
	return m, win
}

// TestLinkChipRenderPaintsBottomRowAndRecordsRect covers the render half of
// the feature: the chip line lands on the pane's bottom content row, the hint
// names it as clickable, and the frame records the rect a click is matched
// against.
func TestLinkChipRenderPaintsBottomRowAndRecordsRect(t *testing.T) {
	m, win := chipPaneOS(t)
	m.noteLinkChip(win.ID, &session.LinkChip{Label: "build logs", Target: "other"})

	canvas := m.GetCanvas(true)
	frame := strings.Split(ansi.Strip(canvas.Render()), "\n")

	off := win.BorderOffset()
	wantY := win.Y + off + win.ContentHeight() - 1
	line := frame[wantY]
	if !strings.Contains(line, "build logs") || !strings.Contains(line, "click to jump") {
		t.Fatalf("row %d = %q, want the chip label and its click hint", wantY, line)
	}

	rect, ok := m.linkChipRects[win.ID]
	if !ok {
		t.Fatalf("no chip rect recorded after the frame")
	}
	if rect.Y != wantY || rect.X0 != win.X+off || rect.X1 != rect.X0+win.ContentWidth() {
		t.Fatalf("chip rect %+v, want the pane's bottom content row starting at column %d", rect, win.X+off)
	}
	if _, chip, found := m.LinkChipClick(win.X+3, wantY); !found || chip.Label != "build logs" {
		t.Fatalf("a click on the chip row found %v", chip)
	}
}

// TestLinkChipClearStopsBeingRenderedAndClickable: a cleared chip leaves the
// frame and the hit map in the same frame, so a click where it was falls
// through to whatever sits there now.
func TestLinkChipClearStopsBeingRenderedAndClickable(t *testing.T) {
	m, win := chipPaneOS(t)
	m.noteLinkChip(win.ID, &session.LinkChip{Label: "build logs", Target: "other"})
	m.GetCanvas(true)

	m.noteLinkChip(win.ID, nil)
	frame := strings.Split(ansi.Strip(m.GetCanvas(true).Render()), "\n")
	line := frame[win.Y+win.BorderOffset()+win.ContentHeight()-1]
	if strings.Contains(line, "build logs") || strings.Contains(line, "click to jump") {
		t.Fatalf("the cleared chip is still painted: %q", line)
	}
	if _, ok := m.linkChipRects[win.ID]; ok {
		t.Fatalf("the cleared chip is still clickable")
	}
}

// TestStateSyncAdoptsChips covers the sync half: a chip the daemon holds
// reaches the client's model on the window it names, on both adoption paths
// (an existing window updated in place, and a window created by the sync),
// and every way the chip can go away takes it off the model.
func TestStateSyncAdoptsChips(t *testing.T) {
	m, existing := chipPaneOS(t)

	state := func(chips map[string]*session.LinkChip, ids ...string) *session.SessionState {
		st := &session.SessionState{Name: "chip-test", Version: 1}
		for _, id := range ids {
			ws := session.WindowState{ID: id, PTYID: "pty-" + id}
			ws.LinkChip = chips[id]
			st.Windows = append(st.Windows, ws)
		}
		return st
	}

	// An existing window takes the chip through updateWindowFromState.
	chip := &session.LinkChip{Label: "build logs", Target: "chip-pane-2"}
	if err := m.ApplyStateSyncFrom(state(map[string]*session.LinkChip{existing.ID: chip}, existing.ID), ""); err != nil {
		t.Fatalf("apply the state that paints the chip: %v", err)
	}
	if got := m.LinkChipForTest(existing.ID); got != chip {
		t.Fatalf("the synced chip did not reach the model: %+v", got)
	}

	// A window the sync creates takes the chip through newWindowFromState.
	if err := m.ApplyStateSyncFrom(state(
		map[string]*session.LinkChip{"chip-pane-2": {Label: "other logs", Target: existing.ID}},
		existing.ID, "chip-pane-2"), ""); err != nil {
		t.Fatalf("apply the state that adds a pane: %v", err)
	}
	if w := m.windowIndexByID("chip-pane-2"); w < 0 {
		t.Fatalf("the synced pane was not created")
	}
	if got := m.LinkChipForTest("chip-pane-2"); got == nil || got.Label != "other logs" {
		t.Fatalf("the new pane's chip is %+v, want other logs", got)
	}

	// A later sync with the chip gone clears it.
	if err := m.ApplyStateSyncFrom(state(nil, existing.ID, "chip-pane-2"), ""); err != nil {
		t.Fatalf("apply the state that clears the chip: %v", err)
	}
	if got := m.LinkChipForTest(existing.ID); got != nil {
		t.Fatalf("the chip survived a sync without it: %+v", got)
	}

	// A pane the daemon dropped takes its chip with it.
	kept := m.Windows[0].ID
	if kept != existing.ID {
		t.Fatalf("the wrong pane survived: %s", kept)
	}
	if err := m.ApplyStateSyncFrom(state(nil, existing.ID), ""); err != nil {
		t.Fatalf("apply the state that closes a pane: %v", err)
	}
	if got := m.LinkChipForTest("chip-pane-2"); got != nil {
		t.Fatalf("the closed pane's chip is still on the model: %+v", got)
	}
}
