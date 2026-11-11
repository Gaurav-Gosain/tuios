package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCardPreviewKeepsItsTopWhenAnAttachmentOpens: in a side panel at 108x30
// (the docs worker's scene), a held card with a picture first and a diff
// second. Stepping from the picture to the diff opens the diff under its row,
// and the preview does not scroll, since the row is in view: the title and
// the holder stay on screen. When the preview is scrolled, its head says how
// many lines are above.
//
// The bug this holds: the step scrolled by the rows of the frame before, in
// which the picture's rows were open above the diff. With the diff read
// already, the next frame was long enough to keep that scroll, so the preview
// showed no title, stage or holder, and nothing said it was scrolled.
//
// How it could pass wrongly: a screen tall enough shows the title whatever
// the scroll, so the size is the scene's, and the check reads the scroll's
// own mark too: after J the head names the lines above.
//
// Artifact: the frame after the step, and after J.
func TestCardPreviewKeepsItsTopWhenAnAttachmentOpens(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[agents]\ncard_preview = \"side\"\ncard_preview_width = \"56\"\n")
	mustCLI(t, base, "new", "S", "--detach")
	waitForSessionInfo(t, base, "S")
	mustCLI(t, base, "new-window", "api", "-s", "S", "--", "/bin/sh", "-c", "sleep 900")
	mustCLI(t, base, "set-agent-state", "working", "-s", "S", "-w", "api", "--harness", "claude-code")
	mustCLI(t, base, "task", "add", "-s", "S", "-p", "# Retry plan\n\nAdd **jitter** to the backoff.\n", "make the backoff jitter configurable")
	mustCLI(t, base, "task", "claim", "-s", "S", "t1", "-w", "api")
	files := filepath.Join(base, "files")
	mustMkdir(files)
	pic := filepath.Join(files, "latency.png")
	if err := os.WriteFile(pic, gradientPNG(t, 480, 320), 0o644); err != nil {
		t.Fatal(err)
	}
	var diff strings.Builder
	diff.WriteString("diff --git a/retry.go b/retry.go\n--- a/retry.go\n+++ b/retry.go\n@@ -1,20 +1,20 @@\n")
	for i := range 20 {
		fmt.Fprintf(&diff, "-old line %c\n+new line %c\n", rune('a'+i), rune('a'+i))
	}
	fix := filepath.Join(files, "fix.diff")
	if err := os.WriteFile(fix, []byte(diff.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	mustCLI(t, base, "task", "attach", "-s", "S", "t1", pic, fix)

	// A host that answers kitty graphics, as the docs worker's is: the
	// picture opens as rows of its own.
	host := &lockedBuffer{}
	term := startIn(t, base, startOpts{args: []string{"attach", "S"}, cols: 108, rows: 30, shippedLooks: true, out: host,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	waitText(t, term, "the agents view", "Board")
	if err := term.SendKeys("b"); err != nil {
		t.Fatalf("press b: %v", err)
	}
	waitText(t, term, "the board's columns", "Doing  1")
	// The cursor starts on the held card's column head; j reaches the card.
	if err := term.SendKeys("l", "l", " "); err != nil {
		t.Fatalf("open the preview: %v", err)
	}
	waitText(t, term, "the preview with the picture open", "Card t1", "latency.png", "480x320")
	saveCockpitFrames(t, term, "card-preview-picture-108x30", hostPictures(t, []byte(host.String()))...)
	_ = term.WaitStable(uiTimeout)
	// The diff is read once, then the picture again, then the diff: now
	// read, it opens at once, in the frame after the picture's rows.
	if err := term.SendKeys("]"); err != nil {
		t.Fatalf("press ]: %v", err)
	}
	waitText(t, term, "the diff read", "new line a")
	if err := term.SendKeys("["); err != nil {
		t.Fatalf("press [: %v", err)
	}
	waitText(t, term, "the picture again", "480x320")
	_ = term.WaitStable(uiTimeout)
	if err := term.SendKeys("]"); err != nil {
		t.Fatalf("press ]: %v", err)
	}
	waitText(t, term, "the diff open", "new line a")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "card-preview-step-108x30", hostPictures(t, []byte(host.String()))...)
	s := term.Screen()
	// The stage line and the holder are the preview's own: the board's card
	// shows the title too.
	for _, want := range []string{"· S t1", "Held by"} {
		if rowWith(s, want) < 0 {
			t.Errorf("ASSERTION: after ] the preview scrolled past %q, though the diff's row was in view:\n%s", want, term.Snapshot())
		}
	}
	// Scrolled, the head says so.
	if err := term.SendKeys("J"); err != nil {
		t.Fatalf("press J: %v", err)
	}
	if err := term.WaitFor(func(sc tuitest.Screen) bool {
		r := rowWith(sc, "Card t1")
		return r >= 0 && strings.Contains(sc.Line(r), "above")
	}, uiTimeout); err != nil {
		t.Errorf("ASSERTION: scrolled, the preview's head does not say how many lines are above: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "card-preview-scrolled-108x30")
}
