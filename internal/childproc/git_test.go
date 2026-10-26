package childproc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestGitRunsNoProgramTheRepositoryNames is a security boundary, beside the
// path tests in internal/worktree: a repository's own config names a
// signing program, an ssh command, a credential helper and a filter whose
// name holds "=", which would split a -c setting. Plain git runs each one;
// git from Git runs none, and still runs the credential helper the person's
// global config names.
func TestGitRunsNoProgramTheRepositoryNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the probes are shell scripts")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	probe := func(name string) string {
		script := filepath.Join(dir, name+".sh")
		if err := os.WriteFile(script, []byte("#!/bin/sh\necho "+name+" >> "+marker+"\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return script
	}
	global := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(global, []byte("[user]\n\tname = t\n\temail = t@example.invalid\n[credential]\n\thelper = \"!"+probe("trusted-helper")+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	// The signing program that replaces the repository's is the real gpg,
	// which must find no key of the person's.
	t.Setenv("GNUPGHOME", filepath.Join(dir, "gnupg"))
	repo := filepath.Join(dir, "repo")
	plain := func(stdin string, args ...string) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = repo
		cmd.Stdin = strings.NewReader(stdin)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		_ = cmd.Run()
	}
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	plain("", "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("*.txt filter=a=b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, kv := range [][2]string{
		{"filter.a=b.clean", probe("filter")},
		{"commit.gpgSign", "true"},
		{"gpg.program", probe("gpg")},
		{"core.sshCommand", probe("ssh")},
		{"credential.helper", "!" + probe("helper")},
	} {
		plain("", "config", kv[0], kv[1])
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cred := "protocol=https\nhost=example.invalid\n\n"
	steps := []struct {
		stdin string
		args  []string
	}{
		{"", []string{"add", "a.txt"}},
		{"", []string{"commit", "-q", "-m", "x"}},
		{"", []string{"ls-remote", "ssh://example.invalid/x"}},
		{cred, []string{"credential", "fill"}},
	}
	ran := func() []string {
		data, _ := os.ReadFile(marker)
		_ = os.Remove(marker)
		return strings.Fields(string(data))
	}
	for _, s := range steps {
		plain(s.stdin, s.args...)
	}
	got := ran()
	for _, want := range []string{"filter", "gpg", "ssh", "helper", "trusted-helper"} {
		if !slices.Contains(got, want) {
			t.Fatalf("plain git did not run the %s probe, so nothing here can be checked; it ran %v", want, got)
		}
	}
	for _, s := range steps {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		cmd := Git(ctx, repo, s.args...)
		cmd.Stdin = strings.NewReader(s.stdin)
		_ = cmd.Run()
		cancel()
	}
	got = ran()
	for _, bad := range []string{"filter", "gpg", "ssh", "helper"} {
		if slices.Contains(got, bad) {
			t.Errorf("git from Git ran the repository's %s program; probes that ran: %v", bad, got)
		}
	}
	if !slices.Contains(got, "trusted-helper") {
		t.Errorf("git from Git did not run the credential helper of the global config; probes that ran: %v", got)
	}
}
