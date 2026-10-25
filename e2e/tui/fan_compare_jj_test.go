package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestFanCompareJJ fans two fake agents out over a jj repository with no
// ".git", one attempt commits a file and leaves another uncommitted, and fan
// compare counts each attempt from where it left its base to the work. In jj
// the work is in @, which git has no name for, and the base is a bookmark
// git cannot resolve without jj's git directory. main moves on while the
// attempt works, and the counts still run from the fork point.
//
// Negative control: with fanRowChanges merging from "HEAD" again instead of
// the revisions worktree.WorkRevs names, the attempts are counted against
// main's new tip: the untouched one shows 1 file, the working one 3.
func TestFanCompareJJ(t *testing.T) {
	base, _ := fanFixture(t)
	repo := testutil.JJRepo(t, false)
	if out, err := tuiosCLI(t, base, "new", "plain", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "fan", "2", "--agent", "claude", "--repo", repo, "--name", "try/jj", "Do the thing."); err != nil {
		t.Fatalf("fan: %v: %s", err, out)
	}
	var path string
	for _, r := range worktreeRows(t, base, "--group", "try/jj") {
		if r["session"] == "repo-try-jj-2" {
			path, _ = r["path"].(string)
		}
	}
	if path == "" {
		t.Fatal("the second attempt is not listed")
	}
	if _, err := os.Stat(filepath.Join(path, ".jj")); err != nil {
		t.Fatalf("precondition: the attempt at %s is not a jj workspace: %v", path, err)
	}
	if err := os.WriteFile(filepath.Join(path, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.JJ(t, path, "commit", "-m", "a")
	if err := os.WriteFile(filepath.Join(path, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The person commits on main meanwhile: the attempt is still counted
	// from where it left main, not from main's new tip.
	if err := os.WriteFile(filepath.Join(repo, "person.txt"), []byte("p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.JJ(t, repo, "commit", "-m", "The person's file")
	testutil.JJ(t, repo, "bookmark", "set", "main", "-r", "@-")

	out, err := tuiosCLI(t, base, "fan", "compare", "repo-try-jj", "--json")
	if err != nil {
		t.Fatalf("fan compare: %v: %s", err, out)
	}
	saveText(t, "fan-compare-jj", out)
	var res struct {
		Rows []struct {
			Session string `json:"session"`
			Note    string `json:"note"`
			Files   *int   `json:"files"`
			Ahead   *int   `json:"ahead"`
			Dirty   *bool  `json:"dirty"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("fan compare --json did not print JSON: %v\n%s", err, out)
	}
	seen := 0
	for _, r := range res.Rows {
		files, ahead := -1, -1
		if r.Files != nil {
			files = *r.Files
		}
		if r.Ahead != nil {
			ahead = *r.Ahead
		}
		switch r.Session {
		case "repo-try-jj":
			seen++
			if r.Note != "" || files != 0 {
				t.Errorf("ASSERTION: the untouched attempt: note %q, %d files, want no note and 0 files", r.Note, files)
			}
		case "repo-try-jj-2":
			seen++
			// a.txt in a, b.txt in @: two commits ahead, both files changed.
			if r.Note != "" || files != 2 || ahead != 2 || r.Dirty == nil || !*r.Dirty {
				t.Errorf("ASSERTION: the working attempt: note %q, %d files, %d ahead, dirty %v; want no note, 2 files, 2 ahead, dirty", r.Note, files, ahead, r.Dirty)
			}
		}
	}
	if seen != 2 {
		t.Errorf("fan compare listed %d of the 2 attempts:\n%s", seen, out)
	}
	if text, err := tuiosCLI(t, base, "fan", "compare", "repo-try-jj"); err != nil || !strings.Contains(text, "2 files") {
		t.Errorf("ASSERTION: fan compare's table lacks the attempt's 2 files: %v\n%s", err, text)
	}
}
