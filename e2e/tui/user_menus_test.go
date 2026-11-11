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
// opens a submenu whose row runs on the pane the first menu was opened on. The
// same [menus.NAME] is the which-key panel a key opens, and a menu in the
// older [keybindings.menus.NAME] spelling opens as a right-click submenu.
//
// It checks: the added rows sit after Zoom and before Minimize; the removed
// row is gone; an action offered only on the desktop and an action whose when
// names a program the pane does not run are left out; the submenu opens under
// its title and its row runs, writing the pane's name whole; leader t u opens
// the same menu as a which-key panel whose s runs the row again; the older
// spelling's menu opens as a submenu with its keyed row.
//
// How it could pass wrongly: a menu that never read the config would still
// show the shipped rows, so each added row is looked for by its own label and
// its place. A submenu row that ran nothing would close the menu all the
// same, so the run is checked by the file it writes.
//
// Artifact: the pane menu and the submenu as text, and the file the row wrote.
//
// Negative controls: with appendUserRow not reading the action's targets, the
// desktop-only row shows on the pane menu. With foldMenus not run, leader t u
// opens nothing. With MenuRows not reading [keybindings.menus], the old
// spelling's row is left out of the pane menu.
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
add = ["user:keep-folder", "user:desk-only", "user:in-vim", { menu = "tools" }, { menu = "legacy" }]
after = "toggle_zoom"
remove = ["screenshot_window"]

[menus.tools]
title = "My tools"
items = [{ action = "user:save-name", key = "s" }, "---", "new_window"]

[keybindings.window_prefix]
"open_menu:tools" = ["u"]

[keybindings.menus.legacy]
title = "Old spelling"

[keybindings.menus.legacy.keys]
toggle_zoom = ["z"]
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

	// The same menu from a key: leader t u is its which-key panel, which
	// shows the keyed row under the action's label, and s runs it.
	if err := os.Remove(out); err != nil {
		t.Fatalf("remove %s: %v", out, err)
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "t", "u"); err != nil {
		t.Fatalf("send leader t u: %v", err)
	}
	waitMenu(t, term, "which-key", "my tools", "Save the pane name", "Cancel")
	fmt.Fprintf(&log, "== which-key\n%s\n", term.Screen().Text())
	if err := term.SendKeys("s"); err != nil {
		t.Fatalf("send s: %v", err)
	}
	if got := waitForFileText(t, out, func(s string) bool { return s != "" }, "the which-key row never ran its action"); got != name {
		t.Errorf("ASSERTION: the which-key row wrote %q, want the pane name %q whole", got, name)
	}

	// A menu in the older [keybindings.menus.NAME] spelling is a submenu of a
	// right-click menu too, under its title, with its rows.
	shiftRightClick(t, term, 20, 10)
	waitMenu(t, term, "pane again", "Old spelling >")
	s = term.Screen()
	oldX, oldY, _ := dockCellFind(s, "Old spelling >")
	leftClick(t, term, oldX+2, oldY)
	waitMenu(t, term, "old spelling submenu", "Old spelling", "Toggle zoom (fullscreen)")
	fmt.Fprintf(&log, "== old spelling\n%s\n", term.Screen().Text())
	if strings.Contains(term.Screen().Text(), "Keep this folder") {
		t.Errorf("ASSERTION: the old spelling did not open as a submenu:\n%s", term.Screen().Text())
	}
	alive(t, term, "after running a submenu row")
}
