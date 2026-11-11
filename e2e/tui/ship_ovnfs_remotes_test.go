package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestShipPushLeavesOvnfsCheckoutRemotesOut: ovnfs 0.4.0 gives a repository a
// git remote ovnfs/NAME for each live checkout of it, by default. It writes
// them to .git/ovnfs-remotes and includes that file from .git/config, so a
// tuios worktree of the repository sees them too. ship push picks the remote
// the branch follows, else the only remote, else origin, so before the fix:
//
//   - with one remote that is not origin (upstream here) and a checkout, it
//     refused: "has 2 remotes and branch feat/ship follows none of them";
//   - with no remote but the checkout's, it pushed into the checkout's
//     private git folder.
//
// The test writes the remotes file the way ovnfs does, with a bare
// repository standing in for the checkout's git folder, so it needs no ovnfs.
func TestShipPushLeavesOvnfsCheckoutRemotesOut(t *testing.T) {
	f := newShipFixture(t)
	f.write("note.txt", "a note\n")
	f.tr.ok("ship", "commit", "-s", f.session, "-m", "Add a note")
	tip := testutil.Git(t, f.worktree, "rev-parse", "HEAD")

	checkoutGit := filepath.Join(t.TempDir(), "checkout.git")
	testutil.Git(t, f.repo, "init", "-q", "--bare", "-b", "main", checkoutGit)
	remotes := "[remote \"ovnfs/demo\"]\n" +
		"\turl = " + checkoutGit + "\n" +
		"\tfetch = +HEAD:refs/remotes/ovnfs/demo/HEAD\n" +
		"\ttagOpt = --no-tags\n\tpruneTags = false\n\tfollowRemoteHEAD = never\n"
	gitDir := testutil.Git(t, f.repo, "rev-parse", "--absolute-git-dir")
	if err := os.WriteFile(filepath.Join(gitDir, "ovnfs-remotes"), []byte(remotes), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, f.repo, "config", "--local", "include.path", "ovnfs-remotes")
	testutil.Git(t, f.repo, "remote", "rename", "origin", "upstream")
	if got := testutil.Git(t, f.worktree, "remote"); !strings.Contains(got, "ovnfs/demo") || !strings.Contains(got, "upstream") {
		t.Fatalf("the worktree's remotes are %q, want upstream and ovnfs/demo", got)
	}
	pushedTo := func(dir string) string {
		out, _ := exec.Command("git", "--git-dir", dir, "rev-parse", "-q", "--verify", "refs/heads/feat/ship").Output()
		return strings.TrimSpace(string(out))
	}

	// One real remote and a checkout's: the push goes to the real one.
	out, err := f.tr.run("ship", "push", "-s", f.session, "--yes")
	if err != nil || !strings.Contains(out, "to upstream") {
		t.Fatalf("ASSERTION: ship push with upstream and an ovnfs checkout remote = %v:\n%s\nwant a push to upstream", err, out)
	}
	if got := pushedTo(f.remote); got != tip {
		t.Errorf("ASSERTION: upstream's feat/ship is %q, want %s", got, tip)
	}
	if got := pushedTo(checkoutGit); got != "" {
		t.Errorf("ASSERTION: the push reached the checkout's git folder: %s", got)
	}

	// Only the checkout's remote: there is nowhere to ship to.
	// The push above made upstream the branch's upstream; it goes with it.
	_ = exec.Command("git", "-C", f.repo, "branch", "--unset-upstream", "feat/ship").Run()
	testutil.Git(t, f.repo, "remote", "remove", "upstream")
	out, err = f.tr.run("ship", "push", "-s", f.session, "--yes")
	if err == nil {
		t.Errorf("ASSERTION: ship push with only an ovnfs checkout remote succeeded:\n%s", out)
	}
	if got := pushedTo(checkoutGit); got != "" {
		t.Errorf("ASSERTION: ship push sent feat/ship into the checkout's git folder: %s", got)
	}
}
