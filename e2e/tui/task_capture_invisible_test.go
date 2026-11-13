package tuie2e

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestTaskCaptureStopsOnInvisibleCharacters files task cards from the New
// task sheet, each draft pasted, in a session whose pane works in a folder of
// no repository, with no agent on the PATH. It holds:
//
//   - a plain draft is added on the first enter, its first line the title
//     and the rest the prompt;
//   - a title that ends in 45 invisible tag characters, and is over 200 bytes
//     only because of them, stops the first enter: the sheet stays open and
//     says "Removed 45 invisible characters" once, not that the title is too
//     long, and no card is added; the next enter adds the card with the title
//     and the prompt as shown;
//   - a prompt that ends in a zero-width space and the 45 tags stops enter on
//     the Project pill the same way, and the notice shows, since the focus
//     goes back to the task;
//   - ctrl+enter on a pill with the tags in the title stops before the sheet
//     says no agent is installed; the next ctrl+enter says it, and enter adds
//     the card;
//   - a draft whose first line is empty and whose second line ends in a
//     zero-width space stops the first enter with "Removed 1 invisible
//     character"; the next enter says to write the task first, and no card
//     is added.
//
// At every stop the composer's notice is the one notice: the sheet's own
// line is empty, so it does not also say to write the task first.
//
// How this could pass wrongly, written down first:
//   - A key could be sent before the paste landed, and the press file a
//     draft without the characters. The test waits for the pasted words on
//     the screen before the next key.
//   - The notice of an earlier row could still be on the screen. Each row
//     opens a new sheet after a filed card, and the count differs between the
//     title row and the prompt row.
//   - A card could be read before the daemon kept it. The toast is waited
//     for first, and each card is read back with get-task; the card count is
//     held for a second after each stop.
//   - The sheet's line could be drawn a frame after the composer's notice.
//     Both are set by the same key, in one update, so the frame that shows
//     the notice shows the line too.
//
// Negative controls, recorded in NEGATIVE_CONTROLS.md.
func TestTaskCaptureStopsOnInvisibleCharacters(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	work := t.TempDir()
	mustCLI(t, base, "new", "S", "--detach", "--cwd", work)
	waitForSessionInfo(t, base, "S")
	// The suite's own tuios comes first, so the PATH holds no agent and no
	// other tuios.
	path := ownTuiosDir + string(os.PathListSeparator) + agentFreePath("/usr/bin", "/bin")
	term := attachIn(t, base, "S", startOpts{cols: 160, rows: 45, shippedLooks: true,
		env: []string{"PATH=" + path, "TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0"}})
	const sheet = "the first line is its title"
	ctrlEnter := tuitest.Key("\x1b[13;5u")

	open := func(row string) {
		t.Helper()
		sendKeys(t, term, tuitest.Ctrl('b'), "+")
		waitText(t, term, "the sheet of row "+row, "New task", sheet, "None, this session", "none installed")
	}
	// paste pastes text into the task and waits for the screen to show the
	// visible words of it.
	paste := func(text string, visible ...string) {
		t.Helper()
		if err := term.Paste(text); err != nil {
			t.Fatal(err)
		}
		waitText(t, term, "the pasted draft", visible...)
	}
	closed := func(what string) {
		t.Helper()
		if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), sheet) }, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: %s did not close the sheet: %v\n%s", what, err, term.Snapshot())
		}
	}
	// cards is the cards of S, as list-tasks has them.
	cards := func() []string {
		t.Helper()
		out, err := tuiosCLI(t, base, "call", "list-tasks", `{"session":"S"}`)
		if err != nil {
			t.Fatalf("list-tasks: %v\n%s", err, out)
		}
		var res struct{ Tasks []struct{ ID, Title string } }
		if err := json.Unmarshal([]byte(out[max(strings.Index(out, "{"), 0):]), &res); err != nil {
			t.Fatalf("list-tasks answer: %v\n%s", err, out)
		}
		var ids []string
		for _, c := range res.Tasks {
			ids = append(ids, c.ID)
		}
		return ids
	}
	// card is the title and the prompt of card id, as get-task has them.
	card := func(id string) (string, string) {
		t.Helper()
		out, err := tuiosCLI(t, base, "call", "get-task", `{"session":"S","id":"`+id+`"}`)
		if err != nil {
			t.Fatalf("get-task %s: %v\n%s", id, err, out)
		}
		var res struct{ Title, Prompt string }
		if err := json.Unmarshal([]byte(out[max(strings.Index(out, "{"), 0):]), &res); err != nil {
			t.Fatalf("get-task %s answer: %v\n%s", id, err, out)
		}
		return res.Title, res.Prompt
	}
	// held holds the count of S's cards at n for a second.
	held := func(what string, n int) {
		t.Helper()
		stayTrue(t, what+": S holds "+strconv.Itoa(n)+" cards", time.Second, func() (bool, string) {
			got := cards()
			return len(got) == n, strings.Join(got, ", ")
		})
	}
	// removedOnce waits for the stop's notice and counts it on the screen.
	// The sheet's own line is empty at a stop: it does not also say to write
	// the task first.
	removedOnce := func(n int) string {
		t.Helper()
		notice := "Removed " + strconv.Itoa(n) + " invisible character"
		if n != 1 {
			notice += "s"
		}
		waitText(t, term, "the notice "+strconv.Quote(notice), notice)
		text := term.Screen().Text()
		if c := strings.Count(text, notice); c != 1 {
			t.Errorf("ASSERTION: the screen shows %q %d times, want once\n%s", notice, c, term.Snapshot())
		}
		if !strings.Contains(text, sheet) {
			t.Fatalf("ASSERTION: the sheet closed at the stop\n%s", term.Snapshot())
		}
		if strings.Contains(text, "Write the task first") {
			t.Errorf("ASSERTION: the stop also says to write the task first\n%s", term.Snapshot())
		}
		return text
	}
	filed := func(id, wantTitle, wantPrompt string) {
		t.Helper()
		if title, prompt := card(id); title != wantTitle || prompt != wantPrompt {
			t.Errorf("ASSERTION: %s is title %q, prompt %q; want title %q, prompt %q", id, title, prompt, wantTitle, wantPrompt)
		}
	}

	// P: a plain draft is added on the first enter.
	open("P")
	paste("Write the release notes.\nList the user-facing changes.", "Write the release notes.", "List the user-facing changes.")
	copiesFrame(t, term, "task-invisible-sheet")
	sendKeys(t, term, tuitest.Enter)
	closed("enter on a plain draft")
	waitText(t, term, "the toast of t1", "Added t1 to S.")
	filed("t1", "Write the release notes.", "List the user-facing changes.")

	// T: the tags in the title, enter in the task.
	open("T")
	paste("Fix the docs typo today."+invisibleTail+"\nCheck the page.", "Fix the docs typo today.", "Check the page.")
	sendKeys(t, term, tuitest.Enter)
	text := removedOnce(45)
	copiesFrame(t, term, "task-invisible-title-stopped")
	if strings.Contains(text, "longer than 200 bytes") {
		t.Errorf("ASSERTION: the stop says the title is too long\n%s", term.Snapshot())
	}
	held("the stop of the title", 1)
	sendKeys(t, term, tuitest.Enter)
	closed("the second enter on the title")
	waitText(t, term, "the toast of t2", "Added t2 to S.")
	filed("t2", "Fix the docs typo today.", "Check the page.")

	// R: the zero-width space and the tags in the prompt, enter on the
	// Project pill.
	open("R")
	paste("Update the changelog.\nName the fix.\u200b"+invisibleTail, "Update the changelog.", "Name the fix.")
	sendKeys(t, term, tuitest.Tab)
	sendKeys(t, term, tuitest.Enter)
	removedOnce(46)
	copiesFrame(t, term, "task-invisible-prompt-stopped")
	held("the stop of the prompt", 2)
	sendKeys(t, term, tuitest.Enter)
	closed("the second enter on the prompt")
	waitText(t, term, "the toast of t3", "Added t3 to S.")
	filed("t3", "Update the changelog.", "Name the fix.")

	// S: the tags in a title under the limit, ctrl+enter on the Project pill
	// with no agent installed.
	open("S")
	paste("Fix the docs typo."+invisibleTail, "Fix the docs typo.")
	sendKeys(t, term, tuitest.Tab)
	sendKeys(t, term, ctrlEnter)
	text = removedOnce(45)
	copiesFrame(t, term, "task-invisible-start-stopped")
	if strings.Contains(text, "No agent is installed") {
		t.Errorf("ASSERTION: the stop says no agent is installed\n%s", term.Snapshot())
	}
	held("the stop of ctrl+enter", 3)
	sendKeys(t, term, ctrlEnter)
	waitText(t, term, "the second ctrl+enter's refusal", "No agent is installed on this machine to start.")
	if got := cards(); len(got) != 3 {
		t.Fatalf("ASSERTION: the refused ctrl+enter left S with the cards %q, want 3", got)
	}
	sendKeys(t, term, tuitest.Enter)
	closed("enter on the Agent pill")
	waitText(t, term, "the toast of t4", "Added t4 to S.")
	filed("t4", "Fix the docs typo.", "")

	// C: no title, and a zero-width space at the end of the prompt.
	open("C")
	paste("\nName the fix.\u200b", "Name the fix.")
	sendKeys(t, term, tuitest.Enter)
	removedOnce(1)
	sendKeys(t, term, tuitest.Enter)
	waitText(t, term, "the next enter's refusal", "Write the task first.")
	held("the stop and the refusal of no title", 4)
}
