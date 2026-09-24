package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFanCompareVerifyAndDiff runs the three fan commands through the binary a
// person runs: a fan of two fake agents, one attempt that adds a file, a
// compare that counts it, a check that passes in that attempt and fails in the
// other, and a diff between the two.
func TestFanCompareVerifyAndDiff(t *testing.T) {
	base, repo := fanFixture(t)
	if out, err := tuiosCLI(t, base, "new", "plain", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "fan", "2", "--agent", "claude", "--repo", repo, "--name", "try/cmp", "Do the thing."); err != nil {
		t.Fatalf("fan: %v: %s", err, out)
	}
	var winner string
	for _, r := range worktreeRows(t, base, "--group", "try/cmp") {
		if r["session"] == "repo-try-cmp-2" {
			winner = r["path"].(string)
		}
	}
	if winner == "" {
		t.Fatal("the second attempt is not listed")
	}
	if err := os.WriteFile(filepath.Join(winner, "done.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := tuiosCLI(t, base, "fan", "compare", "repo-try-cmp")
	if err != nil {
		t.Fatalf("fan compare: %v: %s", err, out)
	}
	for _, want := range []string{"try/cmp in repo, 2 attempts against main", "repo-try-cmp-2", "1 file", "+2 -0", "0 files", "no check yet", "tuios fan keep"} {
		if !strings.Contains(out, want) {
			t.Errorf("fan compare lacks %q:\n%s", want, out)
		}
	}

	// The check passes where done.txt is and fails where it is not, and the
	// command exits 1 because one failed.
	out, err = tuiosCLI(t, base, "fan", "verify", "repo-try-cmp", "--", "test", "-f", "done.txt")
	if err == nil {
		t.Fatalf("fan verify exited 0 with a failed check:\n%s", out)
	}
	for _, want := range []string{"repo-try-cmp: failed, exit 1", "repo-try-cmp-2: passed", "1 of 2 passed."} {
		if !strings.Contains(out, want) {
			t.Errorf("fan verify lacks %q:\n%s", want, out)
		}
	}
	out, err = tuiosCLI(t, base, "fan", "compare", "repo-try-cmp", "--json", "--no-changes")
	if err != nil {
		t.Fatalf("fan compare --json: %v: %s", err, out)
	}
	var res struct {
		Rows []struct {
			Session string `json:"session"`
			Verify  struct {
				State string `json:"state"`
			} `json:"verify"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("fan compare --json did not print JSON: %v\n%s", err, out)
	}
	states := map[string]string{}
	for _, r := range res.Rows {
		states[r.Session] = r.Verify.State
	}
	if states["repo-try-cmp"] != "failed" || states["repo-try-cmp-2"] != "passed" {
		t.Errorf("verify states = %v", states)
	}

	out, err = tuiosCLI(t, base, "fan", "diff", "repo-try-cmp", "repo-try-cmp-2")
	if err != nil {
		t.Fatalf("fan diff: %v: %s", err, out)
	}
	for _, want := range []string{"diff from repo-try-cmp (try/cmp) to repo-try-cmp-2 (try/cmp-2)", "done.txt", "+one"} {
		if !strings.Contains(out, want) {
			t.Errorf("fan diff lacks %q:\n%s", want, out)
		}
	}
}
