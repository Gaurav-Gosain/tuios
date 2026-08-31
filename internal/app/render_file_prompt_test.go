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

// TestTheTrashConfirmationFrameSaysEverything checks the drawn frame carries
// all four things the dialog owes the reader.
func TestTheTrashConfirmationFrameSaysEverything(t *testing.T) {
	tempTrash(t)
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "report.txt"), "body")
	m := filesOS(t, dir, "report.txt")
	m.SidebarFileDelete(false)

	frame := strings.Join(dialogFrame(t, m), "\n")
	for _, want := range []string{
		"report.txt",    // what goes
		"trash",         // where it goes
		"get it back",   // that it comes back
		"Cancel",        // the answer that changes nothing
		"Move to trash", // the answer that does
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("the drawn dialog does not say %q:\n%s", want, frame)
		}
	}
	if strings.Contains(frame, "permanent") {
		t.Errorf("the trash dialog says the delete is permanent:\n%s", frame)
	}
}

// TestThePermanentConfirmationFrameSaysItDoesNotComeBack is the other frame,
// and the two must not read alike.
func TestThePermanentConfirmationFrameSaysItDoesNotComeBack(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "report.txt"), "body")
	m := filesOS(t, dir, "report.txt")
	m.SidebarFileDelete(true)

	frame := strings.Join(dialogFrame(t, m), "\n")
	for _, want := range []string{"report.txt", "permanent", "can not undo", "Cancel", "Delete permanently"} {
		if !strings.Contains(frame, want) {
			t.Errorf("the drawn dialog does not say %q:\n%s", want, frame)
		}
	}
	if strings.Contains(frame, "goes to the trash") {
		t.Errorf("the permanent dialog offers the trash:\n%s", frame)
	}
}

// TestTheConfirmationKeepsTheFileNameOnALongPath is the truncation claim. A
// path cut from the wrong end takes the file name with it, which is the only
// part that says which file is about to go.
func TestTheConfirmationKeepsTheFileNameOnALongPath(t *testing.T) {
	tempTrash(t)
	dir := t.TempDir()
	deep := filepath.Join(dir, strings.Repeat("a-long-folder-name/", 8))
	if err := mkdirAllForTest(deep); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(deep, "report.txt"), "body")
	m := filesOS(t, deep, "report.txt")
	m.SidebarFileDelete(false)

	lines := dialogFrame(t, m)
	frame := strings.Join(lines, "\n")
	// One line has to carry both ends of the cut: the folder it is in and the
	// file itself. A path cut from the tail keeps the disk and loses the name,
	// which is the only part that says which file is about to go.
	found := false
	for _, l := range lines {
		if strings.Contains(l, "a-long-folder-name") && strings.Contains(l, "report.txt") {
			found = true
		}
	}
	if !found {
		t.Errorf("no line carries both the folder and the file name:\n%s", frame)
	}
}

// TestTheDialogFitsANarrowScreen: the dialog is centred and sized to the
// screen, so a client narrower than its preferred width still gets a whole one.
func TestTheDialogFitsANarrowScreen(t *testing.T) {
	tempTrash(t)
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "report.txt"), "body")
	m := filesOS(t, dir, "report.txt")
	m.SidebarFileDelete(false)
	// Narrowed after the dialog is up, which is what a resize under an open
	// dialog does. The dialog has to fit what it now has.
	m.Width = 40

	lines := dialogFrame(t, m)
	for i, l := range lines {
		if w := lipgloss.Width(l); w > m.GetRenderWidth() {
			t.Fatalf("line %d is %d cells wide on a %d column screen:\n%s", i, w, m.GetRenderWidth(), l)
		}
	}
	frame := strings.Join(lines, "\n")
	if !strings.Contains(frame, "Cancel") || !strings.Contains(frame, "Move to trash") {
		t.Errorf("a narrow screen lost an answer:\n%s", frame)
	}
}

// TestTheCreateAndRenamePromptFrames checks the two name dialogs draw the
// question, the field and the standing hint.
func TestTheCreateAndRenamePromptFrames(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "report.txt"), "body")
	m := filesOS(t, dir, "report.txt")

	m.SidebarFileCreate()
	m.FilePromptType("notes.md")
	frame := strings.Join(dialogFrame(t, m), "\n")
	if !strings.Contains(frame, "notes.md") {
		t.Errorf("the create prompt does not show what was typed:\n%s", frame)
	}
	if !strings.Contains(frame, "folder") {
		t.Errorf("the create prompt does not say how to make a folder:\n%s", frame)
	}
	m.FilePromptCancel()

	if !m.SidebarFileRename() {
		t.Fatal("the rename did not open")
	}
	frame = strings.Join(dialogFrame(t, m), "\n")
	if !strings.Contains(frame, "report.txt") {
		t.Errorf("the rename prompt does not name what it renames:\n%s", frame)
	}
	if !strings.Contains(frame, "rename") {
		t.Errorf("the rename prompt is not titled:\n%s", frame)
	}
}

// TestARefusedNameIsDrawnUnderTheField: the reason a name was refused has to be
// on the frame, not only in the model.
func TestARefusedNameIsDrawnUnderTheField(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "report.txt"), "body")
	m := filesOS(t, dir, "report.txt")

	m.SidebarFileCreate()
	m.FilePromptType("../escape")
	m.FilePromptSubmit()
	frame := strings.Join(dialogFrame(t, m), "\n")
	if !strings.Contains(frame, "outside this folder") {
		t.Errorf("the refusal is not on the frame:\n%s", frame)
	}
}

// TestTheConfirmationRecordsARectPerAnswer is the hit-rectangle rule: the rows
// are published by the renderer as it draws them, in drawn order.
func TestTheConfirmationRecordsARectPerAnswer(t *testing.T) {
	tempTrash(t)
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "report.txt"), "body")
	m := filesOS(t, dir, "report.txt")
	m.SidebarFileDelete(false)

	_, geo, rows := m.renderFileDialog()
	if len(rows) != fileConfirmRowCount {
		t.Fatalf("the dialog published %d rectangles, want %d", len(rows), fileConfirmRowCount)
	}
	for i, r := range rows {
		if r.Idx != i {
			t.Errorf("rectangle %d points at row %d", i, r.Idx)
		}
		if r.Rect.Y0 < geo.BodyY || r.Rect.Y1 > geo.Height {
			t.Errorf("rectangle %d is outside the dialog: %+v in a %d tall panel", i, r.Rect, geo.Height)
		}
	}
	if rows[0].Rect.Y0 >= rows[1].Rect.Y0 {
		t.Error("the rectangles are not in drawn order")
	}
}

// TestANamePromptPublishesNoAnswerRects: there is nothing to click in a name
// prompt, so it must publish nothing for a click to find.
func TestANamePromptPublishesNoAnswerRects(t *testing.T) {
	dir := t.TempDir()
	m := filesOS(t, dir, "")
	m.SidebarFileCreate()
	if _, _, rows := m.renderFileDialog(); len(rows) != 0 {
		t.Errorf("the create prompt published %d clickable rows", len(rows))
	}
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
