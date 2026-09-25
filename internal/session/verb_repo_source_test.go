package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestCloneRefusesALocalRepository is the security half of clone: a caller on
// another machine, or in a pane, cannot use it to copy a directory of this
// machine into a checkout, or to run a transport helper.
func TestCloneRefusesALocalRepository(t *testing.T) {
	_, sp, repo := worktreeFixture(t)
	into := t.TempDir()
	c := dialVerb(t, sp)
	for i, url := range []string{repo, "file://" + repo, "ext::sh -c touch% " + filepath.Join(into, "pwned")} {
		resp := c.call(t, `{"id":1,"verb":"new-worktree","params":`+jsonParams(map[string]any{
			"branch": "x", "repo_url": url, "repos_root": into, "clone": true,
		})+`}`)
		if code := errCode(t, resp); code != ErrVerbInvalidParams {
			t.Errorf("case %d: code = %q, want %q: %s", i, code, ErrVerbInvalidParams, resp)
		}
	}
	if entries, _ := os.ReadDir(into); len(entries) != 0 {
		t.Errorf("a refused clone left %d entries in repos_root", len(entries))
	}
}

func TestReposRootMustBeAnAbsoluteDirectory(t *testing.T) {
	_, sp, _ := worktreeFixture(t)
	c := dialVerb(t, sp)
	for _, dir := range []string{"relative/dir", filepath.Join(t.TempDir(), "missing")} {
		resp := c.call(t, `{"id":1,"verb":"new-worktree","params":`+jsonParams(map[string]any{
			"branch": "x", "repo_url": "https://github.com/acme/api", "repos_root": dir,
		})+`}`)
		if code := errCode(t, resp); code != ErrVerbInvalidParams {
			t.Errorf("repos_root %q: code = %q, want %q", dir, code, ErrVerbInvalidParams)
		}
	}
}

// reposRootWith makes a directory holding one checkout of the fixture
// repository whose origin is url, and returns the directory and the checkout.
func reposRootWith(t *testing.T, repo, url string) (string, string) {
	t.Helper()
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	checkout := filepath.Join(root, "acme", "api")
	testutil.Git(t, filepath.Dir(repo), "clone", "-q", repo, checkout)
	testutil.Git(t, checkout, "remote", "set-url", "origin", url)
	return root, checkout
}

func TestTwoCheckoutsOfOneOriginAreNotPickedBetween(t *testing.T) {
	_, sp, repo := worktreeFixture(t)
	root, first := reposRootWith(t, repo, "https://github.com/acme/api")
	second := filepath.Join(root, "copy")
	testutil.Git(t, root, "clone", "-q", repo, second)
	testutil.Git(t, second, "remote", "set-url", "origin", "git@github.com:acme/api")
	c := dialVerb(t, sp)
	resp := c.call(t, `{"id":1,"verb":"new-worktree","params":`+jsonParams(map[string]any{
		"branch": "x", "repo_url": "https://github.com/acme/api", "repos_root": root,
	})+`}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Fatalf("code = %q, want %q", code, ErrVerbInvalidParams)
	}
	for _, p := range []string{first, second} {
		if !strings.Contains(fmt.Sprint(resp), p) {
			t.Errorf("the refusal does not list %s: %s", p, resp)
		}
	}
}
