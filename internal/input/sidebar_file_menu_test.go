package input

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The files section's context menu, driven end to end: the real mouse handler
// opens it, the real dispatcher runs the row that was clicked. A menu row
// carries an action ID and nothing else, so the only proof that clicking it does
// anything is to click it.

// runCmd drains a command the way the event loop does, so an operation that
// runs off the update goroutine has finished before the assertion.
func runCmd(o *app.OS, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if msg := cmd(); msg != nil {
		o.Update(msg)
	}
}

// fileRowCell is the screen cell of the listing row named name, read off the
// rectangles the rail published as it drew. Nothing here works out where a row
// is; it reads where the row was drawn.
func fileRowCell(t *testing.T, o *app.OS, name string) (int, int) {
	t.Helper()
	for _, h := range o.SidebarHits {
		if h.WindowID == name {
			return h.X0 + 2, h.Y0
		}
	}
	t.Fatalf("the rail published no row for %q", name)
	return 0, 0
}

// rightClickRow opens the context menu at a listing row through the real click
// handler, and returns the model it produced.
func rightClickRow(t *testing.T, o *app.OS, name string) *app.OS {
	t.Helper()
	x, y := fileRowCell(t, o, name)
	o, _ = handleMouseClick(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight}, o)
	if !o.ContextMenuActive() {
		t.Fatalf("a right press on %q opened no menu", name)
	}
	// The menu publishes its rectangle as it draws, and a click on a row is
	// hit-tested against that rectangle. Nothing here works out where the rows
	// landed.
	o.View()
	return o
}

// takeMenuRow clicks the row with the given label, through the real handler.
func takeMenuRow(t *testing.T, o *app.OS, label string) (*app.OS, tea.Cmd) {
	t.Helper()
	cm := o.ContextMenu
	for i, it := range cm.Items {
		if it.Label != label {
			continue
		}
		if it.Dim {
			t.Fatalf("the %q row is dimmed, so it cannot be clicked", label)
		}
		cm.Selected = i
		y := cm.FirstRowY + (i-cm.ScrollFrom)*cm.ItemH
		x := cm.BoundsX + cm.BoundsW/2
		return handleMouseClick(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}, o)
	}
	t.Fatalf("the menu has no %q row", label)
	return o, nil
}

// TestFileMenuRowsAreRegistered is the guard that makes a files-menu row more
// than a string: every action it names has to be in the dispatcher and in the
// description registry, or clicking it would silently do nothing.
//
// Negative control, confirmed red: drop the file actions from
// registerHandlers, and every row of every file menu is reported unregistered.
func TestFileMenuRowsAreRegistered(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := filesRailOS(t, dir, "")
	dispatcher := GetDispatcher()

	seen := 0
	for _, row := range []string{"src", "notes.txt", ".."} {
		o = rightClickRow(t, o, row)
		for _, it := range o.ContextMenu.Items {
			if it.Sep || it.Action == "" {
				continue
			}
			seen++
			if !dispatcher.HasAction(it.Action) {
				t.Errorf("row %q on %q names action %q, which the dispatcher does not have",
					it.Label, row, it.Action)
			}
			if _, ok := config.ActionDescriptions[it.Action]; !ok {
				t.Errorf("row %q on %q names action %q, which has no description",
					it.Label, row, it.Action)
			}
		}
		o.CloseContextMenu()
	}
	if seen == 0 {
		t.Fatal("no menu rows were checked")
	}
}

// TestMenuDeleteGoesThroughTheDialog is the confirmation gate on the whole path:
// right-click a file, click Delete, and what happens is a dialog on Cancel and a
// file still on disk.
//
// Negative control, confirmed red: make the dispatcher's file_delete handler
// open the confirmation and answer it on the spot instead of leaving it up. No
// dialog is on screen and the file is gone, and this fails on the first of
// those: "the menu's Delete row did not raise the confirmation".
func TestMenuDeleteGoesThroughTheDialog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.txt")
	if err := os.WriteFile(path, []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := filesRailOS(t, dir, "")

	o = rightClickRow(t, o, "report.txt")
	o, cmd := takeMenuRow(t, o, "Delete")
	runCmd(o, cmd)

	if !o.FileConfirmOpen() {
		t.Fatal("the menu's Delete row did not raise the confirmation")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the file went without an answer: %v", err)
	}

	// Enter answers the dialog on the row it opened on, which is Cancel.
	o, _ = HandleKeyPress(pressEnter(), o)
	if o.FileConfirmOpen() {
		t.Error("enter left the confirmation up")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the dialog destroyed the file on its opening row: %v", err)
	}
}

