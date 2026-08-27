package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// These drive the model the way the keyboard does, over a real temporary
// directory, and read the disk back afterwards. The claim under nearly all of
// them is the same one: the update goroutine decides, and the command acts, so
// a file is still on disk at the moment the handler returns and gone only once
// the command has run.

// filesOS builds a client with the rail up, the files section listing dir, and
// the keyboard cursor on the entry called name.
func filesOS(t *testing.T, dir, name string) *OS {
	t.Helper()
	m := sidebarTestOS(t, 120, 40, "left")
	config.SidebarFileActions = true
	config.SidebarFileDelete = config.SidebarFileDeleteTrash
	t.Cleanup(func() {
		config.SidebarFileActions = true
		config.SidebarFileDelete = config.SidebarFileDeleteTrash
	})
	openFilesOn(t, m, dir)
	m.SidebarFocused = true
	// The nav rows exist only once the rail has drawn, which is the same order
	// the real client works in: the render records what the cursor can land on.
	railLines(t, m)
	if name == "" {
		return m
	}
	if !cursorToFile(m, name) {
		t.Fatalf("no rail row for %q; the listing drew %v", name, entryNames(m))
	}
	return m
}

// cursorToFile puts the keyboard cursor on the listing row for name.
func cursorToFile(m *OS, name string) bool {
	for i, row := range m.SidebarNav {
		if row.Kind != sidebarRowFileEntry {
			continue
		}
		if row.WindowIndex >= 0 && row.WindowIndex < len(m.filesView.Entries) &&
			m.filesView.Entries[row.WindowIndex].Name == name {
			m.SidebarCursor = i
			return true
		}
	}
	return false
}

// entryNames is the listing, for a failure message.
func entryNames(m *OS) []string {
	out := make([]string, 0, len(m.filesView.Entries))
	for _, e := range m.filesView.Entries {
		out = append(out, e.Name)
	}
	return out
}

// runOp runs a file operation command and applies its answer, which is what
// Update does. It fails when the command is nil, because a caller that expected
// work and got none would otherwise pass silently.
func runOp(t *testing.T, m *OS, cmd tea.Cmd) fileOpMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("the action returned no command, so nothing would ever run")
	}
	msg, ok := cmd().(fileOpMsg)
	if !ok {
		t.Fatalf("the command answered with %T, not a file operation result", msg)
	}
	m.HandleFileOp(msg)
	return msg
}

// TestDeleteAlwaysRaisesADialogAndRemovesNothingYet is the confirmation gate.
// The delete key opens a dialog and touches the disk not at all.
func TestDeleteAlwaysRaisesADialog(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "report.txt"), "body")
	m := filesOS(t, dir, "report.txt")

	m.SidebarFileDelete(false)
	if !m.FileConfirmOpen() {
		t.Fatal("the delete key did not raise a confirmation")
	}
	if _, err := os.Lstat(filepath.Join(dir, "report.txt")); err != nil {
		t.Fatalf("the file went before anybody answered: %v", err)
	}
	if m.filePrompt.Selected != fileConfirmRowCancel {
		t.Errorf("the dialog opened on row %d; it must open on Cancel", m.filePrompt.Selected)
	}
}

// TestEnterOnAnUntouchedDialogDeletesNothing is the negative control's target
// and the whole point of the dialog: the answer nobody chose is No.
func TestEnterOnAnUntouchedDialogDeletesNothing(t *testing.T) {
	trash := tempTrash(t)
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "report.txt"), "body")
	m := filesOS(t, dir, "report.txt")

	m.SidebarFileDelete(false)
	// Enter, with nothing moved. This is the accident the dialog exists for.
	if cmd := m.FilePromptSubmit(); cmd != nil {
		t.Fatal("enter on an untouched dialog returned a command; it must return none")
	}
	if m.FilePromptOpen() {
		t.Error("the dialog is still up after it was answered")
	}
	if _, err := os.Lstat(filepath.Join(dir, "report.txt")); err != nil {
		t.Fatalf("the file was deleted by an unanswered dialog: %v", err)
	}
	if entries, err := os.ReadDir(filepath.Join(trash, "files")); err == nil && len(entries) != 0 {
		t.Fatalf("the trash holds %d files after a cancelled delete", len(entries))
	}
}

// TestEscapeCancelsTheDialog covers the other way out.
func TestEscapeCancelsTheDialog(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "report.txt"), "body")
	m := filesOS(t, dir, "report.txt")

	m.SidebarFileDelete(false)
	m.FilePromptCancel()
	if m.FilePromptOpen() {
		t.Error("escape left the dialog up")
	}
	if _, err := os.Lstat(filepath.Join(dir, "report.txt")); err != nil {
		t.Errorf("escape deleted the file: %v", err)
	}
}

