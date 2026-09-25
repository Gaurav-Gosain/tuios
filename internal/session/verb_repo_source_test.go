package session

import (
	"os"
	"path/filepath"
	"testing"
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
