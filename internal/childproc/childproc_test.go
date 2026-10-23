package childproc

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestGitRunsNoFSMonitor is a security boundary: the daemon runs git in
// folders a pane controls, and a repository's core.fsmonitor names a program
// git runs on status. That program would run as a child of the daemon,
// outside every pane. Neither git started here nor git started by a program
// given WithGitSettings may run it.
//
// The first git, started plainly, is the positive control: it runs the
// program, so the harness can see a run.
func TestGitRunsNoFSMonitor(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	hook := filepath.Join(dir, "fsmonitor.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+marker+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(dir, "repo")
	git := func(cmd *exec.Cmd) {
		t.Helper()
		cmd.Dir = repo
		if cmd.Env == nil {
			cmd.Env = os.Environ()
		}
		cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "HOME="+dir, "XDG_CONFIG_HOME="+dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", cmd.Args, err, out)
		}
	}
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git(exec.Command("git", "init", "-q"))
	git(exec.Command("git", "config", "core.fsmonitor", hook))

	git(exec.Command("git", "status"))
	if _, err := os.Stat(marker); err != nil {
		t.Skip("this git does not run core.fsmonitor, so nothing here can be checked")
	}
	_ = os.Remove(marker)

	git(Command("git", "status"))
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("git started by childproc ran the repository's core.fsmonitor")
	}
	plain := exec.Command("git", "status")
	plain.Env = WithGitSettings(os.Environ())
	git(plain)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("git started with WithGitSettings ran the repository's core.fsmonitor")
	}
}
