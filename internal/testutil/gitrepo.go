package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// GitRepo creates a throwaway git repository under t.TempDir() holding one
// commit on branch "main", and returns its path.
//
// The repository is the only git a test may write to. It is isolated from the
// user's configuration, and the identity git needs to write a commit or a
// stash is a placeholder set in this process's environment for the test's
// lifetime, never written to any config file.
func GitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	// A test that creates a repository must not read the user's config: a
	// template directory or a hook there would change what the test sees.
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "tuios-test")
	}
	dir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	Git(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	Git(t, dir, "add", "README")
	Git(t, dir, "commit", "-q", "-m", "first")
	return dir
}

// Git runs one git command in dir and returns its trimmed stdout. It fails the
// test when git does. Tests use it only on repositories GitRepo made.
func Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}
