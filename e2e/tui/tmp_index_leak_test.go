package tuie2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestNoTempIndexLeftWhenTheDaemonStops: a checkpoint reads the work tree
// through a temporary index in a folder named tuios-index-* in the daemon's
// temp dir. The folder used to be removed only by a deferred call, so a
// daemon that stopped while a checkpoint ran left it behind: the e2e run's
// temp folder check then failed now and then with "tuios-index-NNNN: holds
// index", with every test passing.
//
// Each case catches the daemon with the folder made and in use: a stand-in
// git on the daemon's PATH stops at write-tree on a tuios temporary index
// until it is killed. Then the daemon stops in one of the ways a daemon
// stops, and the folder must be gone:
//
//   - kill-server: the shutdown ends the checkpoint and removes the folder.
//   - a live upgrade: the old image removes the folder before it execs the
//     new one, which no deferred call survives.
//   - SIGKILL: nothing runs in the killed daemon, so the folder stays (the
//     case checks that it does), and the next daemon removes it. A folder
//     of a process that still runs stays.
//
// The daemon runs with a temp dir of its own, so the check reads only the
// folders this test made.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Temporary index
// folders").
func TestNoTempIndexLeftWhenTheDaemonStops(t *testing.T) {
	requireReal(t, "the daemon must be a process of its own to stop, exec and be killed")
	t.Run("kill-server", func(t *testing.T) {
		h := newIndexHold(t)
		h.ok(30*time.Second, "kill-server")
		h.wantNoIndexFolder("after kill-server returned")
	})
	t.Run("live upgrade", func(t *testing.T) {
		h := newIndexHold(t)
		h.ok(90*time.Second, "daemon", "upgrade", "--binary", tuiosBin)
		h.wantNoIndexFolder("after the live upgrade returned")
		// The new image also removes a folder the old one left, so the folder
		// alone does not show the old image cleaned up. Its git does: ended
		// and waited for before the exec, it is gone. Left running, it would
		// work on in a folder that is no longer there.
		if processAlive(h.gitPID) {
			t.Errorf("git %d, held at write-tree, still runs after the live upgrade", h.gitPID)
		}
	})
	t.Run("SIGKILL", func(t *testing.T) {
		h := newIndexHold(t)
		pid := h.daemonPID()
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
			t.Fatalf("SIGKILL the daemon %d: %v", pid, err)
		}
		waitUntil(t, "the killed daemon to end", 10*time.Second, func() (bool, string) {
			return !processAlive(pid), fmt.Sprintf("daemon %d still runs", pid)
		})
		// A killed daemon runs nothing: the folder is still there. Without
		// this, the case would pass for a hold that never caught the folder.
		if got := h.indexFolders(); len(got) != 1 {
			t.Fatalf("after SIGKILL the temp dir holds %v, want the one folder of the killed daemon", got)
		}
		// A folder of a process that runs is not the next daemon's to remove:
		// this test process stands in for one.
		running := fmt.Sprintf("tuios-index-%d-running", os.Getpid())
		if err := os.Mkdir(filepath.Join(h.tmp, running), 0o700); err != nil {
			t.Fatal(err)
		}
		h.ok(30*time.Second, "start-server")
		waitUntil(t, "the next daemon to remove the killed daemon's index folder", 10*time.Second, func() (bool, string) {
			got := h.indexFolders()
			return len(got) == 1 && got[0] == running, fmt.Sprintf("temp dir holds %v, want only %s", got, running)
		})
	})
}

// indexHold is a daemon caught in a checkpoint, with its temporary index
// folder made and git stopped at write-tree.
type indexHold struct {
	t          *testing.T
	base, repo string
	tmp        string
	env        []string
	// gitPID is the stand-in git that holds the checkpoint.
	gitPID int
}

