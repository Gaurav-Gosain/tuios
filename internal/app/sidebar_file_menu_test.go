package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/charmbracelet/x/ansi"
)

// The files section's context menu, driven through the real right-click handler
// and read off the drawn frame.
//
// The claims here are the four the feature makes: the menu is about the row the
// pointer was on and not the row the cursor was on, the rows it offers change
// with what was clicked, its hints come from the live keybind registry, and the
// delete it offers is the same delete the key offers, dialog and all.

// fileRowCell is the screen cell of the listing row for name, read off the
// rectangles the render published. "" asks for the ".." row.
func fileRowCell(t *testing.T, m *OS, name string) (int, int) {
	t.Helper()
	want := name
	if want == "" {
		want = ".."
	}
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowFileEntry || h.Kind == sidebarRowFileUp {
			if h.WindowID == want {
				return h.X0 + 2, h.Y0
			}
		}
	}
	t.Fatalf("the rail published no listing row for %q", want)
	return 0, 0
}

// filesBlankCell is a cell in the files section's band that no row covers,
// which is the "empty space below the listing" the menu has to answer on.
func filesBlankCell(t *testing.T, m *OS) (int, int) {
	t.Helper()
	band := m.sidebarSectionY[sidebarSectionFiles]
	for y := band[1] - 1; y >= band[0]; y-- {
		if _, ok := m.sidebarRowAt(2, y); !ok {
			return 2, y
		}
	}
	t.Fatalf("every row of the files band (%v) is covered by a hit", band)
	return 0, 0
}

// menuLabels is the menu's rows as "label" or "label (dim)".
func menuLabels(m *OS) []string {
	var out []string
	for _, it := range m.ContextMenu.Items {
		if it.Sep {
			continue
		}
		if it.Dim {
			out = append(out, it.Label+" (dim)")
			continue
		}
		out = append(out, it.Label)
	}
	return out
}

// menuRow finds a row by label.
func menuRow(t *testing.T, m *OS, label string) ContextMenuItem {
	t.Helper()
	for _, it := range m.ContextMenu.Items {
		if it.Label == label {
			return it
		}
	}
	t.Fatalf("the menu has no %q row; it has %v", label, menuLabels(m))
	return ContextMenuItem{}
}

// rightClickFile opens the menu on a listing row through the real handler.
func rightClickFile(t *testing.T, m *OS, name string) {
	t.Helper()
	x, y := fileRowCell(t, m, name)
	if !m.SidebarClick(x, y, true) {
		t.Fatalf("the rail refused a right press at (%d,%d)", x, y)
	}
	if m.ContextMenu == nil {
		t.Fatalf("a right press on %q opened no menu", name)
	}
}

// TestFileRowMenuOffersWhatTheRowCanDo is the shape claim: a folder, a file, the
// ".." row and the blank space below the listing each get the rows that make
// sense for them, and the ones that do not are dimmed rather than dropped.
//
// Negative control, confirmed red: make fileRowMenu ignore its target and treat
// every menu as an entry row (hasTarget := on), and the ".." and blank-space
// cases fail with Rename and Delete live on a row that names no file.
func TestFileRowMenuOffersWhatTheRowCanDo(t *testing.T) {
	dir := fileViewTree(t)
	m := filesOS(t, dir, "")

	for _, tc := range []struct {
		name string
		open func()
		want []string
	}{
		{
			name: "a file row",
			open: func() { rightClickFile(t, m, "README.md") },
			want: []string{
				"Copy path", "Copy", "Cut", "Paste (dim)", "New file or folder",
				"Rename", "Delete", "Delete for good", "Sidebar settings",
			},
		},
		{
			name: "a folder row",
			open: func() { rightClickFile(t, m, "apple") },
			want: []string{
				"Open folder", "Copy", "Cut", "Paste (dim)", "New file or folder",
				"Rename", "Delete", "Delete for good", "Sidebar settings",
			},
		},
		{
			name: "the up row",
			open: func() { rightClickFile(t, m, "") },
			want: []string{
				"Go up", "Copy (dim)", "Cut (dim)", "Paste (dim)", "New file or folder",
				"Rename (dim)", "Delete (dim)", "Delete for good (dim)", "Sidebar settings",
			},
		},
		{
			name: "the blank space below the listing",
			open: func() {
				x, y := filesBlankCell(t, m)
				if !m.SidebarClick(x, y, true) {
					t.Fatalf("the rail refused a right press at (%d,%d)", x, y)
				}
			},
			want: []string{
				"Open (dim)", "Copy (dim)", "Cut (dim)", "Paste (dim)", "New file or folder",
				"Rename (dim)", "Delete (dim)", "Delete for good (dim)", "Sidebar settings",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m.CloseContextMenu()
			tc.open()
			if got := menuLabels(m); strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("menu rows are\n  %v\nwant\n  %v", got, tc.want)
			}
			// The menu opens on a row that can run, never on a dimmed one.
			cm := m.ContextMenu
			if !cm.selectable(cm.Selected) {
				t.Errorf("the menu opened on row %d, which cannot be run", cm.Selected)
			}
		})
	}
}