// TestTheGoRowSendsTheFileToTheTrash walks the whole gesture: open, move onto
// the destructive row, answer, run the command.
func TestTheGoRowSendsTheFileToTheTrash(t *testing.T) {
	trash := tempTrash(t)
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "report.txt"), "body")
	m := filesOS(t, dir, "report.txt")

	m.SidebarFileDelete(false)
	if !m.filePrompt.Trash {
		t.Fatal("the default delete is not the trash one")
	}
	m.FileConfirmMove(1)
	if m.filePrompt.Selected != fileConfirmRowGo {
		t.Fatalf("the cursor is on row %d, want the destructive row", m.filePrompt.Selected)
	}

	cmd := m.FilePromptSubmit()
	// The file is still there at the moment the handler returns. That is the
	// no-disk-work-on-the-update-loop claim, checked rather than asserted.
	if _, err := os.Lstat(filepath.Join(dir, "report.txt")); err != nil {
		t.Fatalf("the delete ran on the update goroutine: %v", err)
	}
	runOp(t, m, cmd)

	if _, err := os.Lstat(filepath.Join(dir, "report.txt")); err == nil {
		t.Error("the file is still there after the command ran")
	}
	if body, err := os.ReadFile(filepath.Join(trash, "files", "report.txt")); err != nil || string(body) != "body" {
		t.Errorf("the file did not reach the trash: %q %v", body, err)
	}
}

// TestThePermanentDeleteKeySaysItIsPermanent covers the explicit alternative
// and the sentence that separates the two dialogs.
func TestThePermanentDeleteKeySaysItIsPermanent(t *testing.T) {
	trash := tempTrash(t)
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "report.txt"), "body")
	m := filesOS(t, dir, "report.txt")

	m.SidebarFileDelete(true)
	if m.filePrompt.Trash {
		t.Fatal("the permanent delete key opened the trash dialog")
	}
	outcome := m.fileConfirmOutcome()
	if !strings.Contains(outcome, "permanent") || !strings.Contains(outcome, "can not undo") {
		t.Errorf("the permanent dialog says %q; it must say the file does not come back", outcome)
	}

	m.FileConfirmMove(1)
	runOp(t, m, m.FilePromptSubmit())
	if _, err := os.Lstat(filepath.Join(dir, "report.txt")); err == nil {
		t.Error("the permanent delete left the file where it was")
	}
	if entries, err := os.ReadDir(filepath.Join(trash, "files")); err == nil && len(entries) != 0 {
		t.Errorf("the permanent delete put %d files in the trash", len(entries))
	}
}

// TestTheTwoDialogsSayDifferentThings is the requirement that a person can tell
// which one they are looking at.
func TestTheTwoDialogsSayDifferentThings(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "report.txt"), "body")
	m := filesOS(t, dir, "report.txt")

	m.SidebarFileDelete(false)
	trashOutcome, trashTitle := m.fileConfirmOutcome(), m.filePromptTitle()
	m.FilePromptCancel()
	m.SidebarFileDelete(true)
	permOutcome, permTitle := m.fileConfirmOutcome(), m.filePromptTitle()

	if trashOutcome == permOutcome {
		t.Errorf("both dialogs say %q; the two must read differently", trashOutcome)
	}
	if trashTitle == permTitle {
		t.Errorf("both dialogs are titled %q", trashTitle)
	}
	if !strings.Contains(trashOutcome, "trash") || !strings.Contains(trashOutcome, "get it back") {
		t.Errorf("the trash dialog says %q; it must say where the file goes and that it comes back", trashOutcome)
	}
}

// TestTheDialogNamesTheFile covers the "names the target" requirement at the
// model layer. The frame test checks that it survives the width.
func TestTheDialogNamesTheFile(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "report.txt"), "body")
	m := filesOS(t, dir, "report.txt")

	m.SidebarFileDelete(false)
	if q := m.fileConfirmQuestion(); !strings.Contains(q, "report.txt") {
		t.Errorf("the question is %q; it must name the file", q)
	}
	if len(m.filePrompt.Paths) != 1 || m.filePrompt.Paths[0] != filepath.Join(dir, "report.txt") {
		t.Errorf("the dialog captured %v, want the one full path", m.filePrompt.Paths)
	}
}

