package worktree

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

func TestNormalizeRemoteJoinsTheSpellingsOfOneRepository(t *testing.T) {
	same := []string{
		"https://github.com/Gaurav-Gosain/tuios",
		"https://github.com/Gaurav-Gosain/tuios.git",
		"https://GitHub.com/Gaurav-Gosain/tuios/",
		"git@github.com:Gaurav-Gosain/tuios.git",
		"github.com:Gaurav-Gosain/tuios",
		"ssh://git@github.com/Gaurav-Gosain/tuios.git",
		"ssh://git@github.com:22/Gaurav-Gosain/tuios",
		"git://github.com/Gaurav-Gosain/tuios",
	}
	want := "github.com/Gaurav-Gosain/tuios"
	for _, s := range same {
		if got := NormalizeRemote(s); got != want {
			t.Errorf("NormalizeRemote(%q) = %q, want %q", s, got, want)
		}
	}
	different := []string{
		"https://github.com/Gaurav-Gosain/tuios-web",
		"https://gitlab.com/Gaurav-Gosain/tuios",
		"https://github.com/someone-else/tuios",
		"/src/tuios",
	}
	for _, s := range different {
		if got := NormalizeRemote(s); got == want {
			t.Errorf("NormalizeRemote(%q) = %q, which matches a different repository", s, got)
		}
	}
	if a, b := NormalizeRemote("/tmp/x/origin.git"), NormalizeRemote("file:///tmp/x/origin.git"); a != b {
		t.Errorf("a path and its file URL differ: %q and %q", a, b)
	}
	if NormalizeRemote("") != "" {
		t.Error("an empty remote is not empty")
	}
}

func TestValidCloneURLRefusesAnythingButANetworkRemote(t *testing.T) {
	ok := []string{
		"https://github.com/o/r.git",
		"ssh://git@host/o/r",
		"git://host/o/r",
		"git@github.com:o/r.git",
	}
	for _, s := range ok {
		if err := ValidCloneURL(s); err != nil {
			t.Errorf("ValidCloneURL(%q) = %v, want nil", s, err)
		}
	}
	bad := []string{
		"",
		"/etc",
		"../secrets",
		"file:///home/me/.ssh",
		"ext::sh -c touch% /tmp/pwned",
		"fd::17",
		"-uhelp",
		"--upload-pack=touch /tmp/x",
		"http://host/o/r",
		"https://host/o/r\n",
		" https://host/o/r",
		"C:\\src\\repo",
		"-oProxyCommand=x:o/r",
		"ssh://-oProxyCommand=x/o/r",
	}
	for _, s := range bad {
		if err := ValidCloneURL(s); !errors.Is(err, ErrCloneURL) {
			t.Errorf("ValidCloneURL(%q) = %v, want ErrCloneURL", s, err)
		}
	}
}

func TestCloneRefusesALocalPathBeforeRunningGit(t *testing.T) {
	repo := testutil.GitRepo(t)
	parent := t.TempDir()
	if _, err := Clone(repo, parent); !errors.Is(err, ErrCloneURL) {
		t.Fatalf("Clone of a local path = %v, want ErrCloneURL", err)
	}
	if entries, _ := os.ReadDir(parent); len(entries) != 0 {
		t.Errorf("a refused clone left %d entries behind", len(entries))
	}
}

func TestCloneFetchesARepositoryUnderItsName(t *testing.T) {
	repo := testutil.GitRepo(t)
	parent := t.TempDir()
	// The unexported clone takes the protocol list, so the test can allow
	// file, which Clone never does, and clone the fixture without a network.
	dest, err := clone("file://"+repo, parent, "file")
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	if filepath.Base(dest) != "repo" {
		t.Errorf("the clone is at %s, want a directory named after the repository", dest)
	}
	if _, err := os.Stat(filepath.Join(dest, "README")); err != nil {
		t.Errorf("the clone has no README: %v", err)
	}
	// A second clone to the same place is refused, not merged into.
	if _, err := clone("file://"+repo, parent, "file"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("a clone over an existing directory = %v, want a refusal", err)
	}
}

