package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// clickTypesAt types typed into the editor on screen, clicks at cells after
// the start of the typed text, types "X" there, and waits for want. A click
// that missed the editor leaves the X at the end of the text, and want then
// never shows.
func clickTypesAt(t *testing.T, term *tuitest.Terminal, what, typed string, cells int, want string) {
	t.Helper()
	if err := term.SendKeys(typed); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, what+": the typed text", typed)
	row, col := findText(t, term, typed)
	mouseClick(t, term, col+cells, row, tuitest.MouseLeft, 0)
	if err := term.SendKeys("X"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, what+": the key typed where the click put the cursor", want)
}

// TestDraftMouseInEveryEditor clicks in each editor that takes the keys
// other than the agents view's reply (which TestComposerEditsLikeATextField
// drives): the reason typed for a rejection, the New agent sheet's task, the
// task capture sheet and a new message in the mailbox. In each, a click puts
// the cursor where it lands, and in the New agent task a double click selects
// a word that typing then replaces. Frames of each are saved under
// artifactDir.
//
// How it could pass wrongly, written down first:
//   - The key after the click could land at the end, as with no click at
//     all. Each check wants the X inside the text, before a word the click
//     was on.
//   - A click on one editor could reach another drawn under it: the New
//     agent sheet is drawn over the reply area. The sheet's task must take
//     the X.
//   - The reason line is drawn as lines of the card's detail, so its place
//     could be off by the card's head or by the lines above it. The X must
//     be at the cell clicked, not a row or a column off.
//   - A panel's origin could be left out for the mailbox and the capture
//     sheet, which float. The click is on the text where the panel drew it.
func TestDraftMouseInEveryEditor(t *testing.T) {
	base, term := approvalsClient(t)
	_ = startHeldHook(t, base, `{"hook_event_name":"PermissionRequest","session_id":"e2e-draft-mouse","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the held approval", "1 needs you", "go test")
	time.Sleep(answerSettle)

	// The reason for a rejection.
	if err := term.SendKeys("R"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the reason line", "Reason:")
	clickTypesAt(t, term, "the reason line", "too broad here", 4, "too Xbroad here")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "draft-mouse-reason")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	waitGone(t, term, "the reason line after esc", "Reason:")

	// The New agent sheet's task: a click, then a double click that
	// selects a word for typing to replace.
	if err := term.SendKeys("n"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the New agent sheet", "What should the agent do?")
	clickTypesAt(t, term, "the New agent task", "first second", 6, "first Xsecond")
	row, col := findText(t, term, "first Xsecond")
	mouseClick(t, term, col+9, row, tuitest.MouseLeft, 0)
	mouseClick(t, term, col+9, row, tuitest.MouseLeft, 0)
	if err := term.SendKeys("third"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "a word replaced in the New agent task", "first third")
	if strings.Contains(term.Screen().Text(), "Xsecond") {
		t.Errorf("ASSERTION: typing over the double click left the word\n%s", term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "draft-mouse-new-agent")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	waitGone(t, term, "the New agent sheet after esc", "What should the agent do?")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	time.Sleep(insertGuard)

	// The task capture sheet floats over the screen.
	if err := term.SendKeys(tuitest.Ctrl('b'), "+"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the task capture sheet", "New task")
	clickTypesAt(t, term, "the task capture", "fix the login page", 8, "fix the Xlogin page")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "draft-mouse-task-capture")
}

// TestDraftMouseInTheMailbox clicks in a new message in the mailbox, a
// floating panel, and types where the click put the cursor.
func TestDraftMouseInTheMailbox(t *testing.T) {
	term, base := attachClientBase(t)
	renameWindow(t, term, "WORKER")
	if out, err := tuiosCLI(t, base, "set-agent-state", "working", "-s", "e2e-ctrlp", "-w", "WORKER"); err != nil {
		t.Fatalf("set-agent-state failed: %v\n%s", err, out)
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "M"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the Inbox on mail", "No unread mail for you.")
	if err := term.SendKeys("m"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the mailbox", "new message")
	if err := term.SendKeys("n"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the agent picker", "choose an agent", "WORKER")
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the message line", "New message to WORKER")
	clickTypesAt(t, term, "the new message", "please rebase on main", 7, "please Xrebase on main")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "draft-mouse-mail")
}
