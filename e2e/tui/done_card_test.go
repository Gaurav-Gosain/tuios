package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// doneCardLastSaid is the end of a turn longer than a one-line message, with
// no space at the cut: the old clip cut "vanishing" in the middle.
const doneCardLastSaid = "A thread image card that the window cuts now shows its visible part, cropped, instead of vanishing from the thread"

// TestDoneCardDismissAndRecapLines finishes a turn of a Claude Code stand-in
// through the real agent-hook, opens the agents view on it and reads the
// Ready for review card: the file it wrote is named from the pane's folder,
// the test run by its command without flags or shell plumbing, and the last
// words end at a whole word. A click on the card's cross puts the card away
// and leaves the agent's row; the card stays away until the next turn ends,
// and the dismiss key shown beside the cross puts that one away too. Frames
// of the card before and after are saved under artifactDir.
//
// How it could pass wrongly, written down first:
//   - The cross could be drawn and the click land on the row under it, or on
//     nothing. The test clicks the cross's own cell and waits for the card
//     to go. The thread above the card says the same recap lines, so the
//     card is known by its cross.
//   - The click could settle the agent or hide its row. The row must stay.
//   - The card could go for a frame and come back from the next delivery.
//     The test waits past a refresh with the card still gone.
//   - Dismissing could stop every later card. The next turn's card must
//     show, and its key must put it away.
//   - The folder could be cut off by a shorter path that only shares a
//     prefix (repo vs repo2). The file is under the pane's folder itself.
//   - The test name could keep a flag or the pipe. The line must read
//     "go test ./internal/app passed" and hold no "2>&1" and no "-count".
//   - Last said could still end mid-word. The word before the ellipsis must
//     be a whole word of the turn's text.
func TestDoneCardDismissAndRecapLines(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	repo := filepath.Join(base, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "e2e", "tui"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "new", "home", "--detach"); err != nil {
		t.Fatalf("create session home: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new", "docs", "--detach", "--cwd", repo); err != nil {
		t.Fatalf("create session docs: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "docs", "-w", "0", "--name", "docs-cli"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	hook := func(payload string) {
		t.Helper()
		cmd := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "docs", "--window", "0", "--timeout", "10s")
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
		for _, key := range xdgKeys {
			cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
		}
		cmd.Stdin = strings.NewReader(payload)
		if out, err := cmd.CombinedOutput(); err != nil || len(out) != 0 {
			t.Fatalf("the hook for %s failed or printed: %v\n%s", payload, err, out)
		}
	}
	file := filepath.Join(repo, "e2e", "tui", "cockpit_thread_image_crop_test.go")
	turn := func(said string) {
		t.Helper()
		hook(`{"hook_event_name":"UserPromptSubmit","session_id":"e2e-done","prompt":"crop the image card"}`)
		hook(`{"hook_event_name":"PreToolUse","session_id":"e2e-done","tool_name":"Write","tool_input":{"file_path":"` + file + `"}}`)
		hook(`{"hook_event_name":"PostToolUse","session_id":"e2e-done","tool_name":"Write","tool_input":{"file_path":"` + file + `"},"tool_response":{}}`)
		hook(`{"hook_event_name":"PreToolUse","session_id":"e2e-done","tool_name":"Bash","tool_input":{"command":"go test -count=1 ./internal/app/ 2>&1 | tail -5"}}`)
		hook(`{"hook_event_name":"PostToolUse","session_id":"e2e-done","tool_name":"Bash","tool_input":{"command":"go test -count=1 ./internal/app/ 2>&1 | tail -5"},"tool_response":{"stdout":"ok","stderr":"","interrupted":false}}`)
		hook(`{"hook_event_name":"Stop","session_id":"e2e-done","last_assistant_message":"` + said + `"}`)
	}
	turn(doneCardLastSaid)

	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 160, rows: 45, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the Done card", "Done", "docs-cli", "Last said:", "Tests:")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "done-card-160x45")
	text := term.Screen().Text()
	if !strings.Contains(text, "1 file: e2e/tui/cockpit_thread_image_crop_test.go") || strings.Contains(text, repo) {
		t.Errorf("ASSERTION: the file is not named from the pane's folder %s\n%s", repo, term.Snapshot())
	}
	if !strings.Contains(text, "Tests: go test ./internal/app passed") || strings.Contains(text, "2>&1") || strings.Contains(text, "-count") {
		t.Errorf("ASSERTION: the test run is not named short\n%s", term.Snapshot())
	}
	// The card is the last thing on screen to say Last said; the thread
	// above it says the same.
	said := text[strings.LastIndex(text, "Last said:"):]
	if i := strings.Index(said, "..."); i < 0 {
		t.Errorf("ASSERTION: the last words are not cut\n%s", term.Snapshot())
	} else {
		words := strings.Fields(said[:i])
		last := words[len(words)-1]
		if !slices.Contains(strings.Fields(doneCardLastSaid), last) {
			t.Errorf("ASSERTION: Last said ends in %q, not a whole word of the turn\n%s", last, term.Snapshot())
		}
	}

	// The cross on the card's head, then the card is gone and the row is
	// not.
	row, col := findText(t, term, "×")
	if hr, _ := findText(t, term, "dismiss"); hr != row {
		t.Errorf("ASSERTION: the dismiss key is not beside the cross (rows %d and %d)\n%s", hr, row, term.Snapshot())
	}
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	waitGone(t, term, "the card after a click on its cross", "×")
	waitText(t, term, "the agent's row after the dismiss", "docs-cli")
	time.Sleep(2 * time.Second)
	if strings.Contains(term.Screen().Text(), "×") {
		t.Fatalf("ASSERTION: the dismissed card came back in the same turn\n%s", term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "done-card-dismissed-160x45")

	// The next turn brings a new card, and the key shown beside the cross
	// puts it away.
	turn("The second turn is done.")
	waitText(t, term, "the next turn's card", "Last said: The second turn is done.", "×")
	if err := term.SendKeys("d"); err != nil {
		t.Fatal(err)
	}
	waitGone(t, term, "the card after its key", "×")
	waitText(t, term, "the agent's row after the key", "docs-cli")
}
