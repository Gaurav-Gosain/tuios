package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// worktree ls reports a branch as merged once its base holds the branch's
// commits, by whichever way they got there: a merge commit, a fast-forward, a
// squash, or a push to the base's upstream. A branch with no commits of its
// own is never merged, which is the half of the rule the fresh worktree
// checks. Every case is first seen as not merged, so a true answer is the
// merge's doing and not the fixture's.
//
// The transcript of every tuios call is saved as transcript.txt in the test's
// artifact directory.
func TestWorktreeLsMerged(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.GitRepo(t)
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(base, "worktrees"))

	// An origin, so main has an upstream for the last case.
	origin := filepath.Join(t.TempDir(), "origin.git")
	testutil.Git(t, repo, "init", "-q", "--bare", "-b", "main", origin)
	testutil.Git(t, repo, "remote", "add", "origin", origin)
	testutil.Git(t, repo, "push", "-q", "-u", "origin", "main")

	tr := newCheckpointTranscript(t, base, repo)
	tr.ok("new", "main", "--detach")

	type wt struct{ branch, session, dir string }
	mk := func(branch string, withBase bool) wt {
		args := []string{"worktree", "new", branch, "--repo", repo, "--detach"}
		if withBase {
			args = append(args, "--base", "main")
		}
		tr.ok(args...)
		slug := strings.ReplaceAll(branch, "/", "-")
		return wt{branch, "repo-" + slug, filepath.Join(base, "worktrees", "repo", slug)}
	}
	commit := func(w wt) {
		name := strings.ReplaceAll(w.branch, "/", "-") + ".txt"
		if err := os.WriteFile(filepath.Join(w.dir, name), []byte(w.branch+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		testutil.Git(t, w.dir, "add", name)
		testutil.Git(t, w.dir, "commit", "-q", "-m", "work on "+w.branch)
		tr.log("committed %s on %s", name, w.branch)
	}
	type row struct {
		Session    string `json:"session"`
		Merged     *bool  `json:"merged"`
		MergedInto string `json:"merged_into"`
	}
	list := func() map[string]row {
		out := tr.ok("worktree", "ls", "--json")
		var rows []row
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatalf("worktree ls --json printed no JSON: %v\n%s", err, out)
		}
		m := map[string]row{}
		for _, r := range rows {
			m[r.Session] = r
		}
		return m
	}
	want := func(w wt, merged bool, into string) {
		t.Helper()
		r, ok := list()[w.session]
		if !ok {
			t.Fatalf("worktree ls has no row for %s", w.session)
		}
		if r.Merged == nil {
			t.Fatalf("%s: the row has no merged field", w.session)
		}
		if *r.Merged != merged || r.MergedInto != into {
			t.Fatalf("%s: merged=%v merged_into=%q, want merged=%v merged_into=%q", w.session, *r.Merged, r.MergedInto, merged, into)
		}
	}

	mergeCommit := mk("feat/merge", true)
	fastForward := mk("feat/ff", true)
	squash := mk("feat/squash", true)
	upstream := mk("feat/up", true)
	noBase := mk("feat/nobase", false)
	fresh := mk("feat/fresh", true)

	for _, w := range []wt{mergeCommit, fastForward, squash, upstream, noBase} {
		commit(w)
		want(w, false, "")
	}
	want(fresh, false, "")

	tr.log("fast-forward main to feat/ff")
	testutil.Git(t, repo, "merge", "-q", "--ff-only", "feat/ff")
	want(fastForward, true, "main")

	tr.log("merge feat/merge into main with a merge commit")
	testutil.Git(t, repo, "merge", "-q", "--no-ff", "-m", "merge feat/merge", "feat/merge")
	want(mergeCommit, true, "main")

	tr.log("squash feat/squash into main")
	testutil.Git(t, repo, "merge", "-q", "--squash", "feat/squash")
	testutil.Git(t, repo, "commit", "-q", "-m", "squash feat/squash")
	want(squash, true, "main")

	tr.log("merge feat/nobase, a worktree made without --base, into main")
	testutil.Git(t, repo, "merge", "-q", "--no-ff", "-m", "merge feat/nobase", "feat/nobase")
	want(noBase, true, "main")

	tr.log("push feat/up to origin's main: local main does not hold it, origin/main does")
	want(upstream, false, "")
	testutil.Git(t, upstream.dir, "push", "-q", "-f", "origin", "feat/up:main")
	want(upstream, true, "origin/main")

	// main moved four times. The fresh branch still has no commits of its
	// own, so merging it would also change nothing, and it is still not
	// merged.
	want(fresh, false, "")

	// The table a person reads, for the transcript.
	table := tr.ok("worktree", "ls")
	if !strings.Contains(table, "MERGED") || !strings.Contains(table, "into origin/main") {
		t.Fatalf("worktree ls shows no MERGED column with into origin/main:\n%s", table)
	}
}
