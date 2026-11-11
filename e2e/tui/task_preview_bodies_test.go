package tuie2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestTaskBoardPreviewShowsEachAttachment: the card preview shows the
// selected attachment's body, one kind after another as ] steps them: a
// picture with its size, a Markdown file as Markdown, a diff, a Mermaid
// diagram drawn by the sketch plugin or, without the plugin, its source (a
// node's word, which no name holds), and a pull request with its state,
// title and checks from a stub gh. o on the picture opens the image view.
//
// How it could pass wrongly: the attachments' names show in the list
// whatever the body does, so each check reads a word only the body holds:
// the Markdown's heading without its #, a line of the diff, the picture's
// pixel size, the pull request's title.
//
// Artifact: a frame per kind at 200x55.
//
// Negative controls: with ckCardBody not drawing ckAttachmentBody, the
// Markdown check fails. With get-task-attachment not reading the pr link,
// the pull request check fails.
func TestTaskBoardPreviewShowsEachAttachment(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	fakes := filepath.Join(t.TempDir(), "fakes")
	mustMkdir(fakes)
	const pr = "https://github.com/o/r/pull/7"
	gh := "#!/bin/sh\ncase \"$1 $2\" in\n\"pr view\") echo '{\"number\":7,\"url\":\"" + pr +
		"\",\"state\":\"OPEN\",\"title\":\"Fix the retry layout\",\"statusCheckRollup\":[{\"__typename\":\"CheckRun\",\"status\":\"COMPLETED\",\"conclusion\":\"SUCCESS\"}]}'; exit 0;;\nesac\nexit 2\n"
	if err := os.WriteFile(filepath.Join(fakes, "gh"), []byte(gh), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakes+string(os.PathListSeparator)+os.Getenv("PATH"))
	mustCLI(t, base, "new", "S", "--detach")
	waitForSessionInfo(t, base, "S")
	mustCLI(t, base, "task", "add", "-s", "S", "-p", "Show the proof.", "show every kind")

	files := filepath.Join(base, "files")
	mustMkdir(files)
	pic := filepath.Join(files, "after.png")
	writeTestPNG(t, pic)
	write := func(name, body string) string {
		p := filepath.Join(files, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	md := write("results.md", "# Results summary\n\nEvery **retry** test passed.\n")
	diff := write("change.diff", "diff --git a/retry.go b/retry.go\n--- a/retry.go\n+++ b/retry.go\n@@ -1,2 +1,2 @@\n-const jitter = 0\n+const jitter = 0.2\n")
	flow := write("flow.mmd", "graph TD\n  A[Start] --> B[Retry]\n")
	for _, f := range []string{pic, md, diff, flow} {
		mustCLI(t, base, "task", "attach", "-s", "S", "t1", f)
	}
	mustCLI(t, base, "task", "attach", "-s", "S", "t1", "--link", pr)

	term := attachIn(t, base, "S", startOpts{cols: 200, rows: 55, shippedLooks: true})
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
	waitText(t, term, "the picture", "Attachments  5", "32x16")
	saveCockpitFrames(t, term, "card-preview-picture-200x55")

	steps := []struct{ what, frame string }{
		{"Results summary", "markdown"},
		{"const jitter = 0.2", "diff"},
		{"Start", "mermaid"},
		{"Fix the retry layout", "pr"},
	}
	for _, st := range steps {
		if err := term.SendKeys("]"); err != nil {
			t.Fatalf("press ]: %v", err)
		}
		waitText(t, term, "the "+st.frame+" attachment", st.what)
		saveCockpitFrames(t, term, "card-preview-"+st.frame+"-200x55")
	}
	waitText(t, term, "the pull request's state", "#7 open", "checks pass")

	// Back to the picture, and o opens it in the image view.
	if err := term.SendKeys("]"); err != nil {
		t.Fatalf("press ]: %v", err)
	}
	waitText(t, term, "the picture again", "32x16")
	if err := term.SendKeys("o"); err != nil {
		t.Fatalf("press o: %v", err)
	}
	waitText(t, term, "the image view", "attached to t1")
}