// TestFileMenuNamesTheRowThePointerWasOn is requirement one: the row under the
// pointer is the target, and right-clicking it does not first have to move the
// keyboard cursor there.
//
// Negative control, confirmed red: drop the menu carry from fileActionTarget
// (return the cursor row unconditionally), and this fails naming "beta.txt",
// the row the cursor was parked on.
func TestFileMenuNamesTheRowThePointerWasOn(t *testing.T) {
	dir := fileViewTree(t)
	m := filesOS(t, dir, "beta.txt") // the cursor sits here
	if got := m.FileActionTargetNameForTest(); got != "beta.txt" {
		t.Fatalf("the cursor is on %q, want beta.txt", got)
	}

	rightClickFile(t, m, "README.md") // the pointer is here
	if got := m.FileActionTargetNameForTest(); got != "beta.txt" {
		t.Errorf("opening the menu moved the cursor to %q; a right-click must not", got)
	}

	// Taking the Rename row is what hands the menu's row to the action.
	for i, it := range m.ContextMenu.Items {
		if it.Label == "Rename" {
			m.ContextMenu.Selected = i
		}
	}
	if act := m.ContextMenuSelectedAction(); act != "file_rename" {
		t.Fatalf("the Rename row names %q", act)
	}
	m.CloseContextMenu()
	if !m.SidebarFileRename() {
		t.Fatal("the rename refused the row the menu named")
	}
	if got := m.filePrompt.Target; got != "README.md" {
		t.Errorf("the rename opened on %q, want the clicked README.md", got)
	}
	m.ClearMenuTarget()
	// And the carry is spent: the same action by key is back on the cursor row.
	m.FilePromptCancel()
	if !m.SidebarFileRename() {
		t.Fatal("the rename refused the cursor row")
	}
	if got := m.filePrompt.Target; got != "beta.txt" {
		t.Errorf("after the menu, the key renamed %q, want the cursor's beta.txt", got)
	}
}

// TestFileMenuHintsFollowARebind is the property item() exists for: a row's key
// hint is read from the live registry when the menu is built, so rebinding the
// action moves the hint with it and the menu cannot drift from the keys.
//
// Negative control, confirmed red: hardcode the Delete row's Hint to "d"
// instead of calling item(), and the rebound case fails still showing "d".
func TestFileMenuHintsFollowARebind(t *testing.T) {
	dir := fileViewTree(t)
	m := filesOS(t, dir, "")

	rightClickFile(t, m, "README.md")
	if got := menuRow(t, m, "Delete").Hint; got != "d" {
		t.Fatalf("the Delete row hints %q, want the default d", got)
	}
	if got := menuRow(t, m, "New file or folder").Hint; got != "a" {
		t.Fatalf("the New row hints %q, want the default a", got)
	}

	// Rebind through the config the registry actually reads.
	cfg := m.KeybindRegistry.GetConfig()
	cfg.Keybindings.SidebarFiles["file_delete"] = []string{"ctrl+k"}
	m.KeybindRegistry.Reload(cfg)

	m.CloseContextMenu()
	rightClickFile(t, m, "README.md")
	if got := menuRow(t, m, "Delete").Hint; got != "ctrl+k" {
		t.Errorf("after the rebind the Delete row hints %q, want ctrl+k", got)
	}
	// The rows nobody touched are unmoved.
	if got := menuRow(t, m, "New file or folder").Hint; got != "a" {
		t.Errorf("the rebind moved the New row's hint to %q", got)
	}
}

