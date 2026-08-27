package input

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The rail's file keys share three of their letters with rail bindings that
// already exist: r renames a pane, x opens a row's destructive menu, and a was
// free. Which one answers is decided by the row under the cursor, and that is
// what these drive, through the real key handler.

// filesRailOS gives the rail the keyboard with the files section listing dir
// and the cursor on name.
func filesRailOS(t *testing.T, dir, name string) *app.OS {
	t.Helper()
	prev, prevActions := config.SidebarEnabled, config.SidebarFileActions
	config.SidebarEnabled = true
	config.SidebarFileActions = true
	t.Cleanup(func() {
		config.SidebarEnabled = prev
		config.SidebarFileActions = prevActions
	})

	o := twoPaneOS(t)
	o.SessionName = "main"
	o.IsDaemonSession = true
	o.EnterSidebarFocus()
	if !o.OpenFileView(dir) {
		t.Fatal("the rail refused to open the files section")
	}
	// The listing arrives as a message, exactly as it does in the client.
	if cmd := o.TakeSidebarCmd(); cmd != nil {
		if _, c := o.Update(cmd()); c != nil {
			_ = c
		}
	}
	_ = o.View() // the rail publishes its rows as it draws
	if name == "" {
		return o
	}
	if !railCursorToFileRow(t, o, name) {
		t.Fatalf("the rail drew no listing row for %q", name)
	}
	return o
}

// railCursorToFileRow parks the cursor on the listing row whose drawn text
// holds name. The nav rows carry no file name, so the row is found through the
// hit rectangles the render published, which is the same identity a click uses.
func railCursorToFileRow(t *testing.T, o *app.OS, name string) bool {
	t.Helper()
	for i := range o.SidebarNav {
		o.SidebarCursor = i
		if !o.SidebarCursorOnFile() {
			continue
		}
		// The cursor row is the one drawn with the rail's selection, so ask the
		// model what it would act on instead of reading pixels.
		if o.FileActionTargetNameForTest() == name {
			return true
		}
	}
	return false
}

// pressEnter is the return key.
func pressEnter() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyEnter} }

// TestTheDeleteKeyRaisesTheDialogAndNothingElse is the confirmation gate driven
// through the key handler.
func TestTheDeleteKeyRaisesTheDialogAndNothingElse(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "report.txt"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := filesRailOS(t, dir, "report.txt")

	o, _ = HandleKeyPress(press("d"), o)
	if !o.FileConfirmOpen() {
		t.Fatal("d on a file row raised no confirmation")
	}
	if _, err := os.Lstat(filepath.Join(dir, "report.txt")); err != nil {
		t.Fatalf("d deleted the file with no answer: %v", err)
	}

	frame := o.View().Content
	if !strings.Contains(frame, "Delete report.txt?") {
		t.Errorf("the drawn confirmation does not name the file:\n%s", frame)
	}
}

// TestPressingTheDeleteKeyAgainDoesNotAnswerTheDialog is the rule that a
// repeated keypress must never satisfy a confirmation.
func TestPressingTheDeleteKeyAgainDoesNotAnswerTheDialog(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "report.txt"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := filesRailOS(t, dir, "report.txt")

	o, _ = HandleKeyPress(press("d"), o)
	for range 5 {
		o, _ = HandleKeyPress(press("d"), o)
		if _, err := os.Lstat(filepath.Join(dir, "report.txt")); err != nil {
			t.Fatalf("a repeated keypress answered the dialog: %v", err)
		}
	}
	if !o.FileConfirmOpen() {
		t.Fatal("a repeated keypress closed the dialog")
	}
	// And "y" is not a yes either: the answer is a selection.
	o, _ = HandleKeyPress(press("y"), o)
	if _, err := os.Lstat(filepath.Join(dir, "report.txt")); err != nil {
		t.Fatalf("\"y\" answered the dialog: %v", err)
	}
}

