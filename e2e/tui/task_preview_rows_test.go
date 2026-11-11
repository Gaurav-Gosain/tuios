package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCardPreviewOpensTheSelectedAttachmentUnderItsRow: with eleven
// attachments on a card, each is one row in the preview, and the selected
// one opens right under its row with its note, so the last one's body shows
// without scrolling. tuios task get lists the attachments in columns.
//
// How it could pass wrongly: the body could show below the whole list and
// still be on screen on a tall terminal, so the body's line must be the row
// right after the selected row's note. The columns could line up by chance
// for names of one length, so the names differ in length.
//
// Artifact: the preview with the last attachment open, before (the list
// with the body below it) and after, as frames.
//
// Negative control: on the branch before this one, two rows per attachment
// put the last body below the fold, and the check that it shows under its
// row fails.
func TestCardPreviewOpensTheSelectedAttachmentUnderItsRow(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	mustCLI(t, base, "new", "S", "--detach")
	waitForSessionInfo(t, base, "S")
	mustCLI(t, base, "task", "add", "-s", "S", "-p", "Show every step.", "show the steps")
	files := filepath.Join(base, "files")
	mustMkdir(files)
	var paths []string
	for i := range 10 {
		name := fmt.Sprintf("step-%s%d.log", strings.Repeat("x", i), i)
		p := filepath.Join(files, name)
		if err := os.WriteFile(p, fmt.Appendf(nil, "log of step %d\n", i), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	mustCLI(t, base, append([]string{"task", "attach", "-s", "S", "t1"}, paths...)...)
	last := filepath.Join(files, "summary.md")
	if err := os.WriteFile(last, []byte("# Summary\n\nEvery step-body-marker passed.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustCLI(t, base, "task", "attach", "-s", "S", "t1", last, "--note", "what the steps add up to")

	// task get lines the attachments up in columns.
	got := mustCLI(t, base, "task", "get", "-s", "S", "t1")
	var cols []int
	for line := range strings.SplitSeq(got, "\n") {
		if before, _, ok := strings.Cut(line, "text, "); ok && strings.Contains(line, "step-") {
			cols = append(cols, len([]rune(before)))
		}
	}
	for _, c := range cols {
		if c != cols[0] {
			t.Errorf("ASSERTION: task get's attachment kinds start at columns %v, want one column:\n%s", cols, got)
			break
		}
	}

	term := attachIn(t, base, "S", startOpts{cols: 200, rows: 40, shippedLooks: true})
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	waitText(t, term, "the agents view", "Board")
	if err := term.SendKeys("b"); err != nil {
		t.Fatalf("press b: %v", err)
	}
	waitText(t, term, "the board's columns", "Todo  1")
	if err := term.SendKeys(" "); err != nil {
		t.Fatalf("press space: %v", err)
	}
	waitText(t, term, "the preview", "Card t1", "Attachments  11")
	// [ from the first steps back to the last attachment.
	if err := term.SendKeys("["); err != nil {
		t.Fatalf("press [: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		row, note, body := rowWith(s, "summary.md"), rowWith(s, "what the steps add up to"), rowWith(s, "step-body-marker")
		return row >= 0 && note == row+1 && body > note && body <= note+4
	}, uiTimeout); err != nil {
		saveCockpitFrames(t, term, "card-preview-rows-200x40")
		t.Fatalf("ASSERTION: the last attachment did not open under its row, on screen: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "card-preview-rows-200x40")
}
