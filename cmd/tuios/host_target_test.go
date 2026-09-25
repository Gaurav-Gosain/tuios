package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

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

// TestLandBranchLeavesNothingBehindOnFailure covers a transfer whose head is
// not the tip of the bundled branch, and one whose bundle git cannot read. A
// failed pull must not leave the new branch, or every retry is refused with
// "branch already exists here".
func TestLandBranchLeavesNothingBehindOnFailure(t *testing.T) {
	sender := testutil.GitRepo(t)
	testutil.Git(t, sender, "checkout", "-q", "-b", "feat/x")
	if err := os.WriteFile(filepath.Join(sender, "f"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, sender, "add", "f")
	testutil.Git(t, sender, "commit", "-q", "-m", "x")
	tip := strings.TrimSpace(testutil.Git(t, sender, "rev-parse", "HEAD"))
	bundlePath := filepath.Join(t.TempDir(), "b")
	testutil.Git(t, sender, "bundle", "create", "-q", bundlePath, "refs/heads/feat/x")
	bundle, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	receiver := testutil.GitRepo(t)

	cases := map[string]bundleReply{
		// The far side named a head the bundled branch does not end at, as
		// happened when it bundled a stale branch name.
		"head is not the branch tip": {Branch: "feat/x", Head: strings.Repeat("1", 40), BundleBytes: int64(len(bundle)), Size: int64(len(bundle))},
		// git cannot fetch from bytes that are not a bundle.
		"bundle is not a bundle": {Branch: "feat/x", Head: tip, BundleBytes: 4, Size: 4},
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			data := bundle
			if reply.BundleBytes == 4 {
				data = []byte("junk")
			}
			if err := landBranch(receiver, t.TempDir(), "build", "pulled", reply, data); err == nil {
				t.Fatal("landBranch succeeded")
			}
			if worktree.BranchExists(receiver, "pulled") {
				t.Error("the failed pull left branch pulled behind")
			}
		})
	}

	good := bundleReply{Branch: "feat/x", Head: tip, BundleBytes: int64(len(bundle)), Size: int64(len(bundle))}
	if err := landBranch(receiver, t.TempDir(), "build", "pulled", good, bundle); err != nil {
		t.Fatalf("a good transfer after the failed ones: %v", err)
	}
	if got, _ := worktree.BranchCommit(receiver, "pulled"); got != tip {
		t.Errorf("pulled is at %s, want %s", got, tip)
	}
}
