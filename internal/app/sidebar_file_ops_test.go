package app

import (
	"os"
	"path/filepath"
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
	m.Settings.SidebarFileActions = true
	m.Settings.SidebarFileDelete = config.SidebarFileDeleteTrash
	t.Cleanup(func() {
		m.Settings.SidebarFileActions = true
		m.Settings.SidebarFileDelete = config.SidebarFileDeleteTrash
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
	m.Settings.SidebarFileActions = false

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
