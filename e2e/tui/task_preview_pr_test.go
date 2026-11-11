package tuie2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCardPreviewShowsTheWorktreePullRequest: a card of a worktree session
// shows the session's pull request in the card preview, with its number,
// state, checks and branch, as the daemon read it through gh.
//
// How it could pass wrongly: the rail shows the same pull request, so the
// words are read on the preview's row: "Pull request #7 open" is the
// preview's wording, not the rail's badge ("PR #7 open").
//
// Artifact: the preview with the pull request, as a frame.
//
// Negative control: with ckCardBody not reading ckCardWorktree, the line
// never shows.
func TestCardPreviewShowsTheWorktreePullRequest(t *testing.T) {
	f := newShipFixture(t)
	base := f.base
	mustCLI(t, base, "task", "add", "-s", f.session, "-p", "Ship the note.", "ship the note")
	if err := os.WriteFile(filepath.Join(f.fakes, "pr.json"), []byte(shipCannedPass), 0o644); err != nil {
		t.Fatal(err)
	}
	mustCLI(t, base, "ship", "status", "-s", f.session, "--refresh")

	term := attachIn(t, base, f.session, startOpts{cols: 200, rows: 55, shippedLooks: true})
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
	waitText(t, term, "the pull request in the preview", "Card t1", "Pull request #7 open", "checks pass", "feat/ship")
	saveCockpitFrames(t, term, "card-preview-pr-200x55")
}
