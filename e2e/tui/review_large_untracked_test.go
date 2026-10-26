package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestReviewDoesNotReadLargeUntrackedFiles sets max_untracked_mb = 1 and
// routes *.bin through a clean filter that logs each file it is run on, the
// way Git LFS's filter copies each file into .git/lfs/objects. The pane's
// repository holds a 2 MiB untracked data/large.bin and a small small.bin.
// tuios review lists large.bin as untracked and not read, with no counts; the
// filter never ran on it and git's object store never got it, while small.bin
// went through the filter and has its counts.
//
// Before the fix every review staged every untracked file. In a repository
// that LFS-tracks *.db3 and holds untracked multi-gigabyte recordings, each
// review copied them into .git/lfs/objects, and each review its timeout cut
// off left a partial copy under a valid object id.
//
// Negative control (NEGATIVE_CONTROLS.md): with Build passing no limit to
// SnapshotTree, large.bin has counts, the filter ran on it, and the object
// store holds it.
func TestReviewDoesNotReadLargeUntrackedFiles(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[agents.checkpoints]\nmax_untracked_mb = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := testutil.GitRepo(t)
	filtered := filepath.Join(base, "filtered.log")
	testutil.Git(t, repo, "config", "--global", "filter.probe.clean", "echo %f >> '"+filtered+"'; cat")
	if err := os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("*.bin filter=probe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo, "add", ".gitattributes")
	testutil.Git(t, repo, "commit", "-q", "-m", "attributes")
	if out, err := tuiosCLIIn(t, base, repo, "new", "agent", "--detach"); err != nil {
		t.Fatalf("new session in the repository: %v: %s", err, out)
	}

	large := filepath.Join(repo, "data", "large.bin")
	if err := os.MkdirAll(filepath.Dir(large), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(large, []byte(strings.Repeat("0123456789abcdef", 2<<16)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "small.bin"), []byte("small\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filtered)

	out, err := tuiosCLI(t, base, "review", "agent", "--stat")
	if err != nil {
		t.Fatalf("review: %v: %s", err, out)
	}
	if !strings.Contains(out, "small.bin") || !strings.Contains(out, "+1 -0") {
		t.Errorf("tuios review --stat lacks small.bin with its counts:\n%s", out)
	}
	largeLine := ""
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "data/large.bin") {
			largeLine = line
		}
	}
	if !strings.Contains(largeLine, "not read") {
		t.Errorf("tuios review --stat does not list data/large.bin as not read:\n%s", out)
	}

	out, err = tuiosCLI(t, base, "review", "agent", "--json")
	if err != nil {
		t.Fatalf("review --json: %v: %s", err, out)
	}
	var res struct {
		Files []struct {
			Path    string `json:"path"`
			Status  string `json:"status"`
			Added   int    `json:"added"`
			Skipped bool   `json:"skipped"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("review --json: %v\n%s", err, out)
	}
	found := false
	for _, f := range res.Files {
		if f.Path == "data/large.bin" {
			found = true
			if !f.Skipped || f.Status != "U" || f.Added != 0 {
				t.Errorf("data/large.bin = %+v, want skipped, U, no counts", f)
			}
		}
	}
	if !found {
		t.Errorf("review --json leaves data/large.bin out of the files instead of listing it as skipped:\n%s", out)
	}

	log, _ := os.ReadFile(filtered)
	if !strings.Contains(string(log), "small.bin") {
		t.Errorf("the clean filter never ran on small.bin, so the probe proves nothing; its log:\n%s", log)
	}
	if strings.Contains(string(log), "large.bin") {
		t.Errorf("the review ran the clean filter on the 2 MiB untracked file; its log:\n%s", log)
	}
	sha := testutil.Git(t, repo, "hash-object", "--no-filters", large)
	if err := exec.Command("git", "-C", repo, "cat-file", "-e", sha).Run(); err == nil {
		t.Errorf("the object store holds the large file's blob %s, so the review read and wrote it", sha[:7])
	}
	ran := strings.ReplaceAll(strings.TrimSpace(string(log)), "\n", ", ")
	t.Logf("the 2 MiB untracked file was listed as not read; the clean filter ran on: %s", ran)
}