// TestEnterOnTheDialogAsItOpensRunsNothing is the No-is-the-default rule at the
// key layer.
func TestEnterOnTheDialogAsItOpensRunsNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "report.txt"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := filesRailOS(t, dir, "report.txt")

	o, _ = HandleKeyPress(press("d"), o)
	o, cmd := HandleKeyPress(pressEnter(), o)
	if cmd != nil {
		t.Fatal("enter on an untouched dialog produced a command")
	}
	if o.FilePromptOpen() {
		t.Error("enter left the dialog up")
	}
	if _, err := os.Lstat(filepath.Join(dir, "report.txt")); err != nil {
		t.Fatalf("enter on an untouched dialog deleted the file: %v", err)
	}
}

// TestTheKillKeyStillWorksOnAPaneRow is the regression the shared keys risk:
// x means cut on a file row and must still mean the row's menu everywhere else.
func TestTheKillKeyStillWorksOnAPaneRow(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "report.txt"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := filesRailOS(t, dir, "report.txt")

	if !railCursorToWindow(t, o, "b") {
		t.Fatal("the rail drew no terminal row")
	}
	o, _ = HandleKeyPress(press("x"), o)
	if frame := o.View().Content; !strings.Contains(frame, "Close pane") {
		t.Errorf("x on a terminal row no longer opens that pane's menu:\n%s", frame)
	}
}

// TestTheFileKeysAreInertOffTheListing: a on a pane row must still make a
// session-free no-op rather than a file, and d must not raise a confirmation.
func TestTheFileKeysAreInertOffTheListing(t *testing.T) {
	dir := t.TempDir()
	o := filesRailOS(t, dir, "")
	if !railCursorToWindow(t, o, "b") {
		t.Fatal("the rail drew no terminal row")
	}

	for _, key := range []string{"a", "d", "D", "y", "p"} {
		o, _ = HandleKeyPress(press(key), o)
		if o.FilePromptOpen() {
			t.Fatalf("%q on a terminal row opened a file dialog", key)
		}
	}
	if names, err := os.ReadDir(dir); err != nil || len(names) != 0 {
		t.Errorf("the folder grew a file from a key pressed off the listing: %v %v", names, err)
	}
}

// TestTheCreateKeyOpensThePromptAndTypingReachesIt walks the create gesture
// through the key handler, including the letters that are rail bindings.
func TestTheCreateKeyOpensThePromptAndTypingReachesIt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "report.txt"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := filesRailOS(t, dir, "report.txt")

	o, _ = HandleKeyPress(press("a"), o)
	if !o.FilePromptOpen() {
		t.Fatal("a on a file row opened no create prompt")
	}
	// Every one of these is a rail binding. Inside the prompt they are text.
	for _, key := range []string{"d", "a", "x", "y", "p", "s"} {
		o, _ = HandleKeyPress(press(key), o)
	}
	frame := o.View().Content
	if !strings.Contains(frame, "daxyps") {
		t.Errorf("the typed name did not reach the field:\n%s", frame)
	}
	if !o.SidebarFocused {
		t.Error("typing \"s\" into the prompt left the rail")
	}

	o, cmd := HandleKeyPress(pressEnter(), o)
	if cmd == nil {
		t.Fatal("enter on the create prompt produced no command")
	}
	if _, err := os.Lstat(filepath.Join(dir, "daxyps")); err == nil {
		t.Fatal("the create ran on the update goroutine")
	}
	if _, c := o.Update(cmd()); c != nil {
		_ = c
	}
	if _, err := os.Lstat(filepath.Join(dir, "daxyps")); err != nil {
		t.Fatalf("the create never happened: %v", err)
	}
}

// TestEscapeLeavesTheDialogAndKeepsTheRail: escape answers the dialog, and the
// same key a second time is what leaves the rail.
func TestEscapeLeavesTheDialogAndKeepsTheRail(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "report.txt"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := filesRailOS(t, dir, "report.txt")

	o, _ = HandleKeyPress(press("d"), o)
	o, _ = HandleKeyPress(press("esc"), o)
	if o.FilePromptOpen() {
		t.Error("escape left the dialog up")
	}
	if !o.SidebarFocused {
		t.Error("escape on the dialog also dropped the rail's keyboard focus")
	}
	if _, err := os.Lstat(filepath.Join(dir, "report.txt")); err != nil {
		t.Errorf("escape deleted the file: %v", err)
	}
}