// TestTheDeleteActsOnThePathCapturedWhenTheDialogOpened is the race the yeetui
// comment names: an in-flight listing can replace the rows while the dialog is
// up, and a re-derived target would then point at a different file.
func TestTheDeleteActsOnThePathCapturedWhenTheDialogOpened(t *testing.T) {
	trash := tempTrash(t)
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "aimed-at.txt"), "aimed")
	mustWrite(t, filepath.Join(dir, "bystander.txt"), "bystander")
	m := filesOS(t, dir, "aimed-at.txt")

	m.SidebarFileDelete(false)
	// A reply that was already in flight lands and reorders the listing under
	// the open dialog.
	m.filesView.Entries = []fileEntry{{Name: "bystander.txt"}, {Name: "aimed-at.txt"}}

	m.FileConfirmMove(1)
	runOp(t, m, m.FilePromptSubmit())

	if _, err := os.Lstat(filepath.Join(dir, "bystander.txt")); err != nil {
		t.Errorf("the wrong file went: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(trash, "files", "aimed-at.txt")); err != nil {
		t.Errorf("the file the user aimed at is not in the trash: %v", err)
	}
}

// TestAFinishedOperationReReadsThroughTheGeneration is the resurrection guard.
// The listing is asked for again rather than edited, and the read that was
// running before the delete carries the old generation and is dropped.
func TestAFinishedOperationReReadsThroughTheGeneration(t *testing.T) {
	tempTrash(t)
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "report.txt"), "body")
	m := filesOS(t, dir, "report.txt")

	stale := m.filesView.Gen
	m.SidebarFileDelete(false)
	m.FileConfirmMove(1)
	cmd := m.FilePromptSubmit()
	msg, _ := cmd().(fileOpMsg)

	refresh := m.HandleFileOp(msg)
	if refresh == nil {
		t.Fatal("a finished operation did not ask for the listing again")
	}
	if m.filesView.Gen == stale {
		t.Fatal("the re-read reused the old generation")
	}

	// The read that was in flight before the delete lands now, carrying the
	// folder as it was. It has to be dropped whole, so its rows never reach the
	// rail. The marker name is one the folder never held, so finding it can only
	// mean the stale reply was applied.
	m.HandleFileList(fileListMsg{
		Gen:     stale,
		Dir:     dir,
		Entries: []fileEntry{{Name: "report.txt"}, {Name: "from-the-stale-read.txt"}},
	})
	for _, e := range m.filesView.Entries {
		if e.Name == "from-the-stale-read.txt" {
			t.Fatal("a stale reply was applied over the listing")
		}
	}

	// And the fresh read, when it lands, holds the truth.
	fresh, _ := refresh().(fileListMsg)
	m.HandleFileList(fresh)
	for _, e := range m.filesView.Entries {
		if e.Name == "report.txt" {
			t.Fatal("the re-read still lists the deleted file")
		}
	}
}

// TestFileActionsOffMakesEveryKeyInert is the setting, checked on the disk.
func TestFileActionsOffMakesEveryKeyInert(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "report.txt"), "body")
	m := filesOS(t, dir, "report.txt")
	config.SidebarFileActions = false

	if m.SidebarCursorOnFile() {
		t.Error("the file keys are still routed to the listing with the setting off")
	}
	m.SidebarFileCreate()
	m.SidebarFileDelete(false)
	m.SidebarFileCopy()
	if m.FilePromptOpen() {
		t.Error("a file key opened a dialog with the setting off")
	}
	if !m.fileClip.Empty() {
		t.Error("a copy was captured with the setting off")
	}
	if cmd := m.SidebarFilePaste(); cmd != nil {
		t.Error("a paste ran with the setting off")
	}
	if got := namesIn(t, dir); len(got) != 1 || got[0] != "report.txt" {
		t.Errorf("the folder changed with the setting off: %q", got)
	}
}

// TestCreateAndRenameRunOffTheLoop checks the two name prompts end to end, and
// that neither writes anything before its command runs.
func TestCreateAndRenameRunOffTheLoop(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "old.txt"), "body")
	m := filesOS(t, dir, "old.txt")

	m.SidebarFileCreate()
	for _, r := range "notes.md" {
		m.FilePromptType(string(r))
	}
	cmd := m.FilePromptSubmit()
	if _, err := os.Lstat(filepath.Join(dir, "notes.md")); err == nil {
		t.Fatal("the create ran on the update goroutine")
	}
	runOp(t, m, cmd)
	if _, err := os.Lstat(filepath.Join(dir, "notes.md")); err != nil {
		t.Fatalf("the create never happened: %v", err)
	}

	railLines(t, m)
	if !cursorToFile(m, "old.txt") {
		t.Fatalf("the listing lost old.txt: %v", entryNames(m))
	}
	if !m.SidebarFileRename() {
		t.Fatal("the rename key did nothing on a file row")
	}
	if m.filePrompt.Input != "old.txt" {
		t.Errorf("the rename field opened holding %q, want the current name", m.filePrompt.Input)
	}
	m.FilePromptClearInput()
	for _, r := range "new.txt" {
		m.FilePromptType(string(r))
	}
	cmd = m.FilePromptSubmit()
	if _, err := os.Lstat(filepath.Join(dir, "old.txt")); err != nil {
		t.Fatal("the rename ran on the update goroutine")
	}
	runOp(t, m, cmd)
	if _, err := os.Lstat(filepath.Join(dir, "new.txt")); err != nil {
		t.Fatalf("the rename never happened: %v", err)
	}
}