// newIndexHold starts a daemon under a new isolation root with the stand-in
// git on its PATH and a temp dir of its own, ends an agent turn in a
// repository, and returns once the checkpoint of that turn holds its index
// folder.
func newIndexHold(t *testing.T) *indexHold {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	h := &indexHold{t: t, base: base, repo: testutil.GitRepo(t), tmp: filepath.Join(base, "tmp")}
	bin := filepath.Join(base, "bin")
	for _, dir := range []string{h.tmp, bin} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	held := filepath.Join(base, "held.pid")
	never := filepath.Join(base, "never")
	if err := syscall.Mkfifo(never, 0o600); err != nil {
		t.Fatal(err)
	}
	// The stand-in opens a pipe nobody writes to, so it waits until it is
	// killed. Its pid is its process group: git runs in a group of its own.
	script := fmt.Sprintf(`#!/bin/sh
case "$GIT_INDEX_FILE" in
*/tuios-index-*/index)
	for a in "$@"; do
		if [ "$a" = write-tree ]; then
			echo $$ > %[1]s.part && mv %[1]s.part %[1]s
			exec cat %[2]s
		fi
	done ;;
esac
exec %[3]s "$@"
`, shq(held), shq(never), shq(realGit))
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil { //nolint:gosec // an executable stand-in
		t.Fatal(err)
	}
	h.env = withKnobs([]string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TMPDIR=" + h.tmp,
		handoffTestEnv()[0],
	})

	h.ok(30*time.Second, "start-server")
	h.ok(30*time.Second, "new", "agent", "--detach")
	h.ok(30*time.Second, "set-agent-state", "-s", "agent", "working")
	if err := os.WriteFile(filepath.Join(h.repo, "notes.txt"), []byte("a turn\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.ok(30*time.Second, "set-agent-state", "-s", "agent", "done")

	waitUntil(t, "the checkpoint to reach write-tree", 20*time.Second, func() (bool, string) {
		b, err := os.ReadFile(held)
		if err != nil {
			return false, "no write-tree on a tuios temporary index yet"
		}
		h.gitPID, err = strconv.Atoi(strings.TrimSpace(string(b)))
		return err == nil, string(b)
	})
	t.Cleanup(func() {
		_ = syscall.Kill(-h.gitPID, syscall.SIGKILL)
		_ = syscall.Kill(h.gitPID, syscall.SIGKILL)
	})
	if got := h.indexFolders(); len(got) != 1 {
		t.Fatalf("with the checkpoint at write-tree the temp dir holds %v, want one index folder", got)
	}
	return h
}

// run runs tuios in the repository with the hold's environment.
func (h *indexHold) run(timeout time.Duration, args ...string) (string, error) {
	h.t.Helper()
	pinPreV080Looks(h.t, h.base)
	ctx, cancel := context.WithTimeout(context.Background(), scaled(timeout))
	defer cancel()
	cmd := exec.CommandContext(ctx, tuiosBin, args...)
	cmd.Dir = h.repo
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(h.base, key))
	}
	cmd.Env = append(cmd.Env, h.env...)
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (h *indexHold) ok(timeout time.Duration, args ...string) string {
	h.t.Helper()
	out, err := h.run(timeout, args...)
	if err != nil {
		h.t.Fatalf("tuios %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// daemonPID is the pid the daemon gives in its hello.
func (h *indexHold) daemonPID() int {
	h.t.Helper()
	out := h.ok(30*time.Second, "call", "hello", "{}")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var hello struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &hello); err != nil || hello.PID <= 0 {
		h.t.Fatalf("hello gave no pid: %v\n%s", err, out)
	}
	return hello.PID
}

// indexFolders lists the tuios-index-* entries in the daemon's temp dir.
func (h *indexHold) indexFolders() []string {
	h.t.Helper()
	entries, err := os.ReadDir(h.tmp)
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "tuios-index-") {
			out = append(out, e.Name())
		}
	}
	return out
}

func (h *indexHold) wantNoIndexFolder(when string) {
	h.t.Helper()
	if got := h.indexFolders(); len(got) != 0 {
		h.t.Errorf("%s the daemon's temp dir still holds %v", when, got)
	}
}
