package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestMenusFromConfigAddRemoveAndOpenASubmenu: a [menus.pane] table changes
// the shipped pane menu without copying it, a user action shows only on the
// targets it names and while its when matches, and a { menu = "NAME" } row
// opens a submenu whose row runs on the pane the first menu was opened on.
//
// It checks: the added rows sit after Zoom and before Minimize; the removed
// row is gone; an action offered only on the desktop and an action whose when
// names a program the pane does not run are left out; the submenu opens under
// its title and its row runs, writing the pane's name whole.
//
// How it could pass wrongly: a menu that never read the config would still
// show the shipped rows, so each added row is looked for by its own label and
// its place. A submenu row that ran nothing would close the menu all the
// same, so the run is checked by the file it writes.
//
// Artifact: the pane menu and the submenu as text, and the file the row wrote.
//
// Negative control: with userActionRow not reading the action's targets, the
// desktop-only row shows on the pane menu and the check fails.
func TestMenusFromConfigAddRemoveAndOpenASubmenu(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	art := artifactDir(t)
	out := filepath.Join(base, "name.out")
	cfg := `[[actions]]
name = "save-name"
label = "Save the pane name"
run = ["/bin/sh", "-c", "printf '%s' \"$1\" > ` + out + `", "save-name", "{pane.title}"]
in = "silent"

[[actions]]
name = "keep-folder"
label = "Keep this folder"
action = "toggle_zoom"

[[actions]]
name = "desk-only"
label = "Desktop only row"
targets = ["desktop"]
action = "new_window"

[[actions]]
name = "in-vim"
label = "Only inside vim"
when = { program = ["vim", "nvim"] }
action = "toggle_zoom"

[menus.pane]
add = ["user:keep-folder", "user:desk-only", "user:in-vim", { menu = "tools" }]
after = "toggle_zoom"
remove = ["screenshot_window"]

[menus.tools]
title = "My tools"
items = ["user:save-name", "---", "new_window"]
`
	writeConfig(t, base, cfg)
	var log strings.Builder
	defer func() { _ = os.WriteFile(filepath.Join(art, "menus.txt"), []byte(log.String()), 0o644) }()

	mustCLI(t, base, "new", "S", "--detach")
	waitForSessionInfo(t, base, "S")
	term := attachIn(t, base, "S", startOpts{cols: 120, rows: 36})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("client never started: %v\n%s", err, term.Snapshot())
	}
	name := `it's "$(nothing)" here`
	mustCLI(t, base, "set-window", "-s", "S", "-w", "0", "--name", name)
	windowManagementMode(t, term)
	enableTiling(t, term)

	shiftRightClick(t, term, 20, 10)
	waitMenu(t, term, "pane", "Keep this folder", "My tools >", "Minimize")
	s := term.Screen()
	fmt.Fprintf(&log, "== pane menu\n%s\n", s.Text())
	for _, gone := range []string{"Screenshot this window", "Desktop only row", "Only inside vim"} {
		if strings.Contains(s.Text(), gone) {
			t.Errorf("ASSERTION: the pane menu shows %q:\n%s", gone, s.Text())
		}
	}
	_, zoomY, _ := dockCellFind(s, "Zoom")
	_, keepY, _ := dockCellFind(s, "Keep this folder")
	toolsX, toolsY, _ := dockCellFind(s, "My tools >")
	_, minY, _ := dockCellFind(s, "Minimize")
	if !(zoomY < keepY && keepY < toolsY && toolsY < minY) {
		t.Errorf("ASSERTION: the added rows are not between Zoom and Minimize: zoom %d, keep %d, tools %d, minimize %d", zoomY, keepY, toolsY, minY)
	}

	// The submenu row opens the submenu in the menu's place.
	leftClick(t, term, toolsX+2, toolsY)
	waitMenu(t, term, "submenu", "My tools", "Save the pane name", "New window")
	s = term.Screen()
	fmt.Fprintf(&log, "== submenu\n%s\n", s.Text())
	if strings.Contains(s.Text(), "Keep this folder") {
		t.Errorf("ASSERTION: the pane menu is still up under the submenu:\n%s", s.Text())
	}

	// Its row runs on the pane the first menu was opened on.
	saveX, saveY, _ := dockCellFind(s, "Save the pane name")
	leftClick(t, term, saveX+2, saveY)
	got := waitForFileText(t, out, func(s string) bool { return s != "" }, "the submenu row never ran its action")
	fmt.Fprintf(&log, "== %s\n%s\n", out, got)
	if got != name {
		t.Errorf("ASSERTION: the submenu row wrote %q, want the pane name %q whole", got, name)
	}
	alive(t, term, "after running a submenu row")
}
