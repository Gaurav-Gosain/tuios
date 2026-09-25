package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// These read the composed dialog rather than the model. A confirmation that
// says the right thing in a string and truncates it on screen is the failure
// this codebase keeps hitting, so the claims here are about the drawn frame:
// what is on it, and that nothing fell off the side.

// dialogFrame renders whichever file dialog is open, with the styling stripped.
func dialogFrame(t *testing.T, m *OS) []string {
	t.Helper()
	if !m.FilePromptOpen() {
		t.Fatal("no file dialog is open")
	}
	content, geo, _ := m.renderFileDialog()
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		lines[i] = ansi.Strip(l)
		if w := lipgloss.Width(lines[i]); w > geo.Width {
			t.Errorf("line %d is %d cells wide, past the dialog's %d:\n%s", i, w, geo.Width, lines[i])
		}
	}
	return lines
}

// TestClickingTheConfirmationRunsTheRowUnderThePointer drives the pointer
// through the real hit rectangles: the frame is composed, the renderer records
// where it drew the answers, and the click is routed by that record rather than
// by anything a handler works out for itself.
func TestClickingTheConfirmationRunsTheRowUnderThePointer(t *testing.T) {
	trash := tempTrash(t)
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "report.txt"), "body")
	m := filesOS(t, dir, "report.txt")
	m.SidebarFileDelete(false)

	_ = m.View() // the overlay publishes its rectangles as it draws
	hit, ok := overlayHitOfKind(m, "filedialog")
	if !ok {
		t.Fatal("the dialog published no panel geometry")
	}
	if len(hit.Rows) != fileConfirmRowCount {
		t.Fatalf("the dialog published %d rows, want %d", len(hit.Rows), fileConfirmRowCount)
	}

	// Cancel first: a click on it must close the dialog and run nothing.
	x, y := rowCenter(hit, fileConfirmRowCancel)
	handled, cmd := m.OverlayMouseClick(x, y, false)
	if !handled {
		t.Fatal("the click missed the dialog")
	}
	if cmd != nil {
		t.Fatal("clicking Cancel produced a command")
	}
	if _, err := os.Lstat(filepath.Join(dir, "report.txt")); err != nil {
		t.Fatalf("clicking Cancel deleted the file: %v", err)
	}

	// Then the destructive row.
	m.SidebarFileDelete(false)
	_ = m.View()
	hit, _ = overlayHitOfKind(m, "filedialog")
	x, y = rowCenter(hit, fileConfirmRowGo)
	handled, cmd = m.OverlayMouseClick(x, y, false)
	if !handled || cmd == nil {
		t.Fatalf("clicking the destructive row returned handled=%v cmd=%v", handled, cmd != nil)
	}
	if _, err := os.Lstat(filepath.Join(dir, "report.txt")); err != nil {
		t.Fatal("the click deleted the file on the update goroutine")
	}
	runOp(t, m, cmd)
	if _, err := os.Lstat(filepath.Join(trash, "files", "report.txt")); err != nil {
		t.Errorf("the click did not send the file to the trash: %v", err)
	}
}

// TestAClickOutsideTheConfirmationCancelsIt: the ambiguous gesture must never
// be the one that removes a file.
func TestAClickOutsideTheConfirmationCancelsIt(t *testing.T) {
	tempTrash(t)
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "report.txt"), "body")
	m := filesOS(t, dir, "report.txt")
	m.SidebarFileDelete(false)

	_ = m.View()
	if _, ok := overlayHitOfKind(m, "filedialog"); !ok {
		t.Fatal("the dialog published no panel geometry")
	}
	handled, cmd := m.OverlayMouseClick(0, 0, false)
	if !handled || cmd != nil {
		t.Fatalf("a click away returned handled=%v cmd=%v", handled, cmd != nil)
	}
	if m.FilePromptOpen() {
		t.Error("a click away left the dialog up")
	}
	if _, err := os.Lstat(filepath.Join(dir, "report.txt")); err != nil {
		t.Errorf("a click away deleted the file: %v", err)
	}
}

// overlayHitOfKind returns the recorded geometry for one overlay kind.
func overlayHitOfKind(m *OS, kind string) (overlayPanelHit, bool) {
	for _, h := range m.OverlayHits {
		if h.Kind == kind {
			return h, true
		}
	}
	return overlayPanelHit{}, false
}

// rowCenter is a screen point inside one published answer row.
func rowCenter(h overlayPanelHit, idx int) (int, int) {
	r := h.Rows[idx].Rect
	return h.OriginX + (r.X0+r.X1)/2, h.OriginY + r.Y0
}

// TestTheRenamePromptNamesTheWholePath is the second half of the OSC 7 fix. The
// folder a rename acts in came from the pane, so a dialog that says only
// "Rename report.txt" names a file that exists in a hundred folders.
//
// Negative control: drop the path line from renderFileNamePrompt and this fails
// with the tail of the folder missing from the frame.
func TestTheRenamePromptNamesTheWholePath(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "report.txt"), "body")
	m := filesOS(t, dir, "report.txt")

	if !m.SidebarFileRename() {
		t.Fatal("the rename did not open")
	}
	frame := strings.Join(dialogFrame(t, m), "\n")
	want := filepath.Join(filepath.Base(dir), "report.txt")
	if !strings.Contains(frame, want) {
		t.Errorf("the rename prompt does not say which %s it renames (want %q):\n%s",
			"report.txt", want, frame)
	}
}
