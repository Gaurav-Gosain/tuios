package tuie2e

import (
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestWorkspaceTabFollowsPaneTitle (issue #2090): with {title} in
// appearance.dock_workspace_tab_format, a workspace tab shows the title of
// its active pane and follows it, as tmux's automatic-rename does.
//
// How this could pass wrongly, written down first:
//   - The title could reach only a pane the client streams. The pane that
//     sets its title sits on workspace 2 while the client shows 1, so the
//     title has to come from the daemon's listing, and a frame has to be
//     drawn for it with nothing else on screen moving.
//   - The tab could keep the first title it saw. The title changes twice,
//     and the old one must leave the tab.
//   - The pane's title bar shows the same words, so only the dock's own
//     cells are read (dockCellOK).
//   - An empty title could leave a dangling separator or an empty tab. The
//     title is cleared, and the tab must read its number alone.
//
// Negative control: with workspaceTabTitle returning nothing, the first wait
// fails with no title on the tab. The run is in NEGATIVE_CONTROLS.md.
func TestWorkspaceTabFollowsPaneTitle(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\ndock_workspace_tab_format = \"{index} {title}\"\n")
	term := startHome(t, base, wsIconSession)
	if out, err := tuiosCLI(t, base, "new-window", "--session", wsIconSession, "--workspace", "2", "--no-focus"); err != nil {
		t.Fatalf("new-window on 2: %v\n%s", err, out)
	}
	waitPanesOn(t, term, base, wsIconSession, 2, 1, "the pane on workspace 2")
	var home, far string
	for _, r := range iconRows(t, base) {
		switch r.Workspace {
		case 1:
			home = r.ID
		case 2:
			far = r.ID
		}
	}
	if home == "" || far == "" {
		t.Fatalf("panes home=%q far=%q, want one on each workspace: %+v", home, far, iconRows(t, base))
	}

	// A title from a pane the client does not stream.
	sendIconPane(t, base, far, `printf '\033]2;farone\007'`)
	waitIconRow(t, term, base, far, "list-windows carries the title", func(r iconRow) bool { return r.Title == "farone" })
	waitIconDock(t, term, "ASSERTION: workspace 2's tab shows its pane's title", func(s tuitest.Screen) bool { return dockCellOK(s, "2 farone") })
	if ws := currentWorkspace(t, base, wsIconSession); ws != 1 {
		t.Fatalf("the session shows workspace %d, want 1: the title must come from a pane not in view", ws)
	}

	// The tab follows a new title.
	sendIconPane(t, base, far, `printf '\033]2;fartwo\007'`)
	waitIconDock(t, term, "ASSERTION: workspace 2's tab follows the new title", func(s tuitest.Screen) bool {
		return dockCellOK(s, "2 fartwo") && !dockCellOK(s, "farone")
	})

	// The workspace in view follows its focused pane too.
	sendIconPane(t, base, home, `printf '\033]2;homeone\007'`)
	waitIconDock(t, term, "ASSERTION: workspace 1's tab shows the focused pane's title", func(s tuitest.Screen) bool { return dockCellOK(s, "1 homeone") })
	saveFrame(t, term, "workspace-tab-title")

	// An empty title leaves the number alone on the tab.
	sendIconPane(t, base, far, `printf '\033]2;\007'`)
	waitIconDock(t, term, "ASSERTION: an empty title leaves the tab its number", func(s tuitest.Screen) bool {
		return !dockCellOK(s, "fartwo") && dockCellOK(s, "2")
	})
	saveFrame(t, term, "workspace-tab-title-cleared")
}
