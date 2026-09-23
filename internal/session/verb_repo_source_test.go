package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

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

func TestNewWorktreeFindsTheCheckoutByItsOrigin(t *testing.T) {
	_, sp, repo := worktreeFixture(t)
	root, checkout := reposRootWith(t, repo, "git@github.com:acme/api.git")
	c := dialVerb(t, sp)

	// The URL is spelled differently from the checkout's origin, the way a
	// machine that cloned over https names a repository another cloned over
	// ssh.
	res := newWorktreeCall(t, c, "", "feat/remote", map[string]any{
		"repo":       nil,
		"repo_url":   "https://github.com/acme/api",
		"repos_root": root,
	})
	if res["repo_root"] != checkout {
		t.Errorf("repo_root = %v, want the checkout %s", res["repo_root"], checkout)
	}
	if _, has := res["cloned"]; has {
		t.Errorf("cloned = %v for a checkout that was already there", res["cloned"])
	}
	if res["session"] != "api-feat-remote" {
		t.Errorf("session = %v, want api-feat-remote", res["session"])
	}
}

func TestFanFindsTheCheckoutByItsOrigin(t *testing.T) {
	fakeClaudeOnPath(t)
	_, sp, repo := worktreeFixture(t)
	root, checkout := reposRootWith(t, repo, "https://github.com/acme/api.git")
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"fan","params":`+jsonParams(map[string]any{
		"count": 1, "agent": "claude", "prompt": "Add a retry.",
		"repo_url": "ssh://git@github.com/acme/api", "repos_root": root,
	})+`}`))
	if res["repo_root"] != checkout {
		t.Errorf("repo_root = %v, want %s", res["repo_root"], checkout)
	}
}

func TestRepoURLWithNoCheckoutSaysWhatToDo(t *testing.T) {
	_, sp, _ := worktreeFixture(t)
	c := dialVerb(t, sp)
	resp := c.call(t, `{"id":1,"verb":"new-worktree","params":`+jsonParams(map[string]any{
		"branch": "x", "repo_url": "https://github.com/acme/nothing", "repos_root": t.TempDir(),
	})+`}`)
	if code := errCode(t, resp); code != ErrVerbRepoNotFound {
		t.Fatalf("code = %q, want %q", code, ErrVerbRepoNotFound)
	}
	if !strings.Contains(fmt.Sprint(resp), "clone") {
		t.Errorf("the refusal does not name clone as the remedy: %s", resp)
	}
}

func TestRepoAndRepoURLTogetherAreRefused(t *testing.T) {
	_, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	resp := c.call(t, `{"id":1,"verb":"new-worktree","params":`+jsonParams(map[string]any{
		"branch": "x", "repo": repo, "repo_url": "https://github.com/acme/api",
	})+`}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Fatalf("code = %q, want %q", code, ErrVerbInvalidParams)
	}
	resp = c.call(t, `{"id":2,"verb":"new-worktree","params":`+jsonParams(map[string]any{
		"branch": "x", "repo": repo, "clone": true,
	})+`}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Fatalf("clone without repo_url: code = %q, want %q", code, ErrVerbInvalidParams)
	}
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
