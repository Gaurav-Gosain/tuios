package worktree_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// programRepo is a repository whose own config names programs that git
// runs, each of which appends its name to a marker file, and a global config
// with a filter of the person's own, as git-lfs installs one.
type programRepo struct {
	dir, repo, marker string
}

// newProgramRepo makes the repository. Its own config sets a clean, smudge
// and process filter, a textconv and diff.external, and .git/hooks holds the
// hooks git runs on add, ref updates, checkout and commit. A submodule holds
// a filter in its own config. The global config holds the "trusted" filter.
func newProgramRepo(t *testing.T) programRepo {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the probes are shell scripts")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	r := programRepo{dir: dir, repo: filepath.Join(dir, "repo"), marker: filepath.Join(dir, "ran")}
	probe := func(name string) string { return "sh -c 'echo " + name + " >> " + r.marker + "; cat'" }
	global := filepath.Join(dir, "gitconfig")
	write(t, global, "[user]\n\tname = t\n\temail = t@example.invalid\n"+
		"[protocol \"file\"]\n\tallow = always\n"+
		"[filter \"trusted\"]\n\tclean = \""+probe("trusted")+"\"\n\tsmudge = \""+probe("trusted")+"\"\n")
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	sub := filepath.Join(dir, "sub")
	r.git(t, dir, "init", "-q", "sub")
	write(t, filepath.Join(sub, "s.txt"), "s\n")
	r.git(t, sub, "add", ".")
	r.git(t, sub, "commit", "-q", "-m", "sub")

	r.git(t, dir, "init", "-q", "repo")
	write(t, filepath.Join(r.repo, ".gitattributes"), "*.txt filter=probe diff=probe\n*.lfs filter=trusted\n")
	write(t, filepath.Join(r.repo, "a.txt"), "a\n")
	write(t, filepath.Join(r.repo, "b.lfs"), "b\n")
	r.git(t, r.repo, "add", ".")
	r.git(t, r.repo, "commit", "-q", "-m", "first")
	r.git(t, r.repo, "-c", "protocol.file.allow=always", "submodule", "-q", "add", sub, "sub")
	r.git(t, r.repo, "commit", "-q", "-m", "sub")

	for key, value := range map[string]string{
		"filter.probe.clean":    probe("clean"),
		"filter.probe.smudge":   probe("smudge"),
		"filter.probe.required": "true",
		"diff.probe.textconv":   "sh -c 'echo textconv >> " + r.marker + "; cat \"$0\"'",
		"diff.external":         "sh -c 'echo external >> " + r.marker + "'",
		// The repository's own value for the person's filter.
		"filter.trusted.smudge": probe("local-smudge"),
	} {
		r.git(t, r.repo, "config", key, value)
	}
	for _, hook := range []string{"post-index-change", "reference-transaction", "post-checkout", "pre-commit"} {
		path := filepath.Join(r.repo, ".git", "hooks", hook)
		write(t, path, "#!/bin/sh\necho hook-"+hook+" >> "+r.marker+"\n")
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	subGit := filepath.Join(r.repo, ".git", "modules", "sub")
	r.git(t, filepath.Join(r.repo, "sub"), "config", "filter.subprobe.clean", probe("submodule"))
	write(t, filepath.Join(subGit, "info", "attributes"), "*.txt filter=subprobe\n")

	// A change of the same size in each file, so git compares contents.
	write(t, filepath.Join(r.repo, "a.txt"), "A\n")
	write(t, filepath.Join(r.repo, "b.lfs"), "B\n")
	write(t, filepath.Join(r.repo, "sub", "s.txt"), "S\n")
	write(t, filepath.Join(r.repo, "new.txt"), "new\n")
	r.reset(t)
	return r
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// git runs git plainly, with the repository's own config, in dir.
func (r programRepo) git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (r programRepo) reset(t *testing.T) {
	t.Helper()
	if err := os.Remove(r.marker); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// ran is the probes that ran since the last reset, the trusted filter left out.
func (r programRepo) ran() []string {
	data, _ := os.ReadFile(r.marker)
	var out []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if line != "" && line != "trusted" && !slices.Contains(out, line) {
			out = append(out, line)
		}
	}
	return out
}

func (r programRepo) trustedRan() bool {
	data, _ := os.ReadFile(r.marker)
	return slices.Contains(strings.Split(string(data), "\n"), "trusted")
}

// TestGitRunsNoRepositoryProgram is a security boundary. The daemon and the
// client run git in folders a pane controls, and the repository there can
// name programs git runs: filters, textconv, an external diff, hooks, and a
// submodule's own filters. Every path tuios runs git on for itself must leave
// them unrun, while the person's own filter from the global config still
// runs.
//
// The first step runs plain git on the same repository and requires every
// probe to run, so the repository does name programs git runs.
func TestGitRunsNoRepositoryProgram(t *testing.T) {
	r := newProgramRepo(t)
	ctx := context.Background()

	r.git(t, r.repo, "status")
	r.git(t, r.repo, "diff")
	r.git(t, r.repo, "diff", "--no-ext-diff")
	idx := filepath.Join(r.dir, "plain-index")
	r.git(t, r.repo, "update-ref", "refs/plain", "HEAD")
	cmd := exec.Command("git", "add", "--all")
	cmd.Dir, cmd.Env = r.repo, append(os.Environ(), "GIT_INDEX_FILE="+idx)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("plain git add: %v\n%s", err, out)
	}
	for _, want := range []string{"clean", "textconv", "external", "submodule", "hook-post-index-change", "hook-reference-transaction"} {
		if !slices.Contains(r.ran(), want) {
			t.Fatalf("plain git did not run the %s probe, so nothing here can be checked; it ran %v", want, r.ran())
		}
	}

	paths := []struct {
		name  string
		run   func() error
		lfsed bool
	}{
		{"checkpoint", func() error {
			_, _, err := worktree.SaveCheckpoint(ctx, r.repo, worktree.CheckpointMeta{Pane: "a1b2c3d4", Session: "s", Turn: 1}, 0)
			return err
		}, true},
		{"numstat", func() error { _, err := worktree.WorkingNumstat(ctx, r.repo, "HEAD", 0); return err }, true},
		{"detail", func() error { _, _, err := worktree.WorkingDetail(ctx, r.repo, "HEAD", 100, 100, 0); return err }, true},
		{"changes", func() error { _, err := worktree.ChangesCtx(ctx, r.repo); return err }, false},
		{"gitstate dirty", func() error { gitstate.ReadDirty(r.repo); return nil }, false},
		{"worktree diff", func() error { _, err := worktree.Diff(r.repo, false); return err }, false},
		{"working patch", func() error {
			_, err := worktree.WorkingPatch(r.repo, filepath.Join(r.dir, "patch"))
			return err
		}, true},
		{"review", func() error {
			base, err := review.ResolveBase(ctx, r.repo, "HEAD", "", false)
			if err != nil {
				return err
			}
			_, err = review.Build(ctx, review.Options{Dir: r.repo, Base: base})
			return err
		}, true},
		{"review stage", func() error { return review.StageFile(ctx, r.repo, "a.txt", "sub") }, false},
		{"worktree add", func() error {
			_, err := worktree.Add(r.repo, filepath.Join(r.dir, "wt"), "fan-1", "HEAD")
			return err
		}, true},
	}
	for _, p := range paths {
		r.reset(t)
		if err := p.run(); err != nil {
			t.Errorf("%s: %v", p.name, err)
			continue
		}
		if ran := r.ran(); len(ran) > 0 {
			t.Errorf("%s ran the repository's own programs: %v", p.name, ran)
		}
		if p.lfsed && !r.trustedRan() {
			t.Errorf("%s did not run the filter from the global config", p.name)
		}
	}
}

// TestShipCommitRunsHooksOnlyForThePerson is a security boundary: ship-commit
// from a pane needs no person, so its git runs none of the repository's
// hooks. The person's own ship-commit keeps them, as their git would.
func TestShipCommitRunsHooksOnlyForThePerson(t *testing.T) {
	r := newProgramRepo(t)
	if _, err := worktree.CommitAll(context.Background(), r.repo, "from a pane"); err != nil {
		t.Fatal(err)
	}
	if ran := r.ran(); len(ran) > 0 {
		t.Errorf("ship-commit from a pane ran the repository's own programs: %v", ran)
	}
	write(t, filepath.Join(r.repo, "c.txt"), "c\n")
	r.reset(t)
	if _, err := worktree.CommitAll(childproc.AsPerson(context.Background()), r.repo, "from the person"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(r.ran(), "hook-pre-commit") {
		t.Errorf("the person's ship-commit did not run the pre-commit hook; it ran %v", r.ran())
	}
}
