package app

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// bareShellOS is the case the rail was worst at: n panes in one repo, none
// named, none running anything, so every one of them reports the same title.
func bareShellOS(t *testing.T, n int) *OS {
	t.Helper()
	m := newNarrowOS(t, 120, 40)
	m.CurrentWorkspace = 1
	m.SessionName = ""
	m.Windows = nil
	for i := range n {
		w := &terminal.Window{ID: "w" + strconv.Itoa(i), Width: 40, Height: 20, Workspace: 1}
		w.SetTitle("~/dev/tuios - fish")
		m.Windows = append(m.Windows, w)
	}
	m.FocusedWindow = 0
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	m.Settings = config.Global
	m.SidebarOrder = nil
	return m
}

// paneRows returns the rendered rows that carry a pane label, which is every
// row indented past its session and holding want.
func paneRows(rows []string, want string) []string {
	var out []string
	for _, r := range rows {
		if strings.Contains(r, want) {
			out = append(out, strings.TrimRight(r, " "))
		}
	}
	return out
}

// TestRailRowsDistinguishBareShells is the acceptance criterion, on a rendered
// frame: five shells in one directory used to draw five rows reading
// "~/dev/tuios - fish", which cannot answer which pane is which.
func TestRailRowsDistinguishBareShells(t *testing.T) {
	m := bareShellOS(t, 5)
	rows := paneRows(railText(t, m), "tuios")

	if len(rows) != 5 {
		t.Fatalf("expected 5 pane rows, got %d:\n%s", len(rows), strings.Join(rows, "\n"))
	}
	seen := make(map[string]bool, len(rows))
	for _, r := range rows {
		if seen[strings.TrimSpace(r)] {
			t.Fatalf("two pane rows read the same:\n%s", strings.Join(rows, "\n"))
		}
		seen[strings.TrimSpace(r)] = true
	}
	// The noise is gone with the ambiguity: the shell's name says nothing that
	// the pane beside it does not.
	for _, r := range rows {
		if strings.Contains(r, "fish") {
			t.Errorf("row still carries the shell name: %q", r)
		}
	}
}

// TestRailLabelRidesTheCache: the label is a render input, so a pane picking up
// or dropping a command has to rebuild the rail rather than show a stale row.
func TestRailLabelRidesTheCache(t *testing.T) {
	m := bareShellOS(t, 2)
	sidebarText(t, m)
	sig := m.sidebarCache.sig

	m.Windows[0].ForegroundCmd = "nvim"
	m.updateRailTitles()
	sidebarText(t, m)
	if m.sidebarCache.sig == sig {
		t.Fatal("a pane starting a command did not rebuild the rail")
	}
	if len(paneRows(railText(t, m), "nvim")) != 1 {
		t.Error("the command never reached the drawn row")
	}
}
