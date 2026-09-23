package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// inRepoWithOrigin puts the test in a checkout whose origin is url, with a
// [hosts.build] table whose repos_root is reposRoot.
func inRepoWithOrigin(t *testing.T, url, reposRoot string) string {
	t.Helper()
	repo := testutil.GitRepo(t)
	testutil.Git(t, repo, "remote", "add", "origin", url)
	t.Chdir(repo)
	path, err := config.GetConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := config.SetHostInFile(path, "build", config.HostConfig{Addr: "buildbox", ReposRoot: reposRoot}); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestRepoParamsNameTheRepositoryByOriginOnAHost(t *testing.T) {
	repo := inRepoWithOrigin(t, "git@github.com:acme/api.git", "~/src")

	here, err := repoParams("", "", false)
	if err != nil || here["repo"] != repo {
		t.Errorf("on this machine: %v, %v; want the current directory %s", here, err, repo)
	}
	there, err := repoParams("build", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if there["repo_url"] != "git@github.com:acme/api.git" || there["repos_root"] != "~/src" || there["clone"] != true {
		t.Errorf("on build: %v, want the origin URL, the host's repos_root and clone", there)
	}
	if _, has := there["repo"]; has {
		t.Errorf("on build: a local path was sent: %v", there["repo"])
	}
	named, err := repoParams("build", "/srv/api", false)
	if err != nil || named["repo"] != "/srv/api" || named["repo_url"] != nil {
		t.Errorf("--repo on build: %v, %v; want the directory on the host", named, err)
	}

	for name, call := range map[string]func() error{
		"clone here":     func() error { _, err := repoParams("", "", true); return err },
		"repo and clone": func() error { _, err := repoParams("build", "/srv/api", true); return err },
		"start-agent cwd+rp": func() error {
			_, err := startAgentPlace("build", startAgentOptions{cwd: "/x", repo: "/y"})
			return err
		},
	} {
		if call() == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestRepoParamsRefuseARepositoryWithNoOrigin(t *testing.T) {
	repo := testutil.GitRepo(t)
	t.Chdir(repo)
	_, err := repoParams("build", "", false)
	var d *diagnosticError
	if !errors.As(err, &d) || !strings.Contains(d.Fix, "--repo") {
		t.Errorf("err = %v, want a diagnostic naming --repo", err)
	}
}

func TestStartAgentPlace(t *testing.T) {
	repo := inRepoWithOrigin(t, "https://github.com/acme/api", "")
	// On this machine with nothing named, the daemon's default applies: the
	// focused pane's directory.
	local, err := startAgentPlace("", startAgentOptions{agent: "claude"})
	if err != nil || len(local) != 0 {
		t.Errorf("local place = %v, %v; want nothing sent", local, err)
	}
	named, err := startAgentPlace("", startAgentOptions{repo: "."})
	if err != nil || named["repo"] != repo {
		t.Errorf("--repo here = %v, %v; want %s", named, err, repo)
	}
	relative, err := startAgentPlace("", startAgentOptions{cwd: "sub"})
	if err != nil || relative["cwd"] != filepath.Join(repo, "sub") {
		t.Errorf("--cwd sub here = %v, %v; want it made absolute", relative, err)
	}
	remote, err := startAgentPlace("build", startAgentOptions{agent: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if remote["repo_url"] != "https://github.com/acme/api" || remote["cwd"] != nil {
		t.Errorf("remote place = %v, want the origin and no local cwd", remote)
	}
	if _, has := remote["repos_root"]; has {
		t.Errorf("repos_root = %v with none configured", remote["repos_root"])
	}
	if _, err := startAgentPlace("", startAgentOptions{clone: true}); err == nil {
		t.Error("--clone on this machine was accepted")
	}
}

func TestStartAgentTakesOneAgentAndArgsAfterDashDash(t *testing.T) {
	root := newRootCommand()
	cmd, rest, err := root.Find([]string{"start-agent", "-s", "api", "codex", "--prompt", "fix it", "--", "--model", "o4"})
	if err != nil || cmd.Name() != "start-agent" {
		t.Fatalf("find: %v %v", cmd, err)
	}
	if err := cmd.ParseFlags(rest); err != nil {
		t.Fatal(err)
	}
	args := cmd.Flags().Args()
	if dash := cmd.ArgsLenAtDash(); dash != 1 || args[0] != "codex" || strings.Join(args[dash:], " ") != "--model o4" {
		t.Errorf("args = %v, dash = %d", args, cmd.ArgsLenAtDash())
	}
	if p, _ := cmd.Flags().GetString("prompt"); p != "fix it" {
		t.Errorf("prompt = %q", p)
	}
}

func TestSplitHostSession(t *testing.T) {
	cases := map[string][2]string{
		"build:api-fan-2": {"build", "api-fan-2"},
		"api-fan-2":       {"", "api-fan-2"},
		"local:api":       {"", "api"},
	}
	for in, want := range cases {
		if h, s := splitHostSession(in); h != want[0] || s != want[1] {
			t.Errorf("splitHostSession(%q) = %q, %q; want %q, %q", in, h, s, want[0], want[1])
		}
	}
}

func TestExplainHostedVerbNamesTheHostsRemedy(t *testing.T) {
	tgt := &verbTarget{host: "build"}
	old := &session.VerbCallError{Code: session.ErrVerbInvalidParams, Message: `verb fan has no parameter "repo_url"`, Hint: &session.VerbHint{Param: "repo_url"}}
	if err := explainHostedVerb(tgt, "fan", old); !strings.Contains(err.Error(), "too old") {
		t.Errorf("an old host: %v", err)
	}
	missing := &session.VerbCallError{Code: session.ErrVerbRepoNotFound, Message: "no checkout"}
	err := explainHostedVerb(tgt, "fan", missing)
	var d *diagnosticError
	if !errors.As(err, &d) || !strings.Contains(d.Fix, "--clone") || !strings.Contains(d.Fix, "--repos-root") {
		t.Errorf("a missing checkout: %v", err)
	}
}

func TestCheckTransferShapeRefusesWhatBundleWorktreeCannotSend(t *testing.T) {
	good := bundleReply{Token: "t", Branch: "feat/x", Head: strings.Repeat("a", 40), BaseCommit: strings.Repeat("b", 40), BundleBytes: 10, PatchBytes: 5, Size: 15}
	if err := checkTransferShape(good); err != nil {
		t.Fatalf("a good reply was refused: %v", err)
	}
	bad := map[string]func(r *bundleReply){
		"branch with a colon":  func(r *bundleReply) { r.Branch = "a:refs/heads/main" },
		"branch as an option":  func(r *bundleReply) { r.Branch = "-x" },
		"head as an option":    func(r *bundleReply) { r.Head = "--upload-pack=x" },
		"short head":           func(r *bundleReply) { r.Head = "abc" },
		"base not a hash":      func(r *bundleReply) { r.BaseCommit = "main" },
		"sizes do not add up":  func(r *bundleReply) { r.Size = 99 },
		"past the cap":         func(r *bundleReply) { r.BundleBytes = pullMaxBytes; r.Size = pullMaxBytes + 5 },
		"no token to read on":  func(r *bundleReply) { r.Token = "" },
		"negative patch bytes": func(r *bundleReply) { r.PatchBytes = -5; r.BundleBytes = 20 },
	}
	for name, mutate := range bad {
		r := good
		mutate(&r)
		if err := checkTransferShape(r); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestHostsAddKeepsReposRoot(t *testing.T) {
	path, err := config.GetConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := config.SetHostInFile(path, "build", config.HostConfig{Addr: "old", ReposRoot: "~/src"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TUIOS_SSH", "/nonexistent-ssh")
	_ = runHostAdd("build", "new", hostAddFlags{})
	hosts, err := config.HostsInFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if hosts["build"].Addr != "new" || hosts["build"].ReposRoot != "~/src" {
		t.Errorf("after hosts add: %+v, want the new address and the old repos_root", hosts["build"])
	}
}