// TestARefusedNameKeepsThePromptUp is what makes a bad name recoverable: the
// dialog stays, holding what was typed, with the reason under it.
func TestARefusedNameKeepsThePromptUp(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "report.txt"), "body")
	m := filesOS(t, dir, "report.txt")

	m.SidebarFileCreate()
	for _, r := range "../escaped.txt" {
		m.FilePromptType(string(r))
	}
	if cmd := m.FilePromptSubmit(); cmd != nil {
		t.Fatal("a name that leaves the folder was accepted")
	}
	if !m.FilePromptOpen() {
		t.Fatal("the prompt closed on a refusal, losing what was typed")
	}
	if m.filePrompt.Input != "../escaped.txt" {
		t.Errorf("the field holds %q; a refusal must keep the typed name", m.filePrompt.Input)
	}
	if !strings.Contains(m.filePrompt.Err, "outside this folder") {
		t.Errorf("the refusal reads %q; it must say the name leaves the folder", m.filePrompt.Err)
	}
	if _, err := os.Lstat(filepath.Join(filepath.Dir(dir), "escaped.txt")); err == nil {
		t.Fatal("a refused name was written outside the folder anyway")
	}
}

// TestCutIsSpentByThePasteThatMovesIt stops a second paste chasing a source
// that has already moved.
func TestCutIsSpentByThePasteThatMovesIt(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	mustWrite(t, filepath.Join(src, "moved.txt"), "body")
	m := filesOS(t, src, "moved.txt")

	m.SidebarFileCut()
	if m.fileClip.Empty() || !m.fileClip.Move {
		t.Fatal("the cut captured nothing")
	}
	openFilesOn(t, m, dst)
	railLines(t, m)

	cmd := m.SidebarFilePaste()
	if !m.fileClip.Empty() {
		t.Error("the cut is still on the clipboard after the paste that spends it")
	}
	runOp(t, m, cmd)
	if _, err := os.Lstat(filepath.Join(dst, "moved.txt")); err != nil {
		t.Fatalf("the move never landed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(src, "moved.txt")); err == nil {
		t.Error("the source survived a move")
	}
	if cmd := m.SidebarFilePaste(); cmd != nil {
		t.Error("a second paste ran on a spent cut")
	}
}

// TestCopyLeavesTheSourceAlone is the other half, and it is why a copy needs no
// dialog: nothing on either side is destroyed.
func TestCopyLeavesTheSourceAlone(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	mustWrite(t, filepath.Join(src, "kept.txt"), "body")
	m := filesOS(t, src, "kept.txt")

	m.SidebarFileCopy()
	openFilesOn(t, m, dst)
	railLines(t, m)
	runOp(t, m, m.SidebarFilePaste())

	if _, err := os.Lstat(filepath.Join(src, "kept.txt")); err != nil {
		t.Errorf("a copy removed the source: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "kept.txt")); err != nil {
		t.Errorf("the copy never landed: %v", err)
	}
	if m.fileClip.Empty() {
		t.Error("a copy was spent by its paste; it can be pasted again")
	}
}

// TestTheParentRowIsNotADeleteTarget: the ".." row means "go up", and the
// folder it names must not be deletable from it.
func TestTheParentRowIsNotADeleteTarget(t *testing.T) {
	dir := t.TempDir()
	inner := filepath.Join(dir, "inner")
	if err := os.Mkdir(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	m := filesOS(t, inner, "")
	for i, row := range m.SidebarNav {
		if row.Kind == sidebarRowFileUp {
			m.SidebarCursor = i
		}
	}
	if _, _, ok := m.fileActionTarget(); ok {
		t.Fatal("the parent row is a file action target")
	}
	m.SidebarFileDelete(false)
	if m.FileConfirmOpen() {
		t.Error("the parent row raised a delete confirmation")
	}
	if _, err := os.Lstat(dir); err != nil {
		t.Errorf("the parent folder went: %v", err)
	}
}
