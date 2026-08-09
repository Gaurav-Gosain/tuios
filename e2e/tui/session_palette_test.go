package tuie2e

import (
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestPaletteFiltersToCurrentSessionDaemon opens the command palette on a
// daemon-attached client and types the session's own name. The palette must
// filter down to the dynamic "Session: <name>" entry the session tree builds,
// proving the palette's session/window rows are wired into the same filter as
// its static commands.
func TestPaletteFiltersToCurrentSessionDaemon(t *testing.T) {
	term := attachClient(t) // session named "e2e-ctrlp", window-management mode

	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("open palette: %v", err)
	}
	waitPaletteOpen(t, term, "before filtering")

	if err := term.SendKeys("e2e-ctrlp"); err != nil {
		t.Fatalf("type session name to filter: %v", err)
	}
	if err := term.WaitForText("Session: e2e-ctrlp", uiTimeout); err != nil {
		t.Fatalf("palette never filtered to the current session: %v\n%s", err, term.Snapshot())
	}

	closePalette(t, term, "after filtering to the session")
	alive(t, term, "after filtering the palette to the current session")
}

// TestPaletteShowsWindowEntryStandalone covers standalone mode (no daemon),
// where BuildSessionTree takes the synthetic-session path. It renames the
// window to a name unique enough to only match the dynamic window entry, opens
// the palette, filters to that name, and asserts the "Window: <name>" row
// appears. This is also the crash guard: selecting/filtering session entries
// must not panic when m.DaemonClient is nil.
func TestPaletteShowsWindowEntryStandalone(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)

	const winName = "paletteuxwindow"
	renameWindow(t, term, winName)

	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("open palette: %v", err)
	}
	waitPaletteOpen(t, term, "in standalone mode")

	// The palette lists 50-plus static commands before the dynamic session and
	// window entries, so the synthetic standalone session sits past the
	// visible rows until a query narrows the list down to it. "local" matches
	// only "Session: local": no static command name or category contains it.
	if err := term.SendKeys("local"); err != nil {
		t.Fatalf("type session name to filter: %v", err)
	}
	if err := term.WaitForText("Session: local", uiTimeout); err != nil {
		t.Fatalf("palette never showed the standalone session entry: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Ctrl('u')); err != nil {
		t.Fatalf("clear the query: %v", err)
	}
	if err := term.SendKeys(winName); err != nil {
		t.Fatalf("type window name to filter: %v", err)
	}
	if err := term.WaitForText("Window: "+winName, uiTimeout); err != nil {
		t.Fatalf("palette never filtered to the renamed window: %v\n%s", err, term.Snapshot())
	}

	closePalette(t, term, "after filtering to the window")
	alive(t, term, "after filtering the palette to a window in standalone mode")
}
