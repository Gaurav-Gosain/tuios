package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestTaskBoardPreviewsTheFocusedCard: on the board's columns, space on a
// card shows the card whole beside the columns at 200 cells and over them at
// 110, follows the cursor and a click on another card, and closes on space,
// on esc and on a click outside the sheet.
//
// It checks: the side panel names the card and shows its prompt as Markdown
// (the heading's words with no #, the bold words with no stars), its
// attachments with their kinds and its history, while the columns keep the
// room left and count the ones behind the edge; j moves the cursor and the panel follows to the next card; a
// click on a card in another column shows that card; space closes the panel;
// at 110 cells the preview is a sheet over the columns, and a click outside
// the sheet closes it; esc closes it too.
//
// How it could pass wrongly: the Card tab of the list draws the same body,
// so the columns are checked on screen with the preview, which the list
// never shows. A preview that drew one card and never followed would pass
// the first look, so the cursor and a click each move it.
//
// Artifact: the board before the preview, the side panel at 200x55 and the sheet at 110x40, as frames.
//
// Negative controls: with kanbanKey not taking the peek key, space shows no
// panel. With drawKanbanPreview keeping the first card (pv.key never read),
// the panel does not follow the cursor.
func TestTaskBoardPreviewsTheFocusedCard(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	mustCLI(t, base, "new", "S", "--detach")
	waitForSessionInfo(t, base, "S")
	prompt := "# Retry plan\n\nAdd **jitter** to the backoff.\n\n- read api/retry.go\n- keep the old default\n"
	mustCLI(t, base, "task", "add", "-s", "S", "-p", prompt, "make the backoff jitter configurable")
	mustCLI(t, base, "task", "add", "-s", "S", "-p", "Write the notes.", "draft the rollout notes")
	mustCLI(t, base, "task", "add", "-s", "S", "--stage", "doing", "-p", "Port it.", "port the cache to lru")
	files := filepath.Join(base, "files")
	mustMkdir(files)
	notes := filepath.Join(files, "results.md")
	if err := os.WriteFile(notes, []byte("# Results\n\nAll green.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustCLI(t, base, "task", "attach", "-s", "S", "t1", notes, "--note", "the test run")
	mustCLI(t, base, "task", "attach", "-s", "S", "t1", "--link", "https://github.com/o/r/pull/12")

	term := attachIn(t, base, "S", startOpts{cols: 200, rows: 55, shippedLooks: true})
	// openBoard waits for an agent's group, and this board has no agents.
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	waitText(t, term, "the agents view", "Board")
	if err := term.SendKeys("b"); err != nil {
		t.Fatalf("press b: %v", err)
	}
	waitText(t, term, "the board's columns", "Todo  2", "Doing  1")
	saveCockpitFrames(t, term, "card-preview-before-200x55")
	if err := term.SendKeys(" "); err != nil {
		t.Fatalf("press space: %v", err)
	}
	waitText(t, term, "the preview of t1", "Card t1", "Retry plan", "Prompt", "Attachments  2", "results.md", "pull request", "History")
	s := term.Screen()
	if rowWith(s, "# Retry plan") >= 0 || rowWith(s, "**jitter**") >= 0 {
		t.Errorf("ASSERTION: the prompt shows as raw Markdown:\n%s", term.Snapshot())
	}
	// The columns keep the room left: the cursor's column and its
	// neighbours, and a count of the ones behind the edge.
	for _, head := range []string{"Todo  2", "Doing  1", "1›"} {
		if rowWith(s, head) < 0 {
			t.Errorf("ASSERTION: %q is gone with the panel open:\n%s", head, term.Snapshot())
		}
	}
	if r := rowWith(s, "Card t1"); r < 0 || strings.Index(s.Line(r), "Card t1") < 100 {
		t.Errorf("ASSERTION: the preview is not a panel on the right at 200 cells:\n%s", term.Snapshot())
	}
	saveCockpitFrames(t, term, "card-preview-side-200x55")

	// The cursor moves, and the panel follows.
	if err := term.SendKeys("j"); err != nil {
		t.Fatalf("press j: %v", err)
	}
	waitText(t, term, "the preview of t2", "Card t2", "Write the notes.")
	// A click on a card of another column shows it.
	row, col := findText(t, term, "port the cache to lru")
	clickAt(t, term, col+1, row, 1)
	waitText(t, term, "the preview of t3", "Card t3", "Port it.")
	// Space closes it.
	if err := term.SendKeys(" "); err != nil {
		t.Fatalf("press space: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return rowWith(s, "Card t3") < 0 }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: space did not close the preview: %v\n%s", err, term.Snapshot())
	}

	// At 110 cells the preview is a sheet over the columns.
	if err := term.Resize(110, 40); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if err := term.SendKeys("h", "h", " "); err != nil {
		t.Fatalf("open the sheet: %v", err)
	}
	waitText(t, term, "the sheet", "Card t1", "Retry plan")
	s = term.Screen()
	r := rowWith(s, "Card t1")
	if c := strings.Index(s.Line(r), "Card t1"); c < 0 || len([]rune(s.Line(r)[:c])) > 40 {
		t.Errorf("ASSERTION: at 110 cells the preview is not a sheet over the columns:\n%s", term.Snapshot())
	}
	saveCockpitFrames(t, term, "card-preview-sheet-110x40")
	// A click outside the sheet, on the board's header row, closes it.
	clickAt(t, term, 2, 0, 1)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return rowWith(s, "Card t1") < 0 }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a click outside the sheet did not close it: %v\n%s", err, term.Snapshot())
	}
	// Esc closes it too.
	if err := term.SendKeys(" "); err != nil {
		t.Fatalf("press space: %v", err)
	}
	waitText(t, term, "the sheet again", "Card t1")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("press esc: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return rowWith(s, "Card t1") < 0 && rowWith(s, "Todo  2") >= 0 }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: esc did not close the sheet, or closed the board: %v\n%s", err, term.Snapshot())
	}
}