func TestFindByOriginFindsEveryCheckoutOfTheRepository(t *testing.T) {
	// GitRepo is called for the git environment it isolates, so the inits
	// below read no user config.
	_ = testutil.GitRepo(t)
	root := t.TempDir()
	mk := func(rel, url string) string {
		dir := filepath.Join(root, rel)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		testutil.Git(t, dir, "init", "-q")
		if url != "" {
			testutil.Git(t, dir, "remote", "add", "origin", url)
		}
		return dir
	}
	a := mk("github.com/o/api", "git@github.com:o/api.git")
	b := mk("work/api-copy", "https://github.com/o/api")
	mk("other", "https://github.com/o/web")
	mk("bare-init", "")
	// A repository inside a hidden directory, and one too deep, are not found.
	mk(".cache/api", "https://github.com/o/api")
	mk("a/b/c/d/api", "https://github.com/o/api")

	got := FindByOrigin([]SearchRoot{{Dir: root, Depth: 3}}, "ssh://git@github.com/o/api")
	want := []string{a, b}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("FindByOrigin = %v, want %v", got, want)
	}
	if got := FindByOrigin([]SearchRoot{{Dir: root, Depth: 3}}, "https://github.com/o/nothing"); len(got) != 0 {
		t.Errorf("FindByOrigin for a repository nobody has = %v", got)
	}
}

func TestBundleAndPatchCarryAWorktreesWorkToAnotherCheckout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture uses POSIX paths")
	}
	sender := testutil.GitRepo(t)
	receiver := filepath.Join(t.TempDir(), "receiver")
	testutil.Git(t, filepath.Dir(receiver), "clone", "-q", sender, receiver)

	path := filepath.Join(t.TempDir(), "wt")
	if _, err := Add(sender, path, "feat/x", "main"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "README"), []byte("committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, path, "commit", "-q", "-am", "commit on the branch")
	// Uncommitted: one staged edit, one unstaged new file, one binary file.
	if err := os.WriteFile(filepath.Join(path, "README"), []byte("committed\nstaged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, path, "add", "README")
	if err := os.WriteFile(filepath.Join(path, "new.txt"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "blob.bin"), []byte{0, 1, 2, 255, 0}, 0o644); err != nil {
		t.Fatal(err)
	}
	statusBefore := testutil.Git(t, path, "status", "--porcelain")

	head, err := HeadCommit(path)
	if err != nil {
		t.Fatal(err)
	}
	base, err := MergeBase(path, "main", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bundle := filepath.Join(dir, "b.bundle")
	if err := Bundle(path, "feat/x", base, bundle); err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	patch := filepath.Join(dir, "w.patch")
	n, err := WorkingPatch(path, patch)
	if err != nil {
		t.Fatalf("WorkingPatch: %v", err)
	}
	if n != 3 {
		t.Errorf("WorkingPatch counted %d paths, want 3", n)
	}
	// The sender's index is as it was: the staged edit is still the only
	// staged thing, and the new files are still untracked.
	if after := testutil.Git(t, path, "status", "--porcelain"); after != statusBefore {
		t.Errorf("WorkingPatch changed the worktree's status:\nbefore:\n%s\nafter:\n%s", statusBefore, after)
	}

	if !HasCommit(receiver, base) {
		t.Fatal("the receiver lacks the base, so the fixture is wrong")
	}
	if HasCommit(receiver, head) {
		t.Fatal("the receiver already has the head, so the fixture proves nothing")
	}
	if err := FetchBundle(receiver, bundle, "feat/x", "pulled"); err != nil {
		t.Fatalf("FetchBundle: %v", err)
	}
	if got, _ := BranchCommit(receiver, "pulled"); got != head {
		t.Errorf("the pulled branch is at %s, want %s", got, head)
	}
	wt := filepath.Join(t.TempDir(), "pulled-wt")
	if _, err := Add(receiver, wt, "pulled", ""); err != nil {
		t.Fatal(err)
	}
	if err := ApplyPatch(wt, patch); err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	for name, want := range map[string]string{"README": "committed\nstaged\n", "new.txt": "untracked\n", "blob.bin": "\x00\x01\x02\xff\x00"} {
		got, err := os.ReadFile(filepath.Join(wt, name))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", name, got, err, want)
		}
	}

	// A branch with nothing past the base makes no bundle.
	if err := Bundle(path, "feat/x", head, filepath.Join(dir, "empty.bundle")); !errors.Is(err, ErrEmptyBundle) {
		t.Errorf("Bundle with nothing to carry = %v, want ErrEmptyBundle", err)
	}
}
