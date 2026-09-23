package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// Starting agents on another machine and bringing their work back, driven
// the way a person does it: from a checkout on this machine, naming host
// build, with a checkout of the same repository on build found by its origin
// URL. build is the second daemon the link test uses, reached over the real
// subprocess transport through the ssh stand-in, so every call here crosses
// the link.
//
// What would pass a weaker test and fail this one: a --host that ran the fan
// here, a repository matched by path rather than by origin, a pull that
// carried the commits and dropped the uncommitted work, or a start-agent that
// ignored the host.

// tuiosCLIInDir is tuiosCLIEnv run from dir, which is how a command finds the
// repository the person is in.
func tuiosCLIInDir(t *testing.T, base, dir string, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(tuiosBin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// cloneWithOrigin clones src to dst and gives the clone the origin url.
func cloneWithOrigin(t *testing.T, src, dst, url string) {
	t.Helper()
	testutil.Git(t, filepath.Dir(src), "clone", "-q", src, dst)
	testutil.Git(t, dst, "remote", "set-url", "origin", url)
}

func TestFanOnAHostAndPullTheWorkBack(t *testing.T) {
	base, origin := fanFixture(t)
	// Each daemon puts its worktrees under its own data directory, as two
	// machines would. The fixture's shared directory would put the pulled
	// worktree on top of the far one.
	t.Setenv("TUIOS_WORKTREE_DIR", "")
	remote := remoteMachine(t)

	// The same repository on both machines, cloned over two spellings of
	// one URL, at paths that have nothing in common.
	reposRoot := filepath.Join(remote, "src")
	farCheckout := filepath.Join(reposRoot, "acme", "api")
	cloneWithOrigin(t, origin, farCheckout, "https://example.com/acme/api.git")
	here := filepath.Join(base, "work", "api")
	cloneWithOrigin(t, origin, here, "git@example.com:acme/api")

	// build's daemon runs, as it would on a machine someone uses.
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("start build's daemon: %v\n%s", err, out)
	}

	ssh := writeFakeSSHTo(t, base, remote)
	cfgDir := filepath.Join(base, "XDG_CONFIG_HOME", "tuios")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "[hosts.build]\naddr = \"someone@buildbox\"\ncommand = \"" + tuiosBin + "\"\nconnect_timeout = 5\nrepos_root = \"" + reposRoot + "\"\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"TUIOS_SSH=" + ssh}
	killDaemon(t, base)
	if out, err := tuiosCLIEnv(t, base, env, "new", "home", "--detach"); err != nil {
		t.Fatalf("start the hub daemon: %v\n%s", err, out)
	}
	waitForHostListing(t, base, func(s string) bool {
		return strings.Contains(s, "build") && strings.Contains(s, "up")
	}, "the hub never reported build up")

	// Two agents on build, for the repository this directory is in.
	out, err := tuiosCLIInDir(t, base, here, env, "fan", "2", "--host", "build", "--agent", "claude", "--name", "try/remote", "Add a retry to the client.")
	if err != nil {
		t.Fatalf("ASSERTION: fan --host build failed: %v\n%s", err, out)
	}
	for _, want := range []string{"Started 2 agents on try/remote on build", "api-try-remote-2", "worktree pull build:"} {
		if !strings.Contains(out, want) {
			t.Errorf("ASSERTION: fan --host output lacks %q:\n%s", want, out)
		}
	}
	// They are build's sessions, not this machine's.
	if local, _ := tuiosCLI(t, base, "worktree", "ls"); strings.Contains(local, "try/remote") {
		t.Fatalf("ASSERTION: the fan ran on this machine:\n%s", local)
	}

	var rows []map[string]any
	deadline := time.Now().Add(45 * time.Second)
	for {
		out, err := tuiosCLIInDir(t, base, here, env, "worktree", "ls", "--host", "build", "--group", "try/remote", "--json")
		if err != nil {
			t.Fatalf("worktree ls --host build: %v\n%s", err, out)
		}
		rows = nil
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatalf("worktree ls --json did not print JSON: %v\n%s", err, out)
		}
		sent := 0
		for _, r := range rows {
			if r["prompt_status"] == "sent" {
				sent++
			}
		}
		if len(rows) == 2 && sent == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the prompts on build were never sent: %v", rows)
		}
		time.Sleep(500 * time.Millisecond)
	}
	var farPath string
	for _, r := range rows {
		if r["repo_root"] != farCheckout {
			t.Errorf("ASSERTION: build made the worktree from %v, want its checkout found by origin, %s", r["repo_root"], farCheckout)
		}
		if r["session"] == "api-try-remote-2" {
			farPath, _ = r["path"].(string)
		}
	}
	if farPath == "" || !strings.HasPrefix(farPath, remote) {
		t.Fatalf("ASSERTION: the second worktree is not on build: %q", farPath)
	}

	// The agent on build does some work: one commit and one file it has not
	// committed.
	if err := os.WriteFile(filepath.Join(farPath, "retry.go"), []byte("package api\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, farPath, "add", "retry.go")
	testutil.Git(t, farPath, "commit", "-q", "-m", "add retry")
	farHead := testutil.Git(t, farPath, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(farPath, "notes.txt"), []byte("half done\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err = tuiosCLIInDir(t, base, here, env, "worktree", "pull", "build:api-try-remote-2", "--detach")
	if err != nil {
		t.Fatalf("ASSERTION: worktree pull failed: %v\n%s", err, out)
	}
	for _, want := range []string{"Pulled api-try-remote-2 from build into branch try/remote-2", "1 uncommitted change is applied", "Created session 'api-try-remote-2'"} {
		if !strings.Contains(out, want) {
			t.Errorf("ASSERTION: pull output lacks %q:\n%s", want, out)
		}
	}
	if got := testutil.Git(t, here, "rev-parse", "refs/heads/try/remote-2"); got != farHead {
		t.Errorf("ASSERTION: the pulled branch is at %s, want build's head %s", got, farHead)
	}
	localRows := worktreeRows(t, base)
	var localPath string
	for _, r := range localRows {
		if r["session"] == "api-try-remote-2" {
			localPath, _ = r["path"].(string)
		}
	}
	if localPath == "" || strings.HasPrefix(localPath, remote) {
		t.Fatalf("ASSERTION: no local worktree session for the pull: %v", localRows)
	}
	for name, want := range map[string]string{"retry.go": "package api\n", "notes.txt": "half done\n"} {
		got, err := os.ReadFile(filepath.Join(localPath, name))
		if err != nil || string(got) != want {
			t.Errorf("ASSERTION: %s in the pulled worktree = %q, %v; want %q", name, got, err, want)
		}
	}
	// Nothing on build moved.
	if status := testutil.Git(t, farPath, "status", "--porcelain"); !strings.Contains(status, "?? notes.txt") {
		t.Errorf("ASSERTION: the pull changed build's worktree:\n%s", status)
	}
	// A second pull under the same name is refused rather than moving the
	// branch.
	if out, err := tuiosCLIInDir(t, base, here, env, "worktree", "pull", "build:api-try-remote-2", "--detach"); err == nil || !strings.Contains(out, "already exists") {
		t.Errorf("ASSERTION: a second pull onto an existing branch was not refused: %v\n%s", err, out)
	}

	// One more agent on build, in its checkout of this repository. The
	// command returns once the agent is ready and the prompt is taken.
	out, err = tuiosCLIInDir(t, base, here, env, "start-agent", "-s", "build:agents", "claude", "--prompt", "Look at the retry.", "--json")
	if err != nil {
		t.Fatalf("ASSERTION: start-agent on build failed: %v\n%s", err, out)
	}
	var started map[string]any
	if err := json.Unmarshal([]byte(out), &started); err != nil {
		t.Fatalf("start-agent --json did not print JSON: %v\n%s", err, out)
	}
	if started["host"] != "build" || started["cwd"] != farCheckout || started["prompt_status"] != "sent" || started["created_session"] != true {
		t.Errorf("ASSERTION: start-agent = %v, want a new session on build in %s with the prompt sent", started, farCheckout)
	}
	if pane, err := tuiosCLIEnv(t, base, env, "capture-pane", "-s", "build:agents"); err != nil || !strings.Contains(pane, "GOT: Look at the retry.") {
		t.Errorf("ASSERTION: the agent on build never got the prompt: %v\n%s", err, pane)
	}

	// Keep the winner on build: its sibling there goes.
	out, err = tuiosCLIInDir(t, base, here, env, "fan", "keep", "build:api-try-remote-2")
	if err != nil {
		t.Fatalf("ASSERTION: fan keep on build failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Kept api-try-remote-2 on try/remote-2 on build") {
		t.Errorf("ASSERTION: fan keep output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(farPath), "try-remote")); !os.IsNotExist(err) {
		t.Errorf("ASSERTION: the sibling worktree on build is still there: %v", err)
	}
}
