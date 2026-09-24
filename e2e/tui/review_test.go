package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestReviewNotesReachTheAgent runs tuios review through the binary a person
// runs: a fan of two fake agents, one attempt that changes a file, a review
// that shows the change against the fan's base, two notes, and a send that
// types both into the agent as one message once it rests. The repository's
// status is the same before and after the review.
//
// Negative control: with the send-review handler answering before it queues,
// the wait for the agent to receive the notes times out.
func TestReviewNotesReachTheAgent(t *testing.T) {
	base, repo := fanFixture(t)
	if out, err := tuiosCLI(t, base, "new", "plain", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "fan", "2", "--agent", "claude", "--repo", repo, "--name", "try/rv", "Do the thing."); err != nil {
		t.Fatalf("fan: %v: %s", err, out)
	}
	const session = "repo-try-rv-2"
	var path string
	deadline := time.Now().Add(30 * time.Second)
	for {
		sent := false
		for _, r := range worktreeRows(t, base, "--group", "try/rv") {
			if r["session"] == session {
				path, _ = r["path"].(string)
				sent = r["prompt_status"] == "sent"
			}
		}
		if sent {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the first prompt was never sent to %s", session)
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(path, "README"), []byte("hello\nretry three times\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status := testutil.Git(t, path, "status", "--porcelain")

	out, err := tuiosCLI(t, base, "review", session)
	if err != nil {
		t.Fatalf("review: %v: %s", err, out)
	}
	for _, want := range []string{"against main", "1 file, +1 -0", "M  README", "+ retry three times"} {
		if !strings.Contains(out, want) {
			t.Errorf("tuios review lacks %q:\n%s", want, out)
		}
	}
	if got := testutil.Git(t, path, "status", "--porcelain"); got != status {
		t.Errorf("tuios review changed git status from %q to %q", status, got)
	}

	for _, args := range [][]string{
		{"review", "note", "-s", session, "README:2", "say", "why", "three"},
		{"review", "note", "-s", session, "--hunk", "@@ -1 +1,2 @@", "README", "add", "a", "test"},
	} {
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v: %s", args, err, out)
		}
	}
	out, err = tuiosCLI(t, base, "review", "notes", "-s", session, "--json")
	if err != nil {
		t.Fatalf("review notes: %v: %s", err, out)
	}
	var notes struct {
		Notes []struct {
			Quote string `json:"quote"`
			By    string `json:"by"`
		} `json:"notes"`
	}
	if err := json.Unmarshal([]byte(out), &notes); err != nil || len(notes.Notes) != 2 {
		t.Fatalf("review notes --json = %s (%v)", out, err)
	}

	out, err = tuiosCLI(t, base, "review", "send", "-s", session)
	if err != nil || !strings.Contains(out, "2 review notes in one message") {
		t.Fatalf("review send: %v: %s", err, out)
	}
	deadline = time.Now().Add(30 * time.Second)
	for {
		pane, _ := tuiosCLI(t, base, "capture-pane", "-s", session)
		if strings.Contains(pane, "Review notes on your changes (vs main), from a script:") &&
			strings.Contains(pane, "say why three") && strings.Contains(pane, "add a test") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the agent never received the notes:\n%s", pane)
		}
		time.Sleep(500 * time.Millisecond)
	}
	out, err = tuiosCLI(t, base, "review", "notes", "-s", session)
	if err != nil || strings.Count(out, "sent ") != 2 {
		t.Errorf("after the send, review notes = %v: %s", err, out)
	}
}
