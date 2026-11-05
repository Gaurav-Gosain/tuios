package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// JJRepo creates a throwaway jj repository under t.TempDir(), colocated with
// git or not, holding one commit "base" with bookmark "main" and an empty
// working-copy commit on it, and returns its path. trunk() is "main".
//
// jj reads only a config written for the test, so the user's aliases (their
// own trunk() above all) cannot change what the test sees.
func JJRepo(t *testing.T, colocate bool) string {
	t.Helper()
	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj is not installed")
	}
	tmp := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(tmp); err == nil {
		tmp = resolved
	}
	cfg := filepath.Join(tmp, "jj.toml")
	if err := os.WriteFile(cfg, []byte(`user.name = "tuios-test"
user.email = "tuios-test@example.com"
revset-aliases."trunk()" = "main"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JJ_CONFIG", cfg)
	dir := filepath.Join(tmp, "repo")
	flag := "--no-colocate"
	if colocate {
		flag = "--colocate"
	}
	JJ(t, tmp, "git", "init", flag, dir)
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	JJ(t, dir, "commit", "-m", "base")
	JJ(t, dir, "bookmark", "create", "main", "-r", "@-")
	// A process that does not get JJ_CONFIG, such as a tuios daemon in the
	// e2e suite, still finds trunk(): jj's default is main@origin.
	Git(t, tmp, "init", "-q", "--bare", "origin.git")
	JJ(t, dir, "git", "remote", "add", "origin", filepath.Join(tmp, "origin.git"))
	JJ(t, dir, "git", "push", "-b", "main")
	return dir
}

// JJ runs one jj command in dir and returns its trimmed stdout. It fails the
// test when jj does. Tests use it only on repositories JJRepo made.
func JJ(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("jj", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("jj %s in %s: %v\n%s", strings.Join(args, " "), dir, err, stderr)
	}
	return strings.TrimSpace(string(out))
}
