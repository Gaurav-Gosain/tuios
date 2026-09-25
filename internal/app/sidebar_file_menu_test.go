package app

import (
	"strings"
	"testing"
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
