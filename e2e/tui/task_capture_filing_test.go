package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// slowSheetClaude is fakeSheetClaude with a wait before it says it is
// ready, so start-agent, and with it the answer to a ctrl+enter on the
// capture sheet, comes about eight seconds after the file.
const slowSheetClaude = `#!/bin/sh
if [ "$1" = auth ]; then
  if [ -f "__MARK__" ]; then echo '{"loggedIn": true}'; exit 0; fi
  echo '{"loggedIn": false}'; exit 1
fi
sleep 8
"__TUIOS__" set-agent-state idle --harness claude-code -m ready
IFS= read -r line
exec sleep 600
`

// TestTaskCaptureLateAnswerKeepsTheNextSheet files a card with ctrl+enter
// and opens the capture sheet again while the agent is still starting. It
// holds:
//
//   - the sheet opened after a file is a new, empty sheet: the filed draft
//     is not offered again, so enter on it adds no second card;
//   - the answer of the first file, which comes while the person types in
//     the new sheet, leaves that sheet open with its text;
//   - a notice on the sheet goes with the next key, so a sheet with text
//     never says to write the task;
//   - enter on the new sheet then files its own card.
//
// How this could pass wrongly, written down first:
//   - The answer could come before the second sheet is open, and then it has
//     nothing to close. The fake claude waits eight seconds before it is
//     ready, and the test checks the toast is not up when the sheet opens.
//   - A key could reach the pane under the sheet and the screen still show
//     the text. The cards are read back from the daemon: two, with the two
//     titles, and no other.
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: on main the second
// open shows the filed draft, enter files it a second time, and the late
// answer closes the sheet.
func TestTaskCaptureLateAnswerKeepsTheNextSheet(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	if out, err := tuiosCLI(t, base, "new", "home", "--detach"); err != nil {
		t.Fatalf("create session home: %v\n%s", err, out)
	}
	bin := t.TempDir()
	mark := filepath.Join(bin, "signed-in")
	if err := os.WriteFile(mark, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	script := strings.NewReplacer("__MARK__", mark, "__TUIOS__", tuiosBin).Replace(slowSheetClaude)
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 120, rows: 36, shippedLooks: true,
		env: []string{"PATH=" + bin + ":/usr/bin:/bin", "TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	dir := artifactDir(t)
	const sheet = "the first line is its title"
	const toast = "claude works on it."
	has := func(s string) bool { return strings.Contains(term.Screen().Text(), s) }

	// The first card, filed with ctrl+enter: the agent starts on it.
	sendKeys(t, term, tuitest.Ctrl('b'), "+")
	waitText(t, term, "the first sheet", "New task", sheet)
	sendKeys(t, term, "First task")
	waitText(t, term, "the first title", "First task")
	sendKeys(t, term, tuitest.Key("\x1b[13;5u"))
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), sheet) }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the sheet did not close on ctrl+enter: %v\n%s", err, term.Snapshot())
	}

	// The sheet again, while claude starts.
	sendKeys(t, term, tuitest.Ctrl('b'), "+")
	waitText(t, term, "the second sheet", "New task", sheet)
	_ = term.WaitStable(uiTimeout)
	saveArtifact(t, term, dir, "second-sheet")
	if has(toast) {
		t.Fatalf("the first answer came before the second sheet opened; the test proves nothing\n%s", term.Snapshot())
	}
	if has("First task") {
		t.Errorf("ASSERTION: the second sheet offers the filed draft again\n%s", term.Snapshot())
	}
	// enter on the second sheet before it has text adds nothing, and says
	// to write the task. The next key clears that notice.
	const empty = "Write the task first."
	sendKeys(t, term, tuitest.Enter)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), empty) }, uiTimeout); err != nil {
		t.Errorf("ASSERTION: enter on the empty second sheet did not say to write the task\n%s", term.Snapshot())
	}
	sendKeys(t, term, "Second task")
	waitText(t, term, "the second title", "Second task")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), empty) }, uiTimeout); err != nil {
		t.Errorf("ASSERTION: the sheet still says %q after the task was typed\n%s", empty, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), toast) }, 60*time.Second); err != nil {
		t.Fatalf("ASSERTION: no toast says claude works on the first card: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	saveArtifact(t, term, dir, "after-the-answer")
	savePNG(t, term.Screen(), hostPalette(t, ""), dir, "after-the-answer")
	if !has(sheet) || !has("Second task") {
		t.Errorf("ASSERTION: the first card's answer closed the sheet the person types in\n%s", term.Snapshot())
	} else {
		sendKeys(t, term, tuitest.Enter)
		waitText(t, term, "the second card's toast", "Added t2 to home.")
	}

	out, err := tuiosCLI(t, base, "call", "list-tasks", `{"session":"home"}`)
	if err != nil {
		t.Fatalf("list-tasks: %v\n%s", err, out)
	}
	var res struct{ Tasks []struct{ ID, Title string } }
	if err := json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &res); err != nil {
		t.Fatalf("list-tasks answer: %v\n%s", err, out)
	}
	var titles []string
	for _, c := range res.Tasks {
		titles = append(titles, c.ID+" "+c.Title)
	}
	_ = os.WriteFile(filepath.Join(dir, "cards.txt"), []byte(strings.Join(titles, "\n")+"\n"), 0o644)
	if strings.Join(titles, ", ") != "t1 First task, t2 Second task" {
		t.Errorf("ASSERTION: home holds the cards %q, want t1 First task and t2 Second task", titles)
	}
	t.Logf("frames in %s", dir)
}
