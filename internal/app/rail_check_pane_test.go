package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestRailCheckIgnoresPaneOutputTheFrameHasNotTakenIn is the deterministic
// regression for a false report the rail check made in CI on main
// (TestRailPaneSection): row 29, the rail pane's first line, blank in the
// cached rail and "l" in the check's rebuild.
//
// The pane's output reaches its emulator on the window's output writer
// goroutine. The UI goroutine learns of it when MarkTerminalsWithNewContent
// takes HasNewOutput and moves railPaneView.gen, which the key folds. Output
// that lands after a frame's key and before that pass is in the emulator and
// not in the key. The cached render of the pane hides it, except when that
// render was blank: a blank render is never cached (see cacheRender), so the
// pane stays content dirty and the check's rebuild reads the emulator again.
// The test program clears its screen every half second, so a typed "l" echoed
// right after a clear is that case. It is not a key missing an input: the
// pass the write asks for moves gen, and that frame draws it.
//
// Ways this test could pass wrongly, written first:
//   - The cache could miss on the frame the check runs, so no check runs. The
//     test asserts the key is the same before and after the write.
//   - The rebuild could read the pane's cached render, so there is nothing to
//     differ and the guard is never needed. The test asserts the blank render
//     left the pane content dirty, and the negative control below shows the
//     rebuild differs.
//   - The guard could stop the check comparing the rail at all. Once the
//     output is taken in without moving gen, which is a real miss, the check
//     must report it.
//
// Negative control, confirmed red: with railCacheMiss not comparing the pane's
// writes, the first assertion reports row 33, the pane's first line, blank in
// the cached rail and "l" in the build: the CI shape.
func TestRailCheckIgnoresPaneOutputTheFrameHasNotTakenIn(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	m.Settings.SidebarSections = "sessions,terminals,pane"
	m.UserConfig.Appearance.Sidebar.Pane = config.SidebarPaneConfig{Title: "railtv", Height: 4, Command: "railpane"}
	rail := newTestWindow(t, "railpane0001", 29, 6)
	rail.Workspace = session.ScratchWorkspaceBase
	rail.Rail = m.railPaneConfig().CommandLine()
	m.Windows = append(m.Windows, rail)
	m.railPaneView.key = rail.Rail
	m.railPaneView.windowID = rail.ID

	// out writes to the pane as the daemon's stream does, and waits until the
	// writer has applied it.
	out := func(s string) {
		t.Helper()
		rail.WriteOutputAsync([]byte(s))
		rail.DrainPendingOutput()
	}
	// pass is the UI goroutine taking the pane's output in, as a frame does.
	pass := func() {
		t.Helper()
		m.MarkTerminalsWithNewContent()
		m.railPaneApply()
	}
	paneRow := func(lines []string) int {
		t.Helper()
		for i, l := range lines {
			if strings.Contains(l, "railtv") {
				return i + 1
			}
		}
		t.Fatalf("the rail draws no pane section:\n%s", strings.Join(lines, "\n"))
		return -1
	}

	railLines(t, m) // lays the pane out
	pass()
	out("SZ 4 27\r\nTICK-0")
	pass()
	lines := railLines(t, m)
	row := paneRow(lines)
	if !strings.Contains(lines[row], "SZ 4 27") {
		t.Fatalf("setup: the pane's first line is %q, want the program's output", lines[row])
	}

	// The program clears its screen and a frame draws the blank.
	out("\x1b[H\x1b[2J")
	pass()
	railLines(t, m)
	if !rail.ContentDirty {
		t.Fatal("setup: the blank render of the pane was cached, so no rebuild would read the emulator again")
	}
	sig := m.sidebarSignature()

	// The echo of a typed "l" lands before the next pass.
	out("l")
	if m.sidebarSignature() != sig {
		t.Fatal("setup: the key moved on a write no pass has taken in, so the cache would not be served")
	}
	ext := m.railExternalGens()
	if diff := m.railCacheMiss(ext); diff != "" {
		t.Fatalf("the check reported output no pass has taken in as a key miss:\n%s", diff)
	}

	// A pass that takes the output in without moving gen is a key that misses
	// an input, and the check still reports it.
	_, m.railPaneView.noted = rail.EmulatorWrites()
	rail.HasNewOutput.Store(false)
	if diff := m.railCacheMiss(ext); diff == "" {
		t.Fatal("the check found no difference after the output was taken in without moving the key")
	}

	// And the pass the write asks for draws it.
	rail.HasNewOutput.Store(true)
	pass()
	lines = railLines(t, m)
	if got := lines[paneRow(lines)]; !strings.HasPrefix(strings.TrimLeft(ansi.Strip(got), " "), "l") {
		t.Fatalf("the frame after the pass draws the pane's first line as %q, want the l", got)
	}
}
