package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestCheckpointUndoesATurn runs the turn checkpoints through the binary a
// person runs, against a real daemon. A pane sits in a throwaway repository,
// and a fake agent reports working and done through set-agent-state, the way
// a harness hook does. Turn 1 writes a file and turn 2 edits it. The daemon
// takes a checkpoint at the end of each turn. checkpoint list shows two,
// checkpoint diff shows what each turn changed, and checkpoint restore 1
// brings the file back as turn 1 left it, after it saves a safety checkpoint
// that undoes the restore. A restore while the agent works is refused.
//
// The person's HEAD, branch, index and stash are read with git before the
// turns and after the restores, and must be the same. The index holds a
// staged change from the start, so a checkpoint or a restore that used the
// real index would show.
//
// Every call and its output go to transcript.txt in the artifact directory.
//
// Negative controls (NEGATIVE_CONTROLS.md): with the noteCheckpointEvent call
// cut from the session event sink, the wait for the first checkpoint times
// out. With the safety SaveCheckpoint cut from the restore verb, the undo
// step finds no checkpoint 3. With the temporary index dropped from
// RestoreTree's read-tree, the index check fails.
func TestCheckpointUndoesATurn(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.GitRepo(t)
	tr := newCheckpointTranscript(t, base, repo)

	// A staged change of the person's own, which must stay staged.
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("hello\nstaged by the person\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo, "add", "README")
	before := gitState(t, repo)
	tr.log("git state before:\n%s", before)

	if out, err := tuiosCLIIn(t, base, repo, "new", "agent", "--detach"); err != nil {
		t.Fatalf("new session in the repository: %v: %s", err, out)
	}
	notes := filepath.Join(repo, "notes.txt")

	// Turn 1 writes a file, turn 2 edits it.
	turn := func(n int, content string) {
		t.Helper()
		tr.ok("set-agent-state", "-s", "agent", "working")
		if err := os.WriteFile(notes, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		tr.log("turn %d wrote notes.txt: %q", n, content)
		tr.ok("set-agent-state", "-s", "agent", "done", "-m", fmt.Sprintf("turn %d of the notes", n))
		tr.waitCheckpoints(n)
	}
	turn(1, "first\n")
	turn(2, "first\nsecond\n")

	list := tr.list()
	if len(list) != 2 || list[0].N != 1 || list[1].N != 2 || list[0].Kind != "turn" || list[1].State != "done" {
		t.Fatalf("checkpoint list = %+v, want turn checkpoints 1 and 2 ending in done", list)
	}
	if out := tr.ok("checkpoint", "list", "-s", "agent"); !strings.Contains(out, "Checkpoints of agent") || !strings.Contains(out, "turn 2 of the notes") {
		t.Errorf("checkpoint list printed:\n%s", out)
	}

	out := tr.ok("checkpoint", "diff", "-s", "agent", "2")
	for _, want := range []string{"Checkpoint 2 of agent", "against checkpoint 1", "1 file, +1 -0", "notes.txt", "+ second"} {
		if !strings.Contains(out, want) {
			t.Errorf("checkpoint diff 2 lacks %q:\n%s", want, out)
		}
	}
	out = tr.ok("checkpoint", "diff", "-s", "agent", "1")
	for _, want := range []string{"Checkpoint 1 of agent", "against HEAD", "notes.txt", "+ first"} {
		if !strings.Contains(out, want) {
			t.Errorf("checkpoint diff 1 lacks %q:\n%s", want, out)
		}
	}

	// Restore turn 1: the file is back as turn 1 left it, and checkpoint 3
	// holds what was there before.
	out = tr.ok("checkpoint", "restore", "-s", "agent", "1")
	if !strings.Contains(out, "Restored checkpoint 1") || !strings.Contains(out, "Checkpoint 3 holds the work tree as it was before") {
		t.Errorf("checkpoint restore 1 printed:\n%s", out)
	}
	if got := readFile(t, notes); got != "first\n" {
		t.Fatalf("after restore 1, notes.txt = %q, want %q", got, "first\n")
	}
	list = tr.list()
	if len(list) != 3 || list[2].Kind != "safety" {
		t.Fatalf("after the restore, checkpoint list = %+v, want a safety checkpoint 3", list)
	}
	if after := gitState(t, repo); after != before {
		t.Errorf("the restore changed the person's git state.\nbefore:\n%s\nafter:\n%s", before, after)
	}

	// The restore is undone by restoring the safety checkpoint.
	tr.ok("checkpoint", "restore", "-s", "agent", "3")
	if got := readFile(t, notes); got != "first\nsecond\n" {
		t.Fatalf("after restoring the safety checkpoint, notes.txt = %q, want %q", got, "first\nsecond\n")
	}

	// A restore while the agent works is refused and changes nothing.
	tr.ok("set-agent-state", "-s", "agent", "working")
	if out, err := tr.run("checkpoint", "restore", "-s", "agent", "1"); err == nil || !strings.Contains(out, "working") {
		t.Errorf("a restore while the agent works = %v: %s, want a refusal", err, out)
	}
	if got := readFile(t, notes); got != "first\nsecond\n" {
		t.Errorf("the refused restore changed notes.txt to %q", got)
	}

	after := gitState(t, repo)
	tr.log("git state after:\n%s", after)
	if after != before {
		t.Errorf("the checkpoints changed the person's git state.\nbefore:\n%s\nafter:\n%s", before, after)
	}
	refs := testutil.Git(t, repo, "for-each-ref", "--format=%(refname)", "refs/tuios/checkpoints/")
	tr.log("checkpoint refs:\n%s", refs)
	if n := len(strings.Fields(refs)); n < 3 {
		t.Errorf("the repository holds %d checkpoint refs, want at least 3:\n%s", n, refs)
	}
}

// TestCheckpointRestoreKeepsIgnoredFiles: turn 1 leaves a .env file that is
// not ignored yet, so checkpoint 1 holds it. Turn 2 ignores it, and the
// person then puts their own values in it. No checkpoint holds those, the
// safety checkpoint of a restore included, since it leaves ignored files
// out. Restoring checkpoint 1 would write turn 1's .env over them, and the
// restore of the safety checkpoint that undoes it would delete the file. So
// the restore is refused, names the file, and changes nothing.
//
// Negative control (NEGATIVE_CONTROLS.md): with the blockingPath check cut
// from RestoreTree, the restore succeeds and .env holds turn 1's text.
func TestCheckpointRestoreKeepsIgnoredFiles(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.GitRepo(t)
	tr := newCheckpointTranscript(t, base, repo)
	if out, err := tuiosCLIIn(t, base, repo, "new", "agent", "--detach"); err != nil {
		t.Fatalf("new session in the repository: %v: %s", err, out)
	}
	env := filepath.Join(repo, ".env")
	turn := func(n int, write func()) {
		t.Helper()
		tr.ok("set-agent-state", "-s", "agent", "working")
		write()
		tr.ok("set-agent-state", "-s", "agent", "done", "-m", fmt.Sprintf("turn %d", n))
		tr.waitCheckpoints(n)
	}
	turn(1, func() {
		if err := os.WriteFile(env, []byte("TOKEN=from-turn-1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	turn(2, func() {
		if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	if err := os.WriteFile(env, []byte("TOKEN=the-persons-own\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := tr.run("checkpoint", "restore", "-s", "agent", "1")
	if err == nil || !strings.Contains(out, ".env") {
		t.Errorf("checkpoint restore 1 over an ignored .env = %v:\n%s\nwant a refusal that names .env", err, out)
	}
	if got := readFile(t, env); got != "TOKEN=the-persons-own\n" {
		t.Fatalf("after the restore, .env = %q: the person's own values are gone", got)
	}
	if got := readFile(t, filepath.Join(repo, ".gitignore")); got != ".env\n" {
		t.Errorf("the refused restore changed .gitignore to %q", got)
	}
	tr.log("the restore over the ignored .env was refused and .env kept the person's values")
}

// TestCheckpointLeavesOutLargeUntrackedFiles sets max_untracked_mb = 1. A
// turn writes a 2 MiB untracked file and a small one. The checkpoint holds
// the small one and not the large one, git's object store never gets the
// large one, and checkpoint list names it as left out. A restore of the
// checkpoint leaves the large file where it is.
//
// Negative control (NEGATIVE_CONTROLS.md): with SaveCheckpoint snapshotting
// through SnapshotTree again, with no exclusions, the large file is in the
// checkpoint's diff and in the object store.
func TestCheckpointLeavesOutLargeUntrackedFiles(t *testing.T) {
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
	tr := newCheckpointTranscript(t, base, repo)
	if out, err := tuiosCLIIn(t, base, repo, "new", "agent", "--detach"); err != nil {
		t.Fatalf("new session in the repository: %v: %s", err, out)
	}
	large := filepath.Join(repo, "data", "large.bin")
	small := filepath.Join(repo, "small.txt")
	turn := func(n int, content string) {
		t.Helper()
		tr.ok("set-agent-state", "-s", "agent", "working")
		if n == 1 {
			if err := os.MkdirAll(filepath.Dir(large), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(large, []byte(strings.Repeat("0123456789abcdef", 2<<16)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(small, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		tr.ok("set-agent-state", "-s", "agent", "done", "-m", fmt.Sprintf("turn %d", n))
		tr.waitCheckpoints(n)
	}
	turn(1, "one\n")

	out := tr.ok("checkpoint", "diff", "-s", "agent", "1")
	if !strings.Contains(out, "small.txt") {
		t.Errorf("checkpoint 1 lacks the small untracked file:\n%s", out)
	}
	if strings.Contains(out, "large.bin") {
		t.Errorf("checkpoint 1 holds the 2 MiB untracked file over a 1 MB limit:\n%s", out)
	}
	sha := testutil.Git(t, repo, "hash-object", large)
	if err := exec.Command("git", "-C", repo, "cat-file", "-e", sha).Run(); err == nil {
		t.Errorf("the object store holds the large file's blob %s, so it was read and written", sha[:7])
	}
	if out := tr.ok("checkpoint", "list", "-s", "agent"); !strings.Contains(out, "Checkpoint 1 left out 1 untracked file") || !strings.Contains(out, "data/large.bin") {
		t.Errorf("checkpoint list does not name the file left out:\n%s", out)
	}

	turn(2, "two\n")
	tr.ok("checkpoint", "restore", "-s", "agent", "1")
	if got := readFile(t, small); got != "one\n" {
		t.Errorf("after restore 1, small.txt = %q, want %q", got, "one\n")
	}
	if info, err := os.Stat(large); err != nil || info.Size() != 2<<20 {
		t.Errorf("the restore touched the file the checkpoints left out: %v", err)
	}
	tr.log("the 2 MiB untracked file stayed out of every checkpoint and was left in place")
}

// TestCheckpointsGoWithTheirWorktree: a checkpoint taken in a worktree
// session is a ref of the whole repository, so removing the worktree removes
// its refs too, and leaves the refs of a pane in the main checkout.
//
// Negative control (NEGATIVE_CONTROLS.md): with the dropCheckpointsOf call
// cut from remove-worktree, the worktree's ref is still there.
func TestCheckpointsGoWithTheirWorktree(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.GitRepo(t)
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(base, "worktrees"))
	tr := newCheckpointTranscript(t, base, repo)

	tr.ok("new", "main", "--detach")
	tr.ok("worktree", "new", "feat/cp", "--repo", repo, "--detach")
	const wtSession = "repo-feat-cp"
	wt := filepath.Join(base, "worktrees", "repo", "feat-cp")

	turn := func(session, dir string) {
		t.Helper()
		tr.ok("set-agent-state", "-s", session, "working")
		if err := os.WriteFile(filepath.Join(dir, "work.txt"), []byte(session+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		tr.ok("set-agent-state", "-s", session, "done")
	}
	turn("main", repo)
	turn(wtSession, wt)
	refs := func() []string {
		return strings.Fields(testutil.Git(t, repo, "for-each-ref", "--format=%(refname)", "refs/tuios/checkpoints/"))
	}
	deadline := time.Now().Add(20 * time.Second)
	for len(refs()) < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("the two panes have %d checkpoint refs, want 2: %v", len(refs()), refs())
		}
		time.Sleep(100 * time.Millisecond)
	}
	tr.log("refs before the worktree is removed: %v", refs())

	tr.ok("worktree", "rm", wtSession, "--force")
	left := refs()
	tr.log("refs after: %v", left)
	if len(left) != 1 {
		t.Fatalf("after worktree rm, the repository holds checkpoint refs %v, want only the main checkout's", left)
	}
	if out := tr.ok("checkpoint", "list", "-s", "main"); !strings.Contains(out, ": 1\n") {
		t.Errorf("the main checkout's pane lost its checkpoint:\n%s", out)
	}
}

// gitState is what the person's git use depends on: where HEAD points and
// at what, the branches, the index entries and the stash.
func gitState(t *testing.T, repo string) string {
	t.Helper()
	return strings.Join([]string{
		"HEAD -> " + testutil.Git(t, repo, "symbolic-ref", "HEAD"),
		"HEAD = " + testutil.Git(t, repo, "rev-parse", "HEAD"),
		"branches:\n" + testutil.Git(t, repo, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/"),
		"index:\n" + testutil.Git(t, repo, "ls-files", "--stage"),
		"stash:\n" + testutil.Git(t, repo, "stash", "list"),
	}, "\n")
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// checkpointEntry is one checkpoint as list-checkpoints returns it.
type checkpointEntry struct {
	N     int    `json:"n"`
	Kind  string `json:"kind"`
	State string `json:"state"`
	Tree  string `json:"tree"`
}

// checkpointTranscript runs the tuios calls of the checkpoint test and keeps
// them, with their output, for transcript.txt.
type checkpointTranscript struct {
	t    *testing.T
	base string
	repo string
	b    strings.Builder
}

func newCheckpointTranscript(t *testing.T, base, repo string) *checkpointTranscript {
	tr := &checkpointTranscript{t: t, base: base, repo: repo}
	t.Cleanup(func() {
		path := filepath.Join(artifactDir(t), "transcript.txt")
		if err := os.WriteFile(path, []byte(tr.b.String()), 0o644); err != nil {
			t.Logf("save the transcript: %v", err)
		}
	})
	return tr
}

func (tr *checkpointTranscript) log(format string, args ...any) {
	fmt.Fprintf(&tr.b, "# "+format+"\n", args...)
}

func (tr *checkpointTranscript) run(args ...string) (string, error) {
	tr.t.Helper()
	out, err := tuiosCLIIn(tr.t, tr.base, tr.repo, args...)
	rc := 0
	if err != nil {
		rc = 1
	}
	fmt.Fprintf(&tr.b, "$ tuios %s\n%s[rc=%d]\n", strings.Join(args, " "), out, rc)
	return out, err
}

func (tr *checkpointTranscript) ok(args ...string) string {
	tr.t.Helper()
	out, err := tr.run(args...)
	if err != nil {
		tr.t.Fatalf("tuios %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// list reads list-checkpoints for the agent session's pane, without
// recording it: the waits call it many times.
func (tr *checkpointTranscript) list() []checkpointEntry {
	tr.t.Helper()
	out, err := tuiosCLIIn(tr.t, tr.base, tr.repo, "checkpoint", "list", "-s", "agent", "--json")
	if err != nil {
		tr.t.Fatalf("checkpoint list --json: %v\n%s", err, out)
	}
	var res struct {
		Checkpoints []checkpointEntry `json:"checkpoints"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		tr.t.Fatalf("checkpoint list --json printed no JSON: %v\n%s", err, out)
	}
	return res.Checkpoints
}

// waitCheckpoints waits until the pane has n checkpoints. They are taken off
// the daemon's event path, so they land a moment after the state report.
func (tr *checkpointTranscript) waitCheckpoints(n int) {
	tr.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		list := tr.list()
		if len(list) >= n {
			tr.log("checkpoint %d is there after the turn", n)
			return
		}
		if time.Now().After(deadline) {
			tr.t.Fatalf("after turn %d the pane has %d checkpoints, want %d", n, len(list), n)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