// TestFileMenuDeleteStillRaisesTheDialog is what the dialog says when the
// delete came from the menu: the same question, the same two rows, opened on
// Cancel.
//
// It runs the action the row named rather than going through the dispatcher,
// because what is checked here is the dialog's own text. The end-to-end version,
// where the click reaches the dispatcher and the dispatcher reaches this, is
// TestMenuDeleteGoesThroughTheDialog in internal/input, and that is where the
// control lives: making the menu path answer its own confirmation fails it.
func TestFileMenuDeleteStillRaisesTheDialog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.txt")
	if err := os.WriteFile(path, []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := filesOS(t, dir, "")

	rightClickFile(t, m, "report.txt")
	for i, it := range m.ContextMenu.Items {
		if it.Label == "Delete" {
			m.ContextMenu.Selected = i
		}
	}
	act := m.ContextMenuSelectedAction()
	m.CloseContextMenu()
	if act != "file_delete" {
		t.Fatalf("the Delete row names %q", act)
	}
	m.SidebarFileDelete(false)

	if !m.FileConfirmOpen() {
		t.Fatal("the menu's Delete row removed the file with no confirmation")
	}
	if m.filePrompt.Selected != fileConfirmRowCancel {
		t.Errorf("the confirmation opened on row %d, want Cancel", m.filePrompt.Selected)
	}
	if got := m.fileConfirmQuestion(); !strings.Contains(got, "report.txt") {
		t.Errorf("the confirmation asks %q, which does not name the file", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the file is already gone: %v", err)
	}

	// Enter on the untouched dialog is Cancel, so nothing goes.
	if cmd := m.FilePromptSubmit(); cmd != nil {
		cmd()
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the dialog destroyed the file on its opening row: %v", err)
	}
}

// TestFileMenuKeyOpensTheCursorRowsMenu is the keyboard half: the rail's menu
// key opens the same menu on the row the cursor is on, and the row it names is
// the cursor's own.
//
// Negative control, confirmed red: drop the file kinds from sidebarRowHasMenu,
// and the key opens no menu and says "Nothing on this row to act on".
func TestFileMenuKeyOpensTheCursorRowsMenu(t *testing.T) {
	dir := fileViewTree(t)
	m := filesOS(t, dir, "apple")

	m.SidebarOpenCursorMenu(false)
	if m.ContextMenu == nil {
		t.Fatal("the menu key opened no menu on a listing row")
	}
	if m.ContextMenu.Target != CtxTargetFileRow {
		t.Errorf("the menu key opened target %v, want the file menu", m.ContextMenu.Target)
	}
	if got := m.ContextMenu.File.Name; got != "apple" {
		t.Errorf("the menu is about %q, want the cursor's apple", got)
	}
	if !m.ContextMenu.File.IsDir {
		t.Error("the menu does not know apple is a folder")
	}
	if got := menuRow(t, m, "Open folder"); got.Dim {
		t.Error("the folder's Open row is dimmed")
	}
	// Fully operable by keyboard: the selection steps over the dimmed rows and
	// lands on runnable ones only.
	cm := m.ContextMenu
	for range len(cm.Items) * 2 {
		cm.Move(1)
		if !cm.selectable(cm.Selected) {
			t.Fatalf("arrowing landed on row %d, which cannot be run", cm.Selected)
		}
	}
}

// TestFileMenuIsAllDimWhenFileActionsAreOff: with the setting off, the six that
// touch the disk are dimmed and the row that leads to the setting is not, so
// the menu says the feature exists and where to turn it on.
func TestFileMenuIsAllDimWhenFileActionsAreOff(t *testing.T) {
	dir := fileViewTree(t)
	m := filesOS(t, dir, "")
	config.SidebarFileActions = false

	rightClickFile(t, m, "README.md")
	for _, label := range []string{"Copy", "Cut", "Paste", "New file or folder", "Rename", "Delete"} {
		if !menuRow(t, m, label).Dim {
			t.Errorf("%q is live with file actions switched off", label)
		}
	}
	// Opening a row is not one of the six: it copies a path, which a plain
	// click on the same row already does.
	if menuRow(t, m, "Copy path").Dim {
		t.Error("Copy path is dimmed, but a click on the row still copies it")
	}
	if menuRow(t, m, "Sidebar settings").Dim {
		t.Error("the way to the setting is dimmed too, so the menu is a dead end")
	}
}

// TestFileMenuPasteWakesWithAClipboard checks the one row whose dim depends on
// state rather than on the target.
func TestFileMenuPasteWakesWithAClipboard(t *testing.T) {
	dir := fileViewTree(t)
	m := filesOS(t, dir, "")

	rightClickFile(t, m, "README.md")
	if !menuRow(t, m, "Paste").Dim {
		t.Error("Paste is live with an empty clipboard")
	}
	m.CloseContextMenu()

	rightClickFile(t, m, "README.md")
	m.ContextMenuSelectedAction() // arm the carry the way taking a row does
	m.SidebarFileCopy()
	m.ClearMenuTarget()
	m.CloseContextMenu()

	rightClickFile(t, m, "beta.txt")
	if menuRow(t, m, "Paste").Dim {
		t.Error("Paste is dimmed with a file on the clipboard")
	}
}

// TestFileMenuFrames prints the drawn frame for each target. The menu is a
// visual feature and this is what the reviewer reads.
func TestFileMenuFrames(t *testing.T) {
	dir := fileViewTree(t)

	for _, tc := range []struct {
		name string
		open func(m *OS)
	}{
		{"file row", func(m *OS) { rightClickFile(t, m, "README.md") }},
		{"folder row", func(m *OS) { rightClickFile(t, m, "apple") }},
		{"blank space below the listing", func(m *OS) {
			x, y := filesBlankCell(t, m)
			m.SidebarClick(x, y, true)
		}},
		{"the menu key on the cursor row", func(m *OS) {
			if !cursorToFile(m, "beta.txt") {
				t.Fatal("no row for beta.txt")
			}
			m.SidebarOpenCursorMenu(false)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := filesOS(t, dir, "")
			tc.open(m)
			m.View()
			frame := ansi.Strip(m.cachedViewContent)
			t.Logf("%s\n%s", tc.name, frame)
			if !strings.Contains(frame, "Copy") {
				t.Errorf("the frame does not draw the menu:\n%s", frame)
			}
		})
	}
}