// TestMenuDeleteDestroysTheRowThatWasClicked is the other half of the same
// path: once the destructive row is chosen, what goes is the file the pointer
// was on, and not the one the keyboard cursor was parked on.
func TestMenuDeleteDestroysTheRowThatWasClicked(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "keep.txt")
	drop := filepath.Join(dir, "drop.txt")
	for _, p := range []string{keep, drop} {
		if err := os.WriteFile(p, []byte("body"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	o := filesRailOS(t, dir, "keep.txt") // the cursor is here

	o = rightClickRow(t, o, "drop.txt") // the pointer is here
	o, cmd := takeMenuRow(t, o, "Delete for good")
	runCmd(o, cmd)
	if !o.FileConfirmOpen() {
		t.Fatal("the permanent delete raised no confirmation")
	}

	// Move onto the destructive row and take it.
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyDown}, o)
	o, cmd = HandleKeyPress(pressEnter(), o)
	runCmd(o, cmd)

	if _, err := os.Stat(drop); err == nil {
		t.Error("the file the pointer was on is still there")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("the delete took the cursor's file instead: %v", err)
	}
}

// TestTheRailMenuKeyReachesAFileRow is the keyboard half of the feature, driven
// through the real key handler.
//
// The rail's menu key is "m". The files section's own keymap is consulted first
// on a listing row and does not claim that letter, so the rail's binding
// answers there and the row under the cursor decides the menu. Before this
// branch it refused with "Nothing on this row to act on".
//
// Negative control, confirmed red: drop the file kinds from sidebarRowHasMenu
// and the key opens no menu at all.
func TestTheRailMenuKeyReachesAFileRow(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := filesRailOS(t, dir, "notes.txt")

	o, _ = HandleKeyPress(press("m"), o)
	if !o.ContextMenuActive() {
		t.Fatal("m on a listing row opened no menu")
	}
	o.View()

	// It is the same menu the right-click opens, and it is fully operable from
	// here: enter on the row it lands on runs that row.
	labels := map[string]bool{}
	for _, it := range o.ContextMenu.Items {
		labels[it.Label] = true
	}
	for _, want := range []string{"Copy path", "Copy", "Cut", "Rename", "Delete"} {
		if !labels[want] {
			t.Errorf("the key's menu has no %q row", want)
		}
	}

	// Walk to Rename with the arrow keys and take it, all from the keyboard.
	for range len(o.ContextMenu.Items) {
		if o.ContextMenu.Items[o.ContextMenu.Selected].Label == "Rename" {
			break
		}
		o, _ = HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyDown}, o)
	}
	o, _ = HandleKeyPress(pressEnter(), o)
	if o.ContextMenuActive() {
		t.Error("enter left the menu up")
	}
	if !o.FilePromptOpen() {
		t.Fatal("the Rename row opened no prompt")
	}
}

// TestMenuCreateOpensThePrompt checks the row that needs no target, from a cell
// of the files section that names no row at all.
//
// The cell is the section's header, one row above the ".." row. It publishes no
// hit rectangle of its own, so before this branch a right press there fell to
// the rail's settings menu, which is not what a user aiming at a folder listing
// is asking for.
func TestMenuCreateOpensThePrompt(t *testing.T) {
	dir := t.TempDir()
	o := filesRailOS(t, dir, "")

	x, y := fileRowCell(t, o, "..")
	o, _ = handleMouseClick(tea.MouseClickMsg{X: x, Y: y - 1, Button: tea.MouseRight}, o)
	if !o.ContextMenuActive() {
		t.Fatal("a right press on the files header opened no menu")
	}
	o.View()
	o, _ = takeMenuRow(t, o, "New file or folder")
	if !o.FilePromptOpen() {
		t.Fatal("the New row opened no prompt")
	}
}
